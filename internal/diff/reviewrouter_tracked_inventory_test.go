// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package diff

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/alibaba/open-code-review/internal/model"
)

func trackedInventoryRepo(t *testing.T, mode Mode) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	repo := t.TempDir()
	runGitTest(t, repo, "init", "-q")
	runGitTest(t, repo, "config", "user.email", "test@example.com")
	runGitTest(t, repo, "config", "user.name", "Test User")
	runGitTest(t, repo, "config", "commit.gpgsign", "false")
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(repo, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(".gitignore", "ignored/\nvendor/\n")
	modified := []string{"normal.go", "ignored/modify.go", "ignored/[x].go", "ignored/x.go", "ignored/Upper.go", "ignored/upper.go", "ignored/unassigned.go", "ignored/false.go", "vendor/blocked.go"}
	for _, path := range modified {
		write(path, "package fixture\n// "+path+" before\n")
	}
	write("ignored/delete.go", "package deleted\n")
	write("ignored/empty.go", "")
	renames := map[string]string{
		"rename-in.go": "ignored/renamed-in.go", "ignored/rename-out.go": "renamed-out.go",
		"rename-vendor.go": "vendor/renamed-in.go", "vendor/rename-out.go": "code-out.go",
	}
	for old := range renames {
		write(old, "package rename\n// unique original "+old+"\n")
	}
	runGitTest(t, repo, "add", "-f", ".")
	runGitTest(t, repo, "commit", "-q", "-m", "baseline")
	for _, path := range modified {
		write(path, "package fixture\n// "+path+" after\n")
	}
	for _, path := range []string{"ignored/delete.go", "ignored/empty.go"} {
		if err := os.Remove(filepath.Join(repo, path)); err != nil {
			t.Fatal(err)
		}
	}
	for old, next := range renames {
		if err := os.Rename(filepath.Join(repo, old), filepath.Join(repo, next)); err != nil {
			t.Fatal(err)
		}
		runGitTest(t, repo, "add", "-f", next)
	}
	runGitTest(t, repo, "add", "-u")
	if mode != ModeWorkspace {
		runGitTest(t, repo, "commit", "-q", "-m", "tracked changes")
	}
	write("ignored/untracked.go", "package untracked\n")
	return repo
}

func trackedInventoryProvider(repo string, mode Mode) *Provider {
	switch mode {
	case ModeCommit:
		return NewCommitProvider(repo, "HEAD", nil)
	case ModeRange:
		return NewProvider(repo, "HEAD^", "HEAD", nil)
	default:
		return NewWorkspaceProvider(repo, nil)
	}
}

func trackedInventoryPaths(diffs []model.Diff) []string {
	paths := make([]string, 0, len(diffs))
	for _, d := range diffs {
		path := d.NewPath
		if path == "/dev/null" {
			path = d.OldPath
		}
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

func TestReviewRouterManagedTrackedProvider(t *testing.T) {
	for name, mode := range map[string]Mode{"workspace": ModeWorkspace, "commit": ModeCommit, "range": ModeRange} {
		t.Run(name, func(t *testing.T) {
			repo := trackedInventoryRepo(t, mode)
			provider := trackedInventoryProvider(repo, mode)
			inventory := map[string]bool{
				"ignored/modify.go": true, "ignored/delete.go": true, "ignored/empty.go": true,
				"ignored/renamed-in.go": true, "renamed-out.go": true, "code-out.go": true,
				"ignored/[x].go": true, "ignored/Upper.go": true, "ignored/false.go": false,
				"vendor/blocked.go": true, "vendor/renamed-in.go": true, "ignored/untracked.go": true,
			}
			provider.SetManagedPaths(inventory)
			set, err := provider.GetDiffSet(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"code-out.go", "ignored/Upper.go", "ignored/[x].go", "ignored/delete.go", "ignored/empty.go", "ignored/modify.go", "ignored/renamed-in.go", "normal.go", "renamed-out.go"}
			if mode == ModeWorkspace {
				// Upstream 1.14 already retains all tracked workspace changes.
				// Exact admission is applied later by the agent's selection.
				want = append(want, "ignored/false.go", "ignored/unassigned.go", "ignored/upper.go", "ignored/x.go")
			}
			slices.Sort(want)
			if got := trackedInventoryPaths(set.Included); !slices.Equal(got, want) {
				t.Fatalf("included %v; want %v", got, want)
			}
			if got := trackedInventoryPaths(set.Excluded); !slices.Equal(got, []string{"vendor/blocked.go", "vendor/renamed-in.go"}) {
				t.Fatalf("provider directory exclusions changed: %v", got)
			}
			wantIgnored := []string{"ignored/false.go", "ignored/unassigned.go", "ignored/upper.go", "ignored/x.go"}
			if mode == ModeWorkspace {
				wantIgnored = nil
			}
			if got := trackedInventoryPaths(set.Ignored); !slices.Equal(got, wantIgnored) {
				t.Fatalf("ignored identities changed: %v; want %v", got, wantIgnored)
			}
			for _, d := range set.Ignored {
				identity := model.Diff{OldPath: d.OldPath, NewPath: d.NewPath, IsBinary: d.IsBinary}
				if d != identity {
					t.Fatal("ignored context retained content or diff payload")
				}
			}
			for _, d := range set.Included {
				switch d.OldPath {
				case "ignored/empty.go", "ignored/delete.go":
					if !d.IsDeleted {
						t.Errorf("lost deletion identity: %+v", d)
					}
				case "rename-in.go", "ignored/rename-out.go", "vendor/rename-out.go":
					if !d.IsRenamed {
						t.Errorf("lost rename identity: %+v", d)
					}
				}
			}
		})
	}
}

func TestReviewRouterManagedTrackedProviderCopiesInventory(t *testing.T) {
	repo := trackedInventoryRepo(t, ModeCommit)
	provider := NewCommitProvider(repo, "HEAD", nil)
	inventory := map[string]bool{"ignored/modify.go": true, "ignored/false.go": false}
	provider.SetManagedPaths(inventory)
	delete(inventory, "ignored/modify.go")
	inventory["ignored/unassigned.go"] = true
	inventory["ignored/false.go"] = true
	got, err := provider.GetDiff(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if paths := trackedInventoryPaths(got); !slices.Equal(paths, []string{"code-out.go", "ignored/modify.go", "normal.go", "renamed-out.go"}) {
		t.Fatalf("caller mutated provider inventory: %v", paths)
	}
	for name, inventory := range map[string]map[string]bool{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			provider.SetManagedPaths(inventory)
			if !maps.Equal(provider.managedPaths, inventory) || (provider.managedPaths == nil) != (inventory == nil) {
				t.Fatal("inventory nil/empty distinction changed")
			}
			got, err := provider.GetDiff(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if paths := trackedInventoryPaths(got); !slices.Equal(paths, []string{"code-out.go", "normal.go", "renamed-out.go"}) {
				t.Fatalf("legacy ignore behavior changed: %v", paths)
			}
		})
	}
}
