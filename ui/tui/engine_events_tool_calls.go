// engine_events_tool_calls.go — tool:call + tool:result chip
// choreography. Sibling of engine_events_tool.go which keeps the
// handleToolEvent dispatcher and the smaller tool:error / tool:timeout
// / tool:denied / tool:reasoning cases.
//
// Splitting these out keeps engine_events_tool.go scoped to "which
// tool:* event are we looking at" while this file owns the chip-ribbon
// choreography for the call→result lifecycle: pushing a running chip,
// tracking _reason narration, building the params-preview line,
// surfacing batch-fanout previews, computing read_file truncation
// suffixes, finalising the chip with output preview + compression
// stats + hard-truncation flags + per-batch-call breakdown, and
// updating the agentLoop telemetry state. Both methods return (Model,
// line) matching the dispatcher's contract.

package tui

import (
	"fmt"
	"strings"
)

func (m Model) handleToolCallEvent(payload map[string]any) (Model, string) {
	m.agentLoop.active = true
	m.agentLoop.phase = "tool-call"
	toolName := payloadString(payload, "tool", "tool")
	step := payloadInt(payload, "step", 0)
	m.agentLoop.lastTool = toolName
	m.agentLoop.lastStatus = "running"
	m.agentLoop.lastDuration = 0
	// Capture the model's _reason on the call event itself when it
	// arrives bundled (some providers ship it on the call payload
	// directly rather than emitting a separate tool:reasoning).
	// Empty reason here doesn't clear an existing one — we let the
	// fall-through tool:reasoning case overwrite if a richer
	// narration comes in later in the round.
	if r := strings.TrimSpace(payloadString(payload, "reason", "")); r != "" {
		m.agentLoop.lastToolReason = r
	} else {
		// New tool call with no reason → previous narration is
		// stale. Clear so the strip doesn't carry the OLD intent.
		m.agentLoop.lastToolReason = ""
	}
	if step > 0 {
		m.agentLoop.step = step
	}
	if rounds := payloadInt(payload, "tool_rounds", 0); rounds > 0 {
		m.agentLoop.toolRounds = rounds
	}
	m.agentLoop.provider = payloadString(payload, "provider", m.agentLoop.provider)
	m.agentLoop.model = payloadString(payload, "model", m.agentLoop.model)
	paramsPreview := payloadString(payload, "params_preview", "")
	reason := payloadString(payload, "reason", "")
	displayName := displayToolName(toolName, payload)
	callDetail := toolCallChatDetail(payload, step, paramsPreview)
	runningLog := []string(nil)
	if strings.EqualFold(strings.TrimSpace(toolName), "tool_batch_call") {
		runningLog = batchToolCallPreviewLines(payload)
		if len(runningLog) > 0 {
			callDetail = fmt.Sprintf("%d planned calls", len(runningLog))
		}
	}
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
	m.upsertStreamingChatEvent(chatEventLine{
		Key:           toolChatEventKey(toolName, step),
		Kind:          "tool",
		Status:        "running",
		Title:         toolName,
		Detail:        callDetail,
		ToolName:      displayName,
		ParamsPreview: paramsPreview,
		Reason:        reason,
		Step:          step,
		RunningLog:    runningLog,
		DetailLines:   toolCallTimelineLines(toolName, payload, paramsPreview),
	})
	m.telemetry.activeToolCount++
	line := ""
	if step > 0 {
		line = fmt.Sprintf("Agent tool call: %s (step %d)", toolName, step)
	} else {
		line = fmt.Sprintf("Agent tool call: %s", toolName)
	}
	if paramsPreview != "" {
		line += " " + paramsPreview
	}
	return m, line
}

