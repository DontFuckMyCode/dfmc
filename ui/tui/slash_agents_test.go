package tui

import (
	"context"
	"strings"
	"testing"
)

// TestSlashAgents_ListShowsRolesAndProfiles asserts the /agents catalog
// renders BOTH the role personalities (loaded from the embedded prompt
// library) AND the provider profiles (read off Engine.Config.Providers),
// so users see the two halves of the sub-agent surface in one card.
func TestSlashAgents_ListShowsRolesAndProfiles(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)

	out := m.agentsSlash(nil)
	if !strings.Contains(out, "Sub-agent catalog") {
		t.Fatalf("expected catalog header, got:\n%s", out)
	}
	if !strings.Contains(out, "Roles") {
		t.Fatalf("expected Roles section, got:\n%s", out)
	}
	if !strings.Contains(out, "Profiles") {
		t.Fatalf("expected Profiles section, got:\n%s", out)
	}
	// Embedded defaults register at minimum: planner, researcher, debugger,
	// code_reviewer, test_engineer, security_auditor, drive_executor. We
	// don't pin the full list (the YAML may grow) — just sanity-check that
	// at least one canonical role is surfaced.
	for _, must := range []string{"planner", "researcher"} {
		if !strings.Contains(out, must) {
			t.Errorf("expected role %q in /agents output:\n%s", must, out)
		}
	}
	// The trailing tip should explain how to switch sub-agent runtime,
	// otherwise the catalog is just a wall of names.
	if !strings.Contains(out, "show <name>") {
		t.Errorf("/agents list should hint at /agents show <name>, got:\n%s", out)
	}
}

// TestSlashAgents_ShowUnknownDegrades asserts the unknown-name path
// degrades helpfully instead of silently returning empty (matches the
// /retry, /edit, /export contract).
func TestSlashAgents_ShowUnknownDegrades(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)

	out := m.agentsSlash([]string{"show", "definitely-not-a-real-name-xyz"})
	if !strings.Contains(strings.ToLower(out), "no role or profile") {
		t.Fatalf("unknown name should explain itself, got:\n%s", out)
	}
}

// TestSlashAgents_ShowKnownRoleBody asserts /agents show <role> renders
// the overlay body (not just the headline) so users can see what
// "researcher" actually does without reading source.
func TestSlashAgents_ShowKnownRoleBody(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)

	cat := m.eng.Agents()
	if len(cat.Roles) == 0 {
		t.Skip("no roles loaded — embedded defaults missing")
	}
	target := cat.Roles[0].Role
	out := m.agentsSlash([]string{"show", target})
	if !strings.Contains(out, target) {
		t.Fatalf("show body should contain role name %q:\n%s", target, out)
	}
	if !strings.Contains(out, "primary overlay body") {
		t.Fatalf("show body should print primary overlay body marker, got:\n%s", out)
	}
}

// TestSlashAgents_UnknownSubcommand keeps the error path consistent with
// the other slash families (skill, prompt, memory).
func TestSlashAgents_UnknownSubcommand(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)

	out := m.agentsSlash([]string{"invent-a-verb"})
	if !strings.Contains(strings.ToLower(out), "unknown subcommand") {
		t.Fatalf("unknown sub should be named, got:\n%s", out)
	}
}
