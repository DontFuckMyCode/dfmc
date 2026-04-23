package tui

// Tool branch of the engine-event router. Owns the four tool:* cases
// (call/result/error/reasoning). Extracted from engine_events.go so
// the chip-ribbon choreography + batch-fanout preview logic lives in
// one place. engine_events.go dispatches prefix-matched events here.

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func (m Model) handleToolEvent(eventType string, event engine.Event, payload map[string]any) (Model, string) {
	line := ""
	switch eventType {
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
	case "tool:error":
		switch p := event.Payload.(type) {
		case string:
			line = "Tool error: " + strings.TrimSpace(p)
		default:
			_ = p
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
	}
	return m, line
}
