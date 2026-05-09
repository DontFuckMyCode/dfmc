package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Explain on a security-flavoured query identifies audit as the active
// trigger-driven skill and surfaces the matched pattern + weight.
func TestExplain_TriggerActivation(t *testing.T) {
	exp := Explain("", "find any security vulnerabilities in this repo")
	if len(exp.Active) == 0 {
		t.Fatalf("expected at least one active skill, got %+v", exp)
	}
	got := exp.Active[0]
	if got.Name != "audit" {
		t.Errorf("expected 'audit', got %q", got.Name)
	}
	if got.Origin != "trigger" {
		t.Errorf("expected origin 'trigger', got %q", got.Origin)
	}
	if got.MatchedPattern == "" {
		t.Error("expected matched_pattern to be populated for trigger origin")
	}
	if got.Weight < MinTriggerScore {
		t.Errorf("expected weight >= MinTriggerScore (%v), got %v", MinTriggerScore, got.Weight)
	}
}

// Explain on an explicit marker query reports origin=explicit.
func TestExplain_ExplicitMarker(t *testing.T) {
	exp := Explain("", "[[skill:review]] check this code")
	if len(exp.Active) == 0 {
		t.Fatal("expected at least one active skill")
	}
	if exp.Active[0].Origin != "explicit" {
		t.Errorf("expected origin 'explicit', got %q", exp.Active[0].Origin)
	}
	if !strings.Contains(exp.Active[0].Reason, "[[skill:review]]") {
		t.Errorf("reason should mention the marker; got %q", exp.Active[0].Reason)
	}
}

// Explain on an unrelated query returns no active skills.
func TestExplain_NoMatch(t *testing.T) {
	exp := Explain("", "What's the weather like in Istanbul today?")
	for _, a := range exp.Active {
		if a.Origin == "trigger" {
			t.Errorf("did not expect trigger activation for unrelated query, got %+v", a)
		}
	}
}

// Explain surfaces near-misses: skills whose triggers matched but
// lost to a higher-weighted winner.
func TestExplain_NearMissesPopulated(t *testing.T) {
	tmp := t.TempDir()
	skillsDir := filepath.Join(tmp, ".dfmc", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Custom skill with a trigger that overlaps with builtin 'audit'
	// (which fires on 'security' patterns at weight 0.95). A weight
	// of 0.7 here is below audit's, so this should appear as a
	// near-miss when audit wins.
	body := `---
name: custom-sec
description: project-specific security checks
allowed-tools: read_file
triggers:
  - pattern: "security"
    weight: 0.7
---
# Custom Sec

Body.`
	path := filepath.Join(skillsDir, "custom-sec", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	exp := Explain(tmp, "look for security vulnerabilities")
	if len(exp.Active) == 0 {
		t.Fatal("expected an active skill")
	}
	if exp.Active[0].Name != "audit" {
		t.Errorf("expected audit to win (higher weight), got %q", exp.Active[0].Name)
	}
	// custom-sec must show up as near-miss — its 0.7 weight matched
	// but lost to audit's 0.95.
	foundCustom := false
	for _, m := range exp.NearMisses {
		if m.Name == "custom-sec" {
			foundCustom = true
			if m.Weight != 0.7 {
				t.Errorf("expected near-miss weight 0.7, got %v", m.Weight)
			}
		}
	}
	if !foundCustom {
		t.Errorf("expected custom-sec in near-misses, got %+v", exp.NearMisses)
	}
}

// Sub-threshold: a trigger that matched but its weight is below
// MinTriggerScore so it never fires.
func TestExplain_SubThreshold(t *testing.T) {
	tmp := t.TempDir()
	skillsDir := filepath.Join(tmp, ".dfmc", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `---
name: weak-trigger
description: too-low weight
allowed-tools: read_file
triggers:
  - pattern: "specific-niche-token"
    weight: 0.3
---
# Weak

Body.`
	path := filepath.Join(skillsDir, "weak-trigger", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	exp := Explain(tmp, "I need help with a specific-niche-token problem")
	foundSub := false
	for _, s := range exp.SubThreshold {
		if s.Name == "weak-trigger" {
			foundSub = true
			if s.Weight != 0.3 {
				t.Errorf("expected weight 0.3, got %v", s.Weight)
			}
			if !strings.Contains(s.Reason, "MinTriggerScore") {
				t.Errorf("sub-threshold reason should mention MinTriggerScore; got %q", s.Reason)
			}
		}
	}
	if !foundSub {
		t.Errorf("expected weak-trigger in sub-threshold, got %+v", exp.SubThreshold)
	}
	// And the loser must NOT appear in active.
	for _, a := range exp.Active {
		if a.Name == "weak-trigger" {
			t.Errorf("weak-trigger should not be active (below threshold), got %+v", a)
		}
	}
}

// CleanQuery is what's left after explicit markers are stripped.
func TestExplain_CleanQueryStripsMarkers(t *testing.T) {
	exp := Explain("", "[[skill:review]] please look at this code")
	if strings.Contains(exp.CleanQuery, "[[skill:review]]") {
		t.Errorf("clean_query should have markers stripped; got %q", exp.CleanQuery)
	}
	if !strings.Contains(exp.CleanQuery, "please look at this code") {
		t.Errorf("clean_query should retain the user prose; got %q", exp.CleanQuery)
	}
}

// Explain on an empty query returns no active skills, no panic.
func TestExplain_EmptyQuery(t *testing.T) {
	exp := Explain("", "")
	if len(exp.Active) != 0 {
		t.Errorf("expected 0 active for empty query, got %+v", exp.Active)
	}
}

// `requires` chain: when an explicit skill is activated and it has
// requires, those show up with origin=required.
func TestExplain_RequiresOrigin(t *testing.T) {
	tmp := t.TempDir()
	skillsDir := filepath.Join(tmp, ".dfmc", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `---
name: composite
description: composite skill
requires:
  - skill: audit
    reason: security check
---
# Composite

Body.`
	path := filepath.Join(skillsDir, "composite", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	exp := Explain(tmp, "[[skill:composite]] do the thing")
	foundRequired := false
	for _, a := range exp.Active {
		if a.Name == "audit" && a.Origin == "required" {
			foundRequired = true
		}
	}
	if !foundRequired {
		t.Errorf("expected audit with origin=required in active list, got %+v", exp.Active)
	}
}
