package tui

// tool_status_panel.go — Ctrl+Alt+T overlay panel: scrollable, detailed
// tool-call history. Every tool:call/result/error/denied event feeds a
// rolling buffer (toolCallLog) that this panel renders as a timeline.
// The chat transcript gets single-line summaries; full params, results,
// errors, reasoning, and timing live here.

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
)

func (m Model) renderToolStatusView(width int) string {
	width = clampInt(width, 24, 1000)
	entries := m.toolCallLog.entries
	if len(entries) == 0 {
		return sectionHeader("T", "Tool Call History") + "\n" +
			subtleStyle.Render("No tool calls yet.") + "\n" +
			subtleStyle.Render("Events appear here as the agent runs tools.")
	}

	// Render newest-first
	bodyLines := []string{}
	totalOk, totalFail, totalRunning := 0, 0, 0
	var totalMs int64
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		switch e.Status {
		case "ok":
			totalOk++
		case "failed", "denied", "timeout":
			totalFail++
		case "running":
			totalRunning++
		}
		totalMs += int64(e.DurationMs)
		bodyLines = append(bodyLines, m.renderToolLogEntry(e, width-4)...)
	}

	// Header
	parts := []string{fmt.Sprintf("%d calls", len(entries))}
	if totalOk > 0 {
		parts = append(parts, fmt.Sprintf("%d ok", totalOk))
	}
	if totalFail > 0 {
		parts = append(parts, fmt.Sprintf("%d fail", totalFail))
	}
	if totalRunning > 0 {
		parts = append(parts, fmt.Sprintf("%d running", totalRunning))
	}
	if totalMs > 0 {
		parts = append(parts, fmt.Sprintf("%dms total", totalMs))
	}

	header := sectionHeader("T", "Tool Call History") + "\n" +
		subtleStyle.Render(strings.Join(parts, " · ")) + "\n" +
		renderDivider(width-2)

	// Scroll window
	scroll := m.toolStatus.scroll
	if scroll < 0 {
		scroll = 0
	}
	if scroll >= len(bodyLines) {
		scroll = len(bodyLines) - 1
	}
	if scroll < 0 {
		scroll = 0
	}

	// Show visible window
	visible := bodyLines
	if scroll > 0 {
		if scroll < len(visible) {
			visible = visible[scroll:]
		} else {
			visible = nil
		}
	}

	footer := subtleStyle.Render("j/k scroll · G top · g bottom · esc close · ctrl+alt+t toggle")

	return header + "\n" + strings.Join(visible, "\n") + "\n" + footer
}

func (m Model) renderToolLogEntry(e toolCallLogEntry, width int) []string {
	icon, statusStyle := theme.ChipIconStyle(e.Status)
	name := e.ToolName
	if name == "" {
		name = "tool"
	}
	age := ""
	if !e.StartedAt.IsZero() {
		age = " " + subtleStyle.Render(timeAgo(e.StartedAt))
	}

	// Line 1: icon · status · name · duration · age
	head := statusStyle.Render(icon+" "+name) + age
	if e.DurationMs > 0 {
		head += " " + subtleStyle.Render(theme.FormatDurationShort(e.DurationMs))
	}
	if e.Step > 0 {
		head += " " + subtleStyle.Render(fmt.Sprintf("#%d", e.Step))
	}

	lines := []string{truncateSingleLine(head, width)}

	// Reason (model's self-narration)
	if e.Reason != "" {
		lines = append(lines, "  "+accentStyle.Italic(true).Render(truncateSingleLine("💭 "+e.Reason, width-2)))
	}

	// Params
	if e.Params != "" {
		lines = append(lines, "  "+infoStyle.Render(truncateSingleLine("$ "+e.Params, width-2)))
	}

	// Result or Error
	if e.Status == "failed" || e.Status == "denied" || e.Status == "timeout" {
		errText := e.Error
		if errText == "" {
			errText = e.Status
		}
		lines = append(lines, "  "+failStyle.Render(truncateSingleLine("× "+errText, width-2)))
	} else if e.Result != "" {
		// Show first line of result
		firstLine := e.Result
		if idx := strings.Index(e.Result, "\n"); idx >= 0 {
			firstLine = e.Result[:idx]
		}
		lines = append(lines, "  "+okStyle.Render(truncateSingleLine("» "+firstLine, width-2)))
	}

	// Batch info
	if e.IsBatch && e.BatchTotal > 0 {
		batchLine := fmt.Sprintf("%d calls: %d ok", e.BatchTotal, e.BatchOK)
		if e.BatchFail > 0 {
			batchLine += fmt.Sprintf(", %d fail", e.BatchFail)
		}
		lines = append(lines, "  "+subtleStyle.Render(truncateSingleLine(batchLine, width-2)))
	}

	// Tokens
	if e.Tokens > 0 {
		lines = append(lines, "  "+subtleStyle.Render(fmt.Sprintf("%s tok", theme.FormatToolTokenCount(e.Tokens))))
	}

	lines = append(lines, subtleStyle.Render(strings.Repeat("─", min(width, 60))))
	return lines
}

func timeAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < time.Second {
		return "now"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh ago", int(d.Hours()))
}

// handleToolStatusKey processes j/k/g/G/pgup/pgdn for the tool status
// overlay panel. Returns (model, handled).
func (m Model) handleToolStatusKey(key string) (Model, bool) {
	total := len(m.toolCallLog.entries) * 5 // rough line estimate
	switch key {
	case "j", "down":
		if m.toolStatus.scroll < total-1 {
			m.toolStatus.scroll++
		}
		return m, true
	case "k", "up":
		if m.toolStatus.scroll > 0 {
			m.toolStatus.scroll--
		}
		return m, true
	case "g":
		m.toolStatus.scroll = 0
		return m, true
	case "G":
		m.toolStatus.scroll = total - 1
		if m.toolStatus.scroll < 0 {
			m.toolStatus.scroll = 0
		}
		return m, true
	case "pgdown":
		m.toolStatus.scroll += 20
		if m.toolStatus.scroll >= total {
			m.toolStatus.scroll = total - 1
		}
		if m.toolStatus.scroll < 0 {
			m.toolStatus.scroll = 0
		}
		return m, true
	case "pgup":
		m.toolStatus.scroll -= 20
		if m.toolStatus.scroll < 0 {
			m.toolStatus.scroll = 0
		}
		return m, true
	}
	return m, false
}

// handleToolStatusOverlayKey wraps the panel key handler into the
// bubbletea (Model, Cmd) contract expected by update_keypress.go.
func (m Model) handleToolStatusOverlayKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	// Standard overlay close
	if key == "esc" || key == "q" {
		if nm, closed := m.closePanelOverlay(); closed {
			return nm, nil
		}
	}
	if nm, handled := m.handleToolStatusKey(key); handled {
		return nm, nil
	}
	return m, nil
}
