// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/alibaba/open-code-review/internal/config/rules"
	"github.com/alibaba/open-code-review/internal/config/template"
	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/model"
	"github.com/alibaba/open-code-review/internal/tool"
)

func managedTrackedArgs(t *testing.T, mode string) Args {
	t.Helper()
	repo := initPreviewRepo(t)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
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
	write(".gitignore", "tmp/\nvendor/\n")
	write("tmp/delete.go", "package deleted\n")
	write("tmp/empty.go", "")
	write("plain-in.go", "package renamed\n// safe unchanged rename\n")
	write("tmp/.env", "FAKE_SECRET_OLD_PATH_MARKER\n")
	write("source-secret.go", "FAKE_SECRET_NEW_PATH_MARKER\n")
	modified := map[string]string{
		"tmp/modified.go": "package fixture\n// SELECTED_CONTENT_MARKER\n",
		"related.go":      "package related\n// RELATED_CONTEXT_MARKER\n",
		"tmp/.env.direct": "FAKE_SECRET_DIRECT_MARKER\n",
		"tmp/data.bin":    "\x00binary before\n", "vendor/blocked.go": "package vendor\n",
	}
	for path, content := range modified {
		write(path, content+"before\n")
	}
	git("add", "-f", ".")
	git("commit", "-q", "-m", "managed baseline")
	for path, content := range modified {
		write(path, content+"after\n")
	}
	for _, path := range []string{"tmp/delete.go", "tmp/empty.go"} {
		if err := os.Remove(filepath.Join(repo, path)); err != nil {
			t.Fatal(err)
		}
	}
	for old, next := range map[string]string{"plain-in.go": "tmp/renamed.go", "tmp/.env": "tmp/old-secret.go", "source-secret.go": "tmp/.env.new"} {
		if err := os.Rename(filepath.Join(repo, old), filepath.Join(repo, next)); err != nil {
			t.Fatal(err)
		}
		git("add", "-f", next)
	}
	git("add", "-u")
	if mode != "workspace" {
		git("commit", "-q", "-m", "managed changes")
	}
	write("tmp/untracked.go", "package untracked\n")
	args := Args{RepoDir: repo, FileFilter: &rules.FileFilter{ManagedPaths: map[string]bool{
		"tmp/modified.go": true, "tmp/delete.go": true, "tmp/empty.go": true, "tmp/renamed.go": true,
		"tmp/.env.direct": true, "tmp/old-secret.go": true, "tmp/.env.new": true,
		"tmp/data.bin": true, "vendor/blocked.go": true, "tmp/untracked.go": true,
	}}}
	switch mode {
	case "commit":
		args.Commit = "HEAD"
	case "range":
		args.From, args.To = "HEAD^", "HEAD"
	}
	return args
}

type managedTrackedClient struct {
	mu       sync.Mutex
	prompts  []string
	results  []string
	toolArgs string
}

func (c *managedTrackedClient) CompletionsWithCtx(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sawResult := false
	for _, msg := range req.Messages {
		if msg.Role == "tool" {
			c.results = append(c.results, msg.ExtractText())
			sawResult = true
		} else if msg.Role == "user" {
			c.prompts = append(c.prompts, msg.ExtractText())
		}
	}
	if sawResult {
		return agentTaskDoneResponse(), nil
	}
	return &llm.ChatResponse{Model: "fixture", Choices: []llm.Choice{{Message: llm.ResponseMessage{
		ToolCalls: []llm.ToolCall{{ID: "read_diff", Type: "function", Function: llm.FunctionCall{Name: "file_read_diff", Arguments: c.toolArgs}}},
	}}}}, nil
}

