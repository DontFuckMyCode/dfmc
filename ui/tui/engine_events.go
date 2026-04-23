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
	case "agent:loop:start":
		m.agentLoop.active = true
		m.agentLoop.phase = "starting"
		m.agentLoop.step = 0
		m.agentLoop.maxToolStep = payloadInt(payload, "max_tool_steps", m.agentLoop.maxToolStep)
		m.agentLoop.toolRounds = payloadInt(payload, "tool_rounds", 0)
		m.agentLoop.provider = payloadString(payload, "provider", m.agentLoop.provider)
		m.agentLoop.model = payloadString(payload, "model", m.agentLoop.model)
		// A fresh loop start means any previously parked banner is obsolete.
		m.ui.resumePromptActive = false
		files := payloadInt(payload, "context_files", 0)
		tokens := payloadInt(payload, "context_tokens", 0)
		line = fmt.Sprintf("Agent loop started: max_tools=%d context=%df/%dtok", m.agentLoop.maxToolStep, files, tokens)
	case "agent:loop:thinking":
		m.agentLoop.active = true
		m.agentLoop.phase = "thinking"
		step := payloadInt(payload, "step", 0)
		if step > 0 {
			m.agentLoop.step = step
		}
		maxSteps := payloadInt(payload, "max_tool_steps", 0)
		if maxSteps > 0 {
			m.agentLoop.maxToolStep = maxSteps
		}
		rounds := payloadInt(payload, "tool_rounds", 0)
		if rounds >= 0 {
			m.agentLoop.toolRounds = rounds
		}
		m.agentLoop.provider = payloadString(payload, "provider", m.agentLoop.provider)
		m.agentLoop.model = payloadString(payload, "model", m.agentLoop.model)
		if m.agentLoop.step > 0 && m.agentLoop.maxToolStep > 0 {
			line = fmt.Sprintf("Agent thinking: step %d/%d", m.agentLoop.step, m.agentLoop.maxToolStep)
		} else {
			line = "Agent thinking..."
		}
	case "tool:call":
		m.agentLoop.active = true
		m.agentLoop.phase = "tool-call"
		toolName := payloadString(payload, "tool", "tool")
		step := payloadInt(payload, "step", 0)
		m.agentLoop.lastTool = toolName
		m.agentLoop.lastStatus = "running"
		m.agentLoop.lastDuration = 0
		if step > 0 {
			m.agentLoop.step = step
		}
		if rounds := payloadInt(payload, "tool_rounds", 0); rounds > 0 {
			m.agentLoop.toolRounds = rounds
		}
		m.agentLoop.provider = payloadString(payload, "provider", m.agentLoop.provider)
		m.agentLoop.model = payloadString(payload, "model", m.agentLoop.model)
		paramsPreview := payloadString(payload, "params_preview", "")
		// Verb carries the action line (e.g. "Read foo.go (lines N-M)")
		// separately from Preview so the result-side merge can keep both
		// on the finished chip's two-line shape — Preview becomes the
		// result excerpt, Verb stays the params action.
		toolCallChip := toolChip{
			Name:   toolName,
			Status: "running",
			Step:   step,
			Verb:   paramsPreview,
		}
		m.pushToolChip(toolCallChip)
		m.pushStreamingMessageToolChip(toolCallChip)
		m.telemetry.activeToolCount++
		if step > 0 {
			line = fmt.Sprintf("Agent tool call: %s (step %d)", toolName, step)
		} else {
			line = fmt.Sprintf("Agent tool call: %s", toolName)
		}
		if paramsPreview != "" {
			line += " " + paramsPreview
		}
	case "tool:result":
		m.agentLoop.active = true
		m.agentLoop.phase = "tool-result"
		toolName := payloadString(payload, "tool", "tool")
		duration := payloadInt(payload, "durationMs", 0)
		success := payloadBool(payload, "success", true)
		status := "ok"
		if !success {
			status = "failed"
		}
		m.agentLoop.lastTool = toolName
		m.agentLoop.lastStatus = status
		m.agentLoop.lastDuration = duration
		preview := payloadString(payload, "output_preview", "")
		if preview != "" {
			m.agentLoop.lastOutput = preview
		}
		step := payloadInt(payload, "step", 0)
		if step > 0 {
			m.agentLoop.step = step
			if step > m.agentLoop.toolRounds {
				m.agentLoop.toolRounds = step
			}
		}
		m.agentLoop.provider = payloadString(payload, "provider", m.agentLoop.provider)
		m.agentLoop.model = payloadString(payload, "model", m.agentLoop.model)
		chipPreview := preview
		if chipPreview == "" && !success {
			chipPreview = payloadString(payload, "error", "")
		}
		var batchInner []string
		if batchCount := payloadInt(payload, "batch_count", 0); batchCount > 0 {
			batchParallel := payloadInt(payload, "batch_parallel", 0)
			batchOK := payloadInt(payload, "batch_ok", 0)
			batchFail := payloadInt(payload, "batch_fail", 0)
			parts := []string{fmt.Sprintf("%d calls", batchCount)}
			if batchParallel > 0 {
				parts = append(parts, fmt.Sprintf("%d parallel", batchParallel))
			}
			parts = append(parts, fmt.Sprintf("%d ok", batchOK))
			if batchFail > 0 {
				parts = append(parts, fmt.Sprintf("%d fail", batchFail))
			}
			chipPreview = strings.Join(parts, " · ")
			// Per-call breakdown emitted by batchFanoutSummary so the
			// user sees WHAT each batched call did, not just the count.
			batchInner = payloadStringSlice(payload, "batch_inner")
		}
		savedChars := payloadInt(payload, "compression_saved_chars", 0)
		rawChars := payloadInt(payload, "output_chars", 0)
		payloadChars := payloadInt(payload, "payload_chars", 0)
		compressionPct := 0
		if ratio, ok := payload["compression_ratio"].(float64); ok && ratio >= 0 && ratio <= 1 {
			compressionPct = int((1 - ratio) * 100)
		} else if rawChars > 0 && savedChars > 0 {
			compressionPct = int((int64(savedChars) * 100) / int64(rawChars))
		}
		if savedChars > 0 && rawChars > 0 {
			m.telemetry.compressionSavedChars += savedChars
			m.telemetry.compressionRawChars += rawChars
		}
		finishedChip := toolChip{
			Name:            toolName,
			Status:          status,
			Step:            step,
			DurationMs:      duration,
			Preview:         chipPreview,
			OutputTokens:    payloadInt(payload, "output_tokens", 0),
			Truncated:       payloadBool(payload, "truncated", false),
			CompressedChars: payloadChars,
			SavedChars:      savedChars,
			CompressionPct:  compressionPct,
			InnerLines:      batchInner,
		}
		m.finishToolChip(finishedChip)
		m.finishStreamingMessageToolChip(finishedChip)
		if m.telemetry.activeToolCount > 0 {
			m.telemetry.activeToolCount--
		}
		if duration > 0 {
			line = fmt.Sprintf("Agent tool result: %s (%s, %dms)", toolName, status, duration)
		} else {
			line = fmt.Sprintf("Agent tool result: %s (%s)", toolName, status)
		}
		if success && strings.EqualFold(strings.TrimSpace(toolName), "todo_write") {
			m.autoActivateStatsPanelMode(statsPanelModeTodos, "todos")
		}
		if preview != "" {
			line += " -> " + preview
		} else if !success {
			if errText := payloadString(payload, "error", ""); errText != "" {
				line += " -> " + truncateSingleLine(errText, 96)
			}
		}
	case "agent:loop:final":
		m.agentLoop.active = false
		m.agentLoop.phase = "finalizing"
		if rounds := payloadInt(payload, "tool_rounds", 0); rounds >= 0 {
			m.agentLoop.toolRounds = rounds
		}
		if step := payloadInt(payload, "step", 0); step > 0 {
			m.agentLoop.step = step
		}
		line = fmt.Sprintf("Agent loop finalizing answer after %d tool call(s).", m.agentLoop.toolRounds)
	case "agent:loop:max_steps":
		m.agentLoop.active = false
		m.agentLoop.phase = "max-steps"
		maxSteps := payloadInt(payload, "max_tool_steps", m.agentLoop.maxToolStep)
		if maxSteps > 0 {
			m.agentLoop.maxToolStep = maxSteps
		}
		line = fmt.Sprintf("Agent loop reached max tool steps (%d).", m.agentLoop.maxToolStep)
	case "agent:loop:error":
		m.agentLoop.active = false
		m.agentLoop.phase = "error"
		errText := payloadString(payload, "error", "unknown error")
		line = "Agent loop error: " + errText
	case "agent:loop:parked":
		// autonomous_pending=true means the autonomous-resume wrapper will
		// immediately re-enter the loop after this park. In that case we
		// MUST NOT flip into the parked UI (no phase change, no resume
		// prompt) — otherwise the "press Enter to resume" affordance and
		// the spinner-stop flash through, the user types /continue, and by
		// the time it lands the wrapper has already cleared the park
		// state. The 2026-04-18 screenshot ("No parked agent loop"
		// immediately after a budget exhaust) was exactly this race.
		if payloadBool(payload, "autonomous_pending", false) {
			return m
		}
		m.agentLoop.phase = "parked"
		m.agentLoop.active = false
		step := payloadInt(payload, "step", m.agentLoop.step)
		maxSteps := payloadInt(payload, "max_tool_steps", m.agentLoop.maxToolStep)
		m.agentLoop.step = step
		if maxSteps > 0 {
			m.agentLoop.maxToolStep = maxSteps
		}
		m.ui.resumePromptActive = true
		// budget_exhausted already surfaces its own "exhausted %d/%d"
		// transcript line with token counts; suppress the generic parked
		// line in that case so the scrollback reads once, not twice.
		if payloadString(payload, "reason", "") == "budget_exhausted" {
			return m
		}
		line = fmt.Sprintf("Agent loop parked at step %d/%d - press Enter to resume, Esc to dismiss.", step, maxSteps)
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
	case "tool:error":
		switch payload := event.Payload.(type) {
		case string:
			line = "Tool error: " + strings.TrimSpace(payload)
		default:
			line = "Tool error occurred."
		}
	case "tool:reasoning":
		// Self-narration: backfill the most recent running chip for
		// this tool with the model's `_reason` text. Fires AFTER
		// tool:call (which created the chip) and BEFORE tool:result
		// (which finalises it), so the running chip catches it; if
		// timing is racy and the chip already finished, we still write
		// to it so the finished card shows the why.
		toolName := payloadString(payload, "tool", "")
		reason := payloadString(payload, "reason", "")
		if toolName != "" && reason != "" {
			m.attachReasonToLastChip(toolName, reason)
			line = fmt.Sprintf("%s · %s", toolName, truncateForLine(reason, 90))
		}
	case "agent:subagent:start":
		m.autoActivateStatsPanelMode(statsPanelModeSubagents, "subagents")
		task := payloadString(payload, "task", "task")
		role := payloadString(payload, "role", "")
		candidates := payloadStringSlice(payload, "provider_candidates")
		targetLabel := subagentProviderLabel(
			payloadString(payload, "provider", ""),
			payloadString(payload, "model", ""),
		)
		chainLabel := subagentProfileChain(candidates)
		m.telemetry.activeSubagentCount++
		chipName := "subagent"
		if role != "" {
			chipName = "subagent/" + role
		}
		preview := truncateSingleLine(task, 72)
		chip := toolChip{
			Name:    chipName,
			Status:  "subagent-running",
			Preview: preview,
		}
		if chainLabel != "" {
			chip.Verb = chainLabel
		} else if targetLabel != "" {
			chip.Verb = targetLabel
		}
		m.pushToolChip(chip)
		m.pushStreamingMessageToolChip(chip)
		if role != "" {
			line = fmt.Sprintf("Subagent (%s) started: %s", role, preview)
		} else {
			line = "Subagent started: " + preview
		}
		if chainLabel != "" {
			line += " [" + chainLabel + "]"
		} else if targetLabel != "" {
			line += " [" + targetLabel + "]"
		}
	case "agent:subagent:fallback":
		role := payloadString(payload, "role", "")
		attempt := payloadInt(payload, "attempt", 0)
		fromProfile := payloadString(payload, "from_profile", "")
		toProfile := payloadString(payload, "to_profile", "")
		errText := payloadString(payload, "error", "")
		reasons := payloadStringSlice(payload, "fallback_reasons")
		if errText == "" && len(reasons) > 0 {
			errText = reasons[len(reasons)-1]
		}
		chipName := "subagent"
		if role != "" {
			chipName = "subagent/" + role
		}
		preview := subagentProfileTransition(fromProfile, toProfile)
		if preview == "" {
			preview = "provider fallback"
		}
		chip := toolChip{
			Name:    chipName,
			Status:  "subagent-fallback",
			Preview: preview,
		}
		if errText != "" {
			chip.Verb = truncateSingleLine(errText, 72)
		}
		m.pushToolChip(chip)
		if attempt > 0 {
			line = fmt.Sprintf("Subagent fallback #%d: %s", attempt, preview)
		} else {
			line = "Subagent fallback: " + preview
		}
		if errText != "" {
			line += " - " + truncateSingleLine(errText, 120)
		}
	case "agent:subagent:done":
		if m.telemetry.activeSubagentCount > 0 {
			m.telemetry.activeSubagentCount--
		}
		duration := payloadInt(payload, "duration_ms", 0)
		rounds := payloadInt(payload, "tool_rounds", 0)
		parked := payloadBool(payload, "parked", false)
		errText := payloadString(payload, "err", "")
		role := payloadString(payload, "role", "")
		attempts := payloadInt(payload, "attempts", 0)
		fallbackUsed := payloadBool(payload, "fallback_used", false)
		providerLabel := subagentProviderLabel(
			payloadString(payload, "provider", ""),
			payloadString(payload, "model", ""),
		)
		status := "subagent-ok"
		chipPreview := fmt.Sprintf("%d rounds", rounds)
		if providerLabel != "" {
			chipPreview += " · " + providerLabel
		}
		if attempts > 1 {
			chipPreview += fmt.Sprintf(" · %d attempts", attempts)
		}
		if fallbackUsed {
			chipPreview += " · fallback"
		}
		if parked {
			chipPreview += " · parked"
		}
		if errText != "" {
			status = "subagent-failed"
			chipPreview = truncateSingleLine(errText, 72)
		}
		chipName := "subagent"
		if role != "" {
			chipName = "subagent/" + role
		}
		finished := toolChip{
			Name:       chipName,
			Status:     status,
			DurationMs: duration,
			Preview:    chipPreview,
		}
		m.finishToolChip(finished)
		m.finishStreamingMessageToolChip(finished)
		switch {
		case errText != "":
			line = fmt.Sprintf("Subagent failed (%dms): %s", duration, truncateSingleLine(errText, 120))
		case parked:
			line = fmt.Sprintf("Subagent parked after %d rounds (%dms).", rounds, duration)
		default:
			line = fmt.Sprintf("Subagent done: %d rounds (%dms).", rounds, duration)
		}
		if errText == "" && providerLabel != "" {
			line += " via " + providerLabel
		}
		if errText == "" && fallbackUsed && attempts > 1 {
			line += fmt.Sprintf(" after %d attempts", attempts)
		}
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
	case "agent:loop:budget_exhausted":
		m.agentLoop.phase = "budget-exhausted"
		used := payloadInt(payload, "tokens_used", 0)
		budget := payloadInt(payload, "max_tool_tokens", 0)
		m.pushToolChip(toolChip{
			Name:    "token-budget",
			Status:  "budget",
			Preview: fmt.Sprintf("%d/%d tok", used, budget),
		})
		line = fmt.Sprintf("Agent loop exhausted token budget (%d/%d).", used, budget)
	case "agent:loop:auto_resume":
		// Autonomous park→compact→resume inside the same Ask call. We
		// render a compact in-flow chip instead of the disruptive
		// "park / SYS resume / park" sequence — the user wanted
		// hands-off continuation, so we make the continuation feel
		// like one fluent thought rather than three interrupting ones.
		// Belt-and-braces: clear any resume affordance the parked event
		// might have flipped on (it now suppresses itself when
		// autonomous_pending is set, but older engines or out-of-order
		// events shouldn't leave a stale prompt sitting on screen).
		m.ui.resumePromptActive = false
		m.agentLoop.active = true
		m.agentLoop.phase = "auto-resuming"
		cumSteps := payloadInt(payload, "cumulative_steps", 0)
		stepCeiling := payloadInt(payload, "step_ceiling", 0)
		preview := "compacted, continuing"
		if stepCeiling > 0 {
			preview = fmt.Sprintf("compacted, continuing · %d/%d steps", cumSteps, stepCeiling)
		}
		m.pushToolChip(toolChip{
			Name:    "auto-resume",
			Status:  "running",
			Preview: preview,
		})
		// No transcript line — the chip is enough signal. A line in
		// the chat would re-create the noisy "SYS resume" pattern the
		// autonomous loop is supposed to eliminate.
	case "agent:loop:auto_resume_refused":
		// Cumulative ceiling hit during autonomy — surface this so the
		// user knows the auto-progression bottomed out and a manual
		// /continue (or scope refinement) is needed.
		reason := payloadString(payload, "reason", "ceiling")
		m.pushToolChip(toolChip{
			Name:    "auto-resume",
			Status:  "failed",
			Preview: "ceiling: " + reason,
		})
		line = "Autonomous resume stopped — cumulative work ceiling reached. Type /continue to override or refine the question."
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
	case "agent:loop:auto_recover":
		before := payloadInt(payload, "before_tokens", 0)
		after := payloadInt(payload, "after_tokens", 0)
		collapsed := payloadInt(payload, "rounds_collapsed", 0)
		m.pushToolChip(toolChip{
			Name:    "auto-recover",
			Status:  "recover",
			Preview: fmt.Sprintf("%d→%d tok · %d rounds", before, after, collapsed),
		})
		if collapsed > 0 {
			line = fmt.Sprintf("Auto-recover: budget trip, compacted %d→%d tokens (%d rounds). Retrying.", before, after, collapsed)
		} else {
			line = "Auto-recover: budget trip, transcript slimmed. Retrying."
		}
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
	case "drive:run:start":
		task := payloadString(payload, "task", "")
		runID := payloadString(payload, "run_id", "")
		m.telemetry.driveRunID = shortID(runID)
		m.telemetry.driveTodoID = ""
		m.telemetry.driveDone = 0
		m.telemetry.driveTotal = 0
		m.telemetry.driveBlocked = 0
		// Refresh workflow panel run list
		if res, err := buildTUIDriver(m.eng, nil); err == nil {
			if runs, err := res.listRuns(); err == nil {
				m.workflow.runs = runs
			}
		}
		if resumed := payloadBool(payload, "resumed", false); resumed {
			line = fmt.Sprintf("Drive: resumed %s (task: %s)", shortID(runID), truncateForLine(task, 80))
		} else {
			line = fmt.Sprintf("Drive: started %s — %s", shortID(runID), truncateForLine(task, 80))
		}
	case "drive:plan:done":
		count := payloadInt(payload, "todo_count", 0)
		m.telemetry.driveTotal = count
		line = fmt.Sprintf("Drive: plan ready — %d TODOs queued", count)
	case "drive:plan:failed":
		errStr := payloadString(payload, "error", "")
		warning := payloadString(payload, "warning", "")
		if warning != "" {
			line = fmt.Sprintf("Drive: plan warning — %s", warning)
		} else {
			line = fmt.Sprintf("Drive: plan failed — %s", truncateForLine(errStr, 200))
		}
	case "drive:todo:start":
		id := payloadString(payload, "todo_id", "")
		title := payloadString(payload, "title", "")
		attempt := payloadInt(payload, "attempt", 1)
		workerClass := payloadString(payload, "worker_class", "")
		providerTag := payloadString(payload, "provider_tag", "")
		profileSelected := payloadString(payload, "profile_selected", "")
		m.telemetry.driveTodoID = id
		if attempt > 1 {
			line = fmt.Sprintf("Drive: ▶ %s (attempt %d) — %s", id, attempt, truncateForLine(title, 80))
		} else {
			line = fmt.Sprintf("Drive: ▶ %s — %s", id, truncateForLine(title, 80))
		}
		if workerClass != "" {
			line += " [" + workerClass + "]"
		}
		if providerTag != "" {
			if profileSelected != "" {
				line += fmt.Sprintf(" [route:%s→%s]", providerTag, profileSelected)
			} else {
				line += " [route:" + providerTag + "]"
			}
		}
	case "drive:todo:done":
		id := payloadString(payload, "todo_id", "")
		dur := payloadInt(payload, "duration_ms", 0)
		tools := payloadInt(payload, "tool_calls", 0)
		providerLabel := subagentProviderLabel(
			payloadString(payload, "provider", ""),
			payloadString(payload, "model", ""),
		)
		attempts := payloadInt(payload, "attempts", 0)
		fallbackUsed := payloadBool(payload, "fallback", false)
		fallbackReasons := payloadStringSlice(payload, "fallback_reasons")
		m.telemetry.driveDone++
		if m.telemetry.driveTodoID == id {
			m.telemetry.driveTodoID = ""
		}
		line = fmt.Sprintf("Drive: ✓ %s done (%dms, %d tool calls)", id, dur, tools)
		if workerClass := payloadString(payload, "worker_class", ""); workerClass != "" {
			line += " [" + workerClass + "]"
		}
		if providerLabel != "" {
			line += " via " + providerLabel
		}
		if fallbackUsed && attempts > 1 {
			line += fmt.Sprintf(" after %d attempts", attempts)
			if len(fallbackReasons) > 0 {
				line += " - " + truncateForLine(fallbackReasons[len(fallbackReasons)-1], 120)
			}
		}
		if spawned := payloadInt(payload, "spawned", 0); spawned > 0 {
			line += fmt.Sprintf(" (+%d spawned)", spawned)
		}
	case "drive:todo:blocked":
		id := payloadString(payload, "todo_id", "")
		errStr := payloadString(payload, "error", "")
		class := payloadString(payload, "class", "")
		blockedReason := payloadString(payload, "blocked_reason", "")
		m.telemetry.driveBlocked++
		if m.telemetry.driveTodoID == id {
			m.telemetry.driveTodoID = ""
		}
		extra := ""
		if blockedReason != "" && blockedReason != "none" {
			extra = " [" + blockedReason + "]"
		} else if class != "" && class != "unknown" {
			extra = " [class:" + class + "]"
		}
		line = fmt.Sprintf("Drive: ✗ %s blocked — %s%s", id, truncateForLine(errStr, 160), extra)
	case "drive:todo:skipped":
		id := payloadString(payload, "todo_id", "")
		reason := payloadString(payload, "reason", "")
		line = fmt.Sprintf("Drive: ↷ %s skipped — %s", id, reason)
	case "drive:todo:retry":
		id := payloadString(payload, "todo_id", "")
		attempt := payloadInt(payload, "attempt", 0)
		class := payloadString(payload, "class", "")
		var extra string
		if class != "" && class != "unknown" {
			extra = " [" + class + "]"
		}
		line = fmt.Sprintf("Drive: ↻ %s retry (attempt %d)%s", id, attempt, extra)
	case "drive:run:warning":
		errStr := payloadString(payload, "error", "")
		line = fmt.Sprintf("Drive: warning — %s", truncateForLine(errStr, 200))
	case "drive:run:done", "drive:run:stopped", "drive:run:failed":
		status := payloadString(payload, "status", "")
		done := payloadInt(payload, "done", 0)
		blocked := payloadInt(payload, "blocked", 0)
		skipped := payloadInt(payload, "skipped", 0)
		dur := payloadInt(payload, "duration_ms", 0)
		reason := payloadString(payload, "reason", "")
		// Clear the header chip — the run is over.
		m.telemetry.driveRunID = ""
		m.telemetry.driveTodoID = ""
		base := fmt.Sprintf("Drive: %s — %d done, %d blocked, %d skipped (%dms)", status, done, blocked, skipped, dur)
		if reason != "" {
			line = base + " · " + reason
		} else {
			line = base
		}
		// Refresh workflow panel run list when a run ends
		if res, err := buildTUIDriver(m.eng, nil); err == nil {
			if runs, err := res.listRuns(); err == nil {
				m.workflow.runs = runs
			}
		}
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
