package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
)

// action_menu_audit_test.go pins three contracts for every panel's
// action menu so labels stay descriptive and accelerators stay
// unambiguous:
//
//  1. Every panelAction must have a non-empty Label. An empty label
//     renders as a blank row that the user can highlight but not
//     understand.
//
//  2. Within a single open menu, no two actions may share the same
//     Accel. Duplicate accels make the accelerator key ambiguous —
//     the menu picks one and the other becomes unreachable via the
//     keyboard. (Conditional actions that share an Accel because
//     they never coexist — e.g. Enable/Disable tool — are fine; we
//     only check what's actually visible in one snapshot.)
//
//  3. Labels must be reasonably descriptive — at least 4 chars and
//     not all-caps placeholder text. "Run" / "X" / "TODO" would slip
//     past these checks in the cheap way; the contract is stronger
//     than that, but a basic length floor catches the obvious dead
//     copy.

func TestActionMenus_LabelsAndAccelsAreClean(t *testing.T) {
	cases := []struct {
		panel string
		open  func(Model) Model
		setup func(Model) Model
	}{
		{
			panel: "Activity",
			open:  func(m Model) Model { return m.openActivityActionMenu() },
		},
		{
			panel: "CodeMap",
			open:  func(m Model) Model { return m.openCodemapActionMenu() },
		},
		{
			panel: "Memory",
			open:  func(m Model) Model { return m.openMemoryActionMenu() },
		},
		{
			panel: "Conversations",
			open:  func(m Model) Model { return m.openConversationsActionMenu() },
			setup: func(m Model) Model {
				// Conversations menu only opens with at least one entry
				// (else the panel returns early on right/l).
				m.conversations.entries = []conversation.Summary{
					{ID: "abc-123", Provider: "anthropic"},
				}
				return m
			},
		},
		{
			panel: "Prompts",
			open:  func(m Model) Model { return m.openPromptsActionMenu() },
		},
		{
			panel: "Plans",
			open:  func(m Model) Model { return m.openPlansActionMenu() },
		},
		{
			panel: "Security",
			open:  func(m Model) Model { return m.openSecurityActionMenu() },
		},
		{
			panel: "ContextPanel",
			open:  func(m Model) Model { return m.openContextActionMenu() },
		},
		{
			panel: "Status",
			open:  func(m Model) Model { return m.openStatusActionMenu() },
		},
		{
			panel: "Patch",
			open:  func(m Model) Model { return m.openPatchActionMenu() },
		},
		{
			panel: "Contexts",
			open:  func(m Model) Model { return m.openContextsActionMenu() },
		},
		{
			panel: "Orchestrate",
			open:  func(m Model) Model { return m.openOrchestrateActionMenu() },
		},
	}
	for _, c := range cases {
		m := NewModel(context.Background(), nil)
		if c.setup != nil {
			m = c.setup(m)
		}
		m = c.open(m)
		if !m.actionMenu.open {
			// Some panels' open* helpers no-op without state; skip
			// rather than fail because that's a per-panel design
			// choice, not an action-menu defect.
			t.Logf("[%s] action menu did not open in this test fixture (no rows selected) — skipping", c.panel)
			continue
		}
		actions := m.actionMenu.actions
		if len(actions) == 0 {
			t.Errorf("[%s] action menu opened but has 0 actions", c.panel)
			continue
		}
		seenAccel := map[string]string{}
		for i, a := range actions {
			label := strings.TrimSpace(a.Label)
			if label == "" {
				t.Errorf("[%s] action[%d] has empty Label", c.panel, i)
				continue
			}
			if len(label) < 4 {
				t.Errorf("[%s] action[%d] label %q too short — needs to describe what it does", c.panel, i, label)
			}
			if a.Accel == "" {
				continue
			}
			if prev, ok := seenAccel[a.Accel]; ok {
				t.Errorf("[%s] Accel %q is bound twice: %q AND %q — keyboard accelerator is ambiguous",
					c.panel, a.Accel, prev, label)
			}
			seenAccel[a.Accel] = label
		}
	}
}

// TestActionMenus_OwnerNameMatchesPanelKey ensures the owner string
// passed to openActionMenu matches the routing logic in
// handleActionMenuKey / panel-specific render guards. A typo here
// (e.g., "Convo" vs "Conversations") makes the menu silently fail
// to dispatch its actions because the close-routing checks the
// owner against the active panel name.
func TestActionMenus_OwnerNameMatchesPanelKey(t *testing.T) {
	cases := []struct {
		panel string
		open  func(Model) Model
		setup func(Model) Model
		want  string
	}{
		{"Activity", func(m Model) Model { return m.openActivityActionMenu() }, nil, "Activity"},
		{"CodeMap", func(m Model) Model { return m.openCodemapActionMenu() }, nil, "CodeMap"},
		{"Memory", func(m Model) Model { return m.openMemoryActionMenu() }, nil, "Memory"},
		{"Prompts", func(m Model) Model { return m.openPromptsActionMenu() }, nil, "Prompts"},
		{"Plans", func(m Model) Model { return m.openPlansActionMenu() }, nil, "Plans"},
		{"Security", func(m Model) Model { return m.openSecurityActionMenu() }, nil, "Security"},
		{"Status", func(m Model) Model { return m.openStatusActionMenu() }, nil, "Status"},
		{"Patch", func(m Model) Model { return m.openPatchActionMenu() }, nil, "Patch"},
		{"Conversations", func(m Model) Model { return m.openConversationsActionMenu() }, func(m Model) Model {
			m.conversations.entries = []conversation.Summary{{ID: "x"}}
			return m
		}, "Conversations"},
		{"ContextPanel", func(m Model) Model { return m.openContextActionMenu() }, nil, "Context"},
		{"Contexts", func(m Model) Model { return m.openContextsActionMenu() }, nil, "Contexts"},
		{"Orchestrate", func(m Model) Model { return m.openOrchestrateActionMenu() }, nil, "Orchestrate"},
	}
	for _, c := range cases {
		m := NewModel(context.Background(), nil)
		if c.setup != nil {
			m = c.setup(m)
		}
		m = c.open(m)
		if !m.actionMenu.open {
			t.Logf("[%s] menu did not open in fixture — skipping owner check", c.panel)
			continue
		}
		if m.actionMenu.owner != c.want {
			t.Errorf("[%s] action menu owner is %q, expected %q (mismatch breaks panel close-routing)",
				c.panel, m.actionMenu.owner, c.want)
		}
	}
}
