package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestToolStatusPanel_EnterTogglesExpanded — Enter (and 'x') flip the
// toolStatus.expanded flag and reset scroll so the user lands at the
// newest entry in the new layout. Without the reset the offset would
// be measured against the old (smaller) line count and would clip the
// top of the expanded view.
func TestToolStatusPanel_EnterTogglesExpanded(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.toolStatus.scroll = 12
	m.toolStatus.expanded = false

	out, handled := m.handleToolStatusKey("enter")
	if !handled {
		t.Fatal("enter should be handled by toolStatus panel")
	}
	if !out.toolStatus.expanded {
		t.Errorf("enter should turn expanded ON, got false")
	}
	if out.toolStatus.scroll != 0 {
		t.Errorf("toggle should reset scroll to 0, got %d", out.toolStatus.scroll)
	}

	// Second press flips back.
	out, _ = out.handleToolStatusKey("x")
	if out.toolStatus.expanded {
		t.Errorf("second toggle should turn expanded OFF, got true")
	}
}

// TestRenderToolStatus_ExpandedShowsFullError — in expanded mode the
// renderer must include the full multi-line error body, not just the
// first line. The compact mode clips with truncateSingleLine which
// hides exactly the detail users open the panel to see.
func TestRenderToolStatus_ExpandedShowsFullError(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.toolCallLog.entries = []toolCallLogEntry{{
		ToolName:   "edit_file",
		Status:     "failed",
		StartedAt:  time.Now().Add(-5 * time.Second),
		FinishedAt: time.Now(),
		DurationMs: 42,
		Step:       3,
		Error:      "line 1 of error\nline 2 explains why\nline 3 has the fix hint",
	}}

	// Expanded mode: each error line is rendered as its own indented
	// sub-row under a labeled "error:" header so the user can read the
	// whole message instead of squinting at one wrapped paragraph.
	m.toolStatus.expanded = true
	expanded := stripANSI(m.renderToolStatusViewSized(100, 40))
	for _, want := range []string{
		"error:",
		"line 1 of error",
		"line 2 explains why",
		"line 3 has the fix hint",
	} {
		if !strings.Contains(expanded, want) {
			t.Errorf("expanded mode missing %q, got:\n%s", want, expanded)
		}
	}
	if !strings.Contains(expanded, "mode: expanded") {
		t.Errorf("footer should advertise current mode, got:\n%s", expanded)
	}

	// Compact mode footer must advertise "compact" so the user knows
	// the toggle exists and which mode they're in.
	m.toolStatus.expanded = false
	compact := stripANSI(m.renderToolStatusViewSized(100, 40))
	if !strings.Contains(compact, "mode: compact") {
		t.Errorf("compact footer should advertise current mode, got:\n%s", compact)
	}
	if !strings.Contains(compact, "enter/x toggle") {
		t.Errorf("footer should teach the toggle keybinding, got:\n%s", compact)
	}
}

// TestRenderToolStatus_ExpandedShowsFullResult mirrors the error
// coverage for successful tool calls — params + result both honor the
// multi-line layout.
func TestRenderToolStatus_ExpandedShowsFullResult(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.toolCallLog.entries = []toolCallLogEntry{{
		ToolName:   "read_file",
		Status:     "ok",
		StartedAt:  time.Now().Add(-1 * time.Second),
		FinishedAt: time.Now(),
		DurationMs: 8,
		Params:     `{"path":"main.go","line_start":1,"line_end":50}`,
		Result:     "package main\n\nimport \"fmt\"\n\nfunc main() {\n  fmt.Println(\"hi\")\n}",
	}}
	m.toolStatus.expanded = true
	out := stripANSI(m.renderToolStatusViewSized(100, 60))
	for _, want := range []string{
		`"path":"main.go"`,
		"package main",
		`fmt.Println("hi")`,
		"params:",
		"result:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expanded mode missing %q, got:\n%s", want, out)
		}
	}
}

// TestToolStatusOverlayKey_RoutesEnterToToggle ensures the bubbletea
// wrapper handleToolStatusOverlayKey forwards Enter to the toggle
// handler (the overlay close path swallows esc/q first, but enter
// must reach the panel-specific handler).
func TestToolStatusOverlayKey_RoutesEnterToToggle(t *testing.T) {
	m := NewModel(context.Background(), nil)
	out, _ := m.handleToolStatusOverlayKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !out.(Model).toolStatus.expanded {
		t.Errorf("overlay key router should toggle expanded on enter, got false")
	}
}
