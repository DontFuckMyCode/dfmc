package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

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
			"text":     "Consider breaking this into smaller steps",
			"severity": "hint",
			"origin":   "trajectory",
			"action":   "split",
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
			"intent":     "resume",
			"source":     "llm",
			"raw":        "fix the bug",
			"enriched":   "fix the bug in server.go",
			"reasoning":  "user referred to previous conversation",
			"follow_up":  "clarify",
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
			"intent":     "new",
			"source":     "llm",
			"raw":        "do it",
			"enriched":   "refactor the auth module",
			"reasoning":  "clear directive",
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
			"intent":     "resume",
			"source":     "llm",
			"raw":        "fix it",
			"enriched":   "fix the bug",
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

func TestHandleEngineEvent_ContextLifecycleCompacted(t *testing.T) {
	m := newCoverageModel(t)
	m.chat.sending = true
	event := engine.Event{
		Type: "context:lifecycle:compacted",
		Payload: map[string]any{
			"step":             9,
			"before_tokens":    72000,
			"after_tokens":     21000,
			"rounds_collapsed": 6,
			"messages_removed": 18,
			"keep_recent":      3,
		},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.chat.transcript) == 0 {
		t.Fatal("context lifecycle event should append transcript row")
	}
	contents := make([]string, 0, len(m2.chat.transcript))
	for _, line := range m2.chat.transcript {
		contents = append(contents, line.Content)
	}
	got := strings.Join(contents, "\n")
	for _, want := range []string{"context compacted", "72.0k -> 21.0k tok", "6 rounds summarized", "_reason:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected compact lifecycle detail %q, got:\n%s", want, got)
		}
	}
}

