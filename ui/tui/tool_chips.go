// tool_chips.go — the tool-chip ribbon's write path. Two parallel
// buffers land chips at runtime:
//
//   - m.agentLoop.toolTimeline: rolling global timeline shown in the
//     runtime card. Trimmed to maxToolTimelineChips so it stays a
//     single scannable row.
//   - m.chat.transcript[streamIndex].ToolChips: per-message inline
//     ribbon on the currently-streaming assistant line. Trimmed to a
//     per-message cap so one tool-heavy turn doesn't push the text
//     body off-screen.
//
// Five helpers, all called from engine_events.go as the bus publishes
// tool:call / tool:result / tool:reasoning / subagent:*:
//
//   - attachReasonToLastChip: backfills the model's _reason text onto
//     the most recent running chip (both buffers).
//   - pushToolChip / pushStreamingMessageToolChip: start a running chip
//     in the global / per-message ribbon.
//   - finishToolChip / finishStreamingMessageToolChip: merge a terminal
//     status into the most recent matching running chip; append a new
//     terminal chip when no running one is found (e.g. result seen
//     without a prior call because CLI paths bypass the pre-create).
//
// Everything here is a pure write against Model state — no event
// dispatch, no rendering. Render lives in theme.go.

package tui

import "strings"

const maxToolTimelineChips = 18

// attachReasonToLastChip backfills the model's self-narration text onto
// the most recent chip whose Name matches `toolName`. Walks both the
// rolling toolTimeline and the streaming-message chip strip so the
// reason shows in both places. Falls through silently when no chip
// matches — that happens for racy ordering or when the call originated
// from a path that didn't pre-create a chip (e.g. user-initiated
// CallTool from CLI).
func (m *Model) attachReasonToLastChip(toolName, reason string) {
	toolName = strings.TrimSpace(toolName)
	reason = strings.TrimSpace(reason)
	if toolName == "" || reason == "" {
		return
	}
	for i := len(m.agentLoop.toolTimeline) - 1; i >= 0; i-- {
		if strings.EqualFold(m.agentLoop.toolTimeline[i].Name, toolName) {
			m.agentLoop.toolTimeline[i].Reason = reason
			break
		}
	}
	if m.chat.streamIndex < 0 || m.chat.streamIndex >= len(m.chat.transcript) {
		return
	}
	line := &m.chat.transcript[m.chat.streamIndex]
	for i := len(line.ToolChips) - 1; i >= 0; i-- {
		if strings.EqualFold(line.ToolChips[i].Name, toolName) {
			line.ToolChips[i].Reason = reason
			return
		}
	}
}

// pushToolChip appends a new chip (typically a running tool call) to the
// rolling timeline and trims old entries.
func (m *Model) pushToolChip(chip toolChip) {
	chip.Name = strings.TrimSpace(chip.Name)
	if chip.Name == "" {
		return
	}
	m.agentLoop.toolTimeline = append(m.agentLoop.toolTimeline, chip)
	if len(m.agentLoop.toolTimeline) > maxToolTimelineChips {
		drop := len(m.agentLoop.toolTimeline) - maxToolTimelineChips
		m.agentLoop.toolTimeline = m.agentLoop.toolTimeline[drop:]
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
	if m.chat.streamIndex < 0 || m.chat.streamIndex >= len(m.chat.transcript) {
		return
	}
	const maxPerMessage = 32
	line := &m.chat.transcript[m.chat.streamIndex]
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
	if m.chat.streamIndex < 0 || m.chat.streamIndex >= len(m.chat.transcript) {
		return
	}
	wantRunning := "running"
	if strings.HasPrefix(strings.ToLower(chip.Status), "subagent-") {
		wantRunning = "subagent-running"
	}
	line := &m.chat.transcript[m.chat.streamIndex]
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
		// Preserve the params Verb across the finish merge — the
		// running chip carried the action line ("Read foo.go (lines
		// N-M)") and we want it to remain visible on the finished
		// card's second line. The result emit may include a fresh Verb
		// (rare); accept it only if non-empty.
		if strings.TrimSpace(chip.Verb) != "" {
			merged.Verb = chip.Verb
		}
		if chip.Step > merged.Step {
			merged.Step = chip.Step
		}
		// Reason carries over from the running chip; never overwritten by
		// the finish merge (the result event has no _reason knowledge).
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
		if len(chip.InnerLines) > 0 {
			merged.InnerLines = chip.InnerLines
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
	for i := len(m.agentLoop.toolTimeline) - 1; i >= 0; i-- {
		existing := m.agentLoop.toolTimeline[i]
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
		// Preserve the params Verb across the finish merge — the
		// running chip carried the action line ("Read foo.go (lines
		// N-M)") and we want it to remain visible on the finished
		// card's second line. The result emit may include a fresh Verb
		// (rare); accept it only if non-empty.
		if strings.TrimSpace(chip.Verb) != "" {
			merged.Verb = chip.Verb
		}
		if chip.Step > merged.Step {
			merged.Step = chip.Step
		}
		// Reason carries over from the running chip; never overwritten by
		// the finish merge (the result event has no _reason knowledge).
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
		if len(chip.InnerLines) > 0 {
			merged.InnerLines = chip.InnerLines
		}
		m.agentLoop.toolTimeline[i] = merged
		return
	}
	m.pushToolChip(chip)
}
