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
	case "tool:call", "tool:result", "tool:error", "tool:reasoning", "tool:timeout", "tool:denied":
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
	case "agent:coach:stuck":
		// Loop-stall detector. Always surface — this is exactly the
		// signal a user wants when monitoring a long autonomous run.
		// Push a distinct chip so the runtime card / activity strip
		// shows the stall pattern at a glance, and drop one warn-level
		// transcript line per detection so the user can act on it
		// (refine the question, /continue with focus, or interrupt).
		tool := payloadString(payload, "tool", "")
		count := payloadInt(payload, "failure_count", 0)
		errClass := payloadString(payload, "error_class", "")
		if tool == "" || count == 0 {
			return m
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
		// Mark the stall on agentLoopState so the runtime "now" strip
		// renders a warn badge until the next successful tool clears it.
		m.agentLoop.stuckTool = tool
		m.agentLoop.stuckCount = count
		m.agentLoop.stuckErrClass = truncatedErr
		m.agentLoop.turnCoachInterventions++
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
		return m
	case "agent:coach:unverified":
		// Engine fired the directive "STOP editing, validate" hint.
		// Drop a matching warn-level notice in the transcript so the
		// always-visible "unverified: N" badge has a chat scrollback
		// counterpart explaining what the agent has just been told.
		fileCount := payloadInt(payload, "file_count", 0)
		if fileCount < 3 {
			return m
		}
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
		return m
	case "agent:subagent:start", "agent:subagent:fallback", "agent:subagent:done":
		m, line = m.handleSubagentEvent(eventType, payload)
	case "context:built":
		files := payloadInt(payload, "files", 0)
		tokens := payloadInt(payload, "tokens", 0)
		budget := payloadInt(payload, "budget", 0)
		perFile := payloadInt(payload, "per_file", 0)
		task := payloadString(payload, "task", "general")
		comp := payloadString(payload, "compression", "-")
		maxCtx := m.status.ProviderProfile.MaxContext
		if maxCtx == 0 && m.status.ContextIn != nil {
			maxCtx = m.status.ContextIn.ProviderMaxContext
		}
		prev := m.status.ContextIn
		query := ""
		available := 0
		maxFiles := files
		var contextFiles []engine.ContextInFileStatus
		if prev != nil {
			query = prev.Query
			available = prev.ContextAvailable
			maxFiles = prev.MaxFiles
			contextFiles = append([]engine.ContextInFileStatus(nil), prev.Files...)
		}
		if maxFiles <= 0 {
			maxFiles = files
		}
		m.status.ContextIn = &engine.ContextInStatus{
			Query:              query,
			Task:               task,
			TokenCount:         tokens,
			ProviderMaxContext: maxCtx,
			ContextAvailable:   available,
			MaxTokensTotal:     budget,
			MaxTokensPerFile:   perFile,
			Compression:        comp,
			FileCount:          files,
			MaxFiles:           maxFiles,
			Files:              contextFiles,
			Reasons:            payloadStringSlice(payload, "reasons"),
		}
		m.upsertStreamingChatEvent(chatEventLine{
			Key:    "context:built",
			Kind:   "context",
			Status: "ok",
			Title:  "context",
			Detail: contextBuiltChatDetail(files, tokens, budget, perFile, task, comp),
		})
		line = fmt.Sprintf("Context built: %d files, %d tokens (%s, %s)", files, tokens, task, comp)
	case "provider:complete":
		tokens := payloadIntAny(payload, 0, "total_tokens", "tokens")
		inputTokens := payloadInt(payload, "input_tokens", 0)
		outputTokens := payloadInt(payload, "output_tokens", 0)
		if tokens <= 0 {
			tokens = inputTokens + outputTokens
		}
		if inputTokens > 0 || outputTokens > 0 || tokens > 0 {
			m.telemetry.lastInputTokens = inputTokens
			m.telemetry.lastOutputTokens = outputTokens
			m.telemetry.lastTotalTokens = tokens
			m.telemetry.sessionInputTokens += inputTokens
			m.telemetry.sessionOutputTokens += outputTokens
			m.telemetry.sessionTotalTokens += tokens
		}
		if m.agentLoop.active {
			m.agentLoop.phase = "complete"
			m.agentLoop.active = false
			providerName := payloadString(payload, "provider", m.agentLoop.provider)
			modelName := payloadString(payload, "model", m.agentLoop.model)
			detail := ""
			if tokens > 0 {
				detail = "total " + compactMetric(tokens) + " tok"
			}
			if inputTokens > 0 || outputTokens > 0 {
				detail = fmt.Sprintf("in %s | out %s | total %s tok", compactMetric(inputTokens), compactMetric(outputTokens), compactMetric(tokens))
			}
			if providerName != "" || modelName != "" {
				if detail != "" {
					detail += " | "
				}
				detail += strings.Trim(strings.TrimSpace(providerName+"/"+modelName), "/")
			}
			m.upsertStreamingChatEvent(chatEventLine{
				Key:    "provider:complete",
				Kind:   "provider",
				Status: "ok",
				Title:  "provider complete",
				Detail: detail,
			})
			line = fmt.Sprintf("Provider complete: %s/%s (%dtok)", providerName, modelName, tokens)
		}
	case "provider:stream:start":
		inputTokens := payloadIntAny(payload, 0, "input_tokens", "tokens")
		if inputTokens > 0 {
			m.chat.streamInputTokens = inputTokens
			m.telemetry.lastInputTokens = inputTokens
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
		m.upsertStreamingChatEvent(chatEventLine{
			Key:    fmt.Sprintf("provider:throttle:%s:%d", providerName, attempt),
			Kind:   "provider",
			Status: "warn",
			Title:  "provider throttle",
			Detail: fmt.Sprintf("%s %s retry #%d %s", providerName, label, attempt, waitText),
		})
		line = fmt.Sprintf("Provider throttled: %s %s retry #%d %s.", providerName, label, attempt, waitText)
	case "provider:circuit:open":
		providerName := payloadString(payload, "provider", "?")
		cooldownMs := payloadInt(payload, "cooldown_ms", 0)
		detail := providerName + " falling back"
		if cooldownMs > 0 {
			cooldown := (time.Duration(cooldownMs) * time.Millisecond).Round(time.Second)
			detail = fmt.Sprintf("%s skip for %s | falling back", providerName, cooldown)
			line = fmt.Sprintf("Provider %s circuit open — skipping for %s, falling back.", providerName, cooldown)
		} else {
			line = fmt.Sprintf("Provider %s circuit open — falling back.", providerName)
		}
		m.upsertStreamingChatEvent(chatEventLine{
			Key:    "provider:circuit:" + providerName,
			Kind:   "provider",
			Status: "warn",
			Title:  "provider circuit",
			Detail: detail,
		})
	case "provider:circuit:closed":
		providerName := payloadString(payload, "provider", "?")
		line = fmt.Sprintf("Provider %s circuit closed — recovered.", providerName)
	case "provider:stream:recovered":
		from := payloadString(payload, "from", "?")
		to := payloadString(payload, "to", "?")
		line = fmt.Sprintf("↻ Stream resumed on %s after %s blip.", to, from)
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
	case "context:lifecycle:compacted", "context:lifecycle:proactive_compacted":
		before := payloadInt(payload, "before_tokens", 0)
		after := payloadInt(payload, "after_tokens", 0)
		collapsed := payloadInt(payload, "rounds_collapsed", 0)
		removed := payloadInt(payload, "messages_removed", 0)
		step := payloadInt(payload, "step", 0)
		title := "context compacted"
		if eventType == "context:lifecycle:proactive_compacted" {
			title = "context proactive compact"
		}
		preview := fmt.Sprintf("%d→%d tok · %d rounds", before, after, collapsed)
		m.pushToolChip(toolChip{
			Name:    "auto-compact",
			Status:  "compact",
			Preview: preview,
		})
		m.upsertStreamingChatEvent(chatEventLine{
			Key:    fmt.Sprintf("%s:%d", eventType, step),
			Kind:   "context",
			Status: "ok",
			Title:  title,
			Detail: contextLifecycleChatDetail(payload),
			Step:   step,
			Reason: "tool-loop history was summarized to keep provider context headroom",
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
		m.upsertStreamingChatEvent(chatEventLine{
			Key:    "context:handoff",
			Kind:   "context",
			Status: "ok",
			Title:  "context handoff",
			Detail: fmt.Sprintf("%s -> %s tok | %d msgs sealed", compactMetric(historyTokens), compactMetric(briefTokens), sealed),
		})
		if newConv != "" {
			line = fmt.Sprintf("Auto-new-session: rotated to %s (%d→%d tokens, %d msgs sealed).", newConv, historyTokens, briefTokens, sealed)
		} else {
			line = fmt.Sprintf("Auto-new-session: fresh conversation seeded (%d→%d tokens).", historyTokens, briefTokens)
		}
	case "hook:run":
		// Hooks are best-effort lifecycle shell commands — the engine
		// never blocks on them, but a misconfigured hook (typo,
		// missing binary, non-zero exit) used to fall through to the
		// generic info fallback with just the raw event type. Now we
		// distinguish success vs failure so a user can spot a broken
		// hook in the activity feed without reading log files.
		hookEvent := payloadString(payload, "event", "")
		hookName := payloadString(payload, "name", "")
		exitCode := payloadInt(payload, "exit_code", 0)
		durMs := payloadInt(payload, "duration_ms", 0)
		errText := strings.TrimSpace(payloadString(payload, "err", ""))
		failed := exitCode != 0 || errText != ""
		title := "hook ok"
		status := "ok"
		if failed {
			title = "hook failed"
			status = "error"
		}
		display := strings.TrimSpace(hookName)
		if display == "" {
			display = "hook"
		}
		detail := fmt.Sprintf("%s · %s (%dms, exit=%d)", display, hookEvent, durMs, exitCode)
		if errText != "" {
			detail += " · " + truncateSingleLine(errText, 80)
		}
		m.upsertStreamingChatEvent(chatEventLine{
			Key:    "hook:run:" + display,
			Kind:   "system",
			Status: status,
			Title:  title,
			Detail: detail,
		})
		if failed {
			line = fmt.Sprintf("Hook failed [%s/%s]: exit=%d %s", hookEvent, display, exitCode, truncateSingleLine(errText, 120))
		}
	case "conversation:save:error":
		stage := payloadString(payload, "stage", "save")
		errText := payloadString(payload, "error", "save failed")
		m.upsertStreamingChatEvent(chatEventLine{
			Key:    "conversation:save:error",
			Kind:   "context",
			Status: "error",
			Title:  "conversation save failed",
			Detail: fmt.Sprintf("%s: %s", stage, truncateSingleLine(errText, 120)),
		})
		line = fmt.Sprintf("Conversation save failed [%s]: %s", stage, truncateSingleLine(errText, 160))
	case "history:trimmed":
		keptMsgs := payloadInt(payload, "kept_messages", 0)
		keptTokens := payloadInt(payload, "kept_tokens", 0)
		omitted := payloadInt(payload, "omitted_messages", 0)
		summaryTokens := payloadInt(payload, "summary_tokens", 0)
		preview := strings.TrimSpace(payloadString(payload, "summary_preview", ""))
		detail := fmt.Sprintf("kept %d msgs (%s tok) · summarized %d older into %s tok",
			keptMsgs, compactMetric(keptTokens), omitted, compactMetric(summaryTokens))
		m.upsertStreamingChatEvent(chatEventLine{
			Key:    "history:trimmed",
			Kind:   "context",
			Status: "ok",
			Title:  "history trimmed",
			Detail: detail,
		})
		if preview != "" {
			line = fmt.Sprintf("History trimmed: %s — %s", detail, truncateSingleLine(preview, 120))
		} else {
			line = fmt.Sprintf("History trimmed: %s.", detail)
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
	if m.chat.sending && mirror {
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
		"agent:loop:budget_exhausted", "provider:throttle:retry",
		"context:lifecycle:compacted", "context:lifecycle:handoff",
		"conversation:save:error", "coach:note", "tool:denied":
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
