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
	"strconv"
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
	line := ""
	payload, _ := toStringAnyMap(event.Payload)
	switch eventType {
	case "agent:loop:start":
		m.agentLoopActive = true
		m.agentLoopPhase = "starting"
		m.agentLoopStep = 0
		m.agentLoopMaxToolStep = payloadInt(payload, "max_tool_steps", m.agentLoopMaxToolStep)
		m.agentLoopToolRounds = payloadInt(payload, "tool_rounds", 0)
		m.agentLoopProvider = payloadString(payload, "provider", m.agentLoopProvider)
		m.agentLoopModel = payloadString(payload, "model", m.agentLoopModel)
		// A fresh loop start means any previously parked banner is obsolete.
		m.resumePromptActive = false
		files := payloadInt(payload, "context_files", 0)
		tokens := payloadInt(payload, "context_tokens", 0)
		line = fmt.Sprintf("Agent loop started: max_tools=%d context=%df/%dtok", m.agentLoopMaxToolStep, files, tokens)
	case "agent:loop:thinking":
		m.agentLoopActive = true
		m.agentLoopPhase = "thinking"
		step := payloadInt(payload, "step", 0)
		if step > 0 {
			m.agentLoopStep = step
		}
		maxSteps := payloadInt(payload, "max_tool_steps", 0)
		if maxSteps > 0 {
			m.agentLoopMaxToolStep = maxSteps
		}
		rounds := payloadInt(payload, "tool_rounds", 0)
		if rounds >= 0 {
			m.agentLoopToolRounds = rounds
		}
		m.agentLoopProvider = payloadString(payload, "provider", m.agentLoopProvider)
		m.agentLoopModel = payloadString(payload, "model", m.agentLoopModel)
		if m.agentLoopStep > 0 && m.agentLoopMaxToolStep > 0 {
			line = fmt.Sprintf("Agent thinking: step %d/%d", m.agentLoopStep, m.agentLoopMaxToolStep)
		} else {
			line = "Agent thinking..."
		}
	case "tool:call":
		m.agentLoopActive = true
		m.agentLoopPhase = "tool-call"
		toolName := payloadString(payload, "tool", "tool")
		step := payloadInt(payload, "step", 0)
		m.agentLoopLastTool = toolName
		m.agentLoopLastStatus = "running"
		m.agentLoopLastDuration = 0
		if step > 0 {
			m.agentLoopStep = step
		}
		if rounds := payloadInt(payload, "tool_rounds", 0); rounds > 0 {
			m.agentLoopToolRounds = rounds
		}
		m.agentLoopProvider = payloadString(payload, "provider", m.agentLoopProvider)
		m.agentLoopModel = payloadString(payload, "model", m.agentLoopModel)
		paramsPreview := payloadString(payload, "params_preview", "")
		toolCallChip := toolChip{
			Name:    toolName,
			Status:  "running",
			Step:    step,
			Preview: paramsPreview,
		}
		m.pushToolChip(toolCallChip)
		m.pushStreamingMessageToolChip(toolCallChip)
		m.activeToolCount++
		if step > 0 {
			line = fmt.Sprintf("Agent tool call: %s (step %d)", toolName, step)
		} else {
			line = fmt.Sprintf("Agent tool call: %s", toolName)
		}
		if paramsPreview != "" {
			line += " " + paramsPreview
		}
	case "tool:result":
		m.agentLoopActive = true
		m.agentLoopPhase = "tool-result"
		toolName := payloadString(payload, "tool", "tool")
		duration := payloadInt(payload, "durationMs", 0)
		success := payloadBool(payload, "success", true)
		status := "ok"
		if !success {
			status = "failed"
		}
		m.agentLoopLastTool = toolName
		m.agentLoopLastStatus = status
		m.agentLoopLastDuration = duration
		preview := payloadString(payload, "output_preview", "")
		if preview != "" {
			m.agentLoopLastOutput = preview
		}
		step := payloadInt(payload, "step", 0)
		if step > 0 {
			m.agentLoopStep = step
			if step > m.agentLoopToolRounds {
				m.agentLoopToolRounds = step
			}
		}
		m.agentLoopProvider = payloadString(payload, "provider", m.agentLoopProvider)
		m.agentLoopModel = payloadString(payload, "model", m.agentLoopModel)
		chipPreview := preview
		if chipPreview == "" && !success {
			chipPreview = payloadString(payload, "error", "")
		}
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
			m.compressionSavedChars += savedChars
			m.compressionRawChars += rawChars
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
		}
		m.finishToolChip(finishedChip)
		m.finishStreamingMessageToolChip(finishedChip)
		if m.activeToolCount > 0 {
			m.activeToolCount--
		}
		if duration > 0 {
			line = fmt.Sprintf("Agent tool result: %s (%s, %dms)", toolName, status, duration)
		} else {
			line = fmt.Sprintf("Agent tool result: %s (%s)", toolName, status)
		}
		if preview != "" {
			line += " -> " + preview
		} else if !success {
			if errText := payloadString(payload, "error", ""); errText != "" {
				line += " -> " + truncateSingleLine(errText, 96)
			}
		}
	case "agent:loop:final":
		m.agentLoopPhase = "finalizing"
		if rounds := payloadInt(payload, "tool_rounds", 0); rounds >= 0 {
			m.agentLoopToolRounds = rounds
		}
		if step := payloadInt(payload, "step", 0); step > 0 {
			m.agentLoopStep = step
		}
		line = fmt.Sprintf("Agent loop finalizing answer after %d tool call(s).", m.agentLoopToolRounds)
	case "agent:loop:max_steps":
		m.agentLoopPhase = "max-steps"
		maxSteps := payloadInt(payload, "max_tool_steps", m.agentLoopMaxToolStep)
		if maxSteps > 0 {
			m.agentLoopMaxToolStep = maxSteps
		}
		line = fmt.Sprintf("Agent loop reached max tool steps (%d).", m.agentLoopMaxToolStep)
	case "agent:loop:error":
		m.agentLoopPhase = "error"
		errText := payloadString(payload, "error", "unknown error")
		line = "Agent loop error: " + errText
	case "agent:loop:parked":
		m.agentLoopPhase = "parked"
		m.agentLoopActive = false
		step := payloadInt(payload, "step", m.agentLoopStep)
		maxSteps := payloadInt(payload, "max_tool_steps", m.agentLoopMaxToolStep)
		m.agentLoopStep = step
		if maxSteps > 0 {
			m.agentLoopMaxToolStep = maxSteps
		}
		m.resumePromptActive = true
		// budget_exhausted already surfaces its own "exhausted %d/%d"
		// transcript line with token counts; suppress the generic parked
		// line in that case so the scrollback reads once, not twice.
		if payloadString(payload, "reason", "") == "budget_exhausted" {
			return m
		}
		line = fmt.Sprintf("Agent loop parked at step %d/%d — press Enter to resume, Esc to dismiss.", step, maxSteps)
	case "coach:note":
		if m.coachMuted {
			return m
		}
		text := payloadString(payload, "text", "")
		if strings.TrimSpace(text) == "" {
			return m
		}
		severity := payloadString(payload, "severity", "info")
		origin := payloadString(payload, "origin", "")
		m = m.appendCoachMessage(text, severity, origin)
		return m
	case "agent:coach:hint":
		if !m.hintsVerbose {
			return m
		}
		hints, _ := payload["hints"].([]any)
		for _, h := range hints {
			if s, ok := h.(string); ok && strings.TrimSpace(s) != "" {
				m = m.appendCoachMessage("→ "+s, "info", "trajectory")
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
	case "agent:subagent:start":
		task := payloadString(payload, "task", "task")
		role := payloadString(payload, "role", "")
		m.activeSubagentCount++
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
		m.pushToolChip(chip)
		m.pushStreamingMessageToolChip(chip)
		if role != "" {
			line = fmt.Sprintf("Subagent (%s) started: %s", role, preview)
		} else {
			line = "Subagent started: " + preview
		}
	case "agent:subagent:done":
		if m.activeSubagentCount > 0 {
			m.activeSubagentCount--
		}
		duration := payloadInt(payload, "duration_ms", 0)
		rounds := payloadInt(payload, "tool_rounds", 0)
		parked := payloadBool(payload, "parked", false)
		errText := payloadString(payload, "err", "")
		role := payloadString(payload, "role", "")
		status := "subagent-ok"
		chipPreview := fmt.Sprintf("%d rounds", rounds)
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
	case "context:built":
		files := payloadInt(payload, "files", 0)
		tokens := payloadInt(payload, "tokens", 0)
		task := payloadString(payload, "task", "general")
		comp := payloadString(payload, "compression", "-")
		line = fmt.Sprintf("Context built: %d files, %d tokens (%s, %s)", files, tokens, task, comp)
	case "provider:complete":
		if m.agentLoopActive {
			m.agentLoopPhase = "complete"
			m.agentLoopActive = false
			tokens := payloadInt(payload, "tokens", 0)
			providerName := payloadString(payload, "provider", m.agentLoopProvider)
			modelName := payloadString(payload, "model", m.agentLoopModel)
			line = fmt.Sprintf("Provider complete: %s/%s (%dtok)", providerName, modelName, tokens)
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
		m.agentLoopPhase = "budget-exhausted"
		used := payloadInt(payload, "tokens_used", 0)
		budget := payloadInt(payload, "max_tool_tokens", 0)
		m.pushToolChip(toolChip{
			Name:    "token-budget",
			Status:  "budget",
			Preview: fmt.Sprintf("%d/%d tok", used, budget),
		})
		line = fmt.Sprintf("Agent loop exhausted token budget (%d/%d).", used, budget)
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
	if m.sending && mirror {
		m = m.appendToolEventMessage(line)
	}
	return m
}

func payloadString(data map[string]any, key, fallback string) string {
	if data == nil {
		return fallback
	}
	raw, ok := data[key]
	if !ok {
		return fallback
	}
	switch value := raw.(type) {
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return fallback
		}
		return value
	default:
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			return fallback
		}
		return text
	}
}

