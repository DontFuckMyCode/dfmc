package tui

// chat_timeline_builder_batch.go — fan-out preview lines for the
// tool_batch_call meta tool. Each entry in the call list becomes a
// numbered bullet ("1. read_file - ui/tui/tui.go (lines 100-200)")
// with an optional "why: ..." suffix when _reason is present, so the
// chat timeline shows what the LLM intends to do BEFORE the per-call
// results arrive. Companion siblings:
//
//   - chat_timeline_builder.go        toolCallTimelineLines +
//                                     toolResultTimelineLines +
//                                     toolResultTimelineLineLimit
//   - chat_timeline_builder_cards.go  per-tool card lines + per-
//                                     payload helpers (target /
//                                     outcome / impact / diff /
//                                     range / command)

import (
	"fmt"
	"strings"
)

func batchToolCallPreviewLines(payload map[string]any) []string {
	calls := batchToolCallsFromPayload(payload)
	if len(calls) == 0 {
		return nil
	}
	lines := make([]string, 0, len(calls))
	for i, raw := range calls {
		line := batchToolCallPreviewLine(i+1, raw)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func batchToolCallsFromPayload(payload map[string]any) []any {
	if payload == nil {
		return nil
	}
	if calls, ok := payload["calls"].([]any); ok {
		return calls
	}
	params, _ := payload["params"].(map[string]any)
	if params == nil {
		return nil
	}
	switch calls := params["calls"].(type) {
	case []any:
		return calls
	case []map[string]any:
		out := make([]any, 0, len(calls))
		for _, call := range calls {
			out = append(out, call)
		}
		return out
	}
	return nil
}

func batchToolCallPreviewLine(index int, raw any) string {
	call, _ := raw.(map[string]any)
	if call == nil {
		return fmt.Sprintf("%d. tool", index)
	}
	name := strings.TrimSpace(fmt.Sprint(call["name"]))
	if name == "" {
		name = strings.TrimSpace(fmt.Sprint(call["tool"]))
	}
	if name == "" {
		name = "tool"
	}
	args, _ := call["args"].(map[string]any)
	target := batchToolCallTarget(name, args)
	reason := batchToolCallReason(args, call)
	if target == "" {
		if reason != "" {
			return fmt.Sprintf("%d. %s | why: %s", index, name, reason)
		}
		return fmt.Sprintf("%d. %s", index, name)
	}
	line := fmt.Sprintf("%d. %s - %s", index, name, target)
	if reason != "" {
		line += " | why: " + reason
	}
	return line
}

func batchToolCallTarget(name string, args map[string]any) string {
	if args == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "run_command":
		cmd := strings.TrimSpace(fmt.Sprint(args["command"]))
		if rest := batchToolArgsList(args["args"]); rest != "" {
			return "$ " + strings.TrimSpace(cmd+" "+rest)
		}
		if cmd != "" {
			return "$ " + cmd
		}
	case "read_file":
		path := strings.TrimSpace(fmt.Sprint(args["path"]))
		if path == "" {
			return ""
		}
		start, hasStart := pickPayloadInt(args["line_start"])
		end, hasEnd := pickPayloadInt(args["line_end"])
		if hasStart && hasEnd && end > 0 {
			return fmt.Sprintf("Read %s (lines %d-%d)", path, start, end)
		}
		return "Read " + path
	case "edit_file":
		return "Edit " + strings.TrimSpace(fmt.Sprint(args["path"]))
	case "write_file":
		return "Write " + strings.TrimSpace(fmt.Sprint(args["path"]))
	case "list_dir":
		path := strings.TrimSpace(fmt.Sprint(args["path"]))
		if path == "" {
			path = "."
		}
		return "List " + path
	case "grep_codebase":
		pattern := strings.TrimSpace(fmt.Sprint(args["pattern"]))
		if pattern != "" {
			return `Search "` + pattern + `"`
		}
	case "glob":
		return "Glob " + strings.TrimSpace(fmt.Sprint(args["pattern"]))
	}
	for _, key := range []string{"path", "pattern", "query", "command", "url"} {
		if value := strings.TrimSpace(fmt.Sprint(args[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func batchToolArgsList(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case []string:
		return strings.Join(v, " ")
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func batchToolCallReason(args map[string]any, call map[string]any) string {
	for _, raw := range []any{args["_reason"], call["_reason"], call["reason"]} {
		reason := strings.TrimSpace(fmt.Sprint(raw))
		if reason != "" && reason != "<nil>" {
			return timelineEventField(reason)
		}
	}
	return ""
}
