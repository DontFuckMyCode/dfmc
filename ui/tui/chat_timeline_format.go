package tui

// chat_timeline_format.go — pure string formatting helpers used by the
// timeline / chip / chat-event surface. Stateless (no Model receivers,
// no package-level state).
//
// What stays here:
//
//   1. Tool-name labels: displayToolName, isMetaToolName,
//      metaToolCallTarget, batchToolCallNameSummary.
//   2. Transcript-row helpers (used by chatEventTranscriptText):
//      isBatchToolEvent, toolEventLineLimit, toolEventStateLine,
//      toolEventActionVerb, chatEventTranscriptStatusLabel,
//      chatEventToolNameMatches, toolDetailDuplicatesParams.
//   3. Tool/chat-event keys + small numeric formatters: toolNameKey,
//      toolChatEventKey, elapsedLabel, compactMetric.
//
// Companion siblings (extracted to keep this file scannable):
//
//   - chat_timeline_format_fields.go field sanitisation/truncation +
//                                    payload accessor helpers
//   - chat_timeline_format_detail.go tool_call / tool_result / context
//                                    "Detail" line builders
//
// Builder functions that turn payloads into multi-line cards live in
// chat_timeline_builder.go; the Model-receiver methods that own the
// transcript-update side-effects live in chat_event_timeline.go.

import (
	"fmt"
	"strings"
	"time"
)

// --- tool-name labels ------------------------------------------------------

func displayToolName(toolName string, payload map[string]any) string {
	canonical := strings.TrimSpace(toolName)
	switch strings.ToLower(canonical) {
	case "tool_call":
		if target := metaToolCallTarget(payload); target != "" {
			return target
		}
	case "tool_batch_call":
		if summary := batchToolCallNameSummary(payload); summary != "" {
			return "batch " + summary
		}
		return "batch"
	}
	return canonical
}

func isMetaToolName(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "tool_call", "tool_batch_call":
		return true
	default:
		return false
	}
}

func metaToolCallTarget(payload map[string]any) string {
	params, _ := payload["params"].(map[string]any)
	if params == nil {
		return ""
	}
	name := strings.TrimSpace(fmt.Sprint(params["name"]))
	if name == "" {
		name = strings.TrimSpace(fmt.Sprint(params["tool"]))
	}
	return name
}

func batchToolCallNameSummary(payload map[string]any) string {
	calls := batchToolCallsFromPayload(payload)
	if len(calls) == 0 {
		return ""
	}
	counts := map[string]int{}
	order := make([]string, 0, len(calls))
	for _, raw := range calls {
		call, _ := raw.(map[string]any)
		name := strings.TrimSpace(fmt.Sprint(call["name"]))
		if name == "" {
			name = strings.TrimSpace(fmt.Sprint(call["tool"]))
		}
		if name == "" {
			name = "tool"
		}
		if _, seen := counts[name]; !seen {
			order = append(order, name)
		}
		counts[name]++
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		if count := counts[name]; count > 1 {
			parts = append(parts, fmt.Sprintf("%s x%d", name, count))
		} else {
			parts = append(parts, name)
		}
	}
	return fmt.Sprintf("[%d: %s]", len(calls), strings.Join(parts, ", "))
}

// --- transcript-row helpers (used by chatEventTranscriptText) -------------

func isBatchToolEvent(ev chatEventLine) bool {
	return strings.EqualFold(strings.TrimSpace(ev.ToolName), "tool_batch_call") ||
		strings.EqualFold(strings.TrimSpace(ev.Title), "tool_batch_call")
}

func toolEventLineLimit(ev chatEventLine) int {
	name := strings.ToLower(strings.TrimSpace(ev.ToolName))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(ev.Title))
	}
	switch name {
	case "write_file":
		return 13
	case "edit_file", "apply_patch":
		return 13
	default:
		return 8
	}
}

func toolEventStateLine(ev chatEventLine, status string) string {
	name := strings.ToLower(strings.TrimSpace(ev.ToolName))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(ev.Title))
	}
	switch status {
	case "running":
		action := toolEventActionVerb(name)
		if action == "" {
			action = "dispatching"
		}
		return "state: " + action + " -> waiting for result"
	case "done":
		if ev.Duration > 0 {
			return fmt.Sprintf("state: completed in %dms", ev.Duration)
		}
		return "state: completed"
	case "failed":
		if ev.Duration > 0 {
			return fmt.Sprintf("state: failed after %dms", ev.Duration)
		}
		return "state: failed"
	case "warn":
		return "state: warning"
	default:
		return ""
	}
}

func toolEventActionVerb(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_file", "list_dir", "glob":
		return "reading"
	case "grep_codebase", "semantic_search", "ast_query":
		return "searching"
	case "run_command":
		return "running command"
	case "write_file":
		return "writing file"
	case "edit_file":
		return "editing file"
	case "apply_patch":
		return "applying patch"
	case "tool_batch_call":
		return "dispatching batch"
	default:
		return ""
	}
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

func chatEventToolNameMatches(ev chatEventLine, toolName string) bool {
	return strings.EqualFold(strings.TrimSpace(ev.Title), toolName) ||
		strings.EqualFold(strings.TrimSpace(ev.ToolName), toolName)
}

func toolDetailDuplicatesParams(detail, params string) bool {
	detail = strings.TrimSpace(detail)
	params = strings.TrimSpace(params)
	return detail != "" && params != "" && strings.Contains(detail, params)
}

// --- tool/chat-event key + small numeric formatters ----------------------

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
