package tui

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

func chatEventTranscriptText(ev chatEventLine) string {
	status := chatEventTranscriptStatusLabel(ev.Status)
	parts := []string{status + ": " + ev.Title}
	if ev.Duration > 0 {
		parts = append(parts, fmt.Sprintf("%dms", ev.Duration))
	}
	if ev.Detail != "" {
		parts = append(parts, ev.Detail)
	}
	return strings.Join(parts, " | ")
}

func chatEventTranscriptStatusLabel(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "running"
	case "ok", "done":
		return "done"
	case "failed", "error":
		return "failed"
	case "warn", "throttle":
		return "warn"
	default:
		return "info"
	}
}

func mergeChatEventLine(old, next chatEventLine) chatEventLine {
	if next.Key == "" {
		next.Key = old.Key
	}
	if next.Kind == "" {
		next.Kind = old.Kind
	}
	if next.Status == "" {
		next.Status = old.Status
	}
	if next.Title == "" {
		next.Title = old.Title
	}
	if next.Detail == "" {
		next.Detail = old.Detail
	}
	if next.At.IsZero() {
		next.At = old.At
	}
	if next.Duration == 0 {
		next.Duration = old.Duration
	}
	return next
}

func (m *Model) attachReasonToStreamingChatEvent(toolName, reason string) {
	toolName = strings.TrimSpace(toolName)
	reason = strings.TrimSpace(reason)
	if toolName == "" || reason == "" || m.chat.streamIndex < 0 || m.chat.streamIndex >= len(m.chat.transcript) {
		return
	}
	line := &m.chat.transcript[m.chat.streamIndex]
	for i := len(line.EventLines) - 1; i >= 0; i-- {
		ev := line.EventLines[i]
		if ev.Kind != "tool" || !strings.EqualFold(ev.Title, toolName) {
			continue
		}
		ev.Detail = reason
		line.EventLines[i] = ev
		line.Content = chatEventTranscriptText(ev)
		return
	}
}

func toolNameKey(toolName string) string {
	return "tool:" + toolName
}

func toolChatEventKey(toolName string, step int) string {
	toolName = strings.TrimSpace(toolName)
	if step > 0 {
		return fmt.Sprintf("tool:%d:%s", step, toolName)
	}
	return toolNameKey(toolName)
}

// elapsedLabel returns a compact elapsed-time string for a running tool.
// Returns "" if the event is not running or the delta is not positive.
func elapsedLabel(startTime time.Time) string {
	if startTime.IsZero() {
		return ""
	}
	elapsed := time.Since(startTime)
	if elapsed < time.Second {
		return ""
	}
	seconds := int(elapsed.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("+%ds", seconds)
	}
	minutes := seconds / 60
	seconds = seconds % 60
	return fmt.Sprintf("+%dm%02ds", minutes, seconds)
}

func compactMetric(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fm", float64(n)/1000000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func toolCallChatDetail(payload map[string]any, step int, paramsPreview string) string {
	parts := []string{}
	if step > 0 {
		parts = append(parts, fmt.Sprintf("step %d", step))
	}
	if read := readChatDetail(payload); read != "" {
		parts = append(parts, read)
	}
	if mutation := mutationChatDetail(payload, "will change"); mutation != "" {
		parts = append(parts, mutation)
	}
	if paramsPreview = strings.TrimSpace(paramsPreview); paramsPreview != "" {
		parts = append(parts, truncateSingleLine(paramsPreview, 120))
	}
	if provider := payloadString(payload, "provider", ""); provider != "" {
		model := payloadString(payload, "model", "")
		if model != "" {
			parts = append(parts, provider+"/"+model)
		} else {
			parts = append(parts, provider)
		}
	}
	return strings.Join(parts, " | ")
}

