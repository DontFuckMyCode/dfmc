package tui

// render_chat_meta_stats.go — statsPanelInfo, the wide aggregator that
// folds engine.Status, telemetry, agent-loop, drive, todos, plan, git,
// and workflow-activity state into the snapshot the right-hand stats
// panel renders. Sibling to render_chat_meta.go which keeps the
// chatHeaderInfo bundler, intentChipLabel, latestWorkflowActivity,
// transcriptTokenTotals, and the panel-visibility/boost helpers.

import (
	"fmt"
	"time"

	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
)

// statsPanelInfo folds every stat the right-hand panel needs into a single
// snapshot struct. Kept on Model so the renderer stays pure.
func (m Model) statsPanelInfo() statsPanelInfo {
	now := time.Now()
	head := m.chatHeaderInfo()
	context := m.statsContextSnapshot(head)
	elapsed := time.Duration(0)
	if !m.sessionStart.IsZero() {
		elapsed = now.Sub(m.sessionStart)
	}
	liveInputTokens, liveOutputTokens := m.liveStreamTokenCounts()
	transcriptInputTokens, transcriptOutputTokens := transcriptTokenTotals(m.chat.transcript)
	composerTokens := estimatedChatTokens(m.composeInput())

	toolCount := 0
	if m.eng != nil && m.eng.Tools != nil {
		toolCount = len(m.availableTools())
	}

	todos := m.statsTodoSummary()
	workflow := m.statsWorkflowInfo(now, head, todos)

	subagentLines := m.subagentTreeLines(now, 10)
	if len(subagentLines) == 0 {
		if head.ActiveSubagents > 0 {
			subagentLines = append(subagentLines, fmt.Sprintf("%d active now", head.ActiveSubagents))
		} else {
			subagentLines = append(subagentLines, "idle")
		}
	}
	if recent := m.recentWorkflowActivity("agent:subagent:", 8); len(recent) > 0 {
		subagentLines = append(subagentLines, "recent:")
		subagentLines = append(subagentLines, recent...)
	}

	mode := m.ui.statsPanelMode
	if mode == "" {
		mode = statsPanelModeOverview
	}
	boosted := m.statsPanelBoostActive(now)
	boostSeconds := 0
	if boosted && !m.ui.statsPanelFocusLocked {
		boostSeconds = int(m.ui.statsPanelBoostUntil.Sub(now).Round(time.Second) / time.Second)
		if boostSeconds < 1 {
			boostSeconds = 1
		}
	}
	providerRows := m.providerStatusPanelRows()
	providerSelected := clampScroll(m.providers.selectedIndex, len(providerRows))

	return statsPanelInfo{
		Mode:                    theme.StatsPanelMode(mode),
		Provider:                head.Provider,
		Model:                   head.Model,
		Configured:              head.Configured,
		CostPer1kTokens:         m.currentCostPer1kTokens(),
		ContextTokens:           context.EvidenceTokens,
		ContextWindowTokens:     context.WindowTokens,
		MaxContext:              context.MaxContext,
		ContextProvider:         context.Provider,
		ContextModel:            context.Model,
		ContextLimitSource:      context.LimitSource,
		ContextTask:             context.Task,
		ContextFileCount:        context.FileCount,
		ContextMaxFiles:         context.MaxFiles,
		ContextBudgetTokens:     context.BudgetTokens,
		ContextAvailableTokens:  context.AvailableTokens,
		ContextMaxTokensPerFile: context.MaxTokensPerFile,
		ContextCompression:      context.Compression,
		ContextReasons:          context.Reasons,
		ContextTopFiles:         context.TopFiles,
		ContextSystemTokens:     context.SystemTokens,
		ContextHistoryTokens:    context.HistoryTokens,
		ContextHistoryReserve:   context.HistoryReserve,
		ContextResponseTokens:   context.ResponseTokens,
		ContextToolTokens:       context.ToolTokens,
		ContextMessageCount:     context.MessageCount,
		ContextToolCallCount:    context.ToolCallCount,
		ContextPayload:          context.Payload,
		ComposerTokens:          composerTokens,
		TranscriptInputTokens:   transcriptInputTokens,
		TranscriptOutputTokens:  transcriptOutputTokens,
		LiveInputTokens:         liveInputTokens,
		LiveOutputTokens:        liveOutputTokens,
		LiveTotalTokens:         liveInputTokens + liveOutputTokens,
		LastInputTokens:         m.telemetry.lastInputTokens,
		LastOutputTokens:        m.telemetry.lastOutputTokens,
		LastTotalTokens:         m.telemetry.lastTotalTokens,
		SessionInputTokens:      max(m.telemetry.sessionInputTokens, transcriptInputTokens),
		SessionOutputTokens:     max(m.telemetry.sessionOutputTokens, transcriptOutputTokens),
		SessionTotalTokens:      max(m.telemetry.sessionTotalTokens, transcriptInputTokens+transcriptOutputTokens),
		Streaming:               head.Streaming,
		AgentActive:             head.AgentActive,
		AgentPhase:              head.AgentPhase,
		AgentStep:               head.AgentStep,
		AgentMaxSteps:           head.AgentMax,
		ToolRounds:              m.agentLoop.toolRounds,
		LastTool:                m.agentLoop.lastTool,
		LastStatus:              m.agentLoop.lastStatus,
		LastDurationMs:          m.agentLoop.lastDuration,
		Parked:                  head.Parked,
		BannerActive:            head.BannerActive,
		QueuedCount:             head.QueuedCount,
		PendingNotes:            head.PendingNotes,
		ActiveSubagents:         head.ActiveSubagents,
		ActiveTools:             head.ActiveTools,
		ToolsEnabled:            head.ToolsEnabled,
		ToolCount:               toolCount,
		Branch:                  m.gitInfo.Branch,
		Dirty:                   m.gitInfo.Dirty,
		Detached:                m.gitInfo.Detached,
		Inserted:                m.gitInfo.Inserted,
		Deleted:                 m.gitInfo.Deleted,
		SessionElapsed:          elapsed,
		MessageCount:            len(m.chat.transcript),
		Pinned:                  head.Pinned,
		CompressionSavedChars:   m.telemetry.compressionSavedChars,
		CompressionRawChars:     m.telemetry.compressionRawChars,
		TodoTotal:               todos.Total,
		TodoPending:             todos.Pending,
		TodoDoing:               todos.Doing,
		TodoDone:                todos.Done,
		TodoActive:              todos.Active,
		TodoLines:               todos.Lines,
		TaskLines:               workflow.TaskLines,
		TaskTreeLines:           workflow.TaskTreeLines,
		WorkflowStatus:          workflow.Status,
		WorkflowMeter:           workflow.Meter,
		WorkflowExecution:       workflow.Execution,
		WorkflowTimeline:        workflow.Timeline,
		WorkflowRecent:          workflow.Recent,
		Boosted:                 boosted,
		BoostSeconds:            boostSeconds,
		FocusLocked:             m.ui.statsPanelFocusLocked,
		SubagentLines:           subagentLines,
		SubagentSummary:         head.SubagentSummary,
		SubagentLimit:           m.status.SubagentsLimit,
		DriveRunID:              head.DriveRunID,
		DriveDone:               head.DriveDone,
		DriveTotal:              head.DriveTotal,
		DriveBlocked:            head.DriveBlocked,
		PlanSubtasks:            workflow.PlanSubtasks,
		PlanParallel:            workflow.PlanParallel,
		PlanConfidence:          workflow.PlanConfidence,
		SpinnerFrame:            m.chat.spinnerFrame,
		Providers:               providerRows,
		ProvidersSelectedIndex:  providerSelected,
	}
}
