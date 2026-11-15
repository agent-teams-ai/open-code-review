// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 alibaba/open-code-review Contributors

package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
)

// This is the finite native Messages route, not a general protocol translator.
// The parent controls endpoint, credentials, admission, and tighter captured caps.
func validateManagedAnthropicExtraBody(body map[string]any) (map[string]any, error) {
	for key := range body {
		if key != "output_config" {
			return nil, fmt.Errorf("managed Anthropic extra_body key %q is not allowed", key)
		}
	}
	if raw, ok := body["output_config"]; ok {
		config, ok := raw.(map[string]any)
		if !ok || len(config) != 1 {
			return nil, fmt.Errorf("managed Anthropic output_config requires only effort")
		}
		effort, ok := config["effort"].(string)
		if !ok || (effort != "low" && effort != "medium" && effort != "high" && effort != "xhigh" && effort != "max") {
			return nil, fmt.Errorf("managed Anthropic effort is not allowlisted")
		}
	}
	return body, nil
}

// Anthropic's non-streaming SDK defaults a missing stop_reason to a generic
// "stop" in the historical mapper. Managed calls must prove a valid terminal
// and usage before that normalization can happen.
func validateManagedAnthropicResponse(response *anthropic.Message, model string, maxTokens int64, tools []ToolDef) error {
	if response == nil || len(response.RawJSON()) > 8*1024*1024 {
		return fmt.Errorf("managed Anthropic response is absent or oversized")
	}
	var wire struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Role       string `json:"role"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		Usage *struct {
			InputTokens   *int64 `json:"input_tokens"`
			OutputTokens  *int64 `json:"output_tokens"`
			CacheRead     *int64 `json:"cache_read_input_tokens"`
			CacheCreation *int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal([]byte(response.RawJSON()), &wire); err != nil {
		return fmt.Errorf("managed Anthropic response is malformed")
	}
	if wire.Type != "message" || wire.Role != "assistant" || wire.ID == "" || wire.Model != model {
		return fmt.Errorf("managed Anthropic response identity mismatch")
	}
	if wire.StopReason != "end_turn" && wire.StopReason != "tool_use" {
		return fmt.Errorf("managed Anthropic response is not terminal completed")
	}
	usage := wire.Usage
	if usage == nil || usage.InputTokens == nil || usage.OutputTokens == nil ||
		*usage.InputTokens < 0 || *usage.OutputTokens < 0 || *usage.OutputTokens > maxTokens ||
		(usage.CacheRead != nil && *usage.CacheRead < 0) || (usage.CacheCreation != nil && *usage.CacheCreation < 0) {
		return fmt.Errorf("managed Anthropic response usage unavailable or invalid")
	}
	// Receipts use interoperable JSON integers; reject overflow before the
	// historical mapper adds Anthropic's separate cache counters.
	const maxSafeTokens = int64(9007199254740991)
	total := int64(0)
	for _, count := range []*int64{usage.InputTokens, usage.OutputTokens, usage.CacheRead, usage.CacheCreation} {
		if count != nil {
			if *count > maxSafeTokens-total {
				return fmt.Errorf("managed Anthropic response usage exceeds safe integer bound")
			}
			total += *count
		}
	}
	allowed := make(map[string]bool)
	for _, tool := range tools {
		allowed[tool.Function.Name] = true
	}
	ids := make(map[string]bool)
	toolCount, textCount := 0, 0
	for _, block := range wire.Content {
		switch block.Type {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				textCount++
			}
		case "thinking", "redacted_thinking":
			// Preserved by the existing native-turn mapper, never accepted as final text.
		case "tool_use":
			var input map[string]any
			if block.ID == "" || ids[block.ID] || !allowed[block.Name] ||
				json.Unmarshal(block.Input, &input) != nil || input == nil {
				return fmt.Errorf("managed Anthropic tool use is invalid")
			}
			ids[block.ID] = true
			toolCount++
		default:
			return fmt.Errorf("managed Anthropic content type is unsupported")
		}
	}
	if (wire.StopReason == "tool_use" && toolCount == 0) ||
		(wire.StopReason == "end_turn" && (toolCount != 0 || textCount == 0)) {
		return fmt.Errorf("managed Anthropic terminal content is incomplete")
	}
	return nil
}
