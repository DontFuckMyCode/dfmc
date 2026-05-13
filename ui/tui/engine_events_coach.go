package tui

// engine_events_coach.go — handlers for the agent-steering signal cluster:
// coach notes, intent decisions, trajectory hints, autonomy preflight,
// and assistant tail-block events (next-actions, auto-continue,
// cache hit). All of these surface as chat-event lines or coach
// messages and several short-circuit the activity / notice / mirror
// post-processing because they manage their own UI side-effects.

import (
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func registerCoachEventHandlers(r *engineEventRegistry) {
	r.register("coach:note", handleCoachNote)
	r.register("intent:decision", handleIntentDecision)
	r.register("agent:coach:hint", handleCoachHint)
	r.register("agent:coach:stuck", handleCoachStuck)
	r.register("agent:coach:unverified", handleCoachUnverified)
	r.register("agent:autonomy:plan", handleAutonomyPlan)
	r.register("agent:autonomy:kickoff", handleAutonomyKickoff)
	r.register("agent:tool:cache_hit", handleAgentToolCacheHit)
	r.register("assistant:next_actions", handleAssistantNextActions)
	r.register("assistant:auto_continue", handleAssistantAutoContinue)
	r.register("assistant:auto_continue:clarify", handleAssistantAutoContinueClarify)
}

func handleCoachNote(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	if m.ui.coachMuted {
		return m, ""
	}
	text := payloadString(payload, "text", "")
	if strings.TrimSpace(text) == "" {
		return m, ""
	}
	severity := coachSeverityFromWire(payloadString(payload, "severity", "info"))
	origin := payloadString(payload, "origin", "")
	action := payloadString(payload, "action", "")
	m = m.appendCoachMessage(text, severity, origin, action)
	return m, ""
}

func handleIntentDecision(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
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
	return m, ""
}

func handleCoachHint(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	if !m.ui.hintsVerbose {
		return m, ""
	}
	hints, _ := payload["hints"].([]any)
	for _, h := range hints {
		if s, ok := h.(string); ok && strings.TrimSpace(s) != "" {
			m = m.appendCoachMessage("→ "+s, coachSeverityInfo, "trajectory", "")
		}
	}
	return m, ""
}

func handleCoachStuck(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	tool := payloadString(payload, "tool", "")
	count := payloadInt(payload, "failure_count", 0)
	errClass := payloadString(payload, "error_class", "")
	if tool == "" || count == 0 {
		return m, ""
	}
	preview := fmt.Sprintf("%s ×%d failures", tool, count)
	truncatedErr := errClass
	if errClass != "" {
		if len(truncatedErr) > 28 {
			truncatedErr = truncatedErr[:25] + "..."
		}
		preview = fmt.Sprintf("%s ×%d · %s", tool, count, truncatedErr)
	}
	m.pushToolChip(toolChip{
		Name:    "stuck-loop",
		Status:  "warn",
		Preview: preview,
	})
	m.agentLoop.stuckTool = tool
	m.agentLoop.stuckCount = count
	m.agentLoop.stuckErrClass = truncatedErr
	m.agentLoop.turnCoachInterventions++
	noticeKey := strings.ToLower(strings.TrimSpace(tool)) + "\x00" + strings.ToLower(strings.TrimSpace(errClass))
	if m.agentLoop.stuckNoticeKey == noticeKey && count <= m.agentLoop.stuckNoticeAt+1 {
		m.agentLoop.stuckNoticeAt = max(m.agentLoop.stuckNoticeAt, count)
		m.notice = fmt.Sprintf("Loop still stalled on %s ×%d - repeated coach note kept out of chat history. Open ToolStatus with Ctrl+Alt+T.", tool, count)
		return m, ""
	}
	m.agentLoop.stuckNoticeKey = noticeKey
	m.agentLoop.stuckNoticeAt = count
	notice := fmt.Sprintf(
		"⚠ Loop stalled — %s failed %d times with the same error class. The agent has been told to switch tactic.",
		tool, count,
	)
	if errClass != "" {
		notice = fmt.Sprintf(
			"⚠ Loop stalled — %s failed %d times (%s). The agent has been told to switch tactic.",
			tool, count, errClass,
		)
	}
	m = m.appendCoachMessage(notice, coachSeverityWarn, "stuck-loop", "")
	return m, ""
}

func handleCoachUnverified(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	fileCount := payloadInt(payload, "file_count", 0)
	if fileCount < 3 {
		return m, ""
	}
	if m.agentLoop.unverifiedCoachLastCount > 0 && fileCount < m.agentLoop.unverifiedCoachLastCount+3 {
		m.notice = fmt.Sprintf("%d unverified edits - validation still needed. Repeated coach note kept out of chat history.", fileCount)
		return m, ""
	}
	m.agentLoop.unverifiedCoachLastCount = fileCount
	samplePaths := payloadStringSlice(payload, "sample_paths")
	preview := ""
	if len(samplePaths) > 0 {
		truncated := samplePaths
		if len(truncated) > 3 {
			truncated = truncated[:3]
		}
		preview = " (" + strings.Join(truncated, ", ")
		if len(samplePaths) > 3 {
			preview += fmt.Sprintf(", +%d more", len(samplePaths)-3)
		}
		preview += ")"
	}
	notice := fmt.Sprintf(
		"⚠ %d unverified edits%s — agent has been told to STOP editing and run a validation pass before continuing.",
		fileCount, preview,
	)
	m = m.appendCoachMessage(notice, coachSeverityWarn, "unverified-edits", "")
	m.agentLoop.turnCoachInterventions++
	return m, ""
}

func handleAutonomyPlan(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
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
	line := fmt.Sprintf("Autonomy preflight: %d subtasks (%s, %.2f confidence).", count, mode, confidence)
	if scope != "" && scope != "top_level" {
		line = fmt.Sprintf("Autonomy preflight [%s]: %d subtasks (%s, %.2f confidence).", scope, count, mode, confidence)
	}
	if payloadBool(payload, "todo_seeded", false) {
		line += " Todos seeded."
	}
	return m, line
}

func handleAutonomyKickoff(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	m.autoActivateStatsPanelMode(statsPanelModeTasks, "tasks")
	toolName := payloadString(payload, "tool", "orchestrate")
	count := payloadInt(payload, "subtask_count", 0)
	confidence := 0.0
	if raw, ok := payload["confidence"].(float64); ok {
		confidence = raw
	}
	return m, fmt.Sprintf("Autonomy kickoff: %s launched for %d subtasks (%.2f confidence).", toolName, count, confidence)
}

func handleAgentToolCacheHit(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	toolName := strings.TrimSpace(payloadString(payload, "name", "tool"))
	m.agentLoop.cacheHitsThisTurn++
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    fmt.Sprintf("agent:tool:cache_hit:%s:%d", toolName, m.agentLoop.cacheHitsThisTurn),
		Kind:   "tool",
		Status: "ok",
		Title:  "cache hit",
		Detail: toolName + " · reused a prior result, saved a round-trip",
	})
	return m, fmt.Sprintf("Cache hit: %s (turn ×%d)", toolName, m.agentLoop.cacheHitsThisTurn)
}