func TestHandleEngineEvent_ProviderComplete(t *testing.T) {
	m := newCoverageModel(t)
	m.agentLoop.active = true
	m.agentLoop.provider = "anthropic"
	event := engine.Event{
		Type: "provider:complete",
		Payload: map[string]any{
			"tokens":   1200,
			"provider": "anthropic",
			"model":    "sonnet",
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

func TestHandleEngineEvent_ContextLifecycleHandoff(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "context:lifecycle:handoff",
		Payload: map[string]any{
			"history_tokens":   80000,
			"brief_tokens":     5000,
			"messages_sealed":  20,
			"new_conversation": "conv-123",
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
			"error":       "all providers errored",
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

func TestHandleEngineEvent_CoachStuck_FiresIndependentOfVerbose(t *testing.T) {
	// The stuck-loop signal must surface even when hintsVerbose=false —
	// it's the "your long autonomous run is wasting steps" signal that
	// users explicitly want during multi-hour Drive operations.
	m := newCoverageModel(t)
	m.ui.hintsVerbose = false
	event := engine.Event{
		Type: "agent:coach:stuck",
		Payload: map[string]any{
			"step":          12,
			"tool":          "read_file",
			"failure_count": 4,
			"error_class":   "file does not exist",
		},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.agentLoop.sessionCoachNotes) == 0 {
		t.Fatal("stuck-loop event should surface a coach note even when hintsVerbose=false")
	}
	last := m2.agentLoop.sessionCoachNotes[len(m2.agentLoop.sessionCoachNotes)-1]
	if !strings.Contains(last, "Loop stalled") {
		t.Errorf("note should describe the stall, got %q", last)
	}
	if !strings.Contains(last, "read_file") {
		t.Errorf("note should name the stuck tool, got %q", last)
	}
	if !strings.Contains(last, "4 times") {
		t.Errorf("note should cite the failure count, got %q", last)
	}
	// Severity is encoded into the rendered transcript as a leading
	// warn marker; sessionCoachNotes stores the raw text only, so we
	// assert the warn marker landed on the chat line.
	tail := m2.chat.transcript[len(m2.chat.transcript)-1]
	if !strings.Contains(tail.Content, "⚠") {
		t.Errorf("transcript line should carry warn marker, got %q", tail.Content)
	}
	// Chip strip must show the structured indicator.
	timeline := m2.agentLoop.toolTimeline
	if len(timeline) == 0 || timeline[len(timeline)-1].Name != "stuck-loop" {
		t.Errorf("expected a stuck-loop chip, got %+v", timeline)
	}
}

// TestHandleEngineEvent_HeadroomThresholdNotifications pins the
// pre-compact context-fill warnings: when tokens_used crosses 70/85/
// 95% of the live loop budget for the FIRST time in a turn, the
// dispatcher pushes a chat-event "context X% full" notification.
// Each band fires at most once per turn (bitmask dedupe) and the
// tracker resets on agent:loop:start so a fresh ask gets a clean
// slate.
func TestHandleEngineEvent_HeadroomThresholdNotifications(t *testing.T) {
	m := newCoverageModel(t)

	// Loop start primes the budget cap.
	m = m.handleEngineEvent(engine.Event{
		Type: "agent:loop:start",
		Payload: map[string]any{
			"max_tool_steps":  60,
			"max_tool_tokens": 100_000,
		},
	})
	if m.agentLoop.headroomThresholdsHit != 0 {
		t.Fatalf("loop start should reset thresholds, got %d", m.agentLoop.headroomThresholdsHit)
	}

	// First thinking round at 50% — under all thresholds, no notification.
	m = m.handleEngineEvent(engine.Event{
		Type: "agent:loop:thinking",
		Payload: map[string]any{
			"step":        2,
			"tokens_used": 50_000,
		},
	})
	if m.agentLoop.headroomThresholdsHit != 0 {
		t.Errorf("under 70%%: should not fire, got bitmask %d", m.agentLoop.headroomThresholdsHit)
	}

	// Second round at 72% — should hit 70% band.
	m = m.handleEngineEvent(engine.Event{
		Type: "agent:loop:thinking",
		Payload: map[string]any{
			"step":        3,
			"tokens_used": 72_000,
		},
	})
	if m.agentLoop.headroomThresholdsHit&1 == 0 {
		t.Errorf("70%% band should fire at 72%%, bitmask=%d", m.agentLoop.headroomThresholdsHit)
	}

	// Third round still at 75% — 70 already fired, should NOT re-fire.
	beforeMask := m.agentLoop.headroomThresholdsHit
	m = m.handleEngineEvent(engine.Event{
		Type: "agent:loop:thinking",
		Payload: map[string]any{
			"step":        4,
			"tokens_used": 75_000,
		},
	})
	if m.agentLoop.headroomThresholdsHit != beforeMask {
		t.Errorf("70%% should not re-fire, bitmask drift %d→%d", beforeMask, m.agentLoop.headroomThresholdsHit)
	}

	// Jump to 96% — both 85 and 95 bands should now be set.
	m = m.handleEngineEvent(engine.Event{
		Type: "agent:loop:thinking",
		Payload: map[string]any{
			"step":        10,
			"tokens_used": 96_000,
		},
	})
	if m.agentLoop.headroomThresholdsHit&(1|2|4) != (1 | 2 | 4) {
		t.Errorf("at 96%%: all three bands should be set, got bitmask %d", m.agentLoop.headroomThresholdsHit)
	}

	// New ask resets.
	m = m.handleEngineEvent(engine.Event{
		Type: "agent:loop:start",
		Payload: map[string]any{
			"max_tool_steps":  60,
			"max_tool_tokens": 100_000,
		},
	})
	if m.agentLoop.headroomThresholdsHit != 0 {
		t.Errorf("loop start should reset thresholds, got %d", m.agentLoop.headroomThresholdsHit)
	}
}

// TestHandleEngineEvent_CompactClearsCrossedThresholds pins the
// re-arm behaviour: after a compact drops usage below a previously-
// crossed band, that band's bit clears so a subsequent climb back
// over the threshold fires the warning again. Without this, a long
// turn that hit 95% → compacted to 30% → climbed back to 75% would
// stay silent on the second crossing.
func TestHandleEngineEvent_CompactClearsCrossedThresholds(t *testing.T) {
	m := newCoverageModel(t)
	m = m.handleEngineEvent(engine.Event{
		Type: "agent:loop:start",
		Payload: map[string]any{
			"max_tool_tokens": 100_000,
		},
	})
	// Climb to 96%.
	m = m.handleEngineEvent(engine.Event{
		Type: "agent:loop:thinking",
		Payload: map[string]any{
			"tokens_used": 96_000,
		},
	})
	if m.agentLoop.headroomThresholdsHit != (1 | 2 | 4) {
		t.Fatalf("setup: expected all bands set, got %d", m.agentLoop.headroomThresholdsHit)
	}
	// Compact reclaims to 30%.
	m = m.handleEngineEvent(engine.Event{
		Type: "context:lifecycle:compacted",
		Payload: map[string]any{
			"before_tokens": 96_000,
			"after_tokens":  30_000,
		},
	})
	// All three bands should now be cleared (30 < 70 < 85 < 95).
	if m.agentLoop.headroomThresholdsHit != 0 {
		t.Errorf("compact to 30%% should clear all bands, got %d", m.agentLoop.headroomThresholdsHit)
	}
}

// TestHandleEngineEvent_ProviderFallback_SurfacesNotice pins the
// new provider:fallback classifier — distinct from circuit:open
// (cooldown) and stream:recovered (mid-stream swap), this fires
// when the cascade walks past a failing provider to the next.
func TestHandleEngineEvent_ProviderFallback_SurfacesNotice(t *testing.T) {
	m := newCoverageModel(t)
	m = m.handleEngineEvent(engine.Event{
		Type: "provider:fallback",
		Payload: map[string]any{
			"from":    "anthropic",
			"to":      "openai",
			"attempt": 0,
			"error":   "503 service unavailable",
		},
	})
	notice := strings.ToLower(m.notice)
	for _, want := range []string{"provider fallback", "anthropic", "openai", "503"} {
		if !strings.Contains(notice, strings.ToLower(want)) {
			t.Errorf("expected %q in notice, got %q", want, m.notice)
		}
	}
}

// TestHandleEngineEvent_ContextErrorAndShutdownAndResume rounds out
// the audit by pinning three more events the dispatcher used to
// drop: context:error (string-payload special case), engine:
// shutdown_error (multi-stage), and agent:loop:resume (the success
// counterpart to resume_refused — needed so users see /continue
// landed instead of guessing).
func TestHandleEngineEvent_ContextErrorAndShutdownAndResume(t *testing.T) {
	cases := []struct {
		name      string
		evType    string
		payload   any
		wantInMsg []string
	}{
		{
			name:      "context:error string payload",
			evType:    "context:error",
			payload:   "ast parse failed: syntax error at line 42",
			wantInMsg: []string{"context build failed", "ast parse failed", "reduced context"},
		},
		{
			name:   "engine:shutdown_error",
			evType: "engine:shutdown_error",
			payload: map[string]any{
				"stage": "storage",
				"error": "bbolt: timeout closing handle",
			},
			wantInMsg: []string{"shutdown error", "storage", "bbolt"},
		},
		{
			name:   "agent:loop:resume",
			evType: "agent:loop:resume",
			payload: map[string]any{
				"resumed_from_step": 12,
				"tool_rounds":       8,
				"tokens_used":       4200,
			},
			wantInMsg: []string{"loop resumed", "step 12", "8 rounds"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newCoverageModel(t)
			m = m.handleEngineEvent(engine.Event{Type: tc.evType, Payload: tc.payload})
			notice := strings.ToLower(m.notice)
			for _, want := range tc.wantInMsg {
				if !strings.Contains(notice, strings.ToLower(want)) {
					t.Errorf("expected notice to contain %q, got %q", want, m.notice)
				}
			}
		})
	}
}

// TestHandleEngineEvent_AgentLoopGuards_SurfaceNotices pins the
// seven agent:loop:* guard events the TUI dispatcher used to drop:
// tools_force_stop, interrupted, shutdown_parked, resume_refused,
// safety_bound, empty_recovery, empty_final. These are rare but
// critical — a fired safety_bound or resume_refused without a
// visible signal leaves the user wondering why the loop stopped.
func TestHandleEngineEvent_AgentLoopGuards_SurfaceNotices(t *testing.T) {
	cases := []struct {
		name      string
		evType    string
		payload   map[string]any
		wantInMsg []string
	}{
		{
			name:   "tools_force_stop",
			evType: "agent:loop:tools_force_stop",
			payload: map[string]any{
				"tool_rounds": 30,
				"hard_cap":    30,
			},
			wantInMsg: []string{"hard cap", "30/30"},
		},
		{
			name:   "interrupted",
			evType: "agent:loop:interrupted",
			payload: map[string]any{
				"tool_rounds": 12,
				"error":       "context canceled",
			},
			wantInMsg: []string{"interrupted", "12 rounds", "/continue"},
		},
		{
			name:   "shutdown_parked",
			evType: "agent:loop:shutdown_parked",
			payload: map[string]any{
				"step": 8,
			},
			wantInMsg: []string{"shutting down", "step 8"},
		},
		{
			name:   "resume_refused",
			evType: "agent:loop:resume_refused",
			payload: map[string]any{
				"reason": "cumulative ceiling",
			},
			wantInMsg: []string{"resume refused", "cumulative ceiling"},
		},
		{
			name:   "safety_bound",
			evType: "agent:loop:safety_bound",
			payload: map[string]any{
				"safety_bound": 100,
				"source":       "autonomous",
			},
			wantInMsg: []string{"safety bound", "extremely rare"},
		},
		{
			name:   "empty_recovery",
			evType: "agent:loop:empty_recovery",
			payload: map[string]any{
				"tool_rounds": 4,
			},
			wantInMsg: []string{"empty response", "synthesis nudge"},
		},
		{
			name:   "empty_final",
			evType: "agent:loop:empty_final",
			payload: map[string]any{
				"tool_rounds": 6,
			},
			wantInMsg: []string{"empty response", "giving up"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newCoverageModel(t)
			m = m.handleEngineEvent(engine.Event{Type: tc.evType, Payload: tc.payload})
			notice := strings.ToLower(m.notice)
			for _, want := range tc.wantInMsg {
				if !strings.Contains(notice, strings.ToLower(want)) {
					t.Errorf("expected notice to contain %q, got %q", want, m.notice)
				}
			}
		})
	}
}

// TestHandleEngineEvent_CriticalSafetyEvents_SurfaceNotices pins
// the four "engine publishes but UI ignored" safety-critical events
// the activity-feed audit found: runtime panics, tool panics,
// world-writable config warnings, and degraded memory store. All
// four were silently dropped at the dispatcher before this fix —
// process panic recovery, security advisories, and storage
// degradation were invisible to the user.
func TestHandleEngineEvent_CriticalSafetyEvents_SurfaceNotices(t *testing.T) {
	cases := []struct {
		name      string
		evType    string
		payload   map[string]any
		wantInMsg []string
	}{
		{
			name:   "runtime panic",
			evType: "runtime:panic",
			payload: map[string]any{
				"name":  "indexer-bg",
				"panic": "nil pointer dereference",
			},
			wantInMsg: []string{"runtime panic", "indexer-bg", "nil pointer"},
		},
		{
			name:   "tool panic",
			evType: "tool:panicked",
			payload: map[string]any{
				"name":  "edit_file",
				"panic": "index out of range",
			},
			wantInMsg: []string{"tool panicked", "edit_file", "index out of range"},
		},
		{
			name:   "config permissions",
			evType: "security:config_permissions",
			payload: map[string]any{
				"path":   "/etc/dfmc/config.yaml",
				"status": "warn",
				"msg":    "world-writable",
			},
			wantInMsg: []string{"security warning", "world-writable"},
		},
		{
			name:   "memory degraded",
			evType: "memory:degraded",
			payload: map[string]any{
				"reason": "bbolt: timeout",
			},
			wantInMsg: []string{"memory degraded", "bbolt: timeout"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newCoverageModel(t)
			m = m.handleEngineEvent(engine.Event{Type: tc.evType, Payload: tc.payload})
			notice := strings.ToLower(m.notice)
			for _, want := range tc.wantInMsg {
				if !strings.Contains(notice, strings.ToLower(want)) {
					t.Errorf("expected notice to contain %q, got %q", want, m.notice)
				}
			}
		})
	}
}

// TestHandleEngineEvent_ToolDenied_SurfacesNotice pins the new
// tool:denied classifier: the engine has been publishing this
// event since the approval gate landed, but the TUI dispatcher
// didn't route it through handleToolEvent so it fell through to
// the generic info fallback. A denied write_file (gate or sub-
// agent allowlist) used to be invisible — the model saw the
// error string but the user got no signal.
func TestHandleEngineEvent_ToolDenied_SurfacesNotice(t *testing.T) {
	m := newCoverageModel(t)
	m = m.handleEngineEvent(engine.Event{
		Type: "tool:denied",
		Payload: map[string]any{
			"name":   "run_command",
			"reason": "user denied",
			"source": "agent-loop",
		},
	})
	if !strings.Contains(strings.ToLower(m.notice), "denied") {
		t.Errorf("expected denial notice, got %q", m.notice)
	}
	if !strings.Contains(m.notice, "run_command") {
		t.Errorf("expected denied tool name in notice, got %q", m.notice)
	}
	if !strings.Contains(m.notice, "agent-loop") {
		t.Errorf("expected source bracket in notice, got %q", m.notice)
	}
}

// TestHandleEngineEvent_HookRun_FailureSurfacesNotice pins the new
// hook:run classifier: a non-zero exit code or err string must
// produce a footer notice + transcript-friendly line, while a
// success run stays quiet (no notice noise per round).
func TestHandleEngineEvent_HookRun_FailureSurfacesNotice(t *testing.T) {
	m := newCoverageModel(t)
	m = m.handleEngineEvent(engine.Event{
		Type: "hook:run",
		Payload: map[string]any{
			"event":       "pre_tool",
			"name":        "lint",
			"command":     "eslint .",
			"exit_code":   1,
			"duration_ms": 250,
			"err":         "exit status 1",
		},
	})
	if !strings.Contains(strings.ToLower(m.notice), "hook failed") {
		t.Errorf("expected failure notice, got %q", m.notice)
	}
	if !strings.Contains(m.notice, "lint") {
		t.Errorf("expected hook name in notice, got %q", m.notice)
	}
}

func TestHandleEngineEvent_HookRun_SuccessIsQuiet(t *testing.T) {
	m := newCoverageModel(t)
	m.notice = "" // baseline
	m = m.handleEngineEvent(engine.Event{
		Type: "hook:run",
		Payload: map[string]any{
			"event":       "post_tool",
			"name":        "audit",
			"exit_code":   0,
			"duration_ms": 80,
		},
	})
	// Successful hook should leave footer notice empty — chat-event
	// line in the activity feed is enough; surfacing every success
	// would drown out real signal.
	if strings.Contains(strings.ToLower(m.notice), "hook failed") {
		t.Errorf("success hook should not produce failure notice, got %q", m.notice)
	}
}

func TestHandleEngineEvent_CoachStuck_ClearedBySuccessfulTool(t *testing.T) {
	// Stall badge should disappear automatically once the model recovers
	// — a single successful tool call after the stuck signal is the
	// trajectory layer's "switch tactic" hint landing.
	m := newCoverageModel(t)
	m = m.handleEngineEvent(engine.Event{
		Type: "agent:coach:stuck",
		Payload: map[string]any{
			"tool":          "read_file",
			"failure_count": 3,
			"error_class":   "file does not exist",
		},
	})
	if m.agentLoop.stuckTool == "" {
		t.Fatal("setup: stuck signal should set stuckTool")
	}
	// Successful tool call → badge clears.
	m = m.handleEngineEvent(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":    "glob",
			"success": true,
			"step":    14,
		},
	})
	if m.agentLoop.stuckTool != "" {
		t.Errorf("successful tool should clear stuckTool, got %q", m.agentLoop.stuckTool)
	}
	if m.agentLoop.stuckClearedAt != 14 {
		t.Errorf("stuckClearedAt should record the recovery step, got %d", m.agentLoop.stuckClearedAt)
	}
}

func TestHandleEngineEvent_CoachStuck_NotClearedByFailedTool(t *testing.T) {
	// A FAILED tool call must NOT clear the badge — the model is still
	// stuck, and the badge should keep warning the user.
	m := newCoverageModel(t)
	m = m.handleEngineEvent(engine.Event{
		Type: "agent:coach:stuck",
		Payload: map[string]any{
			"tool":          "read_file",
			"failure_count": 3,
			"error_class":   "file does not exist",
		},
	})
	m = m.handleEngineEvent(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":    "read_file",
			"success": false,
			"step":    15,
		},
	})
	if m.agentLoop.stuckTool != "read_file" {
		t.Errorf("failed tool should NOT clear stuckTool, got %q", m.agentLoop.stuckTool)
	}
}

func TestRuntimeStrip_StalledBadge(t *testing.T) {
	vm := runtimeViewModel{
		AgentActive:   true,
		AgentPhase:    "thinking",
		AgentStep:     12,
		AgentMaxSteps: 60,
		StuckTool:     "read_file",
		StuckCount:    4,
		StuckErrClass: "file does not exist",
	}
	parts := runtimeStripNowParts(vm)
	joined := strings.Join(parts, " | ")
	if !strings.Contains(joined, "stalled:") {
		t.Errorf("expected stalled badge in strip, got %q", joined)
	}
	if !strings.Contains(joined, "read_file") {
		t.Errorf("badge should name the tool, got %q", joined)
	}
	if !strings.Contains(joined, "×4") {
		t.Errorf("badge should show count, got %q", joined)
	}
}

func TestRuntimeStrip_AutoResumeBadgeProgresses(t *testing.T) {
	// Ceiling proximity escalates: <50% subtle, 50-80% info, >=80% warn.
	// We can't easily check the lipgloss style applied, but the label
	// content is fixed and that's what the user reads.
	cases := []struct {
		cum, ceil int
		wantSub   string
	}{
		{60, 600, "auto 60/600"},
		{310, 600, "auto 310/600"},
		{540, 600, "auto 540/600"},
	}
	for _, c := range cases {
		vm := runtimeViewModel{
			AgentActive:     true,
			AgentStep:       12,
			CumulativeSteps: c.cum, StepCeiling: c.ceil,
		}
		parts := runtimeStripNowParts(vm)
		joined := strings.Join(parts, " | ")
		if !strings.Contains(joined, c.wantSub) {
			t.Errorf("cum=%d ceil=%d: expected %q in strip, got %q", c.cum, c.ceil, c.wantSub, joined)
		}
	}
}

func TestRuntimeStrip_AutoResumeBadgeIncludesTokenWindow(t *testing.T) {
	vm := runtimeViewModel{
		AgentActive:     true,
		CumulativeSteps: 120, StepCeiling: 600,
		CumulativeTokens: 480_000, TokenCeiling: 2_500_000,
	}
	parts := runtimeStripNowParts(vm)
	joined := strings.Join(parts, " | ")
	if !strings.Contains(joined, "auto 120/600") {
		t.Errorf("badge should show step counts, got %q", joined)
	}
	if !strings.Contains(joined, "480k") {
		t.Errorf("badge should compactly format cumulative tokens, got %q", joined)
	}
	if !strings.Contains(joined, "2.5M") {
		t.Errorf("badge should compactly format token ceiling, got %q", joined)
	}
}

func TestRuntimeStrip_NoAutoResumeBadgeWhenNotEngaged(t *testing.T) {
	vm := runtimeViewModel{AgentActive: true, AgentStep: 5}
	parts := runtimeStripNowParts(vm)
	joined := strings.Join(parts, " | ")
	if strings.Contains(joined, "auto ") {
		t.Errorf("no auto-resume yet → no badge, got %q", joined)
	}
}

func TestFormatTokenCount(t *testing.T) {
	cases := map[int]string{
		0:         "0",
		999:       "999",
		1234:      "1.2k",
		9_999:     "10.0k",
		12_345:    "12k",
		999_999:   "999k",
		1_234_567: "1.2M",
	}
	for in, want := range cases {
		if got := formatTokenCount(in); got != want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestHandleEngineEvent_AutoResumePersistsCumulative(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "agent:loop:auto_resume",
		Payload: map[string]any{
			"cumulative_steps":  120,
			"step_ceiling":      600,
			"cumulative_tokens": 480_000,
			"token_ceiling":     2_500_000,
		},
	}
	m2 := m.handleEngineEvent(event)
	if m2.agentLoop.cumulativeSteps != 120 {
		t.Errorf("cumulativeSteps = %d, want 120", m2.agentLoop.cumulativeSteps)
	}
	if m2.agentLoop.stepCeiling != 600 {
		t.Errorf("stepCeiling = %d, want 600", m2.agentLoop.stepCeiling)
	}
	if m2.agentLoop.tokenCeiling != 2_500_000 {
		t.Errorf("tokenCeiling = %d, want 2_500_000", m2.agentLoop.tokenCeiling)
	}
	// A fresh agent:loop:start zeros them again.
	m3 := m2.handleEngineEvent(engine.Event{
		Type:    "agent:loop:start",
		Payload: map[string]any{"max_tool_steps": 60},
	})
	if m3.agentLoop.cumulativeSteps != 0 || m3.agentLoop.stepCeiling != 0 {
		t.Errorf("agent:loop:start should reset cumulative counters, got steps=%d ceil=%d",
			m3.agentLoop.cumulativeSteps, m3.agentLoop.stepCeiling)
	}
}

func TestLiveLoopTokens_UpdatedByThinkingEvent(t *testing.T) {
	m := newCoverageModel(t)
	m = m.handleEngineEvent(engine.Event{
		Type: "agent:loop:start",
		Payload: map[string]any{
			"max_tool_steps":  60,
			"max_tool_tokens": 250_000,
		},
	})
	if m.agentLoop.liveLoopBudgetCap != 250_000 {
		t.Errorf("budget cap should be picked up from start event, got %d", m.agentLoop.liveLoopBudgetCap)
	}
	if m.agentLoop.liveLoopTokens != 0 {
		t.Errorf("live tokens should reset on start, got %d", m.agentLoop.liveLoopTokens)
	}
	// Three rounds — token count should track the LATEST value, not
	// accumulate (the engine reports rolling footprint, not cumulative).
	for _, used := range []int{12_000, 35_000, 28_000} {
		m = m.handleEngineEvent(engine.Event{
			Type: "agent:loop:thinking",
			Payload: map[string]any{
				"step":           1,
				"max_tool_steps": 60,
				"tokens_used":    used,
			},
		})
	}
	if m.agentLoop.liveLoopTokens != 28_000 {
		t.Errorf("live tokens should track latest value (28000 after compact), got %d", m.agentLoop.liveLoopTokens)
	}
	// Final clears.
	m = m.handleEngineEvent(engine.Event{Type: "agent:loop:final", Payload: map[string]any{}})
	if m.agentLoop.liveLoopTokens != 0 || m.agentLoop.liveLoopBudgetCap != 0 {
		t.Errorf("live tokens should clear on final, got %d/%d",
			m.agentLoop.liveLoopTokens, m.agentLoop.liveLoopBudgetCap)
	}
}

func TestRuntimeStrip_LiveLoopTokensBadge_ProximityEscalates(t *testing.T) {
	cases := []struct {
		used, cap int
		want      string
	}{
		{30_000, 250_000, "loop ~30k/250k"},  // <70%, subtle
		{180_000, 250_000, "loop ~180k/250k"}, // 72%, info
		{230_000, 250_000, "loop ~230k/250k"}, // 92%, warn
	}
	for _, c := range cases {
		vm := runtimeViewModel{
			AgentActive:       true,
			LiveLoopTokens:    c.used,
			LiveLoopBudgetCap: c.cap,
		}
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		if !strings.Contains(joined, c.want) {
			t.Errorf("used=%d cap=%d expected %q in strip, got %q",
				c.used, c.cap, c.want, joined)
		}
	}
}

func TestRuntimeStrip_LiveLoopTokens_WithoutBudget(t *testing.T) {
	// No budget cap → render count alone, no /denom. Some configs
	// disable max_tool_tokens entirely.
	vm := runtimeViewModel{
		AgentActive:    true,
		LiveLoopTokens: 12_000,
	}
	joined := strings.Join(runtimeStripNowParts(vm), " | ")
	if !strings.Contains(joined, "loop ~12k") {
		t.Errorf("expected count alone, got %q", joined)
	}
	if strings.Contains(joined, "loop ~12k/") {
		t.Errorf("no budget → no /denom suffix, got %q", joined)
	}
}

// TestHandleEngineEvent_CacheHit_IncrementsTurnCounter pins the new
// cache_hit handler: the engine has been publishing this since the
// parallel-tool cache landed, but the dispatcher dropped it. Now the
// per-turn counter increments and a chat-event line surfaces the
// silent token savings inline.
func TestHandleEngineEvent_CacheHit_IncrementsTurnCounter(t *testing.T) {
	m := newCoverageModel(t)
	if m.agentLoop.cacheHitsThisTurn != 0 {
		t.Fatalf("setup: counter should start at 0, got %d", m.agentLoop.cacheHitsThisTurn)
	}
	m = m.handleEngineEvent(engine.Event{
		Type:    "agent:tool:cache_hit",
		Payload: map[string]any{"name": "read_file"},
	})
	if m.agentLoop.cacheHitsThisTurn != 1 {
		t.Errorf("first hit should bump counter to 1, got %d", m.agentLoop.cacheHitsThisTurn)
	}
	// Three more hits → counter == 4
	for i := 0; i < 3; i++ {
		m = m.handleEngineEvent(engine.Event{
			Type:    "agent:tool:cache_hit",
			Payload: map[string]any{"name": "grep_codebase"},
		})
	}
	if m.agentLoop.cacheHitsThisTurn != 4 {
		t.Errorf("after 4 total hits, counter should be 4, got %d", m.agentLoop.cacheHitsThisTurn)
	}
	// New ask resets.
	m = m.handleEngineEvent(engine.Event{
		Type:    "agent:loop:start",
		Payload: map[string]any{},
	})
	if m.agentLoop.cacheHitsThisTurn != 0 {
		t.Errorf("loop start should reset counter, got %d", m.agentLoop.cacheHitsThisTurn)
	}
}

// TestHandleEngineEvent_ToolErrorIncrementsTurnCounter pins that a
// failed tool:result bumps toolErrorsThisTurn so the end-of-turn
// summary card can show "errors: N tool failures (recovered to
// final answer)". Successful results must NOT bump the counter,
// otherwise a turn that mixed success+failure would over-report.
func TestHandleEngineEvent_ToolErrorIncrementsTurnCounter(t *testing.T) {
	m := newCoverageModel(t)
	if m.agentLoop.toolErrorsThisTurn != 0 {
		t.Fatalf("setup: counter should start at 0, got %d", m.agentLoop.toolErrorsThisTurn)
	}
	// One success — counter stays at 0.
	m = m.handleEngineEvent(engine.Event{
		Type:    "tool:result",
		Payload: map[string]any{"tool": "read_file", "success": true, "step": 1},
	})
	if m.agentLoop.toolErrorsThisTurn != 0 {
		t.Errorf("success result should not bump counter, got %d", m.agentLoop.toolErrorsThisTurn)
	}
	// Two failures — counter == 2.
	for range 2 {
		m = m.handleEngineEvent(engine.Event{
			Type:    "tool:result",
			Payload: map[string]any{"tool": "edit_file", "success": false, "step": 2},
		})
	}
	if m.agentLoop.toolErrorsThisTurn != 2 {
		t.Errorf("two failures should bump counter to 2, got %d", m.agentLoop.toolErrorsThisTurn)
	}
	// New ask resets.
	m = m.handleEngineEvent(engine.Event{
		Type:    "agent:loop:start",
		Payload: map[string]any{},
	})
	if m.agentLoop.toolErrorsThisTurn != 0 {
		t.Errorf("loop start should reset counter, got %d", m.agentLoop.toolErrorsThisTurn)
	}
}

// TestRuntimeStrip_ToolErrorsBadge_StyleEscalation pins "errs ×N"
// — info at 1-2, warn at 3+. A retry-heavy turn shows up while it's
// still happening, not just in the post-hoc summary card.
func TestRuntimeStrip_ToolErrorsBadge_StyleEscalation(t *testing.T) {
	for _, tc := range []struct {
		count   int
		visible bool
	}{
		{0, false}, // hidden
		{1, true},  // info
		{2, true},  // info
		{3, true},  // warn
		{8, true},  // warn
	} {
		vm := runtimeViewModel{AgentActive: true, ToolErrorsThisTurn: tc.count}
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		hasBadge := strings.Contains(joined, fmt.Sprintf("errs ×%d", tc.count))
		if tc.visible && !hasBadge {
			t.Errorf("count=%d: expected 'errs ×%d' badge, got %q", tc.count, tc.count, joined)
		}
		if !tc.visible && hasBadge {
			t.Errorf("count=%d: expected no badge, got %q", tc.count, joined)
		}
	}
}

// TestRuntimeStrip_LiveTurnDurationBadge pins the "running 2m 34s"
// badge that ticks during an active turn so a long autonomous run
// signals momentum without scrolling the activity feed. Hidden
// between turns; style escalates at 2m and 10m.
func TestRuntimeStrip_LiveTurnDurationBadge(t *testing.T) {
	t.Run("hidden when zero", func(t *testing.T) {
		vm := runtimeViewModel{AgentActive: true, TurnElapsedSec: 0}
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		if strings.Contains(joined, "running ") {
			t.Errorf("zero elapsed should hide badge, got %q", joined)
		}
	})
	t.Run("ticks on short turn", func(t *testing.T) {
		vm := runtimeViewModel{AgentActive: true, TurnElapsedSec: 47}
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		if !strings.Contains(joined, "running 47s") {
			t.Errorf("expected 'running 47s', got %q", joined)
		}
	})
	t.Run("formats minutes-seconds", func(t *testing.T) {
		vm := runtimeViewModel{AgentActive: true, TurnElapsedSec: 154} // 2m 34s
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		if !strings.Contains(joined, "running 2m 34s") {
			t.Errorf("expected 'running 2m 34s', got %q", joined)
		}
	})
	t.Run("formats hours past 1h", func(t *testing.T) {
		vm := runtimeViewModel{AgentActive: true, TurnElapsedSec: 4392} // 1h 13m
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		if !strings.Contains(joined, "running 1h 13m") {
			t.Errorf("expected 'running 1h 13m', got %q", joined)
		}
	})
}

// TestRuntimeStrip_FilesEditedBadge pins "edits ×N file(s)" — a
// fan-out refactor across many files registers as one persistent
// counter instead of N chips that scroll out.
func TestRuntimeStrip_FilesEditedBadge(t *testing.T) {
	for _, tc := range []struct {
		count int
		want  string
	}{
		{0, ""}, // hidden
		{1, "edits ×1 file"},
		{5, "edits ×5 files"},
		{12, "edits ×12 files"},
	} {
		vm := runtimeViewModel{AgentActive: true, TurnFilesEdited: tc.count}
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		if tc.want == "" {
			if strings.Contains(joined, "edits ×") {
				t.Errorf("count=%d: expected no badge, got %q", tc.count, joined)
			}
			continue
		}
		if !strings.Contains(joined, tc.want) {
			t.Errorf("count=%d: expected %q in strip, got %q", tc.count, tc.want, joined)
		}
	}
}

// TestComputeTurnElapsedSec pins the helper — zero when the loop is
// inactive or turnStartedAt is unset; positive when the turn is in
// flight; never negative even if the system clock walks backwards.
func TestComputeTurnElapsedSec(t *testing.T) {
	t.Run("zero when inactive", func(t *testing.T) {
		s := agentLoopState{active: false, turnStartedAt: time.Now().Add(-time.Minute)}
		if got := computeTurnElapsedSec(s); got != 0 {
			t.Errorf("inactive turn: expected 0, got %d", got)
		}
	})
	t.Run("zero when turnStartedAt unset", func(t *testing.T) {
		s := agentLoopState{active: true} // turnStartedAt zero
		if got := computeTurnElapsedSec(s); got != 0 {
			t.Errorf("unset turnStartedAt: expected 0, got %d", got)
		}
	})
	t.Run("positive when active", func(t *testing.T) {
		s := agentLoopState{active: true, turnStartedAt: time.Now().Add(-30 * time.Second)}
		got := computeTurnElapsedSec(s)
		if got < 28 || got > 32 {
			t.Errorf("expected ~30s, got %d", got)
		}
	})
	t.Run("never negative on clock skew", func(t *testing.T) {
		// Future turnStartedAt (clock moved backwards). Should clamp to 0
		// rather than rendering "running -3s".
		s := agentLoopState{active: true, turnStartedAt: time.Now().Add(time.Minute)}
		if got := computeTurnElapsedSec(s); got != 0 {
			t.Errorf("future timestamp: expected 0, got %d", got)
		}
	})
}

// TestRuntimeStrip_SubagentBadgeInNowStrip pins promoting the
// "agents ×N" badge from the workflow sub-strip into the prominent
// runtime "now" line so parallel fan-out registers at a glance.
func TestRuntimeStrip_SubagentBadgeInNowStrip(t *testing.T) {
	t.Run("hidden when zero", func(t *testing.T) {
		vm := runtimeViewModel{AgentActive: true, ActiveSubagents: 0}
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		if strings.Contains(joined, "agents ×") {
			t.Errorf("zero subagents should hide badge, got %q", joined)
		}
	})
	t.Run("visible without limit", func(t *testing.T) {
		vm := runtimeViewModel{AgentActive: true, ActiveSubagents: 3}
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		if !strings.Contains(joined, "agents ×3") {
			t.Errorf("expected 'agents ×3', got %q", joined)
		}
		if strings.Contains(joined, "agents ×3/") {
			t.Errorf("limit zero should suppress slash form, got %q", joined)
		}
	})
	t.Run("visible with limit", func(t *testing.T) {
		vm := runtimeViewModel{AgentActive: true, ActiveSubagents: 2, SubagentLimit: 4}
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		if !strings.Contains(joined, "agents ×2/4") {
			t.Errorf("expected 'agents ×2/4', got %q", joined)
		}
	})
}

// TestRuntimeStrip_DriveProgressInNowStrip pins promoting drive
// done/total into the now strip. A long Drive run with the user
// off the workflow tab still shows progress without tab-switching.
func TestRuntimeStrip_DriveProgressInNowStrip(t *testing.T) {
	t.Run("hidden when no run id and zero total", func(t *testing.T) {
		vm := runtimeViewModel{DriveRunID: "", DriveDone: 0, DriveTotal: 0}
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		if strings.Contains(joined, "drive ") {
			t.Errorf("idle drive should hide badge, got %q", joined)
		}
	})
	t.Run("visible with active run", func(t *testing.T) {
		vm := runtimeViewModel{DriveRunID: "abc12345", DriveDone: 5, DriveTotal: 12}
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		if !strings.Contains(joined, "drive 5/12") {
			t.Errorf("expected 'drive 5/12', got %q", joined)
		}
		if strings.Contains(joined, "blocked") {
			t.Errorf("zero blocked should suppress blocked clause, got %q", joined)
		}
	})
	t.Run("warn-styled with blocked TODOs", func(t *testing.T) {
		vm := runtimeViewModel{DriveRunID: "abc12345", DriveDone: 3, DriveTotal: 8, DriveBlocked: 2}
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		if !strings.Contains(joined, "drive 3/8") {
			t.Errorf("expected 'drive 3/8', got %q", joined)
		}
		if !strings.Contains(joined, "2 blocked") {
			t.Errorf("expected '2 blocked' clause, got %q", joined)
		}
	})
}

// TestRuntimeStrip_CacheHitsBadge_RendersAndHidesAtZero pins the
// "cache ×N" badge in the runtime strip.
func TestRuntimeStrip_CacheHitsBadge_RendersAndHidesAtZero(t *testing.T) {
	// Hidden at zero
	vm := runtimeViewModel{AgentActive: true, CacheHitsThisTurn: 0}
	joined := strings.Join(runtimeStripNowParts(vm), " | ")
	if strings.Contains(joined, "cache ×") {
		t.Errorf("zero hits should hide badge, got %q", joined)
	}
	// Visible with count
	vm.CacheHitsThisTurn = 5
	joined = strings.Join(runtimeStripNowParts(vm), " | ")
	if !strings.Contains(joined, "cache ×5") {
		t.Errorf("expected 'cache ×5' in strip, got %q", joined)
	}
}

// TestRuntimeStrip_LastTurnInlineCost pins the per-turn cost
// rendering on the "last in X out Y total Z" line. A user
// iterating on a question wants to know whether the last turn
// cost $0.001 or $0.30 — without inline cost they had to subtract
// session totals before/after which is impractical.
func TestRuntimeStrip_LastTurnInlineCost(t *testing.T) {
	// $5/Mtok = $0.005/ktok. 4_000 tokens = $0.020.
	vm := runtimeViewModel{
		LastInputTokens:  3_000,
		LastOutputTokens: 1_000,
		LastTotalTokens:  4_000,
		CostPer1kTokens:  0.005,
	}
	joined := strings.Join(runtimeStripTokenParts(vm), " | ")
	if !strings.Contains(joined, "last in 3.0k out 1.0k total 4.0k") {
		t.Errorf("expected last-turn token line, got %q", joined)
	}
	if !strings.Contains(joined, "$0.02") {
		t.Errorf("expected inline cost ~$0.02, got %q", joined)
	}

	// Without a price configured: no cost segment, just counts.
	vm.CostPer1kTokens = 0
	joined = strings.Join(runtimeStripTokenParts(vm), " | ")
	if strings.Contains(joined, "$") {
		t.Errorf("zero CostPer1kTokens should hide cost, got %q", joined)
	}
}

// TestRuntimeStrip_CompactsThisTurnBadge pins the "compacts ×N · -Mk
// reclaimed" badge that surfaces a budget-thrashing turn. Style
// escalates: 1-3 = info, 4+ = warn. Hidden when zero compacts have
// fired this turn so healthy short turns stay quiet.
func TestRuntimeStrip_CompactsThisTurnBadge(t *testing.T) {
	cases := []struct {
		name      string
		count     int
		reclaimed int
		want      string // substring expected in strip
		wantEmpty bool   // overrides the substring check
	}{
		{name: "zero compacts → hidden", count: 0, wantEmpty: true},
		{name: "one compact with reclaim", count: 1, reclaimed: 8_000, want: "compacts ×1 · -8.0k reclaimed"},
		{name: "three compacts (info)", count: 3, reclaimed: 24_000, want: "compacts ×3 · -24k reclaimed"},
		{name: "four compacts (warn)", count: 4, reclaimed: 32_000, want: "compacts ×4 · -32k reclaimed"},
		{name: "compacts without reclaim total", count: 2, reclaimed: 0, want: "compacts ×2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vm := runtimeViewModel{
				AgentActive:              true,
				CompactsThisTurn:         tc.count,
				CompactReclaimedThisTurn: tc.reclaimed,
			}
			joined := strings.Join(runtimeStripNowParts(vm), " | ")
			if tc.wantEmpty {
				if strings.Contains(joined, "compacts ×") {
					t.Errorf("zero compacts should not render badge, got %q", joined)
				}
				return
			}
			if !strings.Contains(joined, tc.want) {
				t.Errorf("expected %q in strip, got %q", tc.want, joined)
			}
		})
	}
}