func payloadInt(data map[string]any, key string, fallback int) int {
	if data == nil {
		return fallback
	}
	raw, ok := data[key]
	if !ok {
		return fallback
	}
	switch value := raw.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return n
		}
	}
	return fallback
}

func payloadBool(data map[string]any, key string, fallback bool) bool {
	if data == nil {
		return fallback
	}
	raw, ok := data[key]
	if !ok {
		return fallback
	}
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
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
		"agent:loop:budget_exhausted",
		"context:lifecycle:compacted", "context:lifecycle:handoff",
		"coach:note":
		return true
	default:
		return false
	}
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

const maxToolTimelineChips = 18

// pushToolChip appends a new chip (typically a running tool call) to the
// rolling timeline and trims old entries.
func (m *Model) pushToolChip(chip toolChip) {
	chip.Name = strings.TrimSpace(chip.Name)
	if chip.Name == "" {
		return
	}
	m.toolTimeline = append(m.toolTimeline, chip)
	if len(m.toolTimeline) > maxToolTimelineChips {
		drop := len(m.toolTimeline) - maxToolTimelineChips
		m.toolTimeline = m.toolTimeline[drop:]
	}
}

// pushStreamingMessageToolChip mirrors a tool call onto the assistant
// transcript line that's currently streaming, so users see inline per-
// message chips (not just the global runtime strip). No-op when no message
// is actively streaming.
func (m *Model) pushStreamingMessageToolChip(chip toolChip) {
	chip.Name = strings.TrimSpace(chip.Name)
	if chip.Name == "" {
		return
	}
	if m.streamIndex < 0 || m.streamIndex >= len(m.transcript) {
		return
	}
	const maxPerMessage = 32
	line := &m.transcript[m.streamIndex]
	line.ToolChips = append(line.ToolChips, chip)
	if len(line.ToolChips) > maxPerMessage {
		drop := len(line.ToolChips) - maxPerMessage
		line.ToolChips = line.ToolChips[drop:]
	}
}

