// skills_test.go - tests for the skills package.

package skills

import (
	"strings"
	"testing"
)

func TestResolveByTask(t *testing.T) {
	tests := []struct {
		task    string
		expected string
	}{
		{"security", "security_auditor"},
		{"review", "code_reviewer"},
		{"planning", "planner"},
		{"debug", "debugger"},
		{"refactor", "refactorer"},
		{"test", "test_engineer"},
		{"doc", "documenter"},
		{"synthesize", "synthesizer"},
		{"research", "researcher"},
		{"verify", "verifier"},
		{"unknown", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.task, func(t *testing.T) {
			got := Resolve("", tt.task)
			if got != tt.expected {
				t.Errorf("Resolve(task=%q) = %q, want %q", tt.task, got, tt.expected)
			}
		})
	}
}

func TestResolveByQueryKeyword(t *testing.T) {
	tests := []struct {
		query   string
		expected string
	}{
		{"fix this memory leak in the cache", "debugger"},
		{"what's the architecture for auth?", "architect"},
		{"improve code quality and clean up", "refactorer"},
		{"find security vulnerabilities in auth flow", "security_auditor"},
		{"analyze all the functions in this file", "researcher"},
		{"write tests for the new API", "test_engineer"},
		{"no specific skill mentioned", "generalist"},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := Resolve(tt.query, "")
			if got != tt.expected {
				t.Errorf("Resolve(query=%q) = %q, want %q", tt.query, got, tt.expected)
			}
		})
	}
}

func TestResolveProfile(t *testing.T) {
	tests := []struct {
		name    string
		query   string
		task    string
		expected string
	}{
		{"deep skill", "detailed analysis of the codebase", "", "deep"},
		{"compact hint", "#profile: compact\nfix the bug", "", "compact"},
		{"tier hint", "tier: deep\nrefactor this", "", "deep"},
		{"security defaults to deep", "", "security", "deep"},
		{"debug defaults to compact", "", "debug", "compact"},
		{"no match defaults compact", "random query", "unknown_task", "compact"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveProfile(tt.query, tt.task)
			if got != tt.expected {
				t.Errorf("ResolveProfile(query=%q, task=%q) = %q, want %q",
					tt.query, tt.task, got, tt.expected)
			}
		})
	}
}

func TestRegisterAndUnregister(t *testing.T) {
	// Save original registry
	original := List()
	defer Reset()

	// Register a custom skill
	customSkill := Skill{
		Name:     "my_custom_skill",
		Role:     "custom_role",
		Profile:  "compact",
		Keywords: []string{"custom", "special"},
		Tools:    []string{"custom_tool"},
	}
	Register(customSkill)

	// Verify it's registered
	got := Resolve("custom keyword", "my_custom_skill")
	if got != "custom_role" {
		t.Errorf("Register failed: Resolve = %q, want %q", got, "custom_role")
	}

	// Verify Get works
	retrieved := Get("my_custom_skill")
	if retrieved == nil || retrieved.Role != "custom_role" {
		t.Error("Get() returned nil or wrong role")
	}

	// Unregister
	Unregister("my_custom_skill")
	got = Resolve("custom keyword", "my_custom_skill")
	if got != "generalist" {
		t.Errorf("Unregister failed: Resolve = %q, want %q", got, "generalist")
	}

	_ = original // suppress unused warning
}

func TestGetTools(t *testing.T) {
	tests := []struct {
		skill    string
		expected []string
	}{
		{"security", []string{"grep_codebase", "audit", "find_symbol"}},
		{"refactor", []string{"grep_codebase", "edit_file", "apply_patch", "find_symbol"}},
		{"nonexistent", nil},
	}

	for _, tt := range tests {
		t.Run(tt.skill, func(t *testing.T) {
			got := GetTools(tt.skill)
			if len(got) != len(tt.expected) {
				t.Errorf("GetTools(%q) = %v, want %v", tt.skill, got, tt.expected)
				return
			}
			for i, tool := range got {
				if tool != tt.expected[i] {
					t.Errorf("GetTools(%q)[%d] = %q, want %q", tt.skill, i, tool, tt.expected[i])
				}
			}
		})
	}
}

func TestCaseInsensitive(t *testing.T) {
	tests := []struct {
		query   string
		expected string
	}{
		{"SECURITY AUDIT", "security_auditor"},
		{"Refactor the code", "refactorer"},
		{"DEBUG this bug", "debugger"},
		{"TESTING is important", "test_engineer"},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := Resolve(tt.query, "")
			if got != tt.expected {
				t.Errorf("Resolve(%q) = %q, want %q", tt.query, got, tt.expected)
			}
		})
	}
}

func TestSkillOverride(t *testing.T) {
	// Save original
	defer Reset()

	// Override existing skill
	Register(Skill{
		Name:     "security",
		Role:     "super_auditor",
		Profile:  "deep",
		Keywords: []string{"security", "super_audit"},
		Tools:    []string{"super_tool"},
	})

	got := Resolve("super_audit the system", "security")
	if !strings.Contains(got, "auditor") {
		t.Errorf("Skill override failed: got %q", got)
	}
}

func TestList(t *testing.T) {
	skills := List()
	if len(skills) == 0 {
		t.Error("List() returned empty slice")
	}

	// Verify all default skills are present
	nameMap := make(map[string]bool)
	for _, s := range skills {
		nameMap[s.Name] = true
	}

	expected := []string{"security", "review", "planning", "debug", "refactor", "test", "doc", "synthesize", "research", "verify", "architect"}
	for _, name := range expected {
		if !nameMap[name] {
			t.Errorf("List() missing expected skill: %s", name)
		}
	}
}
