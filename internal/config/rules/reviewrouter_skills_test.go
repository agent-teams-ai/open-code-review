// SPDX-License-Identifier: MIT
// Copyright 2026 iliya

package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewRouterManagedExactRules(t *testing.T) {
	dir := t.TempDir()
	setTestHome(t, dir)
	file := filepath.Join(dir, "rule.json")
	content := `{"reviewrouter_managed_paths":["src/Foo.go","src/foo.go","src/[x].go"],"rules":[{"path":"src/Foo.go","rule":"upper\nSKILL_A\nSKILL_B","merge_system_rule":true,"reviewrouter_exact_path":true},{"path":"src/foo.go","rule":"lower\n","merge_system_rule":true,"reviewrouter_exact_path":true},{"path":"src/[x].go","rule":"literal\n","merge_system_rule":true,"reviewrouter_exact_path":true}]}`
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	resolver, filter, err := NewResolver(dir, file, ResolverOptions{SkipProjectRules: true})
	if err != nil {
		t.Fatal(err)
	}
	upper := resolver.Resolve("src/Foo.go")
	if !strings.Contains(upper, "SKILL_A\nSKILL_B") || !strings.Contains(upper, "System-Specific Rules") {
		t.Fatal("lost additive or built-in rules", upper)
	}
	if strings.Contains(resolver.Resolve("src/foo.go"), "SKILL_A") {
		t.Fatal("case bleed")
	}
	if strings.Contains(resolver.Resolve("src/x.go"), "literal") {
		t.Fatal("glob bleed")
	}
	if !strings.Contains(resolver.Resolve("src/[x].go"), "literal") {
		t.Fatal("literal path missed")
	}
	if filter.ManagedPaths["src/x.go"] || !filter.ManagedPaths["src/[x].go"] {
		t.Fatal("incorrect exact inventory")
	}
	if _, _, err := NewResolver(dir, file, ResolverOptions{}); err == nil {
		t.Fatal("managed inventory accepted outside managed mode")
	}
}