func handleAssistantNextActions(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	actions := payloadStringSlice(payload, "actions")
	m.assistantNextActions.actions = actions
	m.assistantNextActions.receivedAt = time.Now()
	if len(actions) > 0 {
		return m, fmt.Sprintf("Next-actions ready: %d suggestion(s)", len(actions))
	}
	return m, ""
}

func handleAssistantAutoContinue(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	iter := payloadInt(payload, "iteration", 0)
	maxIter := payloadInt(payload, "max_iterations", 0)
	nextPrompt := payloadString(payload, "prompt", "")
	preview := nextPrompt
	if maxIter > 0 {
		preview = fmt.Sprintf("%d/%d · %s", iter, maxIter, nextPrompt)
	}
	m.pushToolChip(toolChip{
		Name:    "auto-continue",
		Status:  "running",
		Preview: truncateForLine(preview, 96),
	})
	return m, fmt.Sprintf("↻ Auto-continue %d/%d — %s", iter, maxIter, truncateForLine(nextPrompt, 80))
}

// handleAssistantAutoContinueClarify surfaces the engine's "stuck" pause —
// the model didn't assert [done: true] but also gave no concrete [next:]
// action. Pre-fix this just stopped silently and looked like the engine
// had given up. Now we show a notice + chip so the user knows engine is
// awaiting input on purpose, plus the answer body itself carries an
// inline pointer (added by the auto-continue wrapper) describing what
// to type next.
func handleAssistantAutoContinueClarify(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	reason := payloadString(payload, "reason", "missing_next_action")
	iter := payloadInt(payload, "iteration", 0)
	maxIter := payloadInt(payload, "max_iterations", 0)
	preview := reason
	if maxIter > 0 {
		preview = fmt.Sprintf("%d/%d · %s", iter, maxIter, reason)
	}
	m.pushToolChip(toolChip{
		Name:    "auto-continue: clarify",
		Status:  "warn",
		Preview: truncateForLine(preview, 96),
	})
	return m, "↻ Auto-continue paused — model didn't say [done: true] and no [next:] given. Reply with the next step or /cancel."
}
