package theme

// stats_panel_helpers.go — small pure helpers used by stats_panel.go's
// builder methods. Sibling split so the panel renderer file stays
// focused on layout (header, sections, footer composition + the
// builder receiver), while this file owns the per-cell formatters,
// state-string pickers, and the context-window arithmetic.
//
// Most helpers operate on StatsPanelInfo values from theme/types.go;
// they're free functions so the runtime sub-renderers in
// stats_panel_runtime.go can call into them too without going through
// the builder's privately-held state.

import (
	"fmt"
	"strings"
	"time"
)

func panelSectionTitle(title string) string {
	title = strings.ToUpper(strings.TrimSpace(title))
	if title == "" {
		title = "INFO"
	}
	return TitleStyle.Render(" " + title + " ")
}

func cleanPanelRows(rows []string) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		row = strings.TrimSpace(row)
		if row != "" {
			out = append(out, row)
		}
	}
	return out
}

func statsPanelStateLine(info StatsPanelInfo, width int) string {
	state := "ready"
	style := OkStyle
	switch {
	case info.Streaming:
		state = SpinnerFrame(info.SpinnerFrame) + " streaming"
		style = InfoStyle
	case info.AgentActive:
		state = SpinnerFrame(info.SpinnerFrame) + " working"
		style = AccentStyle
	case info.Parked:
		state = "parked"
		style = WarnStyle
	case !info.Configured && strings.TrimSpace(info.Provider) != "":
		state = "needs key"
		style = WarnStyle
	case strings.TrimSpace(info.Provider) == "":
		state = "needs provider"
		style = FailStyle
	}
	left := style.Bold(true).Render(state)
	right := statsPanelContextLabel(statsPanelContextUsed(info), info.MaxContext)
	if info.MessageCount > 0 {
		right += fmt.Sprintf(" | %d msgs", info.MessageCount)
	}
	return TruncateSingleLine(left+"  "+SubtleStyle.Render(right), width)
}

func statsPanelContextLabel(tokens, maxTokens int) string {
	if maxTokens <= 0 {
		return "ctx " + CompactTokens(tokens)
	}
	pct := 0
	if tokens > 0 {
		pct = int((int64(tokens) * 100) / int64(maxTokens))
	}
	return fmt.Sprintf("ctx %s/%s %d%%", CompactTokens(tokens), CompactTokens(maxTokens), pct)
}

func contextReasonContains(reasons []string, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	for _, reason := range reasons {
		if strings.Contains(strings.ToLower(reason), needle) {
			return true
		}
	}
	return false
}

func statsPanelWindowUsage(info StatsPanelInfo) (int, int) {
	used := info.ContextWindowTokens
	if used <= 0 {
		used = info.ContextSystemTokens + info.ContextHistoryTokens + info.ContextTokens + info.ContextResponseTokens + info.ContextToolTokens
	}
	if used <= 0 {
		used = info.ContextTokens
	}
	if used <= 0 {
		return 0, 0
	}
	if info.MaxContext <= 0 {
		return used, -1
	}
	return used, info.MaxContext - used
}

func statsPanelContextUsed(info StatsPanelInfo) int {
	if used, _ := statsPanelWindowUsage(info); used > 0 {
		return used
	}
	return info.ContextTokens
}

func contextUsagePct(tokens, maxTokens int) int {
	if tokens <= 0 || maxTokens <= 0 {
		return 0
	}
	return int((int64(tokens) * 100) / int64(maxTokens))
}

func statsPanelFooterRows(mode StatsPanelMode) []string {
	return []string{statsPanelModeActionHint(mode), "ctrl+s hide | ctrl+h keys"}
}

func statsPanelModeActionHint(mode StatsPanelMode) string {
	switch mode {
	case StatsPanelModeTodos:
		return "/todos | /split task | /drive task"
	case StatsPanelModeTasks:
		return "/tasks tree | ctrl+y Plans | F5 Workflow"
	case StatsPanelModeSubagents:
		return "/subagents | F7 Activity | /drive active"
	case StatsPanelModeProviders:
		return "/provider | /model | /reload"
	default:
		return "alt+a/s/d/f/p switch | F7 Activity"
	}
}

func firstNNonEmpty(items []string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	out := make([]string, 0, min(len(items), limit))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
		if len(out) == limit {
			break
		}
	}
	return out
}

func formatSessionDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	s := int(d.Seconds())
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	s = s % 60
	if m < 60 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	h := m / 60
	m = m % 60
	return fmt.Sprintf("%dh %dm", h, m)
}