func (m Model) handleToolResultEvent(payload map[string]any) (Model, string) {
	m.agentLoop.active = true
	m.agentLoop.phase = "tool-result"
	toolName := payloadString(payload, "tool", "tool")
	duration := payloadInt(payload, "durationMs", 0)
	success := payloadBool(payload, "success", true)
	status := "ok"
	if !success {
		status = "failed"
		// Per-turn error tally — the chip ribbon scrolls and the
		// activity feed buries individual failures, so a retry-heavy
		// turn vanishes once the assistant stitches a final answer.
		// Counted here for the end-of-turn summary card.
		m.agentLoop.toolErrorsThisTurn++
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
	// Clear the stuck-loop badge once the model recovers — a single
	// successful tool call means the trajectory layer's "switch
	// tactic" hint landed and the agent broke out of the failure
	// loop. The badge re-arms on the next agent:coach:stuck event.
	if success && m.agentLoop.stuckTool != "" {
		m.agentLoop.stuckClearedAt = step
		m.agentLoop.stuckTool = ""
		m.agentLoop.stuckCount = 0
		m.agentLoop.stuckErrClass = ""
	}
	// Mutation/validation tracking. Successful edits accumulate on
	// agentLoop.unvalidatedEdits; a successful build/test/vet run
	// clears the slate. The runtime strip surfaces the count as a
	// "unverified: N edits" badge so a long autonomous run that
	// keeps editing without ever validating becomes visually obvious.
	if success {
		m = m.trackMutationOrValidation(toolName, payload, step)
	}
	m.agentLoop.provider = payloadString(payload, "provider", m.agentLoop.provider)
	m.agentLoop.model = payloadString(payload, "model", m.agentLoop.model)
	displayName := displayToolName(toolName, payload)
	chipPreview := preview
	if chipPreview == "" && !success {
		chipPreview = payloadString(payload, "error", "")
	}
	var batchInner []string
	batchCount := payloadInt(payload, "batch_count", 0)
	if batchCount > 0 {
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
	hardTruncated := payloadBool(payload, "hard_truncated", false)
	hardTruncRunes := payloadInt(payload, "hard_truncated_output_runes", 0) +
		payloadInt(payload, "hard_truncated_data_runes", 0)
	// read_file's default 200-line cap is invisible from the chip
	// otherwise — the model often gets a partial slice and the user
	// can't tell unless they read the activity feed. Surface the
	// line accounting as a chip preview prefix so a turn full of
	// "looked but didn't see the rest" reads are obvious.
	if strings.EqualFold(strings.TrimSpace(toolName), "read_file") {
		totalLines := payloadInt(payload, "read_total_lines", 0)
		returnedLines := payloadInt(payload, "read_returned_lines", 0)
		if totalLines > 0 && returnedLines > 0 && returnedLines < totalLines {
			suffix := fmt.Sprintf("%d/%d lines · %d omitted", returnedLines, totalLines, totalLines-returnedLines)
			if strings.TrimSpace(chipPreview) == "" {
				chipPreview = suffix
			} else {
				chipPreview = suffix + " · " + chipPreview
			}
		}
	}
	finishedChip := toolChip{
		Name:               toolName,
		Status:             status,
		Step:               step,
		DurationMs:         duration,
		Preview:            chipPreview,
		OutputTokens:       payloadInt(payload, "output_tokens", 0),
		Truncated:          payloadBool(payload, "truncated", false),
		CompressedChars:    payloadChars,
		SavedChars:         savedChars,
		CompressionPct:     compressionPct,
		HardTruncated:      hardTruncated,
		HardTruncatedRunes: hardTruncRunes,
		InnerLines:         batchInner,
	}
	m.finishToolChip(finishedChip)
	m.finishStreamingMessageToolChip(finishedChip)
	resultDetail := toolResultChatDetail(payload, preview, success, compressionPct)
	if batchCount > 0 {
		resultDetail = batchResultSummaryDetail(payload, resultDetail)
	}
	if strings.EqualFold(strings.TrimSpace(toolName), "tool_batch_call") && batchToolCallNameSummary(payload) == "" {
		displayName = ""
	}
	if isMetaToolName(toolName) && strings.EqualFold(displayName, toolName) {
		displayName = ""
	}
	m.upsertStreamingChatEvent(chatEventLine{
		Key:         toolChatEventKey(toolName, step),
		Kind:        "tool",
		Status:      status,
		Title:       toolName,
		Detail:      resultDetail,
		Duration:    duration,
		ToolName:    displayName,
		Reason:      payloadString(payload, "reason", ""),
		Step:        step,
		RunningLog:  batchInner,
		DetailLines: toolResultTimelineLines(toolName, payload, preview, success, compressionPct),
	})
	if m.telemetry.activeToolCount > 0 {
		m.telemetry.activeToolCount--
	}
	line := ""
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
	return m, line
}
