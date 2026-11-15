// SPDX-License-Identifier: MIT
// Copyright 2026 iliya

package agent

import (
	"strings"
	"testing"

	"github.com/alibaba/open-code-review/internal/config/template"
)

// Every replacement may itself contain any template token. Only occurrences in
// the original template may expand; the table independently states exact bytes.
func TestRenderTaskContentReplacementValuesAreOpaque(t *testing.T) {
	keys := []string{"{{current_system_date_time}}", "{{system_rule}}", "{{change_files}}", "{{diffs}}", "{{requirement_background}}", "{{plan_guidance}}", "{{confirmed_comments}}", "{{plan_tools}}", "{{path}}", "{{diff}}", "{{comments}}"}
	original := strings.Join(keys, "|")
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			replacements := make([]string, 0, 2*len(keys))
			expected := make([]string, len(keys))
			for i, token := range keys {
				value := "VALUE_" + token
				if token == key {
					value = "BEGIN" + original + "END"
				}
				replacements = append(replacements, token, value)
				expected[i] = value
			}
			if got := renderTaskContent(original, replacements...); got != strings.Join(expected, "|") {
				t.Fatalf("recursive replacement changed data: got %q, want %q", got, strings.Join(expected, "|"))
			}
		})
	}
	if got := renderTaskContent("date={{date}} rule={{rule}}", "{{date}}", "2026-10-28", "{{rule}}", "plain"); got != "date=2026-10-28 rule=plain" {
		t.Fatal(got)
	}
}

func TestMainTaskPrunesOnlyOriginalOptionalBlocksAcrossRounds(t *testing.T) {
	memory := "LITERAL_MEMORY_START {{diffs}} {{requirement_background}}\n### Review Plan\n{{plan_guidance}}\n\n### Confirmed Comments\n{{confirmed_comments}}\nEND_LITERAL_MEMORY"
	a := &Agent{currentDate: "DATE", args: Args{Background: "BACKGROUND", Template: template.Template{MainTask: template.LlmConversation{Messages: []template.ChatMessage{{Role: "user", Content: "{{system_rule}}\nDIFF={{diffs}}\n### Review Plan\n{{plan_guidance}}\n\n### Confirmed Comments\n{{confirmed_comments}}\n"}}}}}}
	for _, round := range []struct{ plan, confirmed string }{{"PLAN", ""}, {"", "CONFIRMED"}} {
		messages := a.buildMainTaskMessages(memory, "FILES", "DIFF", round.plan, round.confirmed)
		got := messages[0].ExtractText()
		if !strings.Contains(got, memory) {
			t.Fatalf("frozen memory changed: %q", got)
		}
		if !strings.Contains(got, "DIFF=DIFF") {
			t.Fatal("ordinary template expansion lost")
		}
		tail := strings.TrimPrefix(got, memory)
		if strings.Contains(tail, "{{plan_guidance}}") || strings.Contains(tail, "{{confirmed_comments}}") {
			t.Fatalf("original optional block survived: %q", tail)
		}
	}
}
