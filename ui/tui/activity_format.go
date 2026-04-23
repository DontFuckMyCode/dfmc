// Activity payload formatting + UI mode helpers: how engine events
// turn into human-readable detail lines, which payload keys get
// promoted, the kind→icon / mode→label lookups, and the match
// predicates that drive filtering. Extracted from activity.go —
// pure leaf functions with no Model dependency.

package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func buildActivityDetailLines(ev engine.Event, summary string) []string {
	lines := []string{
		"summary: " + truncateSingleLine(summary, 160),
		"type: " + blankFallback(strings.TrimSpace(ev.Type), "(unknown)"),
	}
	if source := strings.TrimSpace(ev.Source); source != "" {
		lines = append(lines, "source: "+source)
	}
	if !ev.Timestamp.IsZero() {
		lines = append(lines, "event time: "+ev.Timestamp.Format(time.RFC3339))
	}
	lines = append(lines, summarizeActivityPayload(ev.Payload)...)
	return lines
}

func summarizeActivityPayload(payload any) []string {
	switch v := payload.(type) {
	case nil:
		return nil
	case string:
		if text := strings.TrimSpace(v); text != "" {
			return []string{"payload: " + truncateSingleLine(text, 160)}
		}
	case map[string]any:
		if len(v) == 0 {
			return nil
		}
		return summarizeActivityPayloadMap(v)
	default:
		text := strings.TrimSpace(fmt.Sprint(v))
		if text != "" && text != "<nil>" {
			return []string{"payload: " + truncateSingleLine(text, 160)}
		}
	}
	return nil
}

func summarizeActivityPayloadMap(payload map[string]any) []string {
	if len(payload) == 0 {
		return nil
	}
	keys := orderedActivityPayloadKeys(payload)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(fmt.Sprint(payload[key]))
		if value == "" || value == "<nil>" {
			continue
		}
		parts = append(parts, key+"="+truncateSingleLine(value, 42))
	}
	if len(parts) == 0 {
		return nil
	}
	lines := make([]string, 0, (len(parts)+2)/3)
	for i := 0; i < len(parts); i += 3 {
		end := i + 3
		if end > len(parts) {
			end = len(parts)
		}
		lines = append(lines, "payload: "+strings.Join(parts[i:end], " · "))
	}
	return lines
}

func extractActivityPath(ev engine.Event) string {
	payload, _ := toStringAnyMap(ev.Payload)
	if len(payload) == 0 {
		return ""
	}
	for _, key := range []string{"path", "file", "filepath", "rel", "target", "config_path"} {
		if path := strings.TrimSpace(payloadString(payload, key, "")); path != "" {
			return path
		}
	}
	return ""
}

func extractActivityProvider(ev engine.Event) string {
	payload, _ := toStringAnyMap(ev.Payload)
	if len(payload) == 0 {
		return ""
	}
	return strings.TrimSpace(payloadString(payload, "provider", ""))
}

func extractActivityQuery(ev engine.Event, summary string) string {
	payload, _ := toStringAnyMap(ev.Payload)
	for _, key := range []string{"query", "task", "title", "description", "prompt", "goal", "reason", "error"} {
		if text := strings.TrimSpace(payloadString(payload, key, "")); text != "" {
			return truncateSingleLine(text, 200)
		}
	}
	_ = summary
	return ""
}

func orderedActivityPayloadKeys(payload map[string]any) []string {
	preferred := []string{
		"tool", "provider", "model", "reason", "error", "status", "success",
		"run_id", "todo_id", "title", "step", "attempt", "duration_ms",
		"durationMs", "wait_ms", "files", "subtask_count", "scope", "parallel",
		"before_tokens", "after_tokens", "tokens_before", "tokens_after", "path",
	}
	seen := map[string]struct{}{}
	keys := make([]string, 0, len(payload))
	for _, key := range preferred {
		if _, ok := payload[key]; ok {
			seen[key] = struct{}{}
			keys = append(keys, key)
		}
	}
	extra := make([]string, 0, len(payload)-len(keys))
	for key := range payload {
		if _, ok := seen[key]; ok {
			continue
		}
		extra = append(extra, key)
	}
	sort.Strings(extra)
	keys = append(keys, extra...)
	return keys
}

func truncateActivityText(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func payloadIntAny(data map[string]any, fallback int, keys ...string) int {
	if data == nil {
		return fallback
	}
	for _, key := range keys {
		if _, ok := data[key]; !ok {
			continue
		}
		return payloadInt(data, key, fallback)
	}
	return fallback
}

func kindIcon(kind activityKind) string {
	switch kind {
	case activityKindError:
		return warnStyle.Render("!")
	case activityKindTool:
		return accentStyle.Render("*")
	case activityKindAgent:
		return accentStyle.Render("@")
	case activityKindStream:
		return infoStyle.Render(">")
	case activityKindCtx:
		return infoStyle.Render("#")
	case activityKindIndex:
		return subtleStyle.Render("=")
	default:
		return subtleStyle.Render(".")
	}
}

func activityModeLabel(mode activityViewMode) string {
	switch mode {
	case activityViewTools:
		return "tools"
	case activityViewAgents:
		return "agents"
	case activityViewErrors:
		return "errors"
	case activityViewWorkflow:
		return "workflow"
	case activityViewContext:
		return "context"
	default:
		return "all"
	}
}

func nextActivityMode(current activityViewMode) activityViewMode {
	for i, mode := range activityViewModes {
		if mode == current {
			return activityViewModes[(i+1)%len(activityViewModes)]
		}
	}
	return activityViewAll
}

func activityModeShortcut(mode activityViewMode) string {
	switch mode {
	case activityViewAll:
		return "1"
	case activityViewTools:
		return "2"
	case activityViewAgents:
		return "3"
	case activityViewErrors:
		return "4"
	case activityViewWorkflow:
		return "5"
	case activityViewContext:
		return "6"
	default:
		return "1"
	}
}

func activityMatchesMode(entry activityEntry, mode activityViewMode) bool {
	eventID := strings.ToLower(strings.TrimSpace(entry.EventID))
	switch mode {
	case activityViewTools:
		return entry.Kind == activityKindTool
	case activityViewAgents:
		return entry.Kind == activityKindAgent
	case activityViewErrors:
		return entry.Kind == activityKindError
	case activityViewWorkflow:
		return strings.HasPrefix(eventID, "drive:") ||
			strings.HasPrefix(eventID, "agent:autonomy:") ||
			strings.HasPrefix(eventID, "agent:subagent:") ||
			strings.HasPrefix(eventID, "provider:race:") ||
			strings.HasPrefix(eventID, "provider:throttle:")
	case activityViewContext:
		return entry.Kind == activityKindCtx || entry.Kind == activityKindIndex
	default:
		return true
	}
}

func activityMatchesQuery(entry activityEntry, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	if strings.Contains(strings.ToLower(entry.Text), query) ||
		strings.Contains(strings.ToLower(entry.EventID), query) ||
		strings.Contains(strings.ToLower(entry.Source), query) {
		return true
	}
	for _, line := range entry.Details {
		if strings.Contains(strings.ToLower(line), query) {
			return true
		}
	}
	return false
}