func TestRuntimeStrip_NoLiveLoopBadgeWhenIdle(t *testing.T) {
	vm := runtimeViewModel{AgentActive: false, LiveLoopTokens: 0}
	joined := strings.Join(runtimeStripNowParts(vm), " | ")
	if strings.Contains(joined, "loop ~") {
		t.Errorf("no active loop → no badge, got %q", joined)
	}
}

func TestBuildTurnSummary_QuietForTrivialTurn(t *testing.T) {
	// Zero-effort turn (e.g. one-shot Q+A with no tools) → no card.
	if got := buildTurnSummary(agentLoopState{}, 0, 0, 0); got != "" {
		t.Errorf("trivial turn → no summary, got %q", got)
	}
}

func TestBuildTurnSummary_EditsAndValidationRecap(t *testing.T) {
	s := agentLoopState{
		toolRounds:             12,
		turnStartedAt:          time.Now().Add(-2 * time.Minute),
		turnEditedFiles:        []string{"a.go", "b.go", "c.go", "d.go", "e.go"},
		turnValidationPasses:   3,
		turnCoachInterventions: 0,
		unvalidatedEdits:       nil, // all validated
	}
	got := buildTurnSummary(s, 0, 0, 0)
	if !strings.Contains(got, "Turn summary") {
		t.Errorf("missing header in %q", got)
	}
	if !strings.Contains(got, "12 round(s)") {
		t.Errorf("missing tool round count in %q", got)
	}
	if !strings.Contains(got, "edited 5") {
		t.Errorf("missing edit count in %q", got)
	}
	if !strings.Contains(got, "+2 more") {
		t.Errorf("missing path-preview tail in %q", got)
	}
	if !strings.Contains(got, "3 passes ran") {
		t.Errorf("missing validation count in %q", got)
	}
	if !strings.Contains(got, "still unverified: 0") {
		t.Errorf("missing zero-unverified status in %q", got)
	}
	if strings.Contains(got, "coach:") {
		t.Errorf("zero coach interventions → no coach row, got %q", got)
	}
	if !strings.Contains(got, "duration:") {
		t.Errorf("expected duration row, got %q", got)
	}
}

