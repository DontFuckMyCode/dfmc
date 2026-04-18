package tui

import (
	"strings"
	"time"
)

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
	toolsEnabled := m.eng != nil && m.eng.Tools != nil
	parked := m.eng != nil && m.eng.HasParkedAgent()
	gated := false
	if m.eng != nil && m.eng.Config != nil {
		gated = len(m.eng.Config.Tools.RequireApproval) > 0
	}
	return chatHeaderInfo{
		Provider:        provider,
		Model:           model,
		Configured:      configured || strings.EqualFold(provider, "offline"),
		MaxContext:      maxCtx,
		ContextTokens:   tokens,
		Pinned:          strings.TrimSpace(m.filesView.pinned),
		ToolsEnabled:    toolsEnabled,
		Streaming:       m.chat.sending,
		AgentActive:     m.agentLoop.active,
		AgentPhase:      m.agentLoop.phase,
		AgentStep:       m.agentLoop.step,
		AgentMax:        m.agentLoop.maxToolStep,
		QueuedCount:     len(m.chat.pendingQueue),
		Parked:          parked,
		PendingNotes:    m.chat.pendingNoteCount,
		ActiveTools:     m.telemetry.activeToolCount,
		ActiveSubagents: m.telemetry.activeSubagentCount,
		PlanMode:        m.ui.planMode,
		ApprovalGated:   gated,
		ApprovalPending: m.pendingApproval != nil,
		IntentLast:      intentChipLabel(m.intent),
		DriveRunID:      m.telemetry.driveRunID,
		DriveTodoID:     m.telemetry.driveTodoID,
		DriveDone:       m.telemetry.driveDone,
		DriveTotal:      m.telemetry.driveTotal,
		DriveBlocked:    m.telemetry.driveBlocked,
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

// statsPanelInfo folds every stat the right-hand panel needs into a single
// snapshot struct. Kept on Model so the renderer stays pure.
func (m Model) statsPanelInfo() statsPanelInfo {
	head := m.chatHeaderInfo()
	elapsed := time.Duration(0)
	if !m.sessionStart.IsZero() {
		elapsed = time.Since(m.sessionStart)
	}
	toolCount := 0
	if m.eng != nil && m.eng.Tools != nil {
		toolCount = len(m.availableTools())
	}
	return statsPanelInfo{
		Provider:              head.Provider,
		Model:                 head.Model,
		Configured:            head.Configured,
		ContextTokens:         head.ContextTokens,
		MaxContext:            head.MaxContext,
		Streaming:             head.Streaming,
		AgentActive:           head.AgentActive,
		AgentPhase:            head.AgentPhase,
		AgentStep:             head.AgentStep,
		AgentMaxSteps:         head.AgentMax,
		ToolRounds:            m.agentLoop.toolRounds,
		LastTool:              m.agentLoop.lastTool,
		LastStatus:            m.agentLoop.lastStatus,
		LastDurationMs:        m.agentLoop.lastDuration,
		Parked:                head.Parked,
		QueuedCount:           head.QueuedCount,
		PendingNotes:          head.PendingNotes,
		ToolsEnabled:          head.ToolsEnabled,
		ToolCount:             toolCount,
		Branch:                m.gitInfo.Branch,
		Dirty:                 m.gitInfo.Dirty,
		Detached:              m.gitInfo.Detached,
		Inserted:              m.gitInfo.Inserted,
		Deleted:               m.gitInfo.Deleted,
		SessionElapsed:        elapsed,
		MessageCount:          len(m.chat.transcript),
		Pinned:                head.Pinned,
		CompressionSavedChars: m.telemetry.compressionSavedChars,
		CompressionRawChars:   m.telemetry.compressionRawChars,
		SpinnerFrame:          m.chat.spinnerFrame,
	}
}

// statsPanelVisible returns true when the chat tab should render the
// right-side panel alongside the chat body.
func (m Model) statsPanelVisible(contentWidth int) bool {
	return m.ui.showStatsPanel && contentWidth >= statsPanelMinContentWidth
}
