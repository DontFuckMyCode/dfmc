package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// TestReadFileChip_PrefixesLineTruncation verifies that a read_file
// tool:result whose returned_lines < total_lines prepends a
// "<returned>/<total> lines · <omitted> omitted" prefix to the chip
// preview, so the user can see at a glance that the model worked off
// a partial slice. The default 200-line cap is otherwise invisible.
func TestReadFileChip_PrefixesLineTruncation(t *testing.T) {
	m := NewModel(context.Background(), nil)

	m = m.handleEngineEvent(engine.Event{
		Type: "tool:call",
		Payload: map[string]any{
			"tool":           "read_file",
			"step":           1,
			"params_preview": "path=server.go",
		},
	})

	m = m.handleEngineEvent(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":                "read_file",
			"step":                1,
			"durationMs":          12,
			"success":             true,
			"output_preview":      "package server",
			"read_total_lines":    800,
			"read_returned_lines": 200,
		},
	})

	if len(m.agentLoop.toolTimeline) != 1 {
		t.Fatalf("expected one merged chip, got %d", len(m.agentLoop.toolTimeline))
	}
	chip := m.agentLoop.toolTimeline[0]
	if !strings.Contains(chip.Preview, "200/800 lines") {
		t.Errorf("chip preview missing line accounting: %q", chip.Preview)
	}
	if !strings.Contains(chip.Preview, "600 omitted") {
		t.Errorf("chip preview missing omitted count: %q", chip.Preview)
	}
	if !strings.Contains(chip.Preview, "package server") {
		t.Errorf("original preview should still be present: %q", chip.Preview)
	}
}

// TestReadFileChip_NoPrefixWhenFullFile pins the negative — when the
// read returned the entire file, no truncation prefix should appear.
// Otherwise every read would carry a useless "100/100 lines" badge
// that desensitises the user to the real warning.
func TestReadFileChip_NoPrefixWhenFullFile(t *testing.T) {
	m := NewModel(context.Background(), nil)

	m = m.handleEngineEvent(engine.Event{
		Type: "tool:call",
		Payload: map[string]any{
			"tool":           "read_file",
			"step":           1,
			"params_preview": "path=tiny.go",
		},
	})

	m = m.handleEngineEvent(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":                "read_file",
			"step":                1,
			"success":             true,
			"output_preview":      "ok",
			"read_total_lines":    50,
			"read_returned_lines": 50,
		},
	})

	chip := m.agentLoop.toolTimeline[0]
	if strings.Contains(chip.Preview, "50/50") || strings.Contains(chip.Preview, "omitted") {
		t.Errorf("chip preview should NOT advertise truncation when full file was returned: %q", chip.Preview)
	}
}

// TestReadFileChip_OnlyAffectsReadFile verifies the prefix is scoped
// to read_file alone — a different tool emitting accidental
// read_total_lines fields shouldn't trigger the badge.
func TestReadFileChip_OnlyAffectsReadFile(t *testing.T) {
	m := NewModel(context.Background(), nil)

	m = m.handleEngineEvent(engine.Event{
		Type: "tool:call",
		Payload: map[string]any{
			"tool": "list_dir",
			"step": 1,
		},
	})

	m = m.handleEngineEvent(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":                "list_dir",
			"step":                1,
			"success":             true,
			"output_preview":      "5 entries",
			"read_total_lines":    800,
			"read_returned_lines": 200,
		},
	})

	chip := m.agentLoop.toolTimeline[0]
	if strings.Contains(chip.Preview, "lines") || strings.Contains(chip.Preview, "omitted") {
		t.Errorf("non-read_file tool should not get the line-truncation badge: %q", chip.Preview)
	}
}
