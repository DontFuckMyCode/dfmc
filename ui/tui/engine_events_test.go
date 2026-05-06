package tui

import (
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
