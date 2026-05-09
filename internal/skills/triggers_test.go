package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// parseTriggers handles plain string list with default weight.
func TestParseTriggers_StringList(t *testing.T) {
	got := parseTriggers([]any{"foo|bar", "baz"}, "test-skill")
	if len(got) != 2 {
		t.Fatalf("expected 2 triggers, got %d", len(got))
	}
	if got[0].Weight != defaultTriggerWeight {
		t.Errorf("expected default weight, got %v", got[0].Weight)
	}
	if !got[0].Pattern.MatchString("FOO is here") {
		t.Errorf("expected case-insensitive match")
	}
}

// parseTriggers handles object form with explicit weight.
func TestParseTriggers_ObjectWithWeight(t *testing.T) {
	got := parseTriggers([]any{
		map[string]any{"pattern": "vulnerability", "weight": 0.95},
	}, "audit")
	if len(got) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(got))
	}
	if got[0].Weight != 0.95 {
		t.Errorf("expected weight 0.95, got %v", got[0].Weight)
	}
}

// parseTriggers handles inline weight via "<pattern>:<weight>".
func TestParseTriggers_InlineWeight(t *testing.T) {
	got := parseTriggers([]any{"sql.?inject:0.9"}, "audit")
	if len(got) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(got))
	}
	if got[0].Weight != 0.9 {
		t.Errorf("expected weight 0.9, got %v", got[0].Weight)
	}
	if got[0].Raw != "sql.?inject" {
		t.Errorf("expected pattern stripped of weight suffix, got %q", got[0].Raw)
	}
}

// parseTriggers drops invalid regex patterns silently.
func TestParseTriggers_InvalidRegexDropped(t *testing.T) {
	got := parseTriggers([]any{"valid", "[unterminated"}, "skill")
	if len(got) != 1 {
		t.Fatalf("expected 1 trigger (invalid dropped), got %d", len(got))
	}
}

// parseTriggers handles single-string YAML shape.
func TestParseTriggers_SingleString(t *testing.T) {
	got := parseTriggers("foo|bar", "skill")
	if len(got) != 1 {
		t.Fatalf("expected 1 trigger, got %d", len(got))
	}
}

// parseTriggers returns nil for nil input.
func TestParseTriggers_Nil(t *testing.T) {
	if got := parseTriggers(nil, "skill"); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

// matchTriggers picks the highest-weighted match.
func TestMatchTriggers_PicksHighestWeight(t *testing.T) {
	catalog := []Skill{
		{Name: "low", Triggers: []Trigger{builtinTrigger(`hello`, 0.7)}},
		{Name: "high", Triggers: []Trigger{builtinTrigger(`hello`, 0.9)}},
	}
	got := matchTriggers(catalog, "hello world")
	if got != "high" {
		t.Fatalf("expected 'high', got %q", got)
	}
}

// matchTriggers returns empty when no pattern clears MinTriggerScore.
func TestMatchTriggers_BelowThreshold(t *testing.T) {
	catalog := []Skill{
		{Name: "weak", Triggers: []Trigger{builtinTrigger(`hello`, 0.4)}},
	}
	if got := matchTriggers(catalog, "hello world"); got != "" {
		t.Fatalf("expected no match below threshold, got %q", got)
	}
}

// matchTriggers returns empty when no pattern matches.
func TestMatchTriggers_NoMatch(t *testing.T) {
	catalog := []Skill{
		{Name: "x", Triggers: []Trigger{builtinTrigger(`security`, 0.9)}},
	}
	if got := matchTriggers(catalog, "make me a sandwich"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

// matchTriggers returns empty for empty query.
func TestMatchTriggers_EmptyQuery(t *testing.T) {
	catalog := []Skill{
		{Name: "x", Triggers: []Trigger{builtinTrigger(`security`, 0.9)}},
	}
	if got := matchTriggers(catalog, ""); got != "" {
		t.Fatalf("expected empty for empty query, got %q", got)
	}
}

// ResolveForQuery activates a skill via trigger when no explicit marker is present.
func TestResolveForQuery_TriggerActivation(t *testing.T) {
	sel := ResolveForQuery("", "find any security vulnerabilities in this repo", "")
	if len(sel.Skills) == 0 {
		t.Fatal("expected at least one skill via trigger")
	}
	if !sel.Triggered {
		t.Errorf("expected Triggered=true")
	}
	if sel.Skills[0].Name != "audit" {
		t.Errorf("expected audit skill, got %q", sel.Skills[0].Name)
	}
	if sel.Origin["audit"] != "trigger" {
		t.Errorf("expected origin 'trigger', got %q", sel.Origin["audit"])
	}
}

// ResolveForQuery prefers explicit markers over triggers.
func TestResolveForQuery_ExplicitWinsOverTrigger(t *testing.T) {
	// Query mentions security (would trigger audit) but explicitly asks for review.
	sel := ResolveForQuery("", "[[skill:review]] check this for security issues", "")
	if !sel.Explicit {
		t.Fatal("expected Explicit=true")
	}
	if sel.Triggered {
		t.Errorf("explicit marker should suppress trigger fallback")
	}
	if sel.Skills[0].Name != "review" {
		t.Errorf("expected review (explicit), got %q", sel.Skills[0].Name)
	}
}

// ResolveForQuery falls through to task hint when neither explicit nor trigger fires.
func TestResolveForQuery_TaskFallbackStillWorks(t *testing.T) {
	sel := ResolveForQuery("", "make me a sandwich please", "review")
	if len(sel.Skills) == 0 {
		t.Fatal("expected task fallback to fire")
	}
	if sel.Triggered {
		t.Errorf("expected Triggered=false (task fallback, not trigger)")
	}
	if sel.Origin["review"] != "task" {
		t.Errorf("expected origin 'task', got %q", sel.Origin["review"])
	}
}

// SKILL.md with `triggers:` field is parsed.
func TestReadSkillFile_TriggersField(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "custom-audit", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `---
name: custom-audit
description: Project-specific security review
allowed-tools: read_file grep_codebase
version: 1.2.0
metadata:
  author: Security Team
  tags: [security, internal]
triggers:
  - pattern: "internal[-_]secret"
    weight: 0.95
  - "compliance|sox|pci"
---
# Custom Audit

Look for internal patterns.`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	item := readSkillFile(path, "project")
	if item.Name != "custom-audit" {
		t.Fatalf("expected name 'custom-audit', got %q", item.Name)
	}
	if len(item.Triggers) != 2 {
		t.Fatalf("expected 2 triggers, got %d", len(item.Triggers))
	}
	if item.Triggers[0].Weight != 0.95 {
		t.Errorf("expected first trigger weight 0.95, got %v", item.Triggers[0].Weight)
	}
	if item.Version != "1.2.0" {
		t.Errorf("expected version 1.2.0, got %q", item.Version)
	}
	if item.Author != "Security Team" {
		t.Errorf("expected author 'Security Team', got %q", item.Author)
	}
	if len(item.Tags) != 2 {
		t.Errorf("expected 2 tags, got %v", item.Tags)
	}
}
