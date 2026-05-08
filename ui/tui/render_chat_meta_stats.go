package tui

// render_chat_meta_stats.go — statsPanelInfo, the wide aggregator that
// folds engine.Status, telemetry, agent-loop, drive, todos, plan, git,
// and workflow-activity state into the snapshot the right-hand stats
// panel renders. Sibling to render_chat_meta.go which keeps the
// chatHeaderInfo bundler, intentChipLabel, latestWorkflowActivity,
// transcriptTokenTotals, and the panel-visibility/boost helpers.

import (
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
)

// statsPanelInfo folds every stat the right-hand panel needs into a single
// snapshot struct. Kept on Model so the renderer stays pure.
func (m Model) statsPanelInfo() statsPanelInfo {
	now := time.Now()
	head := m.chatHeaderInfo()
	contextTask := ""
	contextFileCount := 0
	contextMaxFiles := 0
	contextBudgetTokens := 0
	contextAvailableTokens := 0
	contextMaxTokensPerFile := 0
	contextCompression := ""
	contextReasons := []string{}
	contextTopFiles := []string{}
	contextSystemTokens := 0
	contextHistoryTokens := 0
	contextResponseTokens := 0
	contextToolTokens := 0
	contextWindowTokens := head.ContextWindowTokens
	if report := m.status.ContextIn; report != nil {
		contextTask = strings.TrimSpace(report.Task)
		contextFileCount = report.FileCount
		contextMaxFiles = report.MaxFiles
		contextBudgetTokens = report.MaxTokensTotal
		contextAvailableTokens = report.ContextAvailable
		contextMaxTokensPerFile = report.MaxTokensPerFile
		contextCompression = strings.TrimSpace(report.Compression)
		for _, reason := range report.Reasons {
			reason = strings.TrimSpace(reason)
			if reason == "" {
				continue
			}
			contextReasons = append(contextReasons, reason)
			if len(contextReasons) >= 2 {
				break
			}
		}
		for _, file := range report.Files {
			path := strings.TrimSpace(file.Path)
			if path == "" {
				continue
			}
			contextTopFiles = append(contextTopFiles, path)
			if len(contextTopFiles) >= 2 {
				break
			}
		}
	}
	if live := m.liveContextSnapshot(); live.ok {
		if live.codeTokens > 0 {
			head.ContextTokens = live.codeTokens
		}
		if live.maxContext > 0 {
			head.MaxContext = live.maxContext
		}
		contextWindowTokens = live.windowTokens
		contextAvailableTokens = live.available
		contextSystemTokens = live.systemTokens
		contextHistoryTokens = live.historyTokens
		contextResponseTokens = live.responseTokens
		contextToolTokens = live.toolTokens
		if live.task != "" {
			contextTask = live.task
		}
		if live.compression != "" {
			contextCompression = live.compression
		}
		if len(live.topFiles) > 0 {
			contextTopFiles = live.topFiles
		}
	}
	elapsed := time.Duration(0)
	if !m.sessionStart.IsZero() {
		elapsed = now.Sub(m.sessionStart)
	}
	liveInputTokens := 0
	liveOutputTokens := 0
	if m.chat.sending {
		liveInputTokens = m.chat.streamInputTokens
		if m.chat.streamIndex >= 0 && m.chat.streamIndex < len(m.chat.transcript) {
			line := m.chat.transcript[m.chat.streamIndex]
			liveOutputTokens = line.TokenCount
			if liveOutputTokens <= 0 && strings.TrimSpace(line.Content) != "" {
				liveOutputTokens = estimatedChatTokens(line.Content)
			}
		}
	}
	transcriptInputTokens, transcriptOutputTokens := transcriptTokenTotals(m.chat.transcript)
	composerTokens := estimatedChatTokens(m.composeInput())

	toolCount := 0
	if m.eng != nil && m.eng.Tools != nil {
		toolCount = len(m.availableTools())
	}

	todoTotal, todoPending, todoDoing, todoDone := 0, 0, 0, 0
	todoActive := ""
	activeTodoIndex := 0
	todoLines := []string{}
	if m.eng != nil && m.eng.Tools != nil {
		todos := m.eng.Tools.TodoSnapshot()
		for idx, it := range todos {
			todoTotal++
			switch strings.ToLower(strings.TrimSpace(it.Status)) {
			case "completed", "done":
				todoDone++
			case "in_progress", "active", "doing":
				todoDoing++
				if todoActive == "" {
					todoActive = strings.TrimSpace(it.ActiveForm)
					if todoActive == "" {
						todoActive = strings.TrimSpace(it.Content)
					}
					activeTodoIndex = idx + 1
				}
			case "pending", "blocked", "skipped", "waiting", "verifying", "external_review":
				todoPending++
			}
		}
		todoLines = formatWorkflowTodoLines(todos, 8)
	}

	planSubtasks := 0
	planParallel := false
	planConfidence := 0.0
	taskLines := []string{}
	taskTreeLines := []string{}
	workflowStatus := ""
	workflowMeter := ""
	workflowExecution := ""
	workflowRecent := []string{}
	workflowTimeline := m.recentWorkflowTimeline(6)
	lastWorkflowText, lastWorkflowAge := m.latestWorkflowActivity(now)

	if m.plans.plan != nil {
		planSubtasks = len(m.plans.plan.Subtasks)
		planParallel = m.plans.plan.Parallel
		planConfidence = m.plans.plan.Confidence
		if planSubtasks > 0 {
			mode := "serial"
			if planParallel {
				mode = "parallel"
			}
			taskLines = append(taskLines, fmt.Sprintf("plan %d tasks%s%s%s%.2f", planSubtasks, workflowSep, mode, workflowSep, planConfidence))
			for i, sub := range m.plans.plan.Subtasks {
				if i >= 6 {
					taskLines = append(taskLines, fmt.Sprintf("... %d more", len(m.plans.plan.Subtasks)-i))
					break
				}
				title := strings.TrimSpace(sub.Title)
				if title == "" {
					title = strings.TrimSpace(sub.Description)
				}
				if title == "" {
					title = "(untitled)"
				}
				taskLines = append(taskLines, fmt.Sprintf("%d. %s", i+1, title))
			}
		}
	}

	// Build hierarchical task tree from the task store.
	if m.eng != nil && m.eng.Tools != nil && m.eng.Tools.TaskStore() != nil {
		storeTasks, _ := m.eng.Tools.TaskStore().ListTasks(taskstore.ListOptions{})
		if len(storeTasks) > 0 {
			children := make(map[string][]*supervisor.Task)
			var roots []*supervisor.Task
			for _, t := range storeTasks {
				if t.ParentID == "" {
					roots = append(roots, t)
				} else {
					children[t.ParentID] = append(children[t.ParentID], t)
				}
			}
			var buildTree func(t *supervisor.Task, indent int, isLast bool)
			buildTree = func(t *supervisor.Task, indent int, isLast bool) {
				prefix := ""
				if indent > 0 {
					treeChar := "+-"
					if isLast {
						treeChar = "`-"
					}
					prefix = strings.Repeat("  ", indent-1) + treeChar + " "
				}
				title := t.Title
				if title == "" {
					title = t.Detail
				}
				line := fmt.Sprintf("%s[%s] %s  %s", prefix, t.State, t.ID, title)
				taskTreeLines = append(taskTreeLines, line)
				kids := children[t.ID]
				for i, child := range kids {
					buildTree(child, indent+1, i == len(kids)-1)
				}
			}
			for i, root := range roots {
				buildTree(root, 0, i == len(roots)-1)
			}
		}
	}

	if head.AgentActive || head.Parked || head.QueuedCount > 0 || head.PendingNotes > 0 {
		phase := strings.TrimSpace(head.AgentPhase)
		if phase == "" {
			phase = "idle"
		}
		if head.AgentMax > 0 && head.AgentStep > 0 {
			taskLines = append(taskLines, fmt.Sprintf("agent %s%s%d/%d", phase, workflowSep, head.AgentStep, head.AgentMax))
		} else {
			taskLines = append(taskLines, "agent "+phase)
		}
		if head.QueuedCount > 0 || head.PendingNotes > 0 {
			taskLines = append(taskLines, fmt.Sprintf("queue %d%snotes %d", head.QueuedCount, workflowSep, head.PendingNotes))
		}
		// While the resume banner is up, the explicit "parked / /continue"
		// task line just echoes it. Drop the line when BannerActive so the
		// workflow column doesn't repeat what's already on screen.
		if head.Parked && !head.BannerActive {
			taskLines = append(taskLines, "parked"+workflowSep+"/continue")
		}
	}

	if strings.TrimSpace(head.DriveRunID) != "" {
		driveLine := fmt.Sprintf("drive %s%s%d/%d", head.DriveRunID, workflowSep, head.DriveDone, head.DriveTotal)
		if head.DriveBlocked > 0 {
			driveLine += fmt.Sprintf("%s%d blocked", workflowSep, head.DriveBlocked)
		}
		taskLines = append(taskLines, driveLine)
	} else if len(taskLines) == 0 {
		if summary := strings.TrimSpace(m.latestWorkflowPlanSummary()); summary != "" {
			taskLines = append(taskLines, summary)
		}
	}

	switch {
	case head.AgentActive || head.Streaming:
		phase := strings.TrimSpace(head.AgentPhase)
		if head.Streaming && !head.AgentActive {
			waitFor := ""
			if !m.chat.streamStartedAt.IsZero() {
				waitFor = formatSessionDuration(now.Sub(m.chat.streamStartedAt))
			}
			if strings.Contains(strings.ToLower(lastWorkflowText), "throttled") {
				workflowStatus = fmt.Sprintf("%s waiting on provider retry", spinnerFrame(m.chat.spinnerFrame+3))
			} else {
				workflowStatus = fmt.Sprintf("%s waiting on model reply", spinnerFrame(m.chat.spinnerFrame+3))
			}
			if waitFor != "" {
				workflowStatus += workflowSep + waitFor
			}
			if lastWorkflowAge >= 3*time.Second {
				workflowStatus += fmt.Sprintf("%sidle %s", workflowSep, formatSessionDuration(lastWorkflowAge))
			}
			break
		}
		if phase == "" {
			phase = "working"
		}
		workflowStatus = fmt.Sprintf("%s live now%s%s", spinnerFrame(m.chat.spinnerFrame+3), workflowSep, phase)
		if tool := strings.TrimSpace(m.agentLoop.lastTool); tool != "" {
			workflowStatus += workflowSep + tool
		}
		if lastWorkflowAge >= 3*time.Second {
			workflowStatus += fmt.Sprintf("%sidle %s", workflowSep, formatSessionDuration(lastWorkflowAge))
		}
		if head.AgentMax > 0 {
			step := head.AgentStep
			if step <= 0 {
				step = 1
			}
			workflowMeter = renderStepBar(step, head.AgentMax, 16, m.chat.spinnerFrame)
		}
	case strings.TrimSpace(head.DriveRunID) != "" && head.DriveTotal > 0:
		workflowStatus = fmt.Sprintf("%s drive running%s%d/%d", spinnerFrame(m.chat.spinnerFrame+3), workflowSep, head.DriveDone, head.DriveTotal)
		if head.DriveBlocked > 0 {
			workflowStatus += fmt.Sprintf("%s%d blocked", workflowSep, head.DriveBlocked)
		}
		workflowMeter = renderStepBar(head.DriveDone, head.DriveTotal, 16, m.chat.spinnerFrame)
	case todoDoing > 0:
		workflowStatus = fmt.Sprintf("%s workflow active%s%d doing", spinnerFrame(m.chat.spinnerFrame+3), workflowSep, todoDoing)
	case head.QueuedCount > 0:
		workflowStatus = fmt.Sprintf("queued%s%d waiting", workflowSep, head.QueuedCount)
	case head.Parked && !head.BannerActive:
		workflowStatus = "parked" + workflowSep + "/continue"
	case planSubtasks > 0:
		workflowStatus = fmt.Sprintf("planned%s%d subtasks ready", workflowSep, planSubtasks)
	}

	lastWorkflowLower := strings.ToLower(strings.TrimSpace(lastWorkflowText))
	if !head.AgentActive && !head.Streaming && (strings.Contains(lastWorkflowLower, "agent error -") || strings.Contains(lastWorkflowLower, "tool failed -") || strings.Contains(lastWorkflowLower, "tool error -")) {
		workflowStatus = "stalled" + workflowSep + truncateSingleLine(lastWorkflowText, 72)
		if lastWorkflowAge > 0 {
			workflowStatus += fmt.Sprintf("%s%s ago", workflowSep, formatSessionDuration(lastWorkflowAge))
		}
		workflowMeter = ""
	}

	if strings.TrimSpace(todoActive) != "" && activeTodoIndex > 0 && todoTotal > 0 {
		workflowExecution = fmt.Sprintf("task %d/%d%s%s", activeTodoIndex, todoTotal, workflowSep, truncateSingleLine(todoActive, 72))
		if head.ActiveSubagents > 0 {
			workflowExecution += fmt.Sprintf("%s%d subagents", workflowSep, head.ActiveSubagents)
		}
		if tool := strings.TrimSpace(m.agentLoop.lastTool); tool != "" {
			workflowExecution += workflowSep + tool
		}
	} else if m.plans.plan != nil && len(m.plans.plan.Subtasks) > 0 {
		taskIndex := head.AgentStep
		if taskIndex <= 0 {
			taskIndex = 1
		}
		if taskIndex > len(m.plans.plan.Subtasks) {
			taskIndex = len(m.plans.plan.Subtasks)
		}
		title := strings.TrimSpace(m.plans.plan.Subtasks[taskIndex-1].Title)
		if title == "" {
			title = strings.TrimSpace(m.plans.plan.Subtasks[taskIndex-1].Description)
		}
		if title == "" {
			title = "(untitled)"
		}
		workflowExecution = fmt.Sprintf("task %d/%d%s%s", taskIndex, len(m.plans.plan.Subtasks), workflowSep, truncateSingleLine(title, 72))
		if head.ActiveSubagents > 0 {
			workflowExecution += fmt.Sprintf("%s%d subagents", workflowSep, head.ActiveSubagents)
		}
		if tool := strings.TrimSpace(m.agentLoop.lastTool); tool != "" {
			workflowExecution += workflowSep + tool
		}
	} else if head.ActiveSubagents > 0 {
		workflowExecution = fmt.Sprintf("%d subagents active", head.ActiveSubagents)
		if tool := strings.TrimSpace(m.agentLoop.lastTool); tool != "" {
			workflowExecution += workflowSep + tool
		}
	} else if strings.TrimSpace(m.agentLoop.lastTool) != "" && (head.AgentActive || head.Streaming) {
		workflowExecution = "active tool" + workflowSep + strings.TrimSpace(m.agentLoop.lastTool)
	}

	seenWorkflowRecent := map[string]struct{}{}
	collectWorkflowRecent := func(prefix string, limit int) {
		for _, line := range m.recentWorkflowActivity(prefix, limit) {
			key := strings.ToLower(strings.TrimSpace(line))
			if key == "" {
				continue
			}
			if _, exists := seenWorkflowRecent[key]; exists {
				continue
			}
			seenWorkflowRecent[key] = struct{}{}
			workflowRecent = append(workflowRecent, line)
			if len(workflowRecent) >= 4 {
				return
			}
		}
	}
	collectWorkflowRecent("tool:", 2)
	collectWorkflowRecent("drive:", 2)
	collectWorkflowRecent("agent:autonomy:", 2)
	collectWorkflowRecent("agent:loop:error", 1)
	collectWorkflowRecent("provider:throttle:retry", 1)

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

	return statsPanelInfo{
		Mode:                    theme.StatsPanelMode(mode),
		Provider:                head.Provider,
		Model:                   head.Model,
		Configured:              head.Configured,
		CostPer1kTokens:         m.currentCostPer1kTokens(),
		ContextTokens:           head.ContextTokens,
		ContextWindowTokens:     contextWindowTokens,
		MaxContext:              head.MaxContext,
		ContextTask:             contextTask,
		ContextFileCount:        contextFileCount,
		ContextMaxFiles:         contextMaxFiles,
		ContextBudgetTokens:     contextBudgetTokens,
		ContextAvailableTokens:  contextAvailableTokens,
		ContextMaxTokensPerFile: contextMaxTokensPerFile,
		ContextCompression:      contextCompression,
		ContextReasons:          contextReasons,
		ContextTopFiles:         contextTopFiles,
		ContextSystemTokens:     contextSystemTokens,
		ContextHistoryTokens:    contextHistoryTokens,
		ContextResponseTokens:   contextResponseTokens,
		ContextToolTokens:       contextToolTokens,
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
		TodoTotal:               todoTotal,
		TodoPending:             todoPending,
		TodoDoing:               todoDoing,
		TodoDone:                todoDone,
		TodoActive:              todoActive,
		TodoLines:               todoLines,
		TaskLines:               taskLines,
		TaskTreeLines:           taskTreeLines,
		WorkflowStatus:          workflowStatus,
		WorkflowMeter:           workflowMeter,
		WorkflowExecution:       workflowExecution,
		WorkflowTimeline:        workflowTimeline,
		WorkflowRecent:          workflowRecent,
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
		PlanSubtasks:            planSubtasks,
		PlanParallel:            planParallel,
		PlanConfidence:          planConfidence,
		SpinnerFrame:            m.chat.spinnerFrame,
		Providers:               m.providerPanelRows(),
		ProvidersSelectedIndex:  m.providers.selectedIndex,
	}
}
