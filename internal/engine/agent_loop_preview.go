package engine

// agent_loop_preview.go — formatToolParamsPreview and the verb-style
// renderer it delegates to. Pre-fix the chat strip dumped Go's raw
// map.String() (`args="map[args:[build ./...] command:go ...]"`) which
// was readable to nobody. The verb branch turns each known tool's
// params into a Claude-Code-style transcript line ("$ go build ./...",
// "Read internal/config/config.go (lines 1-80)", "Search \"loadDot\"")
// and falls back to a sorted key=value dump for unknown tools so we
// never silently lose information.
//
// The request-assembly / history-budget / payload-trim / event-publish
// helpers (buildToolLoopRequestMessages, historyBudgetForRequestWithTail,
// trimToolPayload, truncateRunesWithMarker, publishProviderComplete,
// publishAgentLoopEvent, streamAnswerText) live in agent_loop.go.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// formatToolParamsPreview renders a human-friendly one-liner for the
// `params_preview` event field that the TUI hangs under each tool chip.
// Falls back to the legacy "key=value" dump when the params shape
// doesn't match a known verb so we never silently lose information.
func formatToolParamsPreview(params map[string]any, limit int) string {
	if len(params) == 0 {
		return ""
	}
	if pretty := formatToolParamsVerb(params); pretty != "" {
		return compactToolPayload(pretty, limit)
	}
	return compactToolPayload(formatToolParamsKVDump(params), limit)
}

// formatToolParamsVerb is the verb-style branch. Returns "" when the
// params don't match any known shape so the caller can fall through
// to the kv-dump.
func formatToolParamsVerb(params map[string]any) string {
	// Meta-tool wrappers (tool_call, tool_batch_call) — unwrap so the
	// preview names the underlying file/command, not "tool_call".
	if calls, ok := params["calls"].([]any); ok && len(calls) > 0 {
		return formatToolParamsBatchVerb(calls)
	}
	if name, ok := params["name"].(string); ok && strings.TrimSpace(name) != "" {
		inner, _ := params["args"].(map[string]any)
		if inner == nil {
			inner = map[string]any{}
		}
		return formatToolParamsVerbFor(name, inner)
	}
	return ""
}

// formatToolParamsVerbFor renders the verb line for a single backend
// tool given its name + already-unwrapped args.
func formatToolParamsVerbFor(name string, args map[string]any) string {
	switch name {
	case "run_command":
		cmd := strings.TrimSpace(fmt.Sprint(args["command"]))
		if cmd == "" {
			return ""
		}
		rest := formatArgsList(args["args"])
		if rest != "" {
			return "$ " + cmd + " " + rest
		}
		return "$ " + cmd
	case "read_file":
		path := strings.TrimSpace(fmt.Sprint(args["path"]))
		if path == "" {
			return ""
		}
		if start, ok := pickAnyInt(args["line_start"]); ok {
			if end, ok := pickAnyInt(args["line_end"]); ok && end > 0 {
				return fmt.Sprintf("Read %s (lines %d-%d)", path, start, end)
			}
		}
		return "Read " + path
	case "edit_file":
		path := strings.TrimSpace(fmt.Sprint(args["path"]))
		if path == "" {
			return "Edit"
		}
		return "Edit " + path
	case "write_file":
		path := strings.TrimSpace(fmt.Sprint(args["path"]))
		if path == "" {
			return "Write"
		}
		return "Write " + path
	case "list_dir":
		path := strings.TrimSpace(fmt.Sprint(args["path"]))
		if path == "" {
			return "List ."
		}
		return "List " + path
	case "grep_codebase":
		pattern := strings.TrimSpace(fmt.Sprint(args["pattern"]))
		if pattern == "" {
			return ""
		}
		return `Search "` + pattern + `"`
	case "glob":
		pattern := strings.TrimSpace(fmt.Sprint(args["pattern"]))
		if pattern == "" {
			return ""
		}
		return "Glob " + pattern
	case "tool_search":
		query := strings.TrimSpace(fmt.Sprint(args["query"]))
		if query == "" {
			return ""
		}
		return "Lookup " + query
	case "tool_help":
		target := strings.TrimSpace(fmt.Sprint(args["name"]))
		if target == "" {
			return ""
		}
		return "Help " + target
	}
	// Generic fallback for unknown tools — name + first identifying arg.
	for _, key := range []string{"path", "pattern", "query", "command", "url"} {
		if raw, ok := args[key]; ok {
			value := strings.TrimSpace(fmt.Sprint(raw))
			if value != "" {
				return name + " " + value
			}
		}
	}
	return ""
}

// formatToolParamsBatchVerb renders the verb line for tool_batch_call.
// Counts repeats of the same tool so a "5x read_file" batch reads as
// "Batch [5: read_file ×5]" instead of listing the same name five times.
func formatToolParamsBatchVerb(calls []any) string {
	if len(calls) == 0 {
		return "Batch []"
	}
	counts := make(map[string]int)
	order := make([]string, 0, len(calls))
	for _, raw := range calls {
		obj, _ := raw.(map[string]any)
		name := strings.TrimSpace(fmt.Sprint(obj["name"]))
		if name == "" {
			name = "?"
		}
		if _, seen := counts[name]; !seen {
			order = append(order, name)
		}
		counts[name]++
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		if c := counts[name]; c > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", name, c))
		} else {
			parts = append(parts, name)
		}
	}
	return fmt.Sprintf("Batch [%d: %s]", len(calls), strings.Join(parts, ", "))
}

// formatArgsList renders a short whitespace-joined preview of the
// JSON shapes commandArgs() accepts (string / []string / []any).
func formatArgsList(raw any) string {
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
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// pickAnyInt extracts an int from JSON-derived loose shapes
// (json.Number marshals through float64; some paths preserve int).
func pickAnyInt(raw any) (int, bool) {
	switch v := raw.(type) {
	case nil:
		return 0, false
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
}

// formatToolParamsKVDump is the legacy "key=value" form, kept as a
// last-resort fallback for tools we don't have a verb for. Should be
// rare in practice once formatToolParamsVerbFor covers the registry.
func formatToolParamsKVDump(params map[string]any) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(fmt.Sprint(params[key]))
		if value == "" {
			continue
		}
		if strings.ContainsAny(value, " \t\r\n") {
			value = strconvQuote(value)
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, value))
	}
	return strings.Join(parts, " ")
}

func compactToolPayload(raw string, maxChars int) string {
	text := stripTerminalControlBytes(raw)
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		first := strings.TrimSpace(text[:idx])
		if first == "" {
			first = "[multiline]"
		}
		text = first + " ..."
	}
	if maxChars <= 0 {
		return text
	}
	return truncateRunesWithMarker(text, maxChars, "...")
}

// strconvQuote is a thin alias over strconv.Quote so call sites read
// in the agent-loop file's local vocabulary. The previous hand-rolled
// version only escaped backslash and double quote, which produced
// broken JSON / TUI-line previews for any tool param containing
// newlines, tabs, or other control characters (the model often emits
// multi-line param values for write_file / edit_file). strconv.Quote
// handles every C-style escape plus all `< 0x20` control bytes.
func strconvQuote(s string) string {
	return strconv.Quote(s)
}
