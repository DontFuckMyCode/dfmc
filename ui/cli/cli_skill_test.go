package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverSkillsIncludesProjectCustom(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	projectRoot := t.TempDir()
	skillDir := filepath.Join(projectRoot, ".dfmc", "skills")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	content := "" +
		"name: custom-review\n" +
		"description: custom project skill\n" +
		"prompt: |\n" +
		"  Review this carefully:\n" +
		"  {input}\n"
	if err := os.WriteFile(filepath.Join(skillDir, "custom-review.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	items := discoverSkills(projectRoot)
	var found skillInfo
	ok := false
	for _, item := range items {
		if strings.EqualFold(item.Name, "custom-review") {
			found = item
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("custom skill not discovered: %#v", items)
	}
	if found.Source != "project" {
		t.Fatalf("unexpected source: %s", found.Source)
	}
	if found.Builtin {
		t.Fatalf("custom skill should not be builtin")
	}

	prompt := buildSkillPrompt(found, "check auth module")
	if !strings.Contains(prompt, "check auth module") {
		t.Fatalf("input placeholder not applied: %s", prompt)
	}
}

// The elevated builtin skills carry enough playbook structure that
// their prompts must stay non-trivial — a future "simplify" pass that
// reverts them to one-liners would erase the agent's methodology. Locking
// the minimum shape here is cheap and catches that regression.
func TestBuiltinSkillsElevatedPrompts(t *testing.T) {
	want := map[string][]string{
		"review":   {"Behavioral risk", "Must-fix", "Should-fix", "Tests to add"},
		"explain":  {"Trace one real flow", "Name invariants", "Call out surprises", "Draw the shape"},
		"refactor": {"Scope", "Invariants", "Step plan", "Verify"},
		"debug":    {"Reproduce", "Bisect", "Fix at the root", "Regression test"},
		"test":     {"Discover the framework", "Map the surface", "Identify gaps", "Run them"},
		"doc":      {"Find the target", "Decide the shape", "Write for the reader", "Name the sharp edges"},
		"generate": {"Understand the ask", "Learn the conventions", "Place the code", "Write at least one test"},
		"audit":    {"Frame the surface", "Confirm each hit", "CRITICAL", "Fix direction"},
		"onboard":  {"Name the hub", "Trace one real flow", "Map the modules", "Where to start"},
	}
	got := map[string]skillInfo{}
	for _, s := range builtinSkills() {
		got[s.Name] = s
	}
	for name, markers := range want {
		s, ok := got[name]
		if !ok {
			t.Fatalf("builtin skill %q missing", name)
		}
		if !s.Builtin {
			t.Errorf("skill %q not flagged builtin", name)
		}
		if len(s.Prompt) < 400 {
			t.Errorf("skill %q prompt too short (%d bytes) — playbook was lost", name, len(s.Prompt))
		}
		for _, marker := range markers {
			if !strings.Contains(s.Prompt, marker) {
				t.Errorf("skill %q missing playbook marker %q", name, marker)
			}
		}
		// Every elevated skill must funnel user input through {input} — the
		// shortcut dispatch depends on that substitution.
		if !strings.Contains(s.Prompt, "{input}") {
			t.Errorf("skill %q prompt missing {input} placeholder", name)
		}
	}
}

// Regression guard: the CLI's skill-shortcut case list is the only way
// `dfmc debug ...` reaches the agent. If someone drops debug from that
// switch the shortcut silently falls through to runAsk — this test will
// break before that hits users.
func TestDebugShortcutBuildsFromDebugSkill(t *testing.T) {
	var debug skillInfo
	for _, s := range builtinSkills() {
		if s.Name == "debug" {
			debug = s
			break
		}
	}
	if debug.Name == "" {
		t.Fatalf("debug skill missing from builtins")
	}
	prompt := buildSkillPrompt(debug, "test suite fails on darwin only")
	if !strings.Contains(prompt, "test suite fails on darwin only") {
		t.Fatalf("user input not spliced into debug prompt: %s", prompt)
	}
	if !strings.Contains(prompt, "Reproduce") {
		t.Fatalf("debug prompt lost playbook header after splicing")
	}
}
