// SPDX-License-Identifier: MIT
// Copyright 2026 iliya

package agent

import (
	"github.com/alibaba/open-code-review/internal/config/rules"
	"github.com/alibaba/open-code-review/internal/model"
	"testing"
)

func TestReviewRouterManagedSelectionBeforeDispatch(t *testing.T) {
	agent := New(Args{FileFilter: &rules.FileFilter{ManagedPaths: map[string]bool{"src/Foo.go": true, "src/[x].go": true, ".env": true}}})
	for _, path := range []string{"src/foo.go", "src/x.go", "unassigned.go"} {
		if agent.whyExcluded(model.Diff{NewPath: path}) != ExcludeUserRule {
			t.Fatalf("unassigned selected: %s", path)
		}
	}
	for _, path := range []string{"src/Foo.go", "src/[x].go"} {
		if agent.whyExcluded(model.Diff{NewPath: path}) != ExcludeNone {
			t.Fatalf("assigned excluded: %s", path)
		}
	}
	if agent.whyExcluded(model.Diff{NewPath: ".env"}) != ExcludeSecret {
		t.Fatal("hard credential gate lost")
	}
}