func toolResultChatDetail(payload map[string]any, preview string, success bool, compressionPct int) string {
	reads := payloadInt(payload, "files_read", 0)
	writes := payloadInt(payload, "files_written", 0)
	tokens := payloadInt(payload, "tokens_used", 0)
	parts := []string{}
	if read := readChatDetail(payload); read != "" {
		parts = append(parts, read)
	}
	if mutation := mutationChatDetail(payload, "changed"); mutation != "" {
		parts = append(parts, mutation)
	}
	if reads > 0 {
		if reads == 1 {
			parts = append(parts, "1 file read")
		} else {
			parts = append(parts, fmt.Sprintf("%d files read", reads))
		}
	}
	if writes > 0 {
		if writes == 1 {
			parts = append(parts, "1 file written")
		} else {
			parts = append(parts, fmt.Sprintf("%d files written", writes))
		}
	}
	if tokens > 0 {
		parts = append(parts, fmt.Sprintf("%d tok", tokens))
	}
	if outputTokens := payloadInt(payload, "output_tokens", 0); outputTokens > 0 {
		parts = append(parts, "out "+compactMetric(outputTokens)+" tok")
	}
	if modelTokens := payloadInt(payload, "payload_tokens", 0); modelTokens > 0 {
		parts = append(parts, "model "+compactMetric(modelTokens)+" tok")
	}
	if savedChars := payloadInt(payload, "compression_saved_chars", 0); savedChars > 0 {
		if compressionPct > 0 {
			parts = append(parts, fmt.Sprintf("rtk saved %s chars (%d%%)", compactMetric(savedChars), compressionPct))
		} else {
			parts = append(parts, "rtk saved "+compactMetric(savedChars)+" chars")
		}
	}
	if compressionPct > 0 {
		parts = append(parts, fmt.Sprintf("%d%% compressed", compressionPct))
	}
	if !success {
		if errText := payloadString(payload, "error", ""); errText != "" {
			parts = append(parts, truncateSingleLine(errText, 120))
		}
	} else if preview = strings.TrimSpace(preview); preview != "" {
		parts = append(parts, truncateSingleLine(preview, 120))
	}
	return strings.Join(parts, " | ")
}

func readChatDetail(payload map[string]any) string {
	if path := payloadString(payload, "read_path", ""); path != "" {
		start := payloadInt(payload, "read_line_start", 0)
		end := payloadInt(payload, "read_line_end", 0)
		returned := payloadInt(payload, "read_returned_lines", 0)
		total := payloadInt(payload, "read_total_lines", 0)
		rangeLabel := path
		if start > 0 && end > 0 {
			rangeLabel = fmt.Sprintf("%s:%d-%d", path, start, end)
		}
		if returned > 0 && total > 0 {
			return fmt.Sprintf("read %s (%d/%d lines)", rangeLabel, returned, total)
		}
		if returned > 0 {
			return fmt.Sprintf("read %s (%d lines)", rangeLabel, returned)
		}
		return "read " + rangeLabel
	}
	files := payloadStringSlice(payload, "files_read")
	if len(files) == 0 {
		if single := payloadString(payload, "file_read", ""); single != "" {
			files = []string{single}
		}
	}
	if len(files) == 0 {
		return ""
	}
	count := len(files)
	if count == 0 {
		return ""
	}
	if count == 1 {
		return files[0] + " read"
	}
	return fmt.Sprintf("%d files read", count)
}

func mutationChatDetail(payload map[string]any, label string) string {
	if files := payloadStringSlice(payload, "changed_files"); len(files) > 0 {
		added := payloadInt(payload, "added_lines", 0)
		removed := payloadInt(payload, "removed_lines", 0)
		target := files[0]
		if len(files) > 1 {
			target = fmt.Sprintf("%d files", len(files))
		}
		detail := strings.TrimSpace(label + " " + target)
		if added > 0 || removed > 0 {
			detail += fmt.Sprintf(" +%d -%d lines", added, removed)
		}
		return detail
	}
	mutations := payloadMap(payload, "mutations")
	if mutations == nil {
		return ""
	}
	files := payloadStringSlice(mutations, "files")
	if len(files) == 0 {
		if single := payloadString(mutations, "file", ""); single != "" {
			files = []string{single}
		}
	}
	if len(files) > 0 {
		count := len(files)
		if count == 1 {
			return fmt.Sprintf("%s: %s", files[0], label)
		}
		return fmt.Sprintf("%d files %s", count, label)
	}
	return ""
}

func contextBuiltChatDetail(files, tokens, budget, perFile int, task, compression string) string {
	parts := []string{}
	if files > 0 {
		parts = append(parts, fmt.Sprintf("%d files", files))
	}
	if tokens > 0 {
		parts = append(parts, compactMetric(tokens)+" tok")
	}
	if tokens > 0 && budget > 0 {
		parts = append(parts, fmt.Sprintf("%d%% budget", (tokens*100)/budget))
	}
	if budget > 0 {
		parts = append(parts, "budget "+compactMetric(budget))
	}
	if perFile > 0 {
		parts = append(parts, "per-file "+compactMetric(perFile))
	}
	if task = strings.TrimSpace(task); task != "" && task != "general" {
		parts = append(parts, task)
	}
	if compression = strings.TrimSpace(compression); compression != "" && compression != "-" {
		parts = append(parts, compression)
	}
	return strings.Join(parts, " | ")
}

func payloadMap(payload map[string]any, key string) map[string]any {
	if payload == nil {
		return nil
	}
	v, ok := payload[key]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	return m
}