func TestBuildTurnSummary_IncludesCoachAndCeiling(t *testing.T) {
	s := agentLoopState{
		toolRounds:             20,
		turnEditedFiles:        []string{"a.go"},
		turnValidationPasses:   1,
		turnCoachInterventions: 2,
		cumulativeSteps:        78,
		stepCeiling:            600,
	}
	got := buildTurnSummary(s, 0, 0, 0)
	if !strings.Contains(got, "2 interventions") {
		t.Errorf("expected coach interventions row, got %q", got)
	}
	if !strings.Contains(got, "78/600") {
		t.Errorf("expected ceiling counter, got %q", got)
	}
	if !strings.Contains(got, "13%") {
		t.Errorf("expected ceiling percent, got %q", got)
	}
	// Singular for one validation pass.
	if !strings.Contains(got, "1 pass ran") {
		t.Errorf("expected singular 'pass', got %q", got)
	}
}

func TestBuildTurnSummary_SingularCoachWording(t *testing.T) {
	s := agentLoopState{
		toolRounds:             5,
		turnEditedFiles:        []string{"a.go"},
		turnCoachInterventions: 1,
	}
	got := buildTurnSummary(s, 0, 0, 0)
	if !strings.Contains(got, "1 intervention ") {
		t.Errorf("expected singular 'intervention', got %q", got)
	}
}

