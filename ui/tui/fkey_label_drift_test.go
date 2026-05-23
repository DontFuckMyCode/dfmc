package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// fkey_label_drift_test.go pins the F-key → tab label mapping in copy
// that surfaces to users (status panel jump strip, mention-picker
// reload hint). Earlier copy claimed `F3 Files` and `F4 Patch` — the
// real map (shortcut_panels.go) is F2 Files, F3 Patch. A user
// chasing the misleading hint would land on the wrong tab.

func TestStatusPanel_FKeyLabelsMatchShortcutMap(t *testing.T) {
	m := makeModelForStatusPanel(t)
	out := ansi.Strip(m.renderStatusView(140))
	if strings.Contains(out, "F3 Files") {
		t.Errorf("status panel still claims F3 Files — real map is F2 Files. Got:\n%s", out)
	}
	if strings.Contains(out, "F4 Patch") {
		t.Errorf("status panel still claims F4 Patch — real map is F3 Patch. Got:\n%s", out)
	}
	if !strings.Contains(out, "F2 Files") {
		t.Errorf("status panel must name F2 as the Files tab, got:\n%s", out)
	}
	if !strings.Contains(out, "F3 Patch") {
		t.Errorf("status panel must name F3 as the Patch tab, got:\n%s", out)
	}
}

func makeModelForStatusPanel(t *testing.T) Model {
	t.Helper()
	m := NewModel(context.Background(), nil)
	return m
}