func TestReviewRouterManagedTrackedPreviewAndRun(t *testing.T) {
	for _, mode := range []string{"workspace", "commit", "range"} {
		t.Run(mode, func(t *testing.T) {
			args := managedTrackedArgs(t, mode)
			preview, err := Preview(context.Background(), args)
			if err != nil {
				t.Fatal(err)
			}
			wantReasons := map[string]ExcludeReason{
				"tmp/modified.go": ExcludeNone, "tmp/renamed.go": ExcludeNone,
				"tmp/delete.go": ExcludeDeleted, "tmp/empty.go": ExcludeDeleted,
				"tmp/.env.direct": ExcludeSecret, "tmp/old-secret.go": ExcludeSecret, "tmp/.env.new": ExcludeSecret,
				"tmp/data.bin": ExcludeBinary, "vendor/blocked.go": ExcludeProviderDirectory, "related.go": ExcludeUserRule,
			}
			if len(preview.Entries) != len(wantReasons) || preview.ReviewableCount != 2 {
				t.Fatalf("incomplete preview: %+v", preview)
			}
			var requested []string
			for _, entry := range preview.Entries {
				want, exists := wantReasons[entry.Path]
				if !exists || entry.ExcludeReason != want || entry.WillReview != (want == ExcludeNone) {
					t.Fatalf("unexpected preview entry: %+v", entry)
				}
				requested = append(requested, entry.Path)
			}
			toolArgs, err := json.Marshal(map[string]any{"path_array": requested})
			if err != nil {
				t.Fatal(err)
			}
			client := &managedTrackedClient{toolArgs: string(toolArgs)}
			args.Managed, args.LLMClient, args.Model, args.MaxConcurrency = true, client, "fixture", 1
			args.Tools = tool.NewRegistry()
			args.Tools.Register(tool.NewFileReadDiff(tool.NewDiffMap(nil)))
			args.MainToolDefs = []llm.ToolDef{
				{Type: "function", Function: llm.FunctionDef{Name: "file_read_diff"}},
				{Type: "function", Function: llm.FunctionDef{Name: "task_done"}},
			}
			args.Template = template.Template{MaxTokens: 100000, MaxToolRequestTimes: 4,
				MainTask: template.LlmConversation{Messages: []template.ChatMessage{{Role: "user", Content: "Review {{diffs}}"}}},
			}
			a := New(args)
			if _, err := a.Run(context.Background()); err != nil {
				t.Fatal(err)
			}
			outcome := a.ManagedOutcome()
			if outcome == nil || outcome.RunStopCause != nil {
				t.Fatalf("missing/failed managed outcome: %+v", outcome)
			}
			var completed []string
			for _, group := range outcome.Groups {
				if !group.MainCompleted || group.StopCause != nil {
					t.Fatalf("unfinished selected group: %+v", group)
				}
				completed = append(completed, group.Paths...)
			}
			slices.Sort(completed)
			if !slices.Equal(completed, []string{"tmp/modified.go", "tmp/renamed.go"}) {
				t.Fatalf("preview/run selection differs: %v", completed)
			}
			prompts, results := strings.Join(client.prompts, "\n"), strings.Join(client.results, "\n")
			if !strings.Contains(prompts, "SELECTED_CONTENT_MARKER") || !strings.Contains(prompts, "tmp/renamed.go") || len(client.results) == 0 {
				t.Fatal("selected content or actual tool loop missing")
			}
			for _, path := range []string{"tmp/.env.direct", "tmp/old-secret.go", "tmp/.env.new", "tmp/data.bin", "vendor/blocked.go"} {
				if strings.Contains(results, "==== FILE: "+path+" ====") {
					t.Fatalf("hard exclusion exposed through file_read_diff: %s", path)
				}
			}
			for _, marker := range []string{"FAKE_SECRET_DIRECT_MARKER", "FAKE_SECRET_OLD_PATH_MARKER", "FAKE_SECRET_NEW_PATH_MARKER"} {
				if strings.Contains(prompts+results, marker) {
					t.Fatalf("secret reached model: %s", marker)
				}
			}
			if !strings.Contains(results, "RELATED_CONTEXT_MARKER") || !strings.Contains(results, "==== FILE: tmp/delete.go ====") || !strings.Contains(results, "==== FILE: tmp/empty.go ====") {
				t.Fatal("permitted related or deleted-file diff context lost")
			}
			args.FileFilter.ManagedPaths = map[string]bool{}
			empty, err := Preview(context.Background(), args)
			if err != nil || empty.ReviewableCount != 0 {
				t.Fatalf("empty inventory selected files: %+v, %v", empty, err)
			}
			args.FileFilter = nil
			legacy, err := Preview(context.Background(), args)
			wantTotal, wantReviewable := 2, 1
			if mode == "workspace" {
				wantTotal, wantReviewable = 10, 3
			}
			if err != nil || legacy.TotalFiles != wantTotal || legacy.ReviewableCount != wantReviewable {
				t.Fatalf("legacy ignores changed: %+v, %v", legacy, err)
			}
		})
	}
}

func TestReviewRouterManagedDiffToolHardExclusions(t *testing.T) {
	for name, filter := range map[string]*rules.FileFilter{
		"legacy nil": nil, "legacy filter": {}, "empty inventory": {ManagedPaths: map[string]bool{}},
		"admitted": {ManagedPaths: map[string]bool{"safe.go": true, ".env": true}},
	} {
		t.Run(name, func(t *testing.T) {
			reg := tool.NewRegistry()
			reader := tool.NewFileReadDiff(tool.NewDiffMap(nil))
			reg.Register(reader)
			a := &Agent{args: Args{FileFilter: filter, Tools: reg}, diffs: []model.Diff{
				{NewPath: "safe.go", Diff: "SAFE_MARKER"}, {OldPath: ".env", NewPath: "renamed.go", Diff: "OLD_SECRET_MARKER"},
				{OldPath: "old.go", NewPath: ".env", Diff: "NEW_SECRET_MARKER"}, {NewPath: "bin.dat", IsBinary: true, Diff: "BINARY_MARKER"},
				{NewPath: "related.go", Diff: "RELATED_MARKER"},
			}}
			a.injectDiffMap()
			result, err := reader.Execute(context.Background(), map[string]any{"path_array": []any{"safe.go", "renamed.go", ".env", "bin.dat", "related.go"}})
			if err != nil {
				t.Fatal(err)
			}
			managed := filter != nil && filter.ManagedPaths != nil
			for _, marker := range []string{"OLD_SECRET_MARKER", "NEW_SECRET_MARKER", "BINARY_MARKER"} {
				if strings.Contains(result, marker) == managed {
					t.Fatalf("hard exclusion or legacy behavior changed: %s", marker)
				}
			}
			if !strings.Contains(result, "SAFE_MARKER") || !strings.Contains(result, "RELATED_MARKER") {
				t.Fatal("ordinary context excluded")
			}
		})
	}
}