// finishStreamingMessageToolChip resolves the most recent running chip on
// the streaming assistant message with a terminal status.
func (m *Model) finishStreamingMessageToolChip(chip toolChip) {
	chip.Name = strings.TrimSpace(chip.Name)
	if chip.Name == "" {
		return
	}
	if m.streamIndex < 0 || m.streamIndex >= len(m.transcript) {
		return
	}
	wantRunning := "running"
	if strings.HasPrefix(strings.ToLower(chip.Status), "subagent-") {
		wantRunning = "subagent-running"
	}
	line := &m.transcript[m.streamIndex]
	for i := len(line.ToolChips) - 1; i >= 0; i-- {
		existing := line.ToolChips[i]
		if existing.Status != wantRunning {
			continue
		}
		if !strings.EqualFold(existing.Name, chip.Name) {
			continue
		}
		if chip.Step != 0 && existing.Step != 0 && existing.Step != chip.Step {
			continue
		}
		merged := existing
		merged.Status = chip.Status
		merged.DurationMs = chip.DurationMs
		if strings.TrimSpace(chip.Preview) != "" {
			merged.Preview = chip.Preview
		}
		if chip.Step > merged.Step {
			merged.Step = chip.Step
		}
		if chip.OutputTokens > 0 {
			merged.OutputTokens = chip.OutputTokens
		}
		if chip.Truncated {
			merged.Truncated = true
		}
		if chip.SavedChars > 0 {
			merged.SavedChars = chip.SavedChars
			merged.CompressedChars = chip.CompressedChars
			merged.CompressionPct = chip.CompressionPct
		}
		line.ToolChips[i] = merged
		return
	}
	m.pushStreamingMessageToolChip(chip)
}