func TestBuildTurnSummary_IncludesTodos(t *testing.T) {
	// Turn that used todo_write — summary should call out plan progress
	// alongside edits/validation/coach.
	s := agentLoopState{
		toolRounds:           10,
		turnEditedFiles:      []string{"auth/token.go"},
		turnValidationPasses: 1,
	}
	got := buildTurnSummary(s, 5, 3, 2) // 5 total, 3 done, 2 still pending
	if !strings.Contains(got, "todos:") {
		t.Errorf("expected todos row, got %q", got)
	}
	if !strings.Contains(got, "3 of 5 done") {
		t.Errorf("expected '3 of 5 done', got %q", got)
	}
	if !strings.Contains(got, "2 still pending") {
		t.Errorf("expected pending hint, got %q", got)
	}
}

func TestBuildTurnSummary_TodosAllDone(t *testing.T) {
	// All TODOs completed — no pending hint.
	s := agentLoopState{toolRounds: 8, turnEditedFiles: []string{"a.go"}}
	got := buildTurnSummary(s, 4, 4, 0)
	if !strings.Contains(got, "4 of 4 done") {
		t.Errorf("expected '4 of 4 done', got %q", got)
	}
	if strings.Contains(got, "still pending") {
		t.Errorf("zero pending → no pending hint, got %q", got)
	}
}

