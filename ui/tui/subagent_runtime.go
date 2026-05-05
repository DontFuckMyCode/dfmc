package tui

import (
	"fmt"
	"strings"
	"time"
)

const maxSubagentRuntimeItems = 12

func (m *Model) ensureSubagentRuntimeState() {
	if m.telemetry.subagents == nil {
		m.telemetry.subagents = map[string]subagentRuntimeItem{}
	}
}

func subagentRuntimeKey(role, task string) string {
	role = strings.TrimSpace(role)
	task = strings.TrimSpace(task)
	if role == "" {
		role = "subagent"
	}
	if task == "" {
		task = "task"
	}
	return strings.ToLower(role) + "|" + strings.ToLower(task)
}

func (m *Model) startSubagentRuntime(payload map[string]any, now time.Time) subagentRuntimeItem {
	m.ensureSubagentRuntimeState()
	task := payloadString(payload, "task", "task")
	role := payloadString(payload, "role", "")
	key := subagentRuntimeKey(role, task)
	item := subagentRuntimeItem{
		Key:        key,
		Task:       task,
		Role:       role,
		Status:     "running",
		Provider:   payloadString(payload, "provider", ""),
		Model:      payloadString(payload, "model", ""),
		Candidates: payloadStringSlice(payload, "provider_candidates"),
		StartedAt:  now,
		UpdatedAt:  now,
	}
	m.telemetry.subagents[key] = item
	m.bumpSubagentRuntimeOrder(key)
	return item
}

func (m *Model) fallbackSubagentRuntime(payload map[string]any, now time.Time) subagentRuntimeItem {
	m.ensureSubagentRuntimeState()
	key := m.findSubagentRuntimeKey(payload)
	if key == "" {
		key = subagentRuntimeKey(payloadString(payload, "role", ""), payloadString(payload, "task", "task"))
	}
	item := m.telemetry.subagents[key]
	item.Key = key
	if item.StartedAt.IsZero() {
		item.StartedAt = now
	}
	if item.Task == "" {
		item.Task = payloadString(payload, "task", "task")
	}
	if item.Role == "" {
		item.Role = payloadString(payload, "role", "")
	}
	item.Status = "fallback"
	item.Attempt = payloadInt(payload, "attempt", 0)
	item.Provider = payloadString(payload, "to_provider", item.Provider)
	item.Model = payloadString(payload, "to_model", item.Model)
	item.Candidates = nonEmptyStrings(payloadStringSlice(payload, "provider_candidates"), item.Candidates)
	item.LastReason = payloadString(payload, "error", "")
	if item.LastReason == "" {
		reasons := payloadStringSlice(payload, "fallback_reasons")
		if len(reasons) > 0 {
			item.LastReason = reasons[len(reasons)-1]
		}
	}
	item.UpdatedAt = now
	m.telemetry.subagents[key] = item
	m.bumpSubagentRuntimeOrder(key)
	return item
}

func (m *Model) finishSubagentRuntime(payload map[string]any, now time.Time) subagentRuntimeItem {
	m.ensureSubagentRuntimeState()
	key := m.findSubagentRuntimeKey(payload)
	if key == "" {
		key = subagentRuntimeKey(payloadString(payload, "role", ""), payloadString(payload, "task", "task"))
	}
	item := m.telemetry.subagents[key]
	item.Key = key
	if item.StartedAt.IsZero() {
		item.StartedAt = now
	}
	if item.Task == "" {
		item.Task = payloadString(payload, "task", "task")
	}
	if item.Role == "" {
		item.Role = payloadString(payload, "role", "")
	}
	item.Provider = payloadString(payload, "provider", item.Provider)
	item.Model = payloadString(payload, "model", item.Model)
	item.Candidates = nonEmptyStrings(payloadStringSlice(payload, "provider_candidates"), item.Candidates)
	item.Tried = nonEmptyStrings(payloadStringSlice(payload, "profiles_tried"), item.Tried)
	item.Attempts = payloadInt(payload, "attempts", item.Attempts)
	item.Rounds = payloadInt(payload, "tool_rounds", item.Rounds)
	item.DurationMs = payloadInt(payload, "duration_ms", item.DurationMs)
	item.Fallback = payloadBool(payload, "fallback_used", item.Fallback)
	item.Error = payloadString(payload, "err", "")
	if item.Error != "" {
		item.Status = "failed"
	} else if payloadBool(payload, "parked", false) {
		item.Status = "parked"
	} else {
		item.Status = "done"
	}
	item.UpdatedAt = now
	m.telemetry.subagents[key] = item
	m.bumpSubagentRuntimeOrder(key)
	return item
}

func (m *Model) findSubagentRuntimeKey(payload map[string]any) string {
	task := payloadString(payload, "task", "")
	role := payloadString(payload, "role", "")
	if task != "" {
		key := subagentRuntimeKey(role, task)
		if _, ok := m.telemetry.subagents[key]; ok {
			return key
		}
	}
	for i := len(m.telemetry.subagentOrder) - 1; i >= 0; i-- {
		key := m.telemetry.subagentOrder[i]
		item, ok := m.telemetry.subagents[key]
		if !ok {
			continue
		}
		if role != "" && !strings.EqualFold(strings.TrimSpace(item.Role), role) {
			continue
		}
		switch item.Status {
		case "running", "fallback":
			return key
		}
	}
	return ""
}

