package tui

// engine_events.go — bubbletea router for engine.EventBus events.
//
// Lifted out of tui.go to give the "what does each engine event do
// to the UI" surface a single home. Two related groups live here:
//
//   - handleEngineEvent         — 50+ case switch on event type that
//                                  drives the agent loop badge, the
//                                  tool chip ribbon, the activity
//                                  panel firehose, and the parked-
//                                  resume banner. Single source of
//                                  truth: if a new event type appears
//                                  on the engine side, it lands here.
//   - tool chip helpers          — pushToolChip, pushStreamingMessage
//                                  ToolChip, finishStreamingMessage
//                                  ToolChip, finishToolChip; manage
//                                  the assistant-message inline
//                                  tool-call ribbon.
//   - payload* helpers           — type-safe getters from the
//                                  map[string]any event payload
//                                  shape used by EventBus.
//   - shouldMirrorEventToTranscript / resetAgentRuntime — small
//                                  policy + reset helpers used only
//                                  by the event router.
//
// recordActivityEvent itself lives in activity.go (the activity panel
// owns the in-memory ring buffer); this file just calls it.

import (
	"fmt"
	"strings"
	"time"

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
	line := ""
	payload, _ := toStringAnyMap(event.Payload)
	switch eventType {
	case "agent:loop:start", "agent:loop:thinking", "agent:loop:final",
		"agent:loop:max_steps", "agent:loop:error", "agent:loop:parked",
		"agent:loop:budget_exhausted", "agent:loop:auto_resume",
		"agent:loop:auto_resume_refused", "agent:loop:auto_recover":
		m, line = m.handleAgentLoopEvent(eventType, payload)
	case "tool:call", "tool:result", "tool:error", "tool:reasoning":
		m, line = m.handleToolEvent(eventType, event, payload)
	case "agent:autonomy:plan":
		m.autoActivateStatsPanelMode(statsPanelModeTasks, "tasks")
		count := payloadInt(payload, "subtask_count", 0)
		confidence := 0.0
		if raw, ok := payload["confidence"].(float64); ok {
			confidence = raw
		}
		mode := "sequential"
		if payloadBool(payload, "parallel", false) {
			mode = "parallel"
		}
		scope := payloadString(payload, "scope", "")
		line = fmt.Sprintf("Autonomy preflight: %d subtasks (%s, %.2f confidence).", count, mode, confidence)
		if scope != "" && scope != "top_level" {
			line = fmt.Sprintf("Autonomy preflight [%s]: %d subtasks (%s, %.2f confidence).", scope, count, mode, confidence)
		}
		if payloadBool(payload, "todo_seeded", false) {
			line += " Todos seeded."
		}
	case "agent:autonomy:kickoff":
		m.autoActivateStatsPanelMode(statsPanelModeTasks, "tasks")
		toolName := payloadString(payload, "tool", "orchestrate")
		count := payloadInt(payload, "subtask_count", 0)
		confidence := 0.0
		if raw, ok := payload["confidence"].(float64); ok {
			confidence = raw
		}
		line = fmt.Sprintf("Autonomy kickoff: %s launched for %d subtasks (%.2f confidence).", toolName, count, confidence)
	case "coach:note":
		if m.ui.coachMuted {
			return m
		}
		text := payloadString(payload, "text", "")
		if strings.TrimSpace(text) == "" {
			return m
		}
		severity := coachSeverityFromWire(payloadString(payload, "severity", "info"))
		origin := payloadString(payload, "origin", "")
		action := payloadString(payload, "action", "")
		m = m.appendCoachMessage(text, severity, origin, action)
		return m
	case "intent:decision":
		// Engine's pre-Ask intent router fired. Cache the decision so
		// the header chip + /intent show can surface what the engine
		// actually saw. When verbose mode is on, also append a faint
		// gray transcript line showing the rewrite — useful for
		// debugging "why did it route to resume?" without reaching
		// for the activity log.
		intentName := payloadString(payload, "intent", "")
		source := payloadString(payload, "source", "")
		raw := payloadString(payload, "raw", "")
		enriched := payloadString(payload, "enriched", "")
		reasoning := payloadString(payload, "reasoning", "")
		followUp := payloadString(payload, "follow_up", "")
		latencyMs := int64(payloadInt(payload, "latency_ms", 0))
		m.intent.lastIntent = intentName
		m.intent.lastSource = source
		m.intent.lastRaw = raw
		m.intent.lastEnriched = enriched
		m.intent.lastReasoning = reasoning
		m.intent.lastFollowUp = followUp
		m.intent.lastLatencyMs = latencyMs
		m.intent.lastDecisionAtMs = time.Now().UnixMilli()
		if m.intent.verbose && source == "llm" && raw != "" && enriched != "" && raw != enriched {
			m = m.appendCoachMessage(
				fmt.Sprintf("intent[%s]: %s → %s", intentName, truncateSingleLine(raw, 60), truncateSingleLine(enriched, 80)),
				coachSeverityInfo,
				"intent",
				"",
			)
		}
		return m
	case "agent:coach:hint":
		if !m.ui.hintsVerbose {
			return m
		}
		hints, _ := payload["hints"].([]any)
		for _, h := range hints {
			if s, ok := h.(string); ok && strings.TrimSpace(s) != "" {
				m = m.appendCoachMessage("→ "+s, coachSeverityInfo, "trajectory", "")
			}
		}
		return m
	case "agent:subagent:start", "agent:subagent:fallback", "agent:subagent:done":
		m, line = m.handleSubagentEvent(eventType, payload)
	case "context:built":
		files := payloadInt(payload, "files", 0)
		tokens := payloadInt(payload, "tokens", 0)
		task := payloadString(payload, "task", "general")
		comp := payloadString(payload, "compression", "-")
		line = fmt.Sprintf("Context built: %d files, %d tokens (%s, %s)", files, tokens, task, comp)
	case "provider:complete":
		if m.agentLoop.active {
			m.agentLoop.phase = "complete"
			m.agentLoop.active = false
			tokens := payloadInt(payload, "tokens", 0)
			providerName := payloadString(payload, "provider", m.agentLoop.provider)
			modelName := payloadString(payload, "model", m.agentLoop.model)
			line = fmt.Sprintf("Provider complete: %s/%s (%dtok)", providerName, modelName, tokens)
		}
	case "provider:throttle:retry":
		providerName := payloadString(payload, "provider", "?")
		attempt := payloadInt(payload, "attempt", 0)
		waitMs := payloadInt(payload, "wait_ms", 0)
		streaming := payloadBool(payload, "stream", false)
		label := "request"
		if streaming {
			label = "stream"
		}
		waitText := "immediately"
		if waitMs > 0 {
			waitText = fmt.Sprintf("in %s", (time.Duration(waitMs) * time.Millisecond).Round(100*time.Millisecond))
		}
		line = fmt.Sprintf("Provider throttled: %s %s retry #%d %s.", providerName, label, attempt, waitText)
	case "config:reload:auto":
		path := payloadString(payload, "path", "")
		line = "Config auto-reloaded."
		if path != "" {
			line = fmt.Sprintf("Config auto-reloaded from %s.", truncateSingleLine(path, 96))
		}
	case "config:reload:auto_failed":
		errText := payloadString(payload, "error", "")
		line = "Config auto-reload failed."
		if errText != "" {
			line = fmt.Sprintf("Config auto-reload failed: %s", truncateSingleLine(errText, 120))
		}
	case "context:lifecycle:compacted":
		before := payloadInt(payload, "before_tokens", 0)
		after := payloadInt(payload, "after_tokens", 0)
		collapsed := payloadInt(payload, "rounds_collapsed", 0)
		removed := payloadInt(payload, "messages_removed", 0)
		preview := fmt.Sprintf("%d→%d tok · %d rounds", before, after, collapsed)
		m.pushToolChip(toolChip{
			Name:    "auto-compact",
			Status:  "compact",
			Preview: preview,
		})
		if collapsed > 0 {
			line = fmt.Sprintf("Context auto-compacted: %d→%d tokens (%d rounds, %d msgs removed).", before, after, collapsed, removed)
		} else {
			line = fmt.Sprintf("Context auto-compacted: %d→%d tokens.", before, after)
		}
	case "provider:race:complete":
		winner := payloadString(payload, "winner", "?")
		tokens := payloadInt(payload, "tokens", 0)
		duration := payloadInt(payload, "duration_ms", 0)
		candidates, _ := payload["candidates"].([]any)
		var names []string
		for _, c := range candidates {
			if s, ok := c.(string); ok && strings.TrimSpace(s) != "" {
				names = append(names, s)
			}
		}
		m.pushToolChip(toolChip{
			Name:       "race",
			Status:     "race-ok",
			Preview:    fmt.Sprintf("won by %s", winner),
			DurationMs: duration,
		})
		if len(names) > 0 {
			line = fmt.Sprintf("Provider race: %s won [%s] (%dtok, %dms).", winner, strings.Join(names, ","), tokens, duration)
		} else {
			line = fmt.Sprintf("Provider race: %s won (%dtok, %dms).", winner, tokens, duration)
		}
	case "provider:race:failed":
		errText := payloadString(payload, "error", "all candidates errored")
		duration := payloadInt(payload, "duration_ms", 0)
		m.pushToolChip(toolChip{
			Name:       "race",
			Status:     "race-failed",
			Preview:    truncateSingleLine(errText, 72),
			DurationMs: duration,
		})
		line = fmt.Sprintf("Provider race failed (%dms): %s", duration, truncateSingleLine(errText, 140))
	case "context:lifecycle:handoff":
		historyTokens := payloadInt(payload, "history_tokens", 0)
		briefTokens := payloadInt(payload, "brief_tokens", 0)
		sealed := payloadInt(payload, "messages_sealed", 0)
		newConv := payloadString(payload, "new_conversation", "")
		preview := fmt.Sprintf("%d→%d tok · %d msgs sealed", historyTokens, briefTokens, sealed)
		m.pushToolChip(toolChip{
			Name:    "auto-handoff",
			Status:  "handoff",
			Preview: preview,
		})
		if newConv != "" {
			line = fmt.Sprintf("Auto-new-session: rotated to %s (%d→%d tokens, %d msgs sealed).", newConv, historyTokens, briefTokens, sealed)
		} else {
			line = fmt.Sprintf("Auto-new-session: fresh conversation seeded (%d→%d tokens).", historyTokens, briefTokens)
		}
	case "drive:run:start", "drive:plan:done", "drive:plan:failed",
		"drive:todo:start", "drive:todo:done", "drive:todo:blocked",
		"drive:todo:skipped", "drive:todo:retry",
		"drive:run:warning", "drive:run:done", "drive:run:stopped", "drive:run:failed":
		m, line = m.handleDriveEvent(eventType, payload)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return m
	}
	m.appendActivity(line)
	m.notice = line
	mirror := shouldMirrorEventToTranscript(eventType)
	// Tool failures are rare but critical — never silently drop them from
	// the transcript. A failed chip alone in a long turn is easy to miss,
	// so mirror the error event with its preview/error text.
	if !mirror && eventType == "tool:result" && !payloadBool(payload, "success", true) {
		mirror = true
	}
	if m.chat.sending && mirror {
		m = m.appendToolEventMessage(line)
	}
	return m
}


// shouldMirrorEventToTranscript decides which engine events earn a system
// message in the chat transcript. Per-step tool:call / tool:result chatter is
// deliberately excluded — the tool-chip row, footer notice slot, and activity
// log already carry that; duplicating into the transcript floods scrollback.
// Only events that reflect a real state change the user needs in history
// pass this filter.
func shouldMirrorEventToTranscript(eventType string) bool {
	switch strings.TrimSpace(strings.ToLower(eventType)) {
	case "agent:loop:error", "agent:loop:max_steps", "agent:loop:parked",
		"agent:loop:budget_exhausted", "provider:throttle:retry",
		"context:lifecycle:compacted", "context:lifecycle:handoff",
		"coach:note":
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
}