func TestBuildTurnSummary_TodosAlone(t *testing.T) {
	// Pure-planning turn (no edits/validation/coach/ceiling) but used
	// todo_write — should still produce the summary card.
	got := buildTurnSummary(agentLoopState{}, 3, 1, 2)
	if got == "" {
		t.Fatal("turn with TODOs should produce a summary card even without edits")
	}
	if !strings.Contains(got, "1 of 3 done") {
		t.Errorf("expected todo counter, got %q", got)
	}
}

// TestBuildTurnSummary_IncludesCompactsAndCache pins the two
// budget-pressure rows added so the per-turn signal that lives on
// the runtime badge survives into scrollback after agent:loop:final
// resets the badge state.
func TestBuildTurnSummary_IncludesCompactsAndCache(t *testing.T) {
	s := agentLoopState{
		toolRounds:           14,
		turnEditedFiles:      []string{"x.go"},
		compactsThisTurn:     4,
		compactReclaimedTurn: 142000,
		cacheHitsThisTurn:    3,
	}
	got := buildTurnSummary(s, 0, 0, 0)
	if !strings.Contains(got, "compacts:") {
		t.Errorf("expected compacts row, got %q", got)
	}
	if !strings.Contains(got, "4 cycles") {
		t.Errorf("expected '4 cycles' plural form, got %q", got)
	}
	if !strings.Contains(got, "reclaimed 142.0k") {
		t.Errorf("expected reclaim figure, got %q", got)
	}
	if !strings.Contains(got, "cache:") {
		t.Errorf("expected cache row, got %q", got)
	}
	if !strings.Contains(got, "3 hits") {
		t.Errorf("expected '3 hits' plural form, got %q", got)
	}
}

// TestBuildTurnSummary_SingularCompactAndCacheWording pins the
// "1 cycle"/"1 hit" branches so we don't ship "1 cycles" / "1 hits"
// once a turn happens to have exactly one of each.
func TestBuildTurnSummary_SingularCompactAndCacheWording(t *testing.T) {
	s := agentLoopState{
		toolRounds:           3,
		turnEditedFiles:      []string{"y.go"},
		compactsThisTurn:     1,
		compactReclaimedTurn: 0, // reclaim hint suppressed when zero
		cacheHitsThisTurn:    1,
	}
	got := buildTurnSummary(s, 0, 0, 0)
	if !strings.Contains(got, "1 cycle") || strings.Contains(got, "1 cycles") {
		t.Errorf("expected singular 'cycle' wording, got %q", got)
	}
	if !strings.Contains(got, "1 hit") || strings.Contains(got, "1 hits") {
		t.Errorf("expected singular 'hit' wording, got %q", got)
	}
	if strings.Contains(got, "reclaimed") {
		t.Errorf("zero reclaim should suppress the parenthetical, got %q", got)
	}
}

// TestBuildTurnSummary_CompactsAloneStillRenders pins that a turn
// with NO edits, NO validation, NO coach, NO ceiling, NO todos, but
// non-zero compacts (e.g. a long-context Q+A that thrashed compact
// without writing any files) still produces the card. Without this
// the user has no record of how heavy the turn was.
func TestBuildTurnSummary_CompactsAloneStillRenders(t *testing.T) {
	s := agentLoopState{compactsThisTurn: 2, compactReclaimedTurn: 80000}
	got := buildTurnSummary(s, 0, 0, 0)
	if got == "" {
		t.Fatal("compacts-only turn should still produce a summary card")
	}
	if !strings.Contains(got, "compacts:") {
		t.Errorf("expected compacts row, got %q", got)
	}
}

// TestBuildTurnSummary_IncludesToolErrors pins the per-turn fragility
// row. Without it, a turn that recovered through 8 tool failures
// reads identically to a clean turn once chips scroll.
func TestBuildTurnSummary_IncludesToolErrors(t *testing.T) {
	t.Run("plural", func(t *testing.T) {
		s := agentLoopState{
			toolRounds:         18,
			turnEditedFiles:    []string{"a.go"},
			toolErrorsThisTurn: 8,
		}
		got := buildTurnSummary(s, 0, 0, 0)
		if !strings.Contains(got, "errors:") {
			t.Errorf("expected errors row, got %q", got)
		}
		if !strings.Contains(got, "8 tool failures") {
			t.Errorf("expected '8 tool failures' plural, got %q", got)
		}
		if !strings.Contains(got, "recovered to final answer") {
			t.Errorf("expected recovery clause, got %q", got)
		}
	})
	t.Run("singular wording for one error", func(t *testing.T) {
		s := agentLoopState{
			toolRounds:         3,
			turnEditedFiles:    []string{"a.go"},
			toolErrorsThisTurn: 1,
		}
		got := buildTurnSummary(s, 0, 0, 0)
		if strings.Contains(got, "1 tool failures") {
			t.Errorf("expected singular wording, got %q", got)
		}
		if !strings.Contains(got, "1 tool failure") {
			t.Errorf("expected '1 tool failure' singular, got %q", got)
		}
	})
	t.Run("errors-alone still renders the card", func(t *testing.T) {
		// Pure-error turn (no edits, no validation, no compacts) — still
		// surfaces because "this turn was fragile" is itself a signal.
		s := agentLoopState{toolErrorsThisTurn: 4}
		got := buildTurnSummary(s, 0, 0, 0)
		if got == "" {
			t.Fatal("errors-only turn should produce a summary card")
		}
		if !strings.Contains(got, "errors:") {
			t.Errorf("expected errors row, got %q", got)
		}
	})
}

func TestToolReasoningUpdatesRuntimeStrip(t *testing.T) {
	m := newCoverageModel(t)
	m = m.handleEngineEvent(engine.Event{
		Type: "tool:reasoning",
		Payload: map[string]any{
			"tool":   "read_file",
			"reason": "checking how the SSE handler closes the stream",
		},
	})
	if m.agentLoop.lastToolReason == "" {
		t.Fatal("tool:reasoning should populate lastToolReason")
	}
	if !strings.Contains(m.agentLoop.lastToolReason, "SSE handler") {
		t.Errorf("reason should be stored verbatim, got %q", m.agentLoop.lastToolReason)
	}
}

func TestToolCallClearsStaleReason(t *testing.T) {
	// Round 1: tool:reasoning lands a reason. Round 2: a new tool:call
	// without a bundled reason → previous reason MUST clear so the
	// runtime strip doesn't show stale intent from the previous call.
	m := newCoverageModel(t)
	m = m.handleEngineEvent(engine.Event{
		Type: "tool:reasoning",
		Payload: map[string]any{
			"tool":   "read_file",
			"reason": "checking the SSE handler",
		},
	})
	if m.agentLoop.lastToolReason == "" {
		t.Fatal("setup: reason should be set")
	}
	m = m.handleEngineEvent(engine.Event{
		Type: "tool:call",
		Payload: map[string]any{
			"tool": "edit_file",
			"step": 5,
		},
	})
	if m.agentLoop.lastToolReason != "" {
		t.Errorf("new tool:call without reason should clear stale reason, got %q",
			m.agentLoop.lastToolReason)
	}
}

func TestToolCallCarriesReasonInline(t *testing.T) {
	m := newCoverageModel(t)
	m = m.handleEngineEvent(engine.Event{
		Type: "tool:call",
		Payload: map[string]any{
			"tool":   "grep_codebase",
			"reason": "locate the parking-state save site",
			"step":   3,
		},
	})
	if !strings.Contains(m.agentLoop.lastToolReason, "parking-state save site") {
		t.Errorf("inline reason on tool:call should populate state, got %q",
			m.agentLoop.lastToolReason)
	}
}

