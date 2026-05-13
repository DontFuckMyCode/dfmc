package tui

// Tool branch of the engine-event router. Owns the four tool:* cases
// (call/result/error/reasoning). Extracted from engine_events.go so
// the chip-ribbon choreography + batch-fanout preview logic lives in
// one place. engine_events.go dispatches prefix-matched events here.

import (
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func (m Model) handleToolEvent(eventType string, event engine.Event, payload map[string]any) (Model, string) {
	line := ""
	switch eventType {
	case "tool:call":
		return m.handleToolCallEvent(payload)
	case "tool:result":
		return m.handleToolResultEvent(payload)
	case "tool:error":
		toolName := payloadString(payload, "tool", payloadString(payload, "name", "tool"))
		switch p := event.Payload.(type) {
		case string:
			line = "Tool error: " + strings.TrimSpace(p)
		default:
			_ = p
			line = "Tool error occurred."
		}
		now := time.Now()
		m.pushToolCallLogEntry(toolCallLogEntry{
			ToolName:   toolName,
			Status:     "failed",
			StartedAt:  now,
			FinishedAt: now,
			Error:      strings.TrimSpace(strings.TrimPrefix(line, "Tool error:")),
		})
	case "tool:timeout":
		// Distinct line from tool:error so an operator scanning the feed
		// sees the gate firing without having to read tool error text.
		// Both events fire — tool:error carries the model-facing message,
		// tool:timeout carries the structural fact.
		toolName := payloadString(payload, "name", "tool")
		limitMs := payloadInt(payload, "limit_ms", 0)
		if limitMs > 0 {
			line = fmt.Sprintf("Tool timeout: %s exceeded %dms cap.", toolName, limitMs)
		} else {
			line = fmt.Sprintf("Tool timeout: %s.", toolName)
		}
		now := time.Now()
		m.pushToolCallLogEntry(toolCallLogEntry{
			ToolName:   toolName,
			Status:     "timeout",
			StartedAt:  now,
			FinishedAt: now,
			Error:      line,
		})
	case "tool:denied":
		// Approval gate or sub-agent allowlist refused this tool. Fires
		// BEFORE the tool would have run — there's no chip to finalize,
		// just a denial line. Operators care because a recurring denial
		// often means an over-strict gate or a model trying to use a
		// tool that's intentionally off-limits; either way it's worth
		// surfacing instead of letting the model swallow it silently.
		toolName := payloadString(payload, "name", "tool")
		reason := strings.TrimSpace(payloadString(payload, "reason", "denied"))
		source := payloadString(payload, "source", "")
		// Push a denied chip so the assistant message ribbon shows the
		// refusal at the same spot a real tool would have appeared.
		m.pushToolChip(toolChip{
			Name:    toolName,
			Status:  "denied",
			Preview: truncateSingleLine(reason, 72),
		})
		detail := reason
		if source != "" {
			detail = fmt.Sprintf("[%s] %s", source, reason)
		}
		m.upsertStreamingChatEvent(chatEventLine{
			Key:      "tool:denied:" + toolName,
			Kind:     "tool",
			Status:   "error",
			Title:    toolName + " (denied)",
			Detail:   detail,
			ToolName: toolName,
		})
		if source != "" {
			line = fmt.Sprintf("Tool denied [%s]: %s — %s", source, toolName, truncateSingleLine(reason, 120))
		} else {
			line = fmt.Sprintf("Tool denied: %s — %s", toolName, truncateSingleLine(reason, 120))
		}
		now := time.Now()
		m.pushToolCallLogEntry(toolCallLogEntry{
			ToolName:   toolName,
			Status:     "denied",
			StartedAt:  now,
			FinishedAt: now,
			Reason:     reason,
			Error:      detail,
		})
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
			m.attachReasonToStreamingChatEvent(toolName, reason)
			// Mirror onto agentLoopState so the runtime "now" strip
			// shows the model's current intent inline alongside last-
			// tool. Truncated render-side; we keep the full text here.
			m.agentLoop.lastToolReason = reason
			line = fmt.Sprintf("%s · %s", toolName, truncateForLine(reason, 90))
		}
	}
	return m, line
}

// trackMutationOrValidation + isValidationCommand live in
// engine_events_tool_track.go.
