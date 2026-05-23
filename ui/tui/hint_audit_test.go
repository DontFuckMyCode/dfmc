package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// hint_audit_test.go pins the affordance hint contracts so silent
// regressions (a hint advertising a key the handler doesn't support
// — Status used to claim `/ search` even though the panel has no
// search, Plans and Context Panel used to claim `/ search` even
// though they use `e` to edit) get caught immediately.
//
// Tests render each panel at a reasonable width and assert what the
// hint MUST say and what it MUST NOT say.

func TestStatusHint_OnlyAdvertisesRealKeys(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = m.activityTabIndex("Status")
	if m.activeTab < 0 {
		t.Skip("Status tab not in fixture")
	}
	view := ansi.Strip(m.renderStatusView(140))
	// Status panel has no search — hint must NOT lie about it.
	if strings.Contains(view, "/ search") {
		t.Errorf("Status hint must not claim '/ search' — handler has no `/` key. Got:\n%s", view)
	}
	for _, want := range []string{"hjkl", "enter jump to detail", "r reload"} {
		if !strings.Contains(view, want) {
			t.Errorf("Status hint missing %q. Got:\n%s", want, view)
		}
	}
}

func TestPlansHint_OnlyAdvertisesRealKeys(t *testing.T) {
	m := NewModel(context.Background(), nil)
	view := ansi.Strip(m.renderPlansView(140))
	// Plans uses `e` to edit task, not `/`. The hint must NOT claim search.
	if strings.Contains(view, "/ search") {
		t.Errorf("Plans hint must not claim '/ search' — handler uses `e` to edit. Got:\n%s", view)
	}
	for _, want := range []string{"e edit task", "enter re-run"} {
		if !strings.Contains(view, want) {
			t.Errorf("Plans hint missing %q. Got:\n%s", want, view)
		}
	}
}

func TestContextPanelHint_OnlyAdvertisesRealKeys(t *testing.T) {
	m := NewModel(context.Background(), nil)
	view := ansi.Strip(m.renderContextView(140))
	if strings.Contains(view, "/ search") {
		t.Errorf("Context panel hint must not claim '/ search' — handler uses `e` to edit. Got:\n%s", view)
	}
	for _, want := range []string{"e edit query", "enter preview"} {
		if !strings.Contains(view, want) {
			t.Errorf("Context panel hint missing %q. Got:\n%s", want, view)
		}
	}
}

func TestMemoryHint_NamesTheActualKeys(t *testing.T) {
	m := NewModel(context.Background(), nil)
	view := ansi.Strip(m.renderMemoryView(140))
	for _, want := range []string{"enter expand", "/ search", "t tier", "r reload", "→ action menu"} {
		if !strings.Contains(view, want) {
			t.Errorf("Memory hint missing %q. Got:\n%s", want, view)
		}
	}
	// Esc only cancels search mode — does NOT take you back from the
	// panel. The hint must not claim otherwise.
	if strings.Contains(view, "esc back") {
		t.Errorf("Memory hint claims 'esc back' but esc only exits search mode. Got:\n%s", view)
	}
}
