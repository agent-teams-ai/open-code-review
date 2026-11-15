// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package llm

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

func TestManagedAnthropicTerminalProof(t *testing.T) {
	base := `{"id":"msg_fixture","type":"message","role":"assistant","model":"admitted-model","stop_reason":"end_turn","content":[{"type":"text","text":"done"}],"usage":{"input_tokens":10,"output_tokens":2}}`
	tool := []ToolDef{{Type: "function", Function: FunctionDef{Name: "task_done"}}}
	for _, tc := range []struct {
		name  string
		wire  string
		tools []ToolDef
		valid bool
	}{
		{name: "complete", wire: base, valid: true},
		{name: "missing terminal", wire: strings.Replace(base, `"stop_reason":"end_turn",`, ``, 1)},
		{name: "max tokens", wire: strings.Replace(base, `end_turn`, `max_tokens`, 1)},
		{name: "unknown terminal", wire: strings.Replace(base, `end_turn`, `future`, 1)},
		{name: "wrong model", wire: strings.Replace(base, `admitted-model`, `another-model`, 1)},
		{name: "missing usage", wire: strings.Replace(base, `"input_tokens":10,`, ``, 1)},
		{name: "output exceeds cap", wire: strings.Replace(base, `"output_tokens":2`, `"output_tokens":101`, 1)},
		{name: "overflow usage", wire: strings.Replace(base, `"input_tokens":10`, `"input_tokens":9007199254740991`, 1)},
		{name: "empty tool terminal", wire: strings.Replace(base, `end_turn`, `tool_use`, 1), tools: tool},
		{name: "valid tool", wire: `{"id":"msg_tool","type":"message","role":"assistant","model":"admitted-model","stop_reason":"tool_use","content":[{"type":"tool_use","id":"call","name":"task_done","input":{}}],"usage":{"input_tokens":10,"output_tokens":2}}`, tools: tool, valid: true},
		{name: "unoffered tool", wire: `{"id":"msg_tool","type":"message","role":"assistant","model":"admitted-model","stop_reason":"tool_use","content":[{"type":"tool_use","id":"call","name":"read_secret","input":{}}],"usage":{"input_tokens":10,"output_tokens":2}}`, tools: tool},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var message anthropic.Message
			if err := json.Unmarshal([]byte(tc.wire), &message); err != nil {
				t.Fatal(err)
			}
			err := validateManagedAnthropicResponse(&message, "admitted-model", 100, tc.tools)
			if (err == nil) != tc.valid {
				t.Fatalf("validation = %v; valid = %v", err, tc.valid)
			}
		})
	}
}

func TestManagedAnthropicEndpoint(t *testing.T) {
	cfg := `{"provider":"reviewrouter","language":"en","telemetry":{"enabled":false},"custom_providers":{"reviewrouter":{"protocol":"anthropic","url":"http://127.0.0.1:8787","api_key":"Bearer fixture-capability","model":"admitted-model","timeout_sec":15,"extra_body":{"output_config":{"effort":"high"}}}}}`
	endpoint, err := resolveManaged(t, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Protocol != ProtocolAnthropic || endpoint.RequiresStreaming || !endpoint.StrictTerminal || endpoint.MaxRetries == nil || *endpoint.MaxRetries != 0 || endpoint.Token != "fixture-capability" {
		t.Fatalf("unexpected managed native endpoint: %+v", endpoint)
	}
	for _, body := range []map[string]any{
		{"system": "override"}, {"service_tier": "default"},
		{"output_config": map[string]any{"effort": "ultra"}},
		{"output_config": map[string]any{"effort": "high", "format": "override"}},
	} {
		if _, err := validateManagedAnthropicExtraBody(body); err == nil {
			t.Fatalf("accepted override: %v", body)
		}
	}
}

func TestManagedAnthropicSDKDoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/messages" || r.Header.Get("Authorization") != "Bearer fixture-capability" || r.Header.Get("X-Api-Key") != "" {
			t.Errorf("unexpected native wire: %s %v", r.URL.Path, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"fixture"}}`))
	}))
	defer server.Close()
	zero := 0
	client := NewAnthropicClient(ClientConfig{URL: server.URL, APIKey: "fixture-capability", Model: "admitted-model", AuthHeader: "authorization", Timeout: time.Second, MaxRetries: &zero, StrictTerminal: true})
	_, err := client.CompletionsWithCtx(t.Context(), ChatRequest{Messages: []Message{{Role: "user", Content: "fixture"}}})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("error = %v, calls = %d; expected one failed request", err, calls.Load())
	}
}
