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

func TestHandleEngineEvent_AutonomyPlan(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "agent:autonomy:plan",
		Payload: map[string]any{
			"subtask_count": 5,
			"confidence":    0.85,
			"parallel":      true,
			"scope":         "top_level",
			"todo_seeded":   true,
		},
	}
	m2 := m.handleEngineEvent(event)
	if m2.ui.showStatsPanel != true {
		t.Error("autonomy:plan should activate stats panel")
	}
}

func TestHandleEngineEvent_AutonomyPlanNoScope(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "agent:autonomy:plan",
		Payload: map[string]any{
			"subtask_count": 3,
			"confidence":    0.7,
			"parallel":      false,
		},
	}
	_ = m.handleEngineEvent(event)
}

func TestHandleEngineEvent_AutonomyKickoff(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "agent:autonomy:kickoff",
		Payload: map[string]any{
			"tool":          "orchestrate",
			"subtask_count": 10,
			"confidence":    0.92,
		},
	}
	m2 := m.handleEngineEvent(event)
	if m2.ui.showStatsPanel != true {
		t.Error("autonomy:kickoff should activate stats panel")
	}
}

func TestHandleEngineEvent_CoachNote(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "coach:note",
		Payload: map[string]any{
			"text":    "Consider breaking this into smaller steps",
			"severity": "hint",
			"origin":  "trajectory",
			"action":  "split",
		},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.agentLoop.sessionCoachNotes) != 1 {
		t.Errorf("sessionCoachNotes len: got %d want 1", len(m2.agentLoop.sessionCoachNotes))
	}
}

func TestHandleEngineEvent_CoachNoteMuted(t *testing.T) {
	m := newCoverageModel(t)
	m.ui.coachMuted = true
	event := engine.Event{
		Type: "coach:note",
		Payload: map[string]any{
			"text": "This should be dropped",
		},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.agentLoop.sessionCoachNotes) != 0 {
		t.Error("muted coach note should not be accumulated")
	}
}

func TestHandleEngineEvent_CoachNoteEmpty(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "coach:note",
		Payload: map[string]any{
			"text": "",
		},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.agentLoop.sessionCoachNotes) != 0 {
		t.Error("empty coach note should not be accumulated")
	}
}

func TestHandleEngineEvent_IntentDecision(t *testing.T) {
	m := newCoverageModel(t)
	m.intent.verbose = true
	event := engine.Event{
		Type: "intent:decision",
		Payload: map[string]any{
			"intent":   "resume",
			"source":   "llm",
			"raw":      "fix the bug",
			"enriched": "fix the bug in server.go",
			"reasoning": "user referred to previous conversation",
			"follow_up": "clarify",
			"latency_ms": 150,
		},
	}
	m2 := m.handleEngineEvent(event)
	if m2.intent.lastIntent != "resume" {
		t.Errorf("lastIntent: got %s want resume", m2.intent.lastIntent)
	}
	if m2.intent.lastSource != "llm" {
		t.Errorf("lastSource: got %s want llm", m2.intent.lastSource)
	}
	if m2.intent.lastLatencyMs != 150 {
		t.Errorf("lastLatencyMs: got %d want 150", m2.intent.lastLatencyMs)
	}
}

func TestHandleEngineEvent_IntentDecisionVerboseRewrite(t *testing.T) {
	m := newCoverageModel(t)
	m.intent.verbose = true
	event := engine.Event{
		Type: "intent:decision",
		Payload: map[string]any{
			"intent":   "new",
			"source":   "llm",
			"raw":      "do it",
			"enriched": "refactor the auth module",
			"reasoning": "clear directive",
			"latency_ms": 50,
		},
	}
	_ = m.handleEngineEvent(event)
	// Coach message should be appended because verbose + raw != enriched
}

func TestHandleEngineEvent_IntentDecisionNonVerboseNoMirror(t *testing.T) {
	m := newCoverageModel(t)
	m.intent.verbose = false
	event := engine.Event{
		Type: "intent:decision",
		Payload: map[string]any{
			"intent":   "resume",
			"source":   "llm",
			"raw":      "fix it",
			"enriched": "fix the bug",
			"latency_ms": 50,
		},
	}
	_ = m.handleEngineEvent(event)
}

func TestHandleEngineEvent_ContextBuilt(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "context:built",
		Payload: map[string]any{
			"files":       12,
			"tokens":      5000,
			"task":        "analysis",
			"compression": "heuristic",
		},
	}
	m2 := m.handleEngineEvent(event)
	if m2.notice == "" {
		t.Error("context:built should set notice")
	}
}

func TestHandleEngineEvent_ProviderComplete(t *testing.T) {
	m := newCoverageModel(t)
	m.agentLoop.active = true
	m.agentLoop.provider = "anthropic"
	event := engine.Event{
		Type: "provider:complete",
		Payload: map[string]any{
			"tokens":      1200,
			"provider":     "anthropic",
			"model":        "sonnet",
		},
	}
	m2 := m.handleEngineEvent(event)
	if m2.agentLoop.active != false {
		t.Error("provider:complete should deactivate agentLoop")
	}
	if m2.agentLoop.phase != "complete" {
		t.Errorf("phase: got %s want complete", m2.agentLoop.phase)
	}
}

