package tui

import "strings"

type statsTodoSummary struct {
	Total       int
	Pending     int
	Doing       int
	Done        int
	Active      string
	ActiveIndex int
	Lines       []string
}

func (m Model) liveStreamTokenCounts() (inputTokens int, outputTokens int) {
	if !m.chat.sending {
		return 0, 0
	}
	inputTokens = m.chat.streamInputTokens
	if m.chat.streamIndex >= 0 && m.chat.streamIndex < len(m.chat.transcript) {
		line := m.chat.transcript[m.chat.streamIndex]
		outputTokens = line.TokenCount
		if outputTokens <= 0 && strings.TrimSpace(line.Content) != "" {
			outputTokens = estimatedChatTokens(line.Content)
		}
	}
	return inputTokens, outputTokens
}

func (m Model) statsTodoSummary() statsTodoSummary {
	if m.eng == nil || m.eng.Tools == nil {
		return statsTodoSummary{}
	}
	todos := m.eng.Tools.TodoSnapshot()
	summary := statsTodoSummary{Lines: formatWorkflowTodoLines(todos, 8)}
	for idx, it := range todos {
		summary.Total++
		switch strings.ToLower(strings.TrimSpace(it.Status)) {
		case "completed", "done":
			summary.Done++
		case "in_progress", "active", "doing":
			summary.Doing++
			if summary.Active == "" {
				summary.Active = strings.TrimSpace(it.ActiveForm)
				if summary.Active == "" {
					summary.Active = strings.TrimSpace(it.Content)
				}
				summary.ActiveIndex = idx + 1
			}
		case "pending", "blocked", "skipped", "waiting", "verifying", "external_review":
			summary.Pending++
		}
	}
	return summary
}