func (m *Model) bumpSubagentRuntimeOrder(key string) {
	if key == "" {
		return
	}
	out := make([]string, 0, len(m.telemetry.subagentOrder)+1)
	out = append(out, key)
	for _, existing := range m.telemetry.subagentOrder {
		if existing != key {
			out = append(out, existing)
		}
	}
	if len(out) > maxSubagentRuntimeItems {
		for _, drop := range out[maxSubagentRuntimeItems:] {
			delete(m.telemetry.subagents, drop)
		}
		out = out[:maxSubagentRuntimeItems]
	}
	m.telemetry.subagentOrder = out
}

func nonEmptyStrings(primary, fallback []string) []string {
	if len(primary) > 0 {
		return append([]string(nil), primary...)
	}
	return append([]string(nil), fallback...)
}

func (m Model) activeSubagentSummary() string {
	for _, key := range m.telemetry.subagentOrder {
		item, ok := m.telemetry.subagents[key]
		if !ok || (item.Status != "running" && item.Status != "fallback") {
			continue
		}
		who := strings.Trim(strings.TrimSpace(item.Provider+"/"+item.Model), "/")
		if who == "" && len(item.Candidates) > 0 {
			who = item.Candidates[0]
		}
		if who == "" {
			who = strings.TrimSpace(item.Role)
		}
		if who == "" {
			who = "active"
		}
		return "(" + truncateSingleLine(who, 28) + ")"
	}
	return ""
}

func (m Model) subagentTreeLines(now time.Time, limit int) []string {
	if limit <= 0 {
		limit = 10
	}
	lines := make([]string, 0, limit)
	for _, key := range m.telemetry.subagentOrder {
		item, ok := m.telemetry.subagents[key]
		if !ok {
			continue
		}
		lines = append(lines, formatSubagentRuntimeLine(item, now))
		for _, child := range formatSubagentRuntimeChildren(item) {
			lines = append(lines, child)
			if len(lines) >= limit {
				return lines
			}
		}
		if len(lines) >= limit {
			return lines
		}
	}
	return lines
}

func formatSubagentRuntimeLine(item subagentRuntimeItem, now time.Time) string {
	status := strings.TrimSpace(item.Status)
	if status == "" {
		status = "running"
	}
	role := strings.TrimSpace(item.Role)
	if role == "" {
		role = "worker"
	}
	task := truncateSingleLine(strings.TrimSpace(item.Task), 42)
	if task == "" {
		task = "task"
	}
	age := ""
	if !item.StartedAt.IsZero() {
		if item.Status == "running" || item.Status == "fallback" {
			age = " " + formatSessionDuration(now.Sub(item.StartedAt))
		} else if item.DurationMs > 0 {
			age = fmt.Sprintf(" %dms", item.DurationMs)
		}
	}
	return fmt.Sprintf("%s %s - %s%s", subagentStatusGlyph(status), role, task, age)
}

func formatSubagentRuntimeChildren(item subagentRuntimeItem) []string {
	children := []string{}
	route := strings.Trim(strings.TrimSpace(item.Provider+"/"+item.Model), "/")
	if route != "" {
		children = append(children, "+- route "+route)
	}
	if len(item.Candidates) > 0 {
		children = append(children, "+- candidates "+strings.Join(item.Candidates, " > "))
	}
	if len(item.Tried) > 0 {
		children = append(children, "+- tried "+strings.Join(item.Tried, " > "))
	}
	if item.Attempt > 0 {
		children = append(children, fmt.Sprintf("+- fallback attempt %d", item.Attempt))
	}
	if item.Attempts > 1 || item.Rounds > 0 || item.Fallback {
		parts := []string{}
		if item.Attempts > 0 {
			parts = append(parts, fmt.Sprintf("%d attempts", item.Attempts))
		}
		if item.Rounds > 0 {
			parts = append(parts, fmt.Sprintf("%d rounds", item.Rounds))
		}
		if item.Fallback {
			parts = append(parts, "fallback used")
		}
		children = append(children, "+- "+strings.Join(parts, " | "))
	}
	if reason := strings.TrimSpace(item.LastReason); reason != "" {
		children = append(children, "+- reason "+truncateSingleLine(reason, 64))
	}
	if errText := strings.TrimSpace(item.Error); errText != "" {
		children = append(children, "+- error "+truncateSingleLine(errText, 64))
	}
	return children
}

func subagentStatusGlyph(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running":
		return "[running]"
	case "fallback":
		return "[fallback]"
	case "failed":
		return "[failed]"
	case "parked":
		return "[parked]"
	case "done":
		return "[done]"
	default:
		return "[subagent]"
	}
}
