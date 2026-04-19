package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveForQuery_ExplicitSkillMarkerWins(t *testing.T) {
	sel := ResolveForQuery("", "[[skill:debug]] investigate auth refresh failure", "review")
	if !sel.Explicit {
		t.Fatal("expected explicit selection")
	}
	if got := strings.TrimSpace(sel.Query); got != "investigate auth refresh failure" {
		t.Fatalf("unexpected cleaned query: %q", got)
	}
	if len(sel.Skills) != 1 || !strings.EqualFold(sel.Skills[0].Name, "debug") {
		t.Fatalf("expected explicit debug skill, got %#v", sel.Skills)
	}
}

func TestResolveForQuery_AutoMapsTaskToAudit(t *testing.T) {
	sel := ResolveForQuery("", "review auth boundary for vulnerabilities", "security")
	if sel.Explicit {
		t.Fatal("did not expect explicit skill selection")
	}
	if len(sel.Skills) != 1 || !strings.EqualFold(sel.Skills[0].Name, "audit") {
		t.Fatalf("expected security task to auto-select audit, got %#v", sel.Skills)
	}
}

func TestDiscover_LoadsProjectSkillFile(t *testing.T) {
	root := t.TempDir()
	skillsDir := filepath.Join(root, ".dfmc", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatalf("mkdir skills dir: %v", err)
	}
	path := filepath.Join(skillsDir, "triage.yaml")
	body := `name: triage
description: Triage incidents
system_prompt: |
  Focus on incident impact first.
task: debug
role: planner
preferred_tools:
  - read_file
  - grep_codebase
`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	items := Discover(root)
	var found Skill
	var ok bool
	for _, item := range items {
		if strings.EqualFold(item.Name, "triage") {
			found = item
			ok = true
			break
		}
	}
	if !ok {
		t.Fatalf("expected triage skill in catalog, got %#v", items)
	}
	if found.Source != "project" {
		t.Fatalf("expected project source, got %q", found.Source)
	}
	if found.Task != "debug" || found.Role != "planner" {
		t.Fatalf("unexpected runtime hints: %#v", found)
	}
	if got := found.SystemInstruction(); !strings.Contains(got, "incident impact") {
		t.Fatalf("expected system prompt content, got %q", got)
	}
}
