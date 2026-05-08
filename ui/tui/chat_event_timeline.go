package tui

// chat_event_timeline.go — Model-receiver methods that own the chat
// transcript's tool-event lines. Three entrypoints:
//
//   upsertStreamingChatEvent — primary append/update, called from
//     engine_events_*.go handlers. Tool events go through
//     updateToolEventLine to merge into an existing line; everything
//     else appends a new system line.
//   updateToolEventLine — finds the last transcript line whose
//     EventLines contain a matching Key and merges the new event in;
//     otherwise appends.
//   attachReasonToStreamingChatEvent — backfills the model's
//     `_reason` text into the most recent tool event for a given
//     tool name (called from tool:reasoning).
//
// The pure transcript-text builders (chatEventTranscriptText and
// batchChatEventTranscriptText) live alongside the Model methods
// because they're the renderer's interface to the EventLines slice.
//
// Pure formatters (truncation, status labels, plural suffixes, chat-
// event Detail strings) live in chat_timeline_format.go.
// Payload-driven multi-line builders live in chat_timeline_builder.go.

import (
	"fmt"
	"strings"
	"time"
)

func (m *Model) upsertStreamingChatEvent(ev chatEventLine) {
	ev.Key = strings.TrimSpace(ev.Key)
	ev.Kind = strings.TrimSpace(ev.Kind)
	ev.Status = strings.TrimSpace(ev.Status)
	ev.Title = strings.TrimSpace(ev.Title)
	ev.Detail = strings.TrimSpace(ev.Detail)
	if ev.Title == "" {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	if !m.chat.sending {
		return
	}

	// Tool events: update existing line's EventLines instead of appending a new chatLine.
	// This prevents "TOOL TOOL TOOL" spam when a tool goes running→done/failed.
	if strings.EqualFold(ev.Kind, "tool") {
		m.updateToolEventLine(ev)
		return
	}
	line := newChatLine(chatRoleSystem, chatEventTranscriptText(ev))
	line.Timestamp = ev.At
	line.EventLines = []chatEventLine{ev}
	m.chat.transcript = append(m.chat.transcript, line)
	m.chat.scrollback = 0
}

// updateToolEventLine finds the last chatLine with the same tool Key in its EventLines
// and merges the new event into it. If not found, appends a new chatLine.
func (m *Model) updateToolEventLine(ev chatEventLine) {
	// Search backwards through transcript for a line with this tool's Key.
	for i := len(m.chat.transcript) - 1; i >= 0; i-- {
		line := &m.chat.transcript[i]
		if !line.Role.Eq(chatRoleTool) {
			continue
		}
		found := -1
		for j, existing := range line.EventLines {
			if existing.Key == ev.Key {
				found = j
				break
			}
		}
		if found >= 0 {
			// Merge into existing event; preserve start time, update status/detail/duration.
			line.EventLines[found] = mergeChatEventLine(line.EventLines[found], ev)
			line.Content = chatEventTranscriptText(line.EventLines[found])
			line.Timestamp = ev.At
			m.chat.scrollback = 0
			return
		}
	}
	// No existing line found — append new chatLine for this tool.
	line := newChatLine(chatRoleTool, chatEventTranscriptText(ev))
	line.Timestamp = ev.At
	line.EventLines = []chatEventLine{ev}
	m.chat.transcript = append(m.chat.transcript, line)
	m.chat.scrollback = 0
}

func (m *Model) attachReasonToStreamingChatEvent(toolName, reason string) {
	toolName = strings.TrimSpace(toolName)
	reason = strings.TrimSpace(reason)
	if toolName == "" || reason == "" {
		return
	}
	for lineIndex := len(m.chat.transcript) - 1; lineIndex >= 0; lineIndex-- {
		line := &m.chat.transcript[lineIndex]
		for i := len(line.EventLines) - 1; i >= 0; i-- {
			ev := line.EventLines[i]
			if ev.Kind != "tool" || !chatEventToolNameMatches(ev, toolName) {
				continue
			}
			ev.Reason = reason
			line.EventLines[i] = ev
			line.Content = chatEventTranscriptText(ev)
			return
		}
	}
}

func chatEventTranscriptText(ev chatEventLine) string {
	if isBatchToolEvent(ev) {
		return batchChatEventTranscriptText(ev)
	}
	status := chatEventTranscriptStatusLabel(ev.Status)
	name := strings.TrimSpace(ev.ToolName)
	if name == "" {
		name = strings.TrimSpace(ev.Title)
	}
	if name == "" {
		name = "tool"
	}
	head := []string{status + ": " + name}
	if ev.Step > 0 {
		head = append(head, fmt.Sprintf("step %d", ev.Step))
	}
	if ev.Round > 0 {
		head = append(head, fmt.Sprintf("round %d", ev.Round))
	}
	if ev.Duration > 0 {
		head = append(head, fmt.Sprintf("%dms", ev.Duration))
	}

	lines := []string{strings.Join(head, " | ")}
	if state := toolEventStateLine(ev, status); state != "" {
		lines = append(lines, state)
	}
	if params := timelineEventParamsField(ev.ParamsPreview); params != "" {
		lines = append(lines, "input: "+params)
	}
	if reason := strings.TrimSpace(ev.Reason); reason != "" {
		lines = append(lines, "_reason: "+timelineEventFieldLimit(reason, 260))
	}
	lines = append(lines, ev.DetailLines...)
	if detail := strings.TrimSpace(ev.Detail); detail != "" && !toolDetailDuplicatesParams(detail, ev.ParamsPreview) {
		label := "detail"
		if status == "done" || status == "failed" {
			label = "result"
		}
		lines = append(lines, label+": "+timelineEventFieldLimit(detail, 240))
	}
	if len(ev.RunningLog) > 0 && len(lines) < 7 {
		log := strings.TrimSpace(ev.RunningLog[len(ev.RunningLog)-1])
		if log != "" {
			lines = append(lines, "log: "+timelineEventFieldLimit(log, 180))
		}
	}
	return strings.Join(limitToolEventLines(lines, toolEventLineLimit(ev)), "\n")
}

func batchChatEventTranscriptText(ev chatEventLine) string {
	status := chatEventTranscriptStatusLabel(ev.Status)
	name := strings.TrimSpace(ev.ToolName)
	if name == "" {
		name = strings.TrimSpace(ev.Title)
	}
	if name == "" {
		name = "tool_batch_call"
	}
	head := []string{status + ": " + name}
	if ev.Step > 0 {
		head = append(head, fmt.Sprintf("step %d", ev.Step))
	}
	if ev.Duration > 0 {
		head = append(head, fmt.Sprintf("%dms", ev.Duration))
	}

	lines := []string{strings.Join(head, " | ")}
	if reason := strings.TrimSpace(ev.Reason); reason != "" {
		lines = append(lines, "_reason: "+timelineEventFieldLimit(reason, 260))
	}
	if detail := strings.TrimSpace(ev.Detail); detail != "" {
		lines = append(lines, "summary: "+timelineEventFieldLimit(detail, 240))
	}
	if len(ev.RunningLog) > 0 {
		lines = append(lines, "calls:")
		for _, log := range ev.RunningLog {
			if log = timelineEventFieldLimit(log, 220); log != "" {
				lines = append(lines, "  "+log)
			}
		}
	}
	return strings.Join(lines, "\n")
}
