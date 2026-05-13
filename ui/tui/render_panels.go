package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/session"
)

func (m Model) renderStatusView(width int) string {
	return m.renderStatusViewV2(width)
}

func (m Model) renderFilesView(width int) string {
	return m.renderFilesViewV2(width, 24)
}

func (m Model) renderFilesViewSized(width, height int) string {
	return m.renderFilesViewV2(width, height)
}

func (m Model) renderToolsView(width int) string {
	return m.renderToolsViewV2(width)
}

func (m Model) renderFooter(width int) string {
	maxWidth := max(width-4, 16)

	tab := m.tabs[m.activeTab]
	segments := []string{titleStyle.Render(" " + tab + " ")}
	segments = append(segments, m.footerSegments()...)
	if pinned := strings.TrimSpace(m.filesView.pinned); pinned != "" {
		segments = append(segments, accentStyle.Render("◆ "+truncateSingleLine(pinned, 22)))
	}
	if note := strings.TrimSpace(m.notice); note != "" {
		segments = append(segments, subtleStyle.Render("· ")+truncateSingleLine(note, 80))
	}
	sep := subtleStyle.Render("  ·  ")
	return truncateSingleLine(strings.Join(segments, sep), maxWidth)
}

func (m Model) footerSegments() []string {
	out := []string{}
	tokens, maxCtx := 0, 0
	if m.status.ContextIn != nil {
		tokens = m.status.ContextIn.TokenCount
		maxCtx = m.status.ContextIn.ProviderMaxContext
	}
	if maxCtx == 0 {
		maxCtx = m.status.ProviderProfile.MaxContext
	}
	if live := m.liveContextSnapshot(); live.ok {
		if live.windowTokens > 0 {
			tokens = live.windowTokens
		}
		if live.maxContext > 0 {
			maxCtx = live.maxContext
		}
	}
	out = append(out, renderContextBar(tokens, maxCtx, 10))
	if runtime := strings.TrimSpace(m.footerRuntimeSegment()); runtime != "" {
		out = append(out, runtime)
	}
	// Session agents segment — shows when there are multiple agents.
	if m.session != nil && m.session.AgentCount() > 1 {
		tree := m.session.AgentTree()
		running := 0
		waiting := 0
		for _, n := range tree {
			if n.Status == session.StatusRunning {
				running++
			} else if n.Status == session.StatusWaitingUserInput {
				waiting++
			}
		}
		label := fmt.Sprintf("agents:%d", m.session.AgentCount())
		if running > 0 {
			label += fmt.Sprintf(" · %d running", running)
		}
		if waiting > 0 {
			label += accentStyle.Render(" · "+fmt.Sprintf("%d needs input", waiting))
		}
		out = append(out, label)
	}

	info := m.gitInfo
	if strings.TrimSpace(info.Branch) != "" {
		label := info.Branch
		if info.Detached {
			label = "(" + label + ")"
		}
		chip := accentStyle.Render("⎇ ") + boldStyle.Render(label)
		if info.Dirty {
			chip += warnStyle.Render("*")
		}
		out = append(out, chip)
	}
	if info.Inserted > 0 || info.Deleted > 0 {
		churn := okStyle.Render(fmt.Sprintf("+%d", info.Inserted)) +
			subtleStyle.Render(",") +
			failStyle.Render(fmt.Sprintf("-%d", info.Deleted))
		out = append(out, churn)
	}
	if !m.sessionStart.IsZero() {
		out = append(out, subtleStyle.Render("⏱ ")+boldStyle.Render(formatSessionDuration(time.Since(m.sessionStart))))
	}
	return out
}

func (m Model) footerRuntimeSegment() string {
	info := m.statsPanelInfo()
	parts := []string{}
	switch {
	case info.AgentActive:
		phase := strings.TrimSpace(info.AgentPhase)
		if phase == "" {
			phase = "working"
		}
		label := spinnerFrame(m.chat.spinnerFrame) + " " + humanizeAgentPhase(phase)
		if info.AgentMaxSteps > 0 {
			label += fmt.Sprintf(" %d/%d", max(info.AgentStep, 1), info.AgentMaxSteps)
		} else if info.AgentStep > 0 {
			label += fmt.Sprintf(" step %d", info.AgentStep)
		}
		parts = append(parts, accentStyle.Render(label))
	case info.Streaming:
		parts = append(parts, infoStyle.Render(spinnerFrame(m.chat.spinnerFrame)+" streaming"))
	case info.Parked:
		parts = append(parts, warnStyle.Render("parked"))
	}
	if info.ActiveTools > 0 {
		parts = append(parts, infoStyle.Render(fmt.Sprintf("tools %d", info.ActiveTools)))
	}
	if info.ActiveSubagents > 0 {
		parts = append(parts, accentStyle.Render(fmt.Sprintf("agents %d", info.ActiveSubagents)))
	}
	if tool := strings.TrimSpace(info.LastTool); tool != "" && (info.Streaming || info.AgentActive || info.LastStatus == "failed") {
		label := "last " + tool
		if info.LastStatus != "" {
			label += " " + info.LastStatus
		}
		if info.LastDurationMs > 0 {
			label += fmt.Sprintf(" %dms", info.LastDurationMs)
		}
		if info.LastStatus == "failed" {
			parts = append(parts, warnStyle.Render(label))
		} else {
			parts = append(parts, subtleStyle.Render(label))
		}
	}
	if info.QueuedCount > 0 {
		parts = append(parts, accentStyle.Render(fmt.Sprintf("queue %d", info.QueuedCount)))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, subtleStyle.Render(" / "))
}

func (m Model) workbenchRuntimeStatus() string {
	info := m.statsPanelInfo()
	parts := []string{}
	switch {
	case info.AgentActive:
		phase := strings.TrimSpace(info.AgentPhase)
		if phase == "" {
			phase = "working"
		}
		label := "working " + humanizeAgentPhase(phase)
		if info.AgentMaxSteps > 0 {
			label += fmt.Sprintf(" %d/%d", max(info.AgentStep, 1), info.AgentMaxSteps)
		} else if info.AgentStep > 0 {
			label += fmt.Sprintf(" step %d", info.AgentStep)
		}
		parts = append(parts, label)
	case info.Streaming:
		parts = append(parts, "streaming")
	case info.Parked:
		parts = append(parts, "parked")
	}
	if info.ActiveTools > 0 {
		parts = append(parts, fmt.Sprintf("tools %d", info.ActiveTools))
	}
	if info.ActiveSubagents > 0 {
		parts = append(parts, fmt.Sprintf("agents %d", info.ActiveSubagents))
	}
	if tool := strings.TrimSpace(info.LastTool); tool != "" && (info.Streaming || info.AgentActive) {
		parts = append(parts, "last "+tool)
	}
	if info.QueuedCount > 0 {
		parts = append(parts, fmt.Sprintf("queue %d", info.QueuedCount))
	}
	return strings.Join(parts, " / ")
}

// renderHelpOverlay + helpOverlayTabHints live in
// render_panels_help.go.
