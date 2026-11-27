// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package tool

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/gitcmd"
)

func TestManagedContextTools(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	repo := setupTestRepo(t)
	for name, content := range map[string]string{
		".env":              "PRIVATE_TOOL_MARKER\n",
		".env.old":          "PRIVATE_TOOL_MARKER renamed\n",
		"odd:dir/.env":      "PRIVATE_TOOL_MARKER colon\n",
		"line\nbreak/.env":  "PRIVATE_TOOL_MARKER newline\n",
		"binary.dat":        "BINARY_TOOL_MARKER\x00\n",
		"vendor/blocked.go": "PROVIDER_TOOL_MARKER\n",
		"related:name.go":   "RELATED_TOOL_MARKER\n",
		"deleted.go":        "DELETED_TOOL_MARKER\n",
		"late-binary.dat":   "BINARY_TOOL_MARKER first line\n" + strings.Repeat("padding\n", 2000) + "\x00last line\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, repo, name, content)
	}
	comparisonGit(t, repo, "add", "-f", ".")
	comparisonGit(t, repo, "commit", "-qm", "old context")
	old := getHeadCommit(t, repo)
	comparisonGit(t, repo, "mv", ".env.old", "renamed.go")
	comparisonGit(t, repo, "rm", "deleted.go")
	comparisonGit(t, repo, "commit", "-qm", "new context")
	head := getHeadCommit(t, repo)
	ctx := context.Background()
	for _, workspace := range []bool{false, true} {
		name := "comparison"
		if workspace {
			name = "workspace"
		}
		t.Run(name, func(t *testing.T) {
			current := FileReader{RepoDir: repo, Mode: ModeCommit, Ref: head, Runner: gitcmd.New(2)}
			previous := FileReader{RepoDir: repo, Mode: ModeCommit, Ref: old, Runner: current.Runner}
			access, err := NewComparisonCodeAccess(current, previous)
			if workspace {
				current.Mode, current.Ref, current.Runner = ModeWorkspace, "", nil
				access, err = NewWorkspaceCodeAccess(current, previous)
			}
			if err != nil {
				t.Fatal(err)
			}
			providers := comparisonProviders(&current, access)
			registry := NewRegistry()
			for _, p := range providers {
				registry.Register(p)
			}
			excluded := []string{".env.old", "renamed.go"}
			registry.SetManagedContextExclusions(excluded)
			excluded[1] = "related:name.go"
			registry.Freeze()
			reader, finder, search := providers[0], providers[1], providers[2]
			for _, side := range []string{"old", "new"} {
				for _, path := range []string{".env", "./.env", "odd:dir/.env", "line\nbreak/.env", "binary.dat", "late-binary.dat", "vendor/blocked.go", ".env.old", "renamed.go"} {
					result, err := reader.Execute(ctx, map[string]any{"file_path": path, "side": side, "end_line": float64(1)})
					if err == nil || result != "" {
						t.Fatalf("%s/%s exposed content: %q, %v", side, path, result, err)
					}
				}
				result, err := reader.Execute(ctx, map[string]any{"file_path": "related:name.go", "side": side})
				if err != nil || !strings.Contains(result, "RELATED_TOOL_MARKER") {
					t.Fatalf("related context lost: %q, %v", result, err)
				}
				result, err = search.Execute(ctx, map[string]any{"search_text": "TOOL_MARKER", "side": side})
				if err != nil || !strings.Contains(result, "RELATED_TOOL_MARKER") {
					t.Fatalf("related search lost: %q, %v", result, err)
				}
				for _, marker := range []string{"PRIVATE_TOOL_MARKER", "BINARY_TOOL_MARKER", "PROVIDER_TOOL_MARKER"} {
					if strings.Contains(result, marker) {
						t.Fatalf("%s search exposed %s: %q", side, marker, result)
					}
				}
				if strings.Contains(result, "DELETED_TOOL_MARKER") != (side == "old") {
					t.Fatalf("deleted search side changed: %q", result)
				}
				result, err = finder.Execute(ctx, map[string]any{"query_name": ".go", "side": side})
				if err != nil || !strings.Contains(result, "related:name.go") || strings.Contains(result, "renamed.go") || strings.Contains(result, "vendor/") {
					t.Fatalf("file discovery bypassed exclusions: %q, %v", result, err)
				}
			}
			result, err := reader.Execute(ctx, map[string]any{"file_path": "deleted.go", "side": "old"})
			if err != nil || !strings.Contains(result, "DELETED_TOOL_MARKER") {
				t.Fatalf("deleted old context lost: %q, %v", result, err)
			}
			if _, err := current.Read(ctx, "binary.dat"); err == nil {
				t.Fatal("full read accepted binary")
			}
			legacy := NewFileRead(&FileReader{RepoDir: repo, Mode: current.Mode, Ref: current.Ref})
			result, err = legacy.Execute(ctx, map[string]any{"file_path": ".env"})
			if err != nil || !strings.Contains(result, "PRIVATE_TOOL_MARKER") {
				t.Fatalf("non-managed behavior changed: %q, %v", result, err)
			}
		})
	}
}

func TestManagedContextWorkspaceSymlink(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, repo, ".env", "PRIVATE_TOOL_MARKER\n")
	writeTestFile(t, repo, "related.go", "RELATED_TOOL_MARKER\n")
	for alias, target := range map[string]string{"secret-link.go": ".env", "related-link.go": "related.go"} {
		if err := os.Symlink(target, filepath.Join(repo, alias)); err != nil {
			t.Fatal(err)
		}
	}
	reader := &FileReader{RepoDir: repo, Mode: ModeWorkspace}
	registry := NewRegistry()
	registry.Register(NewFileRead(reader))
	registry.SetManagedContextExclusions(nil)
	if _, err := reader.Read(context.Background(), "secret-link.go"); err == nil {
		t.Fatal("symlink bypassed secret exclusion")
	}
	if got, err := reader.Read(context.Background(), "related-link.go"); err != nil || !strings.Contains(got, "RELATED_TOOL_MARKER") {
		t.Fatalf("related symlink blocked: %q, %v", got, err)
	}
	registry.Freeze()
	defer func() {
		if recover() == nil {
			t.Fatal("managed context changed after freeze")
		}
	}()
	registry.SetManagedContextExclusions(nil)
}