func TestHandleEngineEvent_ProviderThrottleRetry(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "provider:throttle:retry",
		Payload: map[string]any{
			"provider": "anthropic",
			"attempt":  2,
			"wait_ms":  5000,
			"stream":   true,
		},
	}
	m2 := m.handleEngineEvent(event)
	if m2.notice == "" {
		t.Error("throttle retry should set notice")
	}
}

func TestHandleEngineEvent_ProviderCircuitOpen(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "provider:circuit:open",
		Payload: map[string]any{
			"provider":    "deepseek",
			"cooldown_ms": 30000,
		},
	}
	m2 := m.handleEngineEvent(event)
	if m2.notice == "" {
		t.Error("circuit open should set notice")
	}
}

func TestHandleEngineEvent_ProviderCircuitClosed(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "provider:circuit:closed",
		Payload: map[string]any{
			"provider": "deepseek",
		},
	}
	m2 := m.handleEngineEvent(event)
	if m2.notice == "" {
		t.Error("circuit closed should set notice")
	}
}

func TestHandleEngineEvent_ProviderStreamRecovered(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "provider:stream:recovered",
		Payload: map[string]any{
			"from": "anthropic",
			"to":   "deepseek",
		},
	}
	m2 := m.handleEngineEvent(event)
	if m2.notice == "" {
		t.Error("stream recovered should set notice")
	}
}

func TestHandleEngineEvent_ConfigReloadAuto(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "config:reload:auto",
		Payload: map[string]any{
			"path": "/path/to/config.yaml",
		},
	}
	m2 := m.handleEngineEvent(event)
	if m2.notice == "" {
		t.Error("config reload should set notice")
	}
}

func TestHandleEngineEvent_ConfigReloadAutoFailed(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "config:reload:auto_failed",
		Payload: map[string]any{
			"error": "parse error at line 42",
		},
	}
	m2 := m.handleEngineEvent(event)
	if m2.notice == "" {
		t.Error("config reload failed should set notice")
	}
}

func TestHandleEngineEvent_ContextLifecycleCompacted(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "context:lifecycle:compacted",
		Payload: map[string]any{
			"before_tokens":     100000,
			"after_tokens":      25000,
			"rounds_collapsed":  3,
			"messages_removed":  15,
		},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.agentLoop.toolTimeline) == 0 {
		t.Error("compacted event should push a tool chip")
	}
}

func TestHandleEngineEvent_ContextLifecycleHandoff(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "context:lifecycle:handoff",
		Payload: map[string]any{
			"history_tokens":    80000,
			"brief_tokens":      5000,
			"messages_sealed":   20,
			"new_conversation":  "conv-123",
		},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.agentLoop.toolTimeline) == 0 {
		t.Error("handoff event should push a tool chip")
	}
}

func TestHandleEngineEvent_ProviderRaceComplete(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "provider:race:complete",
		Payload: map[string]any{
			"winner":      "anthropic",
			"tokens":      800,
			"duration_ms": 1200,
			"candidates":  []any{"anthropic", "deepseek"},
		},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.agentLoop.toolTimeline) == 0 {
		t.Error("race complete should push a tool chip")
	}
}

func TestHandleEngineEvent_ProviderRaceFailed(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "provider:race:failed",
		Payload: map[string]any{
			"error":        "all providers errored",
			"duration_ms": 5000,
		},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.agentLoop.toolTimeline) == 0 {
		t.Error("race failed should push a tool chip")
	}
}

func TestHandleEngineEvent_SubagentEvents(t *testing.T) {
	events := []string{
		"agent:subagent:start",
		"agent:subagent:fallback",
		"agent:subagent:done",
	}
	for _, eventType := range events {
		m := newCoverageModel(t)
		event := engine.Event{Type: eventType, Payload: map[string]any{}}
		_ = m.handleEngineEvent(event) // should not panic
	}
}

func TestHandleEngineEvent_CoachHint(t *testing.T) {
	m := newCoverageModel(t)
	m.ui.hintsVerbose = true
	event := engine.Event{
		Type: "agent:coach:hint",
		Payload: map[string]any{
			"hints": []any{"try breaking this down", "check the logs first"},
		},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.agentLoop.sessionCoachNotes) == 0 {
		t.Error("coach hint should accumulate notes when hintsVerbose")
	}
}

func TestHandleEngineEvent_CoachHintNotVerbose(t *testing.T) {
	m := newCoverageModel(t)
	m.ui.hintsVerbose = false
	event := engine.Event{
		Type: "agent:coach:hint",
		Payload: map[string]any{
			"hints": []any{"try breaking this down"},
		},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.agentLoop.sessionCoachNotes) != 0 {
		t.Error("coach hint should not accumulate when hintsVerbose is false")
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