// finishToolChip updates the most recent running chip for the same tool+step
// with a terminal status. Falls back to appending a fresh chip when no
// matching in-flight entry is found (e.g. result seen without a prior call).
func (m *Model) finishToolChip(chip toolChip) {
	chip.Name = strings.TrimSpace(chip.Name)
	if chip.Name == "" {
		return
	}
	wantRunning := "running"
	if strings.HasPrefix(strings.ToLower(chip.Status), "subagent-") {
		wantRunning = "subagent-running"
	}
	for i := len(m.toolTimeline) - 1; i >= 0; i-- {
		existing := m.toolTimeline[i]
		if existing.Status != wantRunning {
			continue
		}
		if !strings.EqualFold(existing.Name, chip.Name) {
			continue
		}
		if chip.Step != 0 && existing.Step != 0 && existing.Step != chip.Step {
			continue
		}
		merged := existing
		merged.Status = chip.Status
		merged.DurationMs = chip.DurationMs
		if strings.TrimSpace(chip.Preview) != "" {
			merged.Preview = chip.Preview
		}
		if chip.Step > merged.Step {
			merged.Step = chip.Step
		}
		if chip.OutputTokens > 0 {
			merged.OutputTokens = chip.OutputTokens
		}
		if chip.Truncated {
			merged.Truncated = true
		}
		if chip.SavedChars > 0 {
			merged.SavedChars = chip.SavedChars
			merged.CompressedChars = chip.CompressedChars
			merged.CompressionPct = chip.CompressionPct
		}
		m.toolTimeline[i] = merged
		return
	}
	m.pushToolChip(chip)
}

func (m *Model) resetAgentRuntime() {
	m.agentLoopActive = false
	m.agentLoopStep = 0
	m.agentLoopMaxToolStep = 0
	m.agentLoopToolRounds = 0
	m.agentLoopPhase = ""
	m.agentLoopProvider = ""
	m.agentLoopModel = ""
	m.agentLoopLastTool = ""
	m.agentLoopLastStatus = ""
	m.agentLoopLastDuration = 0
	m.agentLoopLastOutput = ""
	m.agentLoopContextScope = ""
}

