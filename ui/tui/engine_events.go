package tui

// engine_events.go — bubbletea router entry-point for engine.EventBus
// events. Most of the work happens in engineEventRegistry (see
// engine_event_registry.go) and per-domain handler siblings:
//
//   engine_events_loop.go      — agent:loop:* lifecycle
//   engine_events_tool.go      — tool:* (call/result/error/reasoning/
//                                timeout/denied) + chip-ribbon plumbing
//   engine_events_subagent.go  — agent:subagent:start/fallback/done
//   engine_events_drive.go     — drive:* run/plan/todo events
//   engine_events_provider.go  — provider:* throttle/race/fallback/circuit
//   engine_events_context.go   — context:* + history:trimmed
//   engine_events_coach.go     — coach:* + intent:decision + autonomy:*
//                                + assistant:* + agent:tool:cache_hit
//   engine_events_system.go    — config / shutdown / panic / hook /
//                                memory / index / security / save errors
//
// engine_event_registry.go owns the dispatch table; engine_event_adapters.go
// wires the legacy per-domain helpers into the same shape.
//
// What stays in this file:
//
//   - handleEngineEvent          — the tea entrypoint. Normalizes the
//                                  event type, fans the activity-panel
//                                  firehose, dispatches via the registry,
//                                  and runs the activity / notice /
//                                  transcript-mirror post-processing.
//   - shouldMirrorEventToTranscript
//                                — policy: which events earn a system
//                                  message in chat history.
//   - refreshWorkflowOnTabEnter  — pulls the drive run list when the
//                                  user enters the Workflow tab.
//   - appendActivity             — bounded ring-buffer append for the
//                                  activity log strip.
//   - resetAgentRuntime          — reset helper used after a turn ends.

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func (m Model) handleEngineEvent(event engine.Event) Model {
	eventType := strings.TrimSpace(strings.ToLower(event.Type))
	if eventType == "" {
		return m
	}
	// Activity panel captures every event before any filtering — it's the
	// firehose so users can see what the agent actually did.
	m.recordActivityEvent(event)
	payload, _ := toStringAnyMap(event.Payload)
	m, line, _ := engineEvents.dispatch(m, event, payload)
	line = strings.TrimSpace(line)
	if line == "" {
		return m
	}
	m.appendActivity(line)
	m.notice = line
	if m.chat.sending && shouldMirrorEventToTranscript(eventType) {
		m = m.appendToolEventMessage(line)
	}
	return m
}

// shouldMirrorEventToTranscript decides which engine events earn a system
// message in the chat transcript. tool:result is excluded — tool activity
// lives in the assistant message chip strip, not as separate TOOL lines.
// Other events are mirrored selectively — only real state changes the user
// needs in history.
func shouldMirrorEventToTranscript(eventType string) bool {
	switch strings.TrimSpace(strings.ToLower(eventType)) {
	case "agent:loop:error", "agent:loop:max_steps", "agent:loop:parked",
		"agent:loop:budget_exhausted", "agent:loop:tools_force_stop",
		"agent:loop:interrupted", "agent:loop:shutdown_parked",
		"agent:loop:resume_refused", "agent:loop:safety_bound",
		"agent:loop:empty_final", "provider:throttle:retry",
		"context:lifecycle:compacted", "context:lifecycle:handoff",
		"conversation:save:error", "coach:note", "tool:denied",
		"runtime:panic", "tool:panicked", "security:config_permissions",
		"memory:degraded", "context:error", "engine:shutdown_error",
		"provider:fallback", "config:reload:auto_failed", "index:error":
		return true
	default:
		return false
	}
}

// refreshWorkflowOnTabEnter is called when the user switches to the Workflow
// tab (F5 or alt+5). It reloads the run list from the drive store so the
// panel shows current state without requiring a drive event to have fired.
func (m Model) refreshWorkflowOnTabEnter() Model {
	if res, err := buildTUIDriver(m.eng, nil); err == nil {
		if runs, err := res.listRuns(); err == nil {
			m.workflow.runs = runs
		}
	}
	return m
}

func (m *Model) appendActivity(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if n := len(m.activityLog); n > 0 && strings.EqualFold(strings.TrimSpace(m.activityLog[n-1]), line) {
		return
	}
	m.activityLog = append(m.activityLog, line)
	if len(m.activityLog) > 24 {
		drop := len(m.activityLog) - 24
		m.activityLog = m.activityLog[drop:]
	}
}

func (m *Model) resetAgentRuntime() {
	m.agentLoop.active = false
	m.agentLoop.step = 0
	m.agentLoop.maxToolStep = 0
	m.agentLoop.toolRounds = 0
	m.agentLoop.phase = ""
	m.agentLoop.provider = ""
	m.agentLoop.model = ""
	m.agentLoop.lastTool = ""
	m.agentLoop.lastStatus = ""
	m.agentLoop.lastDuration = 0
	m.agentLoop.lastOutput = ""
	m.agentLoop.contextScope = ""
	m.agentLoop.sessionCoachNotes = nil
}
