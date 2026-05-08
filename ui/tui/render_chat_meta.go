// render_chat_meta.go — chatHeaderInfo bundler, intent chip, panel
// visibility / boost width helpers, latestWorkflowActivity scanner,
// transcriptTokenTotals. Sibling to render_chat_meta_stats.go which
// owns the heavy statsPanelInfo aggregator.

package tui

import (
	"strings"
	"time"
)

const workflowSep = " | "

// chatHeaderInfo snapshots the pieces of engine.Status + agent-loop state
// into the compact bundle renderChatHeader consumes.
func (m Model) chatHeaderInfo() chatHeaderInfo {
	provider := strings.TrimSpace(m.status.Provider)
	model := strings.TrimSpace(m.status.Model)
	maxCtx := m.status.ProviderProfile.MaxContext
	configured := m.status.ProviderProfile.Configured
	tokens := 0
	if m.status.ContextIn != nil {
		tokens = m.status.ContextIn.TokenCount
		if maxCtx == 0 && m.status.ContextIn.ProviderMaxContext > 0 {
			maxCtx = m.status.ContextIn.ProviderMaxContext
		}
	}
	windowTokens := 0
	if live := m.liveContextSnapshot(); live.ok {
		if live.maxContext > 0 {
			maxCtx = live.maxContext
		}
		if live.codeTokens > 0 {
			tokens = live.codeTokens
		}
		windowTokens = live.windowTokens
	}
	toolsEnabled := m.eng != nil && m.eng.Tools != nil
	parked := m.eng != nil && m.eng.HasParkedAgent()
	gated := false
	if m.eng != nil && m.eng.Config != nil {
		gated = len(m.eng.Config.Tools.RequireApproval) > 0
	}
	activeSubagents := m.telemetry.activeSubagentCount
	if m.status.SubagentsActive > activeSubagents {
		activeSubagents = m.status.SubagentsActive
	}
	return chatHeaderInfo{
		Provider:            provider,
		Model:               model,
		Configured:          configured || strings.EqualFold(provider, "offline"),
		MaxContext:          maxCtx,
		ContextTokens:       tokens,
		ContextWindowTokens: windowTokens,
		Pinned:              strings.TrimSpace(m.filesView.pinned),
		ToolsEnabled:        toolsEnabled,
		Streaming:           m.chat.sending,
		AgentActive:         m.agentLoop.active,
		AgentPhase:          m.agentLoop.phase,
		AgentStep:           m.agentLoop.step,
		AgentMax:            m.agentLoop.maxToolStep,
		QueuedCount:         len(m.chat.pendingQueue),
		Parked:              parked,
		BannerActive:        parked && m.ui.resumePromptActive,
		PendingNotes:        m.chat.pendingNoteCount,
		ActiveTools:         m.telemetry.activeToolCount,
		ActiveSubagents:     activeSubagents,
		PlanMode:            m.ui.planMode,
		ApprovalGated:       gated,
		ApprovalPending:     m.pendingApproval != nil,
		IntentLast:          intentChipLabel(m.intent),
		DriveRunID:          m.telemetry.driveRunID,
		DriveTodoID:         m.telemetry.driveTodoID,
		DriveDone:           m.telemetry.driveDone,
		DriveTotal:          m.telemetry.driveTotal,
		DriveBlocked:        m.telemetry.driveBlocked,
		SubagentSummary:     m.activeSubagentSummary(),
	}
}

// intentChipLabel returns the short string the header chip shows for
// the most recent intent decision.
func intentChipLabel(s intentState) string {
	if s.lastDecisionAtMs == 0 || s.lastSource != "llm" {
		return ""
	}
	return s.lastIntent
}

func (m Model) latestWorkflowActivity(now time.Time) (string, time.Duration) {
	for i := len(m.activity.entries) - 1; i >= 0; i-- {
		entry := m.activity.entries[i]
		eventID := strings.ToLower(strings.TrimSpace(entry.EventID))
		switch {
		case strings.HasPrefix(eventID, "tool:"),
			strings.HasPrefix(eventID, "stream:"),
			strings.HasPrefix(eventID, "drive:"),
			strings.HasPrefix(eventID, "provider:throttle:retry"),
			strings.HasPrefix(eventID, "agent:loop:"),
			strings.HasPrefix(eventID, "agent:subagent:"),
			strings.HasPrefix(eventID, "agent:autonomy:"):
			if entry.At.IsZero() {
				return strings.TrimSpace(entry.Text), 0
			}
			return strings.TrimSpace(entry.Text), now.Sub(entry.At)
		}
	}
	return "", 0
}

func transcriptTokenTotals(lines []chatLine) (inputTokens, outputTokens int) {
	for _, line := range lines {
		tokens := line.TokenCount
		if tokens <= 0 {
			tokens = estimatedChatTokens(line.Content)
		}
		switch {
		case line.Role.Eq(chatRoleUser):
			inputTokens += tokens
		case line.Role.Eq(chatRoleAssistant):
			outputTokens += tokens
		}
	}
	return inputTokens, outputTokens
}

// statsPanelVisible returns true when the chat tab should render the
// right-side panel alongside the chat body. The threshold is fixed
// regardless of boost state — toggling visibility on/off as boost
// expires would re-wrap the whole transcript, which is exactly the
// reflow the reserved-width scheme above is trying to avoid.
func (m Model) statsPanelVisible(contentWidth int) bool {
	return m.ui.showStatsPanel && contentWidth >= statsPanelMinContentWidth
}

func (m Model) statsPanelBoostActive(now time.Time) bool {
	if m.ui.statsPanelFocusLocked {
		return true
	}
	if m.ui.statsPanelBoostUntil.IsZero() {
		return false
	}
	return now.Before(m.ui.statsPanelBoostUntil)
}

func (m Model) statsPanelRenderWidth(contentWidth int) int {
	if !m.statsPanelBoostActive(time.Now()) {
		return statsPanelWidth
	}
	return m.statsPanelReservedWidth(contentWidth)
}

// statsPanelReservedWidth is the column width the chat layout must
// always hold open for the stats panel whenever it is visible,
// regardless of current boost state. Reserving the max possible
// panel width keeps the chat body's word-wrap boundary stable across
// boost expand/collapse so the transcript never reflows mid-session.
// The panel itself still renders at its variable statsPanelRenderWidth
// inside this reserved column — unused slack appears as right-side
// padding in the outer frame, not as a re-wrap of the chat text.
func (m Model) statsPanelReservedWidth(contentWidth int) int {
	width := contentWidth*2/5 + 2
	if width < statsPanelBoostWidthMin {
		width = statsPanelBoostWidthMin
	}
	if width > 64 {
		width = 64
	}
	maxWidth := contentWidth - 56
	if maxWidth < statsPanelWidth {
		maxWidth = statsPanelWidth
	}
	if width > maxWidth {
		width = maxWidth
	}
	return width
}
