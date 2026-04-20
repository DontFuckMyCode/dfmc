package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
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
	now := time.Now()
	head := m.chatHeaderInfo()
	elapsed := time.Duration(0)
	if !m.sessionStart.IsZero() {
		elapsed = now.Sub(m.sessionStart)
	}

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
			default:
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
					treeChar := "├─"
					if isLast {
						treeChar = "└─"
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
		if head.Parked {
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
	case head.Parked:
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
	if len(workflowRecent) < 4 {
		collectWorkflowRecent("drive:", 2)
	}
	if len(workflowRecent) < 4 {
		collectWorkflowRecent("agent:autonomy:", 2)
	}
	if len(workflowRecent) < 4 {
		collectWorkflowRecent("agent:loop:error", 1)
	}
	if len(workflowRecent) < 4 {
		collectWorkflowRecent("provider:throttle:retry", 1)
	}

	subagentLines := []string{}
	if head.ActiveSubagents > 0 {
		subagentLines = append(subagentLines, fmt.Sprintf("%d active now", head.ActiveSubagents))
	} else {
		subagentLines = append(subagentLines, "idle")
	}
	if recent := m.recentWorkflowActivity("agent:subagent:", 8); len(recent) > 0 {
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
		Mode:                  theme.StatsPanelMode(mode),
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
		ActiveSubagents:       head.ActiveSubagents,
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
		TodoTotal:             todoTotal,
		TodoPending:           todoPending,
		TodoDoing:             todoDoing,
		TodoDone:              todoDone,
		TodoActive:            todoActive,
		TodoLines:             todoLines,
		TaskLines:             taskLines,
		TaskTreeLines:        taskTreeLines,
		WorkflowStatus:        workflowStatus,
		WorkflowMeter:         workflowMeter,
		WorkflowExecution:     workflowExecution,
		WorkflowTimeline:      workflowTimeline,
		WorkflowRecent:        workflowRecent,
		Boosted:               boosted,
		BoostSeconds:          boostSeconds,
		FocusLocked:           m.ui.statsPanelFocusLocked,
		SubagentLines:         subagentLines,
		DriveRunID:            head.DriveRunID,
		DriveDone:             head.DriveDone,
		DriveTotal:            head.DriveTotal,
		DriveBlocked:          head.DriveBlocked,
		PlanSubtasks:          planSubtasks,
		PlanParallel:          planParallel,
		PlanConfidence:        planConfidence,
		SpinnerFrame:          m.chat.spinnerFrame,
	}
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

// statsPanelVisible returns true when the chat tab should render the
// right-side panel alongside the chat body.
func (m Model) statsPanelVisible(contentWidth int) bool {
	minWidth := statsPanelMinContentWidth
	if m.statsPanelBoostActive(time.Now()) {
		minWidth = statsPanelBoostMinContentWidth
	}
	return m.ui.showStatsPanel && contentWidth >= minWidth
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
	width := contentWidth/2 + 2
	if width < statsPanelBoostWidthMin {
		width = statsPanelBoostWidthMin
	}
	maxWidth := contentWidth - 28
	if maxWidth < statsPanelWidth {
		maxWidth = statsPanelWidth
	}
	if width > maxWidth {
		width = maxWidth
	}
	return width
}
