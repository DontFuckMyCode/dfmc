package tui

// chat_timeline_format_fields.go — single-line field sanitisation /
// truncation + payload accessor helpers shared across the timeline
// formatters. Companion siblings:
//
//   - chat_timeline_format.go        tool-name labels, transcript-row
//                                    helpers, event keys, numeric
//                                    formatters
//   - chat_timeline_format_detail.go tool_call / tool_result / context
//                                    "Detail" line builders
//
// timelineEventField collapses CR/LF/tabs into single spaces so the
// transcript stays one row per field; timelineEventParamsField hides
// heavy mutation payloads (old_string/new_string/content/patch) past
// a length threshold and surfaces the path/file marker instead so
// the model and the user see what's about to change without the
// noise of the raw bytes.

import "strings"

func timelineEventField(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, `\r\n`, " ")
	text = strings.ReplaceAll(text, `\n`, " ")
	text = strings.ReplaceAll(text, `\t`, " ")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return strings.Join(strings.Fields(text), " ")
}

func timelineEventFieldLimit(text string, width int) string {
	field := timelineEventField(text)
	if field == "" {
		return ""
	}
	if width <= 0 {
		return field
	}
	return truncateSingleLine(field, width)
}

func timelineEventParamsField(params string) string {
	params = strings.TrimSpace(params)
	if params == "" {
		return ""
	}
	field := timelineEventField(params)
	if field == "" {
		return ""
	}
	if shouldHideTimelineParams(params, field) {
		if target := timelineEventMutationTarget(field); target != "" {
			return target
		}
		return ""
	}
	return truncateSingleLine(field, 220)
}

func shouldHideTimelineParams(params, field string) bool {
	lower := strings.ToLower(field)
	heavyMutationPayload := strings.Contains(lower, "old_string=") ||
		strings.Contains(lower, "new_string=") ||
		strings.Contains(lower, "content=") ||
		strings.Contains(lower, "patch=")
	if !heavyMutationPayload {
		return false
	}
	return len([]rune(field)) > 160 ||
		strings.Count(params, `\n`) > 1 ||
		strings.Count(params, "\n") > 1
}

func timelineEventMutationTarget(field string) string {
	if path := timelineEventKVValue(field, "path"); path != "" {
		return "path=" + truncateSingleLine(path, 180)
	}
	if file := timelineEventKVValue(field, "file"); file != "" {
		return "file=" + truncateSingleLine(file, 180)
	}
	if strings.Contains(strings.ToLower(field), "patch=") {
		return "patch payload hidden"
	}
	return ""
}

func timelineEventKVValue(field, key string) string {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return ""
	}
	lower := strings.ToLower(field)
	marker := key + "="
	idx := strings.LastIndex(lower, " "+marker)
	if idx >= 0 {
		idx++
	} else if strings.HasPrefix(lower, marker) {
		idx = 0
	}
	if idx < 0 {
		return ""
	}
	value := strings.TrimSpace(field[idx+len(marker):])
	if value == "" {
		return ""
	}
	if quote := value[0]; quote == '"' || quote == '\'' {
		value = value[1:]
		if end := strings.IndexRune(value, rune(quote)); end >= 0 {
			value = value[:end]
		}
		return strings.TrimSpace(value)
	}
	if fields := strings.Fields(value); len(fields) > 0 {
		return strings.Trim(fields[0], `"'`)
	}
	return strings.Trim(value, `"'`)
}

// --- payload helpers ------------------------------------------------------

func firstTimelineString(primary, secondary map[string]any, keys ...string) string {
	for _, data := range []map[string]any{primary, secondary} {
		if data == nil {
			continue
		}
		for _, key := range keys {
			if value := payloadString(data, key, ""); value != "" && value != "<nil>" {
				return value
			}
		}
	}
	return ""
}

func firstTimelineInt(primary, secondary map[string]any, keys ...string) int {
	for _, data := range []map[string]any{primary, secondary} {
		if data == nil {
			continue
		}
		for _, key := range keys {
			if value := payloadInt(data, key, 0); value > 0 {
				return value
			}
		}
	}
	return 0
}

func firstTimelineBool(primary, secondary map[string]any, keys ...string) bool {
	for _, data := range []map[string]any{primary, secondary} {
		if data == nil {
			continue
		}
		for _, key := range keys {
			if payloadBool(data, key, false) {
				return true
			}
		}
	}
	return false
}

func pickPayloadInt(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case float32:
		return int(v), true
	default:
		return 0, false
	}
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

func pluralSuffix(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// --- compaction / line limiting ------------------------------------------

func compactTimelineLines(lines []string, limit int) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out
}

func limitToolEventLines(lines []string, maxLines int) []string {
	if maxLines <= 0 {
		return nil
	}
	out := make([]string, 0, min(len(lines), maxLines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, line)
		if len(out) == maxLines {
			break
		}
	}
	return out
}
