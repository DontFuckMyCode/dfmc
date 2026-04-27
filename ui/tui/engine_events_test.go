package tui

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestHandleEngineEvent_DriveEventsDoNotCrash(t *testing.T) {
	events := []string{
		"drive:run:start",
		"drive:plan:start",
		"drive:plan:done",
		"drive:plan:failed",
		"drive:todo:start",
		"drive:todo:done",
		"drive:todo:blocked",
		"drive:todo:skipped",
		"drive:todo:retry",
		"drive:run:warning",
		"drive:run:done",
		"drive:run:stopped",
		"drive:run:failed",
	}

	for _, eventType := range events {
		m := newCoverageModel(t)
		event := engine.Event{Type: eventType, Payload: map[string]any{}}
		_ = m.handleEngineEvent(event) // should not panic
	}
}

func TestHandleEngineEvent_AgentEventsDoNotCrash(t *testing.T) {
	events := []string{
		"agent:loop:start",
		"agent:loop:thinking",
		"agent:loop:final",
		"agent:loop:max_steps",
		"agent:loop:error",
		"agent:loop:parked",
		"agent:loop:budget_exhausted",
		"agent:loop:auto_resume",
		"agent:loop:auto_resume_refused",
		"agent:loop:auto_recover",
	}

	for _, eventType := range events {
		m := newCoverageModel(t)
		event := engine.Event{Type: eventType, Payload: map[string]any{}}
		_ = m.handleEngineEvent(event) // should not panic
	}
}

func TestHandleEngineEvent_ToolEventsDoNotCrash(t *testing.T) {
	events := []string{
		"tool:call",
		"tool:result",
		"tool:error",
		"tool:reasoning",
	}

	for _, eventType := range events {
		m := newCoverageModel(t)
		event := engine.Event{Type: eventType, Payload: map[string]any{}}
		_ = m.handleEngineEvent(event) // should not panic
	}
}

func TestHandleEngineEvent_EmptyEventTypeDoesNotCrash(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{Type: "", Payload: map[string]any{}}
	_ = m.handleEngineEvent(event) // should not panic
}

func TestHandleEngineEvent_WhitespaceEventTypeDoesNotCrash(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{Type: "   ", Payload: map[string]any{}}
	_ = m.handleEngineEvent(event) // should not panic
}

func TestHandleEngineEvent_UnknownEventTypeDoesNotCrash(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{Type: "completely:unknown:event", Payload: map[string]any{"key": "value"}}
	_ = m.handleEngineEvent(event) // should not panic
}

func TestHandleEngineEvent_ProviderEventsDoNotCrash(t *testing.T) {
	events := []string{
		"provider:selected",
		"provider:changed",
		"provider:error",
	}

	for _, eventType := range events {
		m := newCoverageModel(t)
		event := engine.Event{Type: eventType, Payload: map[string]any{}}
		_ = m.handleEngineEvent(event) // should not panic
	}
}

func TestPayloadInt(t *testing.T) {
	cases := []struct {
		data     map[string]any
		key      string
		fallback int
		want     int
	}{
		{nil, "x", 99, 99},
		{map[string]any{"x": 42}, "x", 99, 42},
		{map[string]any{"x": int64(100)}, "x", 99, 100},
		{map[string]any{"x": float64(3.14)}, "x", 99, 3},
		{map[string]any{"x": "  77  "}, "x", 99, 77},
		{map[string]any{"x": "not-a-number"}, "x", 99, 99},
		{map[string]any{"x": "  not-numeric  "}, "x", 99, 99},
		{map[string]any{"x": int32(50)}, "x", 99, 50},
	}
	for _, c := range cases {
		got := payloadInt(c.data, c.key, c.fallback)
		if got != c.want {
			t.Errorf("payloadInt(%v, %q, %d) = %d, want %d", c.data, c.key, c.fallback, got, c.want)
		}
	}
}