func TestRuntimeStrip_RendersToolReasonBadge(t *testing.T) {
	vm := runtimeViewModel{
		AgentActive:    true,
		AgentStep:      8,
		AgentMaxSteps:  60,
		LastToolReason: "investigating the SSE close behaviour",
	}
	joined := strings.Join(runtimeStripNowParts(vm), " | ")
	if !strings.Contains(joined, "→ ") {
		t.Errorf("expected narration arrow in strip, got %q", joined)
	}
	if !strings.Contains(joined, "SSE close behaviour") {
		t.Errorf("expected reason text in strip, got %q", joined)
	}
}

func TestRuntimeStrip_NoReasonBadgeWhenEmpty(t *testing.T) {
	vm := runtimeViewModel{AgentActive: true, AgentStep: 1, AgentMaxSteps: 60}
	joined := strings.Join(runtimeStripNowParts(vm), " | ")
	if strings.Contains(joined, "→ ") {
		t.Errorf("no reason → no narration badge, got %q", joined)
	}
}

func TestAutonomyHealthLine_QuietWhenHealthy(t *testing.T) {
	if got := autonomyHealthLine(agentLoopState{}); got != "" {
		t.Errorf("empty state → empty line, got %q", got)
	}
	// Tool name set but count zero → still quiet (defensive).
	if got := autonomyHealthLine(agentLoopState{stuckTool: "read_file", stuckCount: 0}); got != "" {
		t.Errorf("zero count → empty line, got %q", got)
	}
}

func TestAutonomyHealthLine_NamesToolCountAndErrClass(t *testing.T) {
	s := agentLoopState{
		stuckTool:     "read_file",
		stuckCount:    4,
		stuckErrClass: "file does not exist",
	}
	got := autonomyHealthLine(s)
	if !strings.Contains(got, "read_file") {
		t.Errorf("missing tool name in %q", got)
	}
	if !strings.Contains(got, "×4") {
		t.Errorf("missing count in %q", got)
	}
	if !strings.Contains(got, "file does not exist") {
		t.Errorf("missing error class in %q", got)
	}
	if !strings.Contains(got, "switch tactic") {
		t.Errorf("missing prescription in %q", got)
	}
}

func TestAutonomyCeilingLine_ShowsStepPercentAndTokenWindow(t *testing.T) {
	s := agentLoopState{
		cumulativeSteps:  240,
		stepCeiling:      600,
		cumulativeTokens: 920_000,
		tokenCeiling:     2_500_000,
	}
	got := autonomyCeilingLine(s)
	if !strings.Contains(got, "240/600") {
		t.Errorf("missing step counter in %q", got)
	}
	if !strings.Contains(got, "40%") {
		t.Errorf("missing step percent in %q", got)
	}
	if !strings.Contains(got, "tokens") {
		t.Errorf("missing token window in %q", got)
	}
	if !strings.Contains(got, "100%") {
		t.Errorf("missing ceiling explanation in %q", got)
	}
}

func TestAutonomyCeilingLine_QuietBeforeFirstAutoResume(t *testing.T) {
	if got := autonomyCeilingLine(agentLoopState{}); got != "" {
		t.Errorf("no auto-resume yet → empty line, got %q", got)
	}
}

func TestAutonomyUnverifiedLine_EscalatesAtThreeEdits(t *testing.T) {
	gentle := autonomyUnverifiedLine(agentLoopState{
		unvalidatedEdits: []string{"a.go", "b.go"},
	})
	if !strings.Contains(gentle, "2 edits") {
		t.Errorf("missing count in %q", gentle)
	}
	if strings.Contains(gentle, "STOP") {
		t.Errorf("count<3 should not include STOP, got %q", gentle)
	}

	directive := autonomyUnverifiedLine(agentLoopState{
		unvalidatedEdits: []string{"a.go", "b.go", "c.go", "d.go", "e.go"},
	})
	if !strings.Contains(directive, "5 EDITS") {
		t.Errorf("count>=3 should escalate; got %q", directive)
	}
	if !strings.Contains(directive, "STOP") {
		t.Errorf("count>=3 should include STOP directive; got %q", directive)
	}
	// Path preview caps at 3 + "+N more".
	if !strings.Contains(directive, "+2 more") {
		t.Errorf("expected +2 more tail for 5 paths, got %q", directive)
	}
	// And only the first 3 paths appear in the preview.
	if !strings.Contains(directive, "a.go, b.go, c.go") {
		t.Errorf("expected first 3 paths in preview, got %q", directive)
	}
	if strings.Contains(directive, "d.go") {
		t.Errorf("4th path should be hidden behind +N more, got %q", directive)
	}
}

func TestAutonomyUnverifiedLine_QuietWhenLedgerEmpty(t *testing.T) {
	if got := autonomyUnverifiedLine(agentLoopState{}); got != "" {
		t.Errorf("empty ledger → empty line, got %q", got)
	}
}

func TestHandleEngineEvent_StuckForceStop_SurfacesChipAndLine(t *testing.T) {
	m := newCoverageModel(t)
	payload := map[string]any{
		"step":         12,
		"stuck_streak": 3,
		"threshold":    3,
	}
	m2, line := m.handleAgentLoopEvent("agent:loop:stuck_force_stop", payload)
	if !strings.Contains(line, "stuck for 3 consecutive rounds") {
		t.Errorf("expected stuck-streak narration in line, got %q", line)
	}
	if !strings.Contains(line, "text-only") {
		t.Errorf("line should explain the force-stop, got %q", line)
	}
	timeline := m2.agentLoop.toolTimeline
	if len(timeline) == 0 {
		t.Fatal("expected a force-stop chip on the timeline")
	}
	chip := timeline[len(timeline)-1]
	if chip.Name != "force-stop" {
		t.Errorf("expected chip name 'force-stop', got %q", chip.Name)
	}
	if chip.Status != "warn" {
		t.Errorf("expected warn status, got %q", chip.Status)
	}
	if !strings.Contains(chip.Preview, "3 rounds stuck") {
		t.Errorf("chip preview should cite streak, got %q", chip.Preview)
	}
}

func TestHandleEngineEvent_CoachUnverified_FiresAtThreshold(t *testing.T) {
	m := newCoverageModel(t)
	event := engine.Event{
		Type: "agent:coach:unverified",
		Payload: map[string]any{
			"step":         8,
			"file_count":   4,
			"sample_paths": []any{"a.go", "b.go", "c.go", "d.go"},
		},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.agentLoop.sessionCoachNotes) == 0 {
		t.Fatal("unverified event should add a coach note")
	}
	last := m2.agentLoop.sessionCoachNotes[len(m2.agentLoop.sessionCoachNotes)-1]
	if !strings.Contains(last, "4 unverified edits") {
		t.Errorf("note should cite count, got %q", last)
	}
	if !strings.Contains(last, "STOP editing") {
		t.Errorf("note should be directive, got %q", last)
	}
	if !strings.Contains(last, "a.go") {
		t.Errorf("note should preview paths, got %q", last)
	}
	tail := m2.chat.transcript[len(m2.chat.transcript)-1]
	if !strings.Contains(tail.Content, "⚠") {
		t.Errorf("notice should carry warn marker, got %q", tail.Content)
	}
}

func TestHandleEngineEvent_CoachUnverified_BelowThresholdDropped(t *testing.T) {
	m := newCoverageModel(t)
	before := len(m.agentLoop.sessionCoachNotes)
	event := engine.Event{
		Type: "agent:coach:unverified",
		Payload: map[string]any{
			"file_count":   2, // below threshold
			"sample_paths": []any{"a.go", "b.go"},
		},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.agentLoop.sessionCoachNotes) != before {
		t.Errorf("count<3 should not surface a notice, got %d new notes", len(m2.agentLoop.sessionCoachNotes)-before)
	}
}

func TestTrackMutationOrValidation_EditAccumulates(t *testing.T) {
	m := newCoverageModel(t)
	m = m.trackMutationOrValidation("edit_file", map[string]any{
		"changed_files": []any{"internal/auth/token.go"},
	}, 5)
	if got := len(m.agentLoop.unvalidatedEdits); got != 1 {
		t.Fatalf("expected 1 unvalidated edit, got %d", got)
	}
	if m.agentLoop.unvalidatedSinceStep != 5 {
		t.Errorf("expected unvalidatedSinceStep=5, got %d", m.agentLoop.unvalidatedSinceStep)
	}
	// Second edit on a different file → count grows.
	m = m.trackMutationOrValidation("write_file", map[string]any{
		"changed_files": []any{"internal/auth/handler.go"},
	}, 8)
	if got := len(m.agentLoop.unvalidatedEdits); got != 2 {
		t.Errorf("expected 2 unvalidated edits, got %d", got)
	}
	// since-step should pin to the FIRST edit, not move with each new one.
	if m.agentLoop.unvalidatedSinceStep != 5 {
		t.Errorf("unvalidatedSinceStep should pin to first edit, got %d", m.agentLoop.unvalidatedSinceStep)
	}
	// Re-editing the same file → no double-count.
	m = m.trackMutationOrValidation("edit_file", map[string]any{
		"changed_files": []any{"internal/auth/token.go"},
	}, 10)
	if got := len(m.agentLoop.unvalidatedEdits); got != 2 {
		t.Errorf("re-edit should not double-count, got %d", got)
	}
}

