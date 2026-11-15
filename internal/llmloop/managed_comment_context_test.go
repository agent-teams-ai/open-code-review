// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package llmloop

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alibaba/open-code-review/internal/config/template"
	"github.com/alibaba/open-code-review/internal/llm"
	"github.com/alibaba/open-code-review/internal/model"
	"github.com/alibaba/open-code-review/internal/session"
	"github.com/alibaba/open-code-review/internal/tool"
)

type managedCommentContextClient struct {
	entered  chan context.Context
	release  chan struct{}
	requests atomic.Int32
}

func (c *managedCommentContextClient) CompletionsWithCtx(ctx context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.requests.Add(1)
	c.entered <- ctx
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.release:
		return &llm.ChatResponse{}, nil
	}
}

// A live command root must not keep a comment worker alive after its bounded
// group is cancelled. The second comment must never start another model call.
func TestManagedCommentWorkersKeepGroupDeadlineAndCancellation(t *testing.T) {
	root := context.Background()
	group, cancelGroup := context.WithDeadline(root, time.Now().Add(time.Minute))
	defer cancelGroup()
	client := &managedCommentContextClient{entered: make(chan context.Context, 2), release: make(chan struct{})}
	collector := tool.NewCommentCollector()
	pool := NewCommentWorkerPool(1)
	t.Cleanup(func() {
		close(client.release) // Also releases the deliberately broken pre-fix worker.
		pool.Await()
	})
	registry := tool.NewRegistry()
	registry.Register(&tool.CodeCommentProvider{Collector: collector})
	registry.Freeze()
	runner := NewRunner(Deps{
		Managed:           true,
		LLMClient:         llm.NewSerializingClient(client, root),
		Model:             "test-model",
		Tools:             registry,
		CommentCollector:  collector,
		CommentWorkerPool: pool,
		Session:           session.New(t.TempDir(), "main", "test-model", session.SessionOptions{ReviewMode: "diff"}),
		Template: template.Template{ReLocationTask: &template.LlmConversation{
			Messages: []template.ChatMessage{{Role: "user", Content: "Locate {existing_code}"}},
		}},
		DiffLookup: func(path string) *model.Diff {
			return &model.Diff{NewPath: path, NewFileContent: "unrelated code"}
		},
	})
	runner.executeToolCall(group, "a.go", llm.ToolCall{Function: llm.FunctionCall{
		Name: "code_comment",
		Arguments: `{"comments":[` +
			`{"path":"a.go","content":"first issue","existing_code":"first missing snippet"},` +
			`{"path":"a.go","content":"second issue","existing_code":"second missing snippet"}]}`,
	}}, nil, "")
	var requestCtx context.Context
	select {
	case requestCtx = <-client.entered:
	case <-time.After(time.Second):
		t.Fatal("relocation request did not start")
	}
	wantDeadline, _ := group.Deadline()
	if deadline, ok := requestCtx.Deadline(); !ok || !deadline.Equal(wantDeadline) {
		t.Errorf("relocation deadline = %v (%v), want group deadline %v", deadline, ok, wantDeadline)
	}
	cancelGroup()
	drained := make(chan struct{})
	go func() { pool.AwaitKey("a.go"); close(drained) }()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("group cancellation did not stop the relocation worker")
	}
	if root.Err() != nil {
		t.Fatal("test cancelled the command root instead of only its group")
	}
	if got := client.requests.Load(); got != 1 {
		t.Fatalf("model dispatches = %d, want only the cancelled first relocation", got)
	}
}
