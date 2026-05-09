package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// parseRequires handles plain string list (skill name only).
func TestParseRequires_StringList(t *testing.T) {
	got := parseRequires([]any{"onboard", "audit"}, "x")
	if len(got) != 2 {
		t.Fatalf("expected 2 requires, got %d", len(got))
	}
	if got[0].Skill != "onboard" || got[1].Skill != "audit" {
		t.Errorf("got %+v", got)
	}
}

// parseRequires handles object form with reason.
func TestParseRequires_ObjectWithReason(t *testing.T) {
	got := parseRequires([]any{
		map[string]any{"skill": "audit", "reason": "security check"},
	}, "x")
	if len(got) != 1 {
		t.Fatalf("expected 1 require, got %d", len(got))
	}
	if got[0].Skill != "audit" || got[0].Reason != "security check" {
		t.Errorf("got %+v", got)
	}
}

// parseRequires drops entries without a skill name.
func TestParseRequires_DropEmpty(t *testing.T) {
	got := parseRequires([]any{
		map[string]any{"reason": "no skill name"},
		"valid",
	}, "x")
	if len(got) != 1 {
		t.Fatalf("expected 1 require (empty dropped), got %d", len(got))
	}
}

// parseRequires returns nil for nil input.
func TestParseRequires_Nil(t *testing.T) {
	if got := parseRequires(nil, "x"); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

// expandRequires walks a simple A→B→C chain depth-first.
func TestExpandRequires_TransitiveChain(t *testing.T) {
	a := Skill{Name: "a", Requires: []Requirement{{Skill: "b"}}}
	b := Skill{Name: "b", Requires: []Requirement{{Skill: "c"}}}
	c := Skill{Name: "c"}
	byName := map[string]Skill{"a": a, "b": b, "c": c}
	got := expandRequires([]Skill{a}, byName)
	if len(got) != 2 {
		t.Fatalf("expected 2 dependencies (b, c), got %d: %+v", len(got), got)
	}
	// c (transitive) must come before b (direct).
	if got[0].Name != "c" || got[1].Name != "b" {
		t.Fatalf("expected order [c, b], got [%s, %s]", got[0].Name, got[1].Name)
	}
}

// expandRequires breaks cycles silently.
func TestExpandRequires_CycleBreak(t *testing.T) {
	a := Skill{Name: "a", Requires: []Requirement{{Skill: "b"}}}
	b := Skill{Name: "b", Requires: []Requirement{{Skill: "a"}}}
	byName := map[string]Skill{"a": a, "b": b}
	got := expandRequires([]Skill{a}, byName)
	// a is the seed (already visited); b is its only dep.
	if len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("expected [b], got %+v", got)
	}
}

// expandRequires handles missing dependency without panicking.
func TestExpandRequires_MissingDependency(t *testing.T) {
	a := Skill{Name: "a", Requires: []Requirement{{Skill: "ghost"}}}
	byName := map[string]Skill{"a": a}
	got := expandRequires([]Skill{a}, byName)
	if len(got) != 0 {
		t.Fatalf("expected empty (missing dep skipped), got %+v", got)
	}
}

// expandRequires de-duplicates a dependency reached via multiple paths.
func TestExpandRequires_DiamondDedup(t *testing.T) {
	a := Skill{Name: "a", Requires: []Requirement{{Skill: "b"}, {Skill: "c"}}}
	b := Skill{Name: "b", Requires: []Requirement{{Skill: "d"}}}
	c := Skill{Name: "c", Requires: []Requirement{{Skill: "d"}}}
	d := Skill{Name: "d"}
	byName := map[string]Skill{"a": a, "b": b, "c": c, "d": d}
	got := expandRequires([]Skill{a}, byName)
	if len(got) != 3 {
		t.Fatalf("expected 3 unique deps (d, b, c), got %d: %+v", len(got), got)
	}
}

// ResolveForQuery pulls in required skills and labels them in Origin.
func TestResolveForQuery_RequiresExpansion(t *testing.T) {
	tmp := t.TempDir()
	skillsDir := filepath.Join(tmp, ".dfmc", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Custom skill that requires a builtin.
	body := `---
name: hardening
description: Project hardening playbook
allowed-tools: read_file
requires:
  - skill: audit
    reason: security baseline
  - onboard
triggers:
  - pattern: "harden(ing)?"
    weight: 0.9
---
# Hardening

Run baselines first.`
	path := filepath.Join(skillsDir, "hardening", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	sel := ResolveForQuery(tmp, "harden the auth subsystem", "")
	// Expected order: required (audit, onboard) BEFORE primary (hardening).
	names := make([]string, 0, len(sel.Skills))
	for _, s := range sel.Skills {
		names = append(names, s.Name)
	}
	if len(names) < 3 {
		t.Fatalf("expected at least 3 skills (audit, onboard, hardening), got %v", names)
	}
	if names[len(names)-1] != "hardening" {
		t.Errorf("expected primary 'hardening' last, got %v", names)
	}
	if sel.Origin["hardening"] != "trigger" {
		t.Errorf("expected origin 'trigger' for hardening, got %q", sel.Origin["hardening"])
	}
	if sel.Origin["audit"] != "required" {
		t.Errorf("expected origin 'required' for audit, got %q", sel.Origin["audit"])
	}
	if sel.Origin["onboard"] != "required" {
		t.Errorf("expected origin 'required' for onboard, got %q", sel.Origin["onboard"])
	}
}
