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
	if m.mouseCaptureEnabled {
		t.Fatal("mouse capture should start off by default")
	}
	next, _, handled := m.executeChatCommand("/mouse")
	if !handled {
		t.Fatal("/mouse must be handled")
	}
	nm := next.(Model)
	if !nm.mouseCaptureEnabled {
		t.Fatal("first /mouse should turn capture on")
	}
	if !strings.Contains(strings.ToLower(nm.notice), "on") {
		t.Fatalf("notice should announce capture on, got: %q", nm.notice)
	}
}

func TestSlashMouse_TogglesBackOff(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	m.mouseCaptureEnabled = true

	next, _, _ := m.executeChatCommand("/mouse")
	nm := next.(Model)
	if nm.mouseCaptureEnabled {
		t.Fatal("/mouse on a capture-on model should flip it off")
	}
	if !strings.Contains(strings.ToLower(nm.notice), "off") {
		t.Fatalf("notice should announce capture off, got: %q", nm.notice)
	}
	if !strings.Contains(strings.ToLower(nm.notice), "select") {
		t.Fatalf("off notice should hint at drag-to-select, got: %q", nm.notice)
	}
}