func TestTrackMutationOrValidation_BuildClears(t *testing.T) {
	m := newCoverageModel(t)
	m.agentLoop.unvalidatedEdits = []string{"a.go", "b.go", "c.go"}
	m.agentLoop.unvalidatedSinceStep = 5
	m = m.trackMutationOrValidation("run_command", map[string]any{
		"command": "go test ./internal/auth/...",
	}, 12)
	if len(m.agentLoop.unvalidatedEdits) != 0 {
		t.Errorf("validation command should clear ledger, got %v", m.agentLoop.unvalidatedEdits)
	}
	if m.agentLoop.unvalidatedSinceStep != 0 {
		t.Errorf("since-step should reset, got %d", m.agentLoop.unvalidatedSinceStep)
	}
}

func TestTrackMutationOrValidation_NonValidationCmdLeavesLedger(t *testing.T) {
	m := newCoverageModel(t)
	m.agentLoop.unvalidatedEdits = []string{"a.go"}
	m = m.trackMutationOrValidation("run_command", map[string]any{
		"command": "git status",
	}, 5)
	if len(m.agentLoop.unvalidatedEdits) != 1 {
		t.Errorf("non-validation command should NOT clear ledger, got %v", m.agentLoop.unvalidatedEdits)
	}
}

func TestIsValidationCommand(t *testing.T) {
	yes := []string{
		"go test",
		"go test ./...",
		"go test -race ./internal/engine",
		"go vet ./...",
		"go build",
		"npm test",
		"pnpm test",
		"yarn test",
		"npm run test",
		"pytest",
		"pytest tests/",
		"cargo test",
		"cargo check",
		"tsc",
		"tsc --noEmit",
		"eslint .",
		"biome check",
		"make build",
		"make test",
	}
	no := []string{
		"",
		"git status",
		"ls -la",
		"go run main.go", // run, not test/build/vet
		"npm install",
		"echo done",
	}
	for _, c := range yes {
		if !isValidationCommand(c) {
			t.Errorf("isValidationCommand(%q) = false, want true", c)
		}
	}
	for _, c := range no {
		if isValidationCommand(c) {
			t.Errorf("isValidationCommand(%q) = true, want false", c)
		}
	}
}

func TestRuntimeStrip_UnverifiedEditsBadgeEscalates(t *testing.T) {
	cases := []struct {
		count    int
		want     string
		variant  string
		wantSubs []string
	}{
		{1, "unverified: 1 edit", "info", []string{"unverified: 1 edit"}},
		{2, "unverified: 2 edits", "info", []string{"unverified: 2 edits"}},
		{3, "unverified: 3 edits", "warn", []string{"unverified: 3 edits"}},
		{7, "unverified: 7 edits", "warn", []string{"unverified: 7 edits"}},
	}
	for _, c := range cases {
		vm := runtimeViewModel{AgentActive: true, UnvalidatedEdits: c.count}
		joined := strings.Join(runtimeStripNowParts(vm), " | ")
		for _, sub := range c.wantSubs {
			if !strings.Contains(joined, sub) {
				t.Errorf("count=%d expected %q in %q", c.count, sub, joined)
			}
		}
	}
	// Zero edits → no badge.
	vm := runtimeViewModel{AgentActive: true}
	joined := strings.Join(runtimeStripNowParts(vm), " | ")
	if strings.Contains(joined, "unverified") {
		t.Errorf("zero edits → no badge, got %q", joined)
	}
}

func TestRuntimeStrip_NoBadgeWhenNotStuck(t *testing.T) {
	vm := runtimeViewModel{AgentActive: true, AgentStep: 5, AgentMaxSteps: 60}
	parts := runtimeStripNowParts(vm)
	joined := strings.Join(parts, " | ")
	if strings.Contains(joined, "stalled:") {
		t.Errorf("no stuck signal → no badge, got %q", joined)
	}
}

func TestHandleEngineEvent_CoachStuck_IgnoresEmptyTool(t *testing.T) {
	// Defensive: malformed payload (missing tool name or zero count)
	// should not produce a chip or a note — better to drop the event
	// than to render an empty "× failures" box.
	m := newCoverageModel(t)
	before := len(m.agentLoop.sessionCoachNotes)
	event := engine.Event{
		Type:    "agent:coach:stuck",
		Payload: map[string]any{"failure_count": 4},
	}
	m2 := m.handleEngineEvent(event)
	if len(m2.agentLoop.sessionCoachNotes) != before {
		t.Errorf("malformed stuck event should not add a note")
	}
}

// TestHandleEngineEvent_IndexErrorSurfacesChatEvent pins the new
// classifier for index:error. Engine publishes the error string as
// the raw Payload (not a map) — the handler must read event.Payload
// directly when it's a string. Without a chat-event line, a stale
// codemap silently degrades context retrieval and "wrong answer"
// becomes hard to diagnose.
func TestHandleEngineEvent_IndexErrorSurfacesChatEvent(t *testing.T) {
	m := newCoverageModel(t)
	m.chat.sending = true
	m = m.handleEngineEvent(engine.Event{
		Type:    "index:error",
		Payload: "tree-sitter: parse failed at pkg/foo/bar.go:42",
	})
	if m.notice == "" {
		t.Fatal("index:error should set notice line")
	}
	if !strings.Contains(strings.ToLower(m.notice), "workspace index failed") {
		t.Errorf("notice should mention 'workspace index failed': %q", m.notice)
	}
	if !strings.Contains(m.notice, "tree-sitter") {
		t.Errorf("notice should include the underlying error: %q", m.notice)
	}
	// Chat-event line should land in the transcript with warn status.
	found := false
	for _, line := range m.chat.transcript {
		for _, ev := range line.EventLines {
			if ev.Key == "index:error" && ev.Status == "warn" &&
				strings.Contains(strings.ToLower(ev.Title), "workspace index failed") {
				found = true
			}
		}
	}
	if !found {
		t.Error("index:error should produce a warn chat-event line titled 'workspace index failed'")
	}
}

// TestHandleEngineEvent_AgentNoteQueuedSurfacesChatEvent pins the
// /btw mid-flight buffer signal. Without it the user types a note,
// sees the composer clear, and has no confirmation it landed.
func TestHandleEngineEvent_AgentNoteQueuedSurfacesChatEvent(t *testing.T) {
	m := newCoverageModel(t)
	m.chat.sending = true
	m = m.handleEngineEvent(engine.Event{
		Type: "agent:note:queued",
		Payload: map[string]any{
			"note":  "remember to check error path on retry",
			"queue": 2,
		},
	})
	if m.notice == "" {
		t.Fatal("agent:note:queued should set notice")
	}
	if !strings.Contains(strings.ToLower(m.notice), "note queued") {
		t.Errorf("notice should mention 'note queued': %q", m.notice)
	}
	found := false
	for _, line := range m.chat.transcript {
		for _, ev := range line.EventLines {
			if strings.HasPrefix(ev.Key, "agent:note:queued:") &&
				strings.Contains(ev.Title, "note queued") &&
				strings.Contains(ev.Detail, "queue depth 2") {
				found = true
			}
		}
	}
	if !found {
		t.Error("agent:note:queued should produce a chat-event line with queue depth in detail")
	}
}

// TestHandleEngineEvent_ConfigReloadSurfacesChatEvent pins both
// auto-reload paths. A silently changed provider profile would
// otherwise look like the model started behaving differently for
// no reason; a silent reload-failed leaves the user thinking their
// edits applied when they didn't.
func TestHandleEngineEvent_ConfigReloadSurfacesChatEvent(t *testing.T) {
	t.Run("auto success", func(t *testing.T) {
		m := newCoverageModel(t)
		m.chat.sending = true
		m = m.handleEngineEvent(engine.Event{
			Type: "config:reload:auto",
			Payload: map[string]any{
				"path":       "/some/path/.dfmc/config.yaml",
				"updated_at": int64(1700000000),
			},
		})
		if !strings.Contains(strings.ToLower(m.notice), "auto-reloaded") {
			t.Errorf("notice should mention auto-reloaded: %q", m.notice)
		}
		found := false
		for _, line := range m.chat.transcript {
			for _, ev := range line.EventLines {
				if ev.Key == "config:reload:auto" && ev.Status == "ok" &&
					strings.Contains(ev.Detail, "config.yaml") {
					found = true
				}
			}
		}
		if !found {
			t.Error("config:reload:auto should produce ok chat-event line with basename in detail")
		}
	})
	t.Run("auto failed", func(t *testing.T) {
		m := newCoverageModel(t)
		m.chat.sending = true
		m = m.handleEngineEvent(engine.Event{
			Type: "config:reload:auto_failed",
			Payload: map[string]any{
				"path":  "/some/path/.dfmc/config.yaml",
				"error": "invalid provider profile: missing api_key",
			},
		})
		if !strings.Contains(strings.ToLower(m.notice), "config auto-reload failed") {
			t.Errorf("notice should mention reload failed: %q", m.notice)
		}
		found := false
		for _, line := range m.chat.transcript {
			for _, ev := range line.EventLines {
				if ev.Key == "config:reload:auto_failed" && ev.Status == "warn" &&
					strings.Contains(ev.Detail, "still on previous config") &&
					strings.Contains(ev.Detail, "missing api_key") {
					found = true
				}
			}
		}
		if !found {
			t.Error("config:reload:auto_failed should produce warn chat-event line including 'still on previous config' and the error")
		}
	})
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
