// /mouse slash flips bubbletea's mouse capture at runtime so users who
// want terminal drag-to-select / right-click-copy don't have to restart
// or edit config. These tests pin state transitions; actual enable /
// disable commands are opaque tea.Msg values so we only assert the
// boolean flip + notice wording.

package tui

import (
	"context"
	"strings"
	"testing"
)

func TestSlashMouse_TogglesFromOffToOn(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	// Default is off (tui.mouse_capture defaults to false).
	if m.ui.mouseCaptureEnabled {
		t.Fatal("mouse capture should start off by default")
	}
	next, _, handled := m.executeChatCommand("/mouse")
	if !handled {
		t.Fatal("/mouse must be handled")
	}
	nm := next.(Model)
	if !nm.ui.mouseCaptureEnabled {
		t.Fatal("first /mouse should turn capture on")
	}
	if !strings.Contains(strings.ToLower(nm.notice), "on") {
		t.Fatalf("notice should announce capture on, got: %q", nm.notice)
	}
}

func TestSlashMouse_TogglesBackOff(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.ui.mouseCaptureEnabled = true

	next, _, _ := m.executeChatCommand("/mouse")
	nm := next.(Model)
	if nm.ui.mouseCaptureEnabled {
		t.Fatal("/mouse on a capture-on model should flip it off")
	}
	if !strings.Contains(strings.ToLower(nm.notice), "off") {
		t.Fatalf("notice should announce capture off, got: %q", nm.notice)
	}
	if !strings.Contains(strings.ToLower(nm.notice), "select") {
		t.Fatalf("off notice should hint at drag-to-select, got: %q", nm.notice)
	}
}

func TestSlashSelect_TogglesSelectionModeAndRestoresLayout(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.ui.showStatsPanel = true
	m.ui.mouseCaptureEnabled = true

	next, _, handled := m.executeChatCommand("/select")
	if !handled {
		t.Fatal("/select must be handled")
	}
	nm := next.(Model)
	if !nm.ui.selectionModeActive {
		t.Fatal("/select should enable selection mode")
	}
	if nm.ui.showStatsPanel {
		t.Fatal("selection mode should hide stats panel")
	}
	if nm.ui.mouseCaptureEnabled {
		t.Fatal("selection mode should disable mouse capture")
	}

	next2, _, handled := nm.executeChatCommand("/select")
	if !handled {
		t.Fatal("second /select must be handled")
	}
	restored := next2.(Model)
	if restored.ui.selectionModeActive {
		t.Fatal("second /select should disable selection mode")
	}
	if !restored.ui.showStatsPanel {
		t.Fatal("second /select should restore stats panel state")
	}
	if !restored.ui.mouseCaptureEnabled {
		t.Fatal("second /select should restore mouse capture state")
	}
}
