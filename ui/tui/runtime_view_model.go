package tui

import (
	"fmt"
	"strings"
	"time"
)

// computeTurnElapsedSec returns the seconds since the current turn's
// agent:loop:start. Returns 0 when no turn is active (turnStartedAt zero
// or agentLoop inactive) so the badge stays hidden between turns.
func computeTurnElapsedSec(s agentLoopState) int {
	if !s.active || s.turnStartedAt.IsZero() {
		return 0
	}
	d := time.Since(s.turnStartedAt)
	if d < 0 {
		return 0
	}
	return int(d.Seconds())
}

// formatTurnElapsed renders an int-seconds duration as "2m 34s" /
// "47s" / "1h 12m". Drops sub-minute precision past 60m so a long
// autonomous run reads cleanly instead of "73m 14s".
func formatTurnElapsed(sec int) string {
	if sec <= 0 {
		return ""
	}
	if sec < 60 {
		return fmt.Sprintf("%ds", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%dm %02ds", sec/60, sec%60)
	}
	return fmt.Sprintf("%dh %02dm", sec/3600, (sec%3600)/60)
}

type runtimeViewModel struct {
	State      string
	StateStyle string

	Provider        string
	Model           string
	Configured      bool
	CostPer1kTokens float64

	ContextTokens          int
	ContextWindowTokens    int
	MaxContext             int
	ContextTask            string
	ContextBudgetTokens    int
	ContextFileCount       int
	ContextMaxFiles        int
	ContextAvailable       int
	ContextMaxPerFile      int
	ContextCompression     string
	ContextReasons         []string
	ContextTopFiles        []string
	ContextSystemTokens    int
	ContextHistoryTokens   int
	ContextResponseTokens  int
	ContextToolTokens      int
	ComposerTokens         int
	TranscriptInputTokens  int
	TranscriptOutputTokens int
	LastInputTokens        int
	LastOutputTokens       int
	LastTotalTokens        int
	SessionInputTokens     int
	SessionOutputTokens    int
	SessionTotalTokens     int

	WorkflowStatus    string
	WorkflowExecution string
	WorkflowMeter     string
	WorkflowTimeline  []string
	WorkflowRecent    []string

	AgentActive   bool
	Streaming     bool
	AgentPhase    string
	AgentStep     int
	AgentMaxSteps int
	ToolRounds    int
	ActiveTools   int
	LastTool      string
	LastStatus    string
	LastDuration  int
	// LastToolReason is the model's `_reason` self-narration on the
	// most recent tool call. Rendered as "→ thinking: <reason>" in the
	// runtime "now" strip so a user watching a long autonomous run can
	// see WHY without scrolling back through chips. Empty → no badge.
	LastToolReason string

	// Stuck* — populated when the trajectory coach has flagged a
	// repeated-failure pattern that hasn't been cleared by a successful
	// tool call yet. Empty StuckTool means no current stall.
	StuckTool     string
	StuckCount    int
	StuckErrClass string

	// Cumulative* — running totals across every auto-resume cycle for
	// this root ask. Both ceiling fields zero means no auto-resume has
	// fired yet (badge stays hidden). Drawn as "auto · S/SCeil · TokK/Ceil"
	// in the runtime strip so a multi-hour run shows ceiling proximity
	// continuously, not just on each transient resume chip.
	CumulativeSteps  int
	StepCeiling      int
	CumulativeTokens int
	TokenCeiling     int

	// UnvalidatedEdits is the count of files mutated since the last
	// successful build/test/vet command. Surfaces as
	// "unverified: N edits" in the runtime strip. Style escalates with
	// count: 1-2 = info nudge, 3+ = warn. Cleared by the first
	// validation command after the streak.
	UnvalidatedEdits int

	// LiveLoopTokens is the rolling conversation footprint as reported
	// by the engine on every agent:loop:thinking, NOT the static
	// "context that would be sent if you asked now" number from
	// context:built. Pairs with LiveLoopBudgetCap (the per-turn
	// max_tool_tokens) to render "loop ~47k/250k" in the runtime
	// strip — the only surface that updates round-by-round during a
	// long autonomous run. Zero when not in a loop.
	LiveLoopTokens    int
	LiveLoopBudgetCap int

	// CompactsThisTurn / CompactReclaimedThisTurn track how many
	// auto-compact cycles have fired during the current turn and how
	// many tokens they reclaimed. Drawn as "compacts ×N · -Mk" in the
	// runtime strip when N > 0 — surfaces a budget-thrashing turn
	// without requiring the user to read individual compact events
	// from the activity feed. Reset on every agent:loop:start.
	CompactsThisTurn         int
	CompactReclaimedThisTurn int

	// CacheHitsThisTurn counts sub-agent / parallel-tool cache hits
	// during the current turn. Drawn as "cache ×N" in the runtime
	// strip when N > 0 so silent token savings register visibly.
	// Reset on agent:loop:start.
	CacheHitsThisTurn int

	// ToolErrorsThisTurn tracks how many tool:result events arrived
	// with success=false in the current turn. Renders as "errs ×N"
	// in the runtime strip — info at 1-2, warn at 3+ — so a fragile
	// turn surfaces while it's still happening, not just in the
	// post-hoc summary card. Reset on agent:loop:start.
	ToolErrorsThisTurn int

	// TurnElapsedSec is the seconds elapsed since the current turn's
	// agent:loop:start. Drawn as "running 2m 34s" in the runtime "now"
	// strip when AgentActive is true. Updates on every UI event (tool
	// call, thinking, provider stream chunk) so on busy turns the
	// reader sees live motion; on quiet stretches (provider thinking)
	// it ticks via the spinner Tick. Zero between turns hides the
	// badge entirely.
	TurnElapsedSec int

	// TurnFilesEdited is the count of distinct files touched by
	// successful edit/write tools this turn. Shown live as
	// "edits ×N" so a fan-out turn (refactor across 12 files)
	// registers visibly without scrolling chips. Capped to len of the
	// agentLoop.turnEditedFiles slice. Reset on agent:loop:start.
	TurnFilesEdited int

	Parked          bool
	ApprovalPending bool
	QueuedCount     int
	PendingNotes    int

	ToolsEnabled          bool
	ToolCount             int
	CompressionSavedChars int
	CompressionRawChars   int

	TodoTotal   int
	TodoDone    int
	TodoDoing   int
	TodoPending int
	TodoActive  string

	TaskLines     []string
	TaskTreeLines []string

	ActiveSubagents int
	SubagentLimit   int
	SubagentSummary string
	SubagentLines   []string

	DriveRunID   string
	DriveDone    int
	DriveTotal   int
	DriveBlocked int

	PlanSubtasks   int
	PlanParallel   bool
	PlanConfidence float64

	Branch   string
	Dirty    bool
	Inserted int
	Deleted  int

	MessageCount int
	Pinned       string
	SpinnerFrame int
}

func (m Model) runtimeViewModel() runtimeViewModel {
	info := m.statsPanelInfo()
	state, style := runtimeStateLabel(info)
	return runtimeViewModel{
		State:                  state,
		StateStyle:             style,
		Provider:               info.Provider,
		Model:                  info.Model,
		Configured:             info.Configured,
		CostPer1kTokens:        info.CostPer1kTokens,
		ContextTokens:          info.ContextTokens,
		ContextWindowTokens:    info.ContextWindowTokens,
		MaxContext:             info.MaxContext,
		ContextTask:            info.ContextTask,
		ContextBudgetTokens:    info.ContextBudgetTokens,
		ContextFileCount:       info.ContextFileCount,
		ContextMaxFiles:        info.ContextMaxFiles,
		ContextAvailable:       info.ContextAvailableTokens,
		ContextMaxPerFile:      info.ContextMaxTokensPerFile,
		ContextCompression:     info.ContextCompression,
		ContextReasons:         append([]string(nil), info.ContextReasons...),
		ContextTopFiles:        append([]string(nil), info.ContextTopFiles...),
		ContextSystemTokens:    info.ContextSystemTokens,
		ContextHistoryTokens:   info.ContextHistoryTokens,
		ContextResponseTokens:  info.ContextResponseTokens,
		ContextToolTokens:      info.ContextToolTokens,
		ComposerTokens:         info.ComposerTokens,
		TranscriptInputTokens:  info.TranscriptInputTokens,
		TranscriptOutputTokens: info.TranscriptOutputTokens,
		LastInputTokens:        info.LastInputTokens,
		LastOutputTokens:       info.LastOutputTokens,
		LastTotalTokens:        info.LastTotalTokens,
		SessionInputTokens:     info.SessionInputTokens,
		SessionOutputTokens:    info.SessionOutputTokens,
		SessionTotalTokens:     info.SessionTotalTokens,
		WorkflowStatus:         info.WorkflowStatus,
		WorkflowExecution:      info.WorkflowExecution,
		WorkflowMeter:          info.WorkflowMeter,
		WorkflowTimeline:       append([]string(nil), info.WorkflowTimeline...),
		WorkflowRecent:         append([]string(nil), info.WorkflowRecent...),
		AgentActive:            info.AgentActive,
		Streaming:              info.Streaming,
		AgentPhase:             info.AgentPhase,
		AgentStep:              info.AgentStep,
		AgentMaxSteps:          info.AgentMaxSteps,
		ToolRounds:             info.ToolRounds,
		ActiveTools:            info.ActiveTools,
		LastTool:               info.LastTool,
		LastStatus:             info.LastStatus,
		LastDuration:           info.LastDurationMs,
		LastToolReason:         m.agentLoop.lastToolReason,
		LiveLoopTokens:         m.agentLoop.liveLoopTokens,
		LiveLoopBudgetCap:      m.agentLoop.liveLoopBudgetCap,

		CompactsThisTurn:         m.agentLoop.compactsThisTurn,
		CompactReclaimedThisTurn: m.agentLoop.compactReclaimedTurn,
		CacheHitsThisTurn:        m.agentLoop.cacheHitsThisTurn,
		ToolErrorsThisTurn:       m.agentLoop.toolErrorsThisTurn,
		TurnElapsedSec:           computeTurnElapsedSec(m.agentLoop),
		TurnFilesEdited:          len(m.agentLoop.turnEditedFiles),
		StuckTool:              m.agentLoop.stuckTool,
		StuckCount:             m.agentLoop.stuckCount,
		StuckErrClass:          m.agentLoop.stuckErrClass,
		CumulativeSteps:        m.agentLoop.cumulativeSteps,
		StepCeiling:            m.agentLoop.stepCeiling,
		CumulativeTokens:       m.agentLoop.cumulativeTokens,
		TokenCeiling:           m.agentLoop.tokenCeiling,
		UnvalidatedEdits:       len(m.agentLoop.unvalidatedEdits),
		Parked:                 info.Parked,
		ApprovalPending:        m.pendingApproval != nil,
		QueuedCount:            info.QueuedCount,
		PendingNotes:           info.PendingNotes,
		ToolsEnabled:           info.ToolsEnabled,
		ToolCount:              info.ToolCount,
		CompressionSavedChars:  info.CompressionSavedChars,
		CompressionRawChars:    info.CompressionRawChars,
		TodoTotal:              info.TodoTotal,
		TodoDone:               info.TodoDone,
		TodoDoing:              info.TodoDoing,
		TodoPending:            info.TodoPending,
		TodoActive:             info.TodoActive,
		TaskLines:              append([]string(nil), info.TaskLines...),
		TaskTreeLines:          append([]string(nil), info.TaskTreeLines...),
		ActiveSubagents:        info.ActiveSubagents,
		SubagentLimit:          info.SubagentLimit,
		SubagentSummary:        info.SubagentSummary,
		SubagentLines:          append([]string(nil), info.SubagentLines...),
		DriveRunID:             info.DriveRunID,
		DriveDone:              info.DriveDone,
		DriveTotal:             info.DriveTotal,
		DriveBlocked:           info.DriveBlocked,
		PlanSubtasks:           info.PlanSubtasks,
		PlanParallel:           info.PlanParallel,
		PlanConfidence:         info.PlanConfidence,
		Branch:                 info.Branch,
		Dirty:                  info.Dirty,
		Inserted:               info.Inserted,
		Deleted:                info.Deleted,
		MessageCount:           info.MessageCount,
		Pinned:                 info.Pinned,
		SpinnerFrame:           info.SpinnerFrame,
	}
}

func runtimeStateLabel(info statsPanelInfo) (string, string) {
	switch {
	case info.Parked:
		return "parked", "warn"
	case info.AgentActive:
		return "running", "accent"
	case info.Streaming:
		return "waiting", "info"
	case strings.TrimSpace(info.DriveRunID) != "" && info.DriveTotal > 0:
		return "drive", "accent"
	case info.TodoDoing > 0:
		return "working", "accent"
	case info.QueuedCount > 0:
		return "queued", "warn"
	case !info.Configured && strings.TrimSpace(info.Provider) != "":
		return "needs key", "warn"
	case strings.TrimSpace(info.Provider) == "":
		return "needs provider", "fail"
	default:
		return "ready", "ok"
	}
}

func renderRuntimeStrip(vm runtimeViewModel, width int, slim bool) []string {
	if width < 40 {
		width = 40
	}
	rows := []string{
		truncateSingleLine(strings.Join(runtimeStripTopParts(vm), subtleStyle.Render("  |  ")), width),
		truncateSingleLine(strings.Join(runtimeStripNowParts(vm), subtleStyle.Render("  |  ")), width),
	}
	if !slim {
		if work := runtimeStripWorkParts(vm); len(work) > 0 {
			rows = append(rows, truncateSingleLine(strings.Join(work, subtleStyle.Render("  |  ")), width))
		}
		if tasks := runtimeStripTaskParts(vm); len(tasks) > 0 {
			rows = append(rows, truncateSingleLine(subtleStyle.Render("tasks: ")+strings.Join(tasks, subtleStyle.Render("  |  ")), width))
		}
		if orchestration := runtimeStripOrchestrationParts(vm); len(orchestration) > 0 {
			rows = append(rows, truncateSingleLine(infoStyle.Render("map: ")+strings.Join(orchestration, subtleStyle.Render("  |  ")), width))
		}
		if tools := runtimeStripToolParts(vm); len(tools) > 0 {
			rows = append(rows, truncateSingleLine(subtleStyle.Render("tools: ")+strings.Join(tools, subtleStyle.Render("  |  ")), width))
		}
		if budget := runtimeStripBudgetParts(vm); len(budget) > 0 {
			rows = append(rows, truncateSingleLine(subtleStyle.Render("budget: ")+strings.Join(budget, subtleStyle.Render("  |  ")), width))
		}
		if tokenParts := runtimeStripTokenParts(vm); len(tokenParts) > 0 {
			rows = append(rows, truncateSingleLine(infoStyle.Render("tokens: ")+strings.Join(tokenParts, subtleStyle.Render("  |  ")), width))
		}
		if activity := runtimeStripActivityParts(vm); len(activity) > 0 {
			rows = append(rows, truncateSingleLine(subtleStyle.Render("activity: ")+strings.Join(activity, subtleStyle.Render("  |  ")), width))
		}
		if actions := runtimeStripActionParts(vm); len(actions) > 0 {
			rows = append(rows, truncateSingleLine(warnStyle.Render("next: ")+strings.Join(actions, subtleStyle.Render("  |  ")), width))
		}
	}
	return rows
}

func runtimeStripTopParts(vm runtimeViewModel) []string {
	provider := strings.TrimSpace(vm.Provider)
	model := strings.TrimSpace(vm.Model)
	if provider == "" {
		provider = "provider?"
	}
	if model == "" {
		model = "model?"
	}
	state := vm.State
	if state == "" {
		state = "ready"
	}
	parts := []string{
		titleStyle.Render("DFMC CHAT"),
		runtimeStateStyle(vm.StateStyle)(state),
		subtleStyle.Render(provider + "/" + model),
		subtleStyle.Render(runtimeContextLabel(runtimeContextUsed(vm), vm.MaxContext)),
	}
	if vm.MessageCount > 0 {
		parts = append(parts, subtleStyle.Render(fmt.Sprintf("%d messages", vm.MessageCount)))
	}
	if git := runtimeGitLabel(vm); git != "" {
		parts = append(parts, subtleStyle.Render(git))
	}
	if pinned := strings.TrimSpace(vm.Pinned); pinned != "" {
		parts = append(parts, subtleStyle.Render("pinned: "+fileMarker(pinned)))
	}
	return parts
}

func runtimeStripNowParts(vm runtimeViewModel) []string {
	now := strings.TrimSpace(vm.WorkflowStatus)
	if now == "" {
		switch {
		case vm.ApprovalPending:
			now = "waiting on approval"
		case vm.Streaming:
			now = "waiting on model reply"
		case vm.AgentActive:
			now = "agent " + humanizeAgentPhase(vm.AgentPhase)
		default:
			now = "ready for input"
		}
	}
	parts := []string{accentStyle.Render("now: " + humanizeWorkflowText(now))}
	if meter := strings.TrimSpace(vm.WorkflowMeter); meter != "" {
		parts = append(parts, meter)
	}
	if vm.AgentActive && vm.AgentMaxSteps > 0 {
		parts = append(parts, fmt.Sprintf("call %d/%d", max(vm.AgentStep, 1), vm.AgentMaxSteps))
	} else if vm.AgentActive && vm.AgentStep > 0 {
		parts = append(parts, fmt.Sprintf("call %d", vm.AgentStep))
	}
	if vm.ToolRounds > 0 {
		parts = append(parts, fmt.Sprintf("rounds %d", vm.ToolRounds))
	}
	if vm.ActiveTools > 0 {
		parts = append(parts, fmt.Sprintf("tools %d", vm.ActiveTools))
	}
	// Live conversation-footprint badge — the only surface that updates
	// round-by-round during an active loop. CONTEXT panel above the
	// chat shows the static "what would be sent if you asked now"
	// number from context:built; this one reflects the actual growing
	// (and force-compact-shrinking) working set the engine sees.
	if badge := liveLoopTokensBadge(vm); badge != "" {
		parts = append(parts, badge)
	}
	// Compacts-this-turn badge — when N > 0 the engine has actively
	// fought the budget at least once this turn. Showing it surfaces
	// "thrashing" turns (compacts ×3 + reclaimed 28k) where the user
	// might want to scope down to give the loop more headroom.
	if badge := compactsThisTurnBadge(vm); badge != "" {
		parts = append(parts, badge)
	}
	// Cache-hits badge — silent token savings made visible. A turn
	// with many cache hits has been efficient; this badge surfaces
	// that so the user sees the system working in their favour.
	if vm.CacheHitsThisTurn > 0 {
		parts = append(parts, infoStyle.Render(fmt.Sprintf("cache ×%d", vm.CacheHitsThisTurn)))
	}
	// Live turn duration — shows momentum. Updates on every tool call,
	// thinking event, or stream chunk so a busy turn ticks visibly.
	// Hidden between turns. Style escalates with duration so a
	// runaway autonomous run signals "still going" louder over time.
	if vm.TurnElapsedSec > 0 {
		label := "running " + formatTurnElapsed(vm.TurnElapsedSec)
		switch {
		case vm.TurnElapsedSec >= 600: // 10 minutes
			parts = append(parts, warnStyle.Render(label))
		case vm.TurnElapsedSec >= 120: // 2 minutes
			parts = append(parts, infoStyle.Render(label))
		default:
			parts = append(parts, subtleStyle.Render(label))
		}
	}
	// Files-edited-this-turn badge: a refactor that fans out across 12
	// files registers as one persistent count instead of 12 chips that
	// scroll. Hidden at zero; pluralized correctly.
	if vm.TurnFilesEdited > 0 {
		word := "files"
		if vm.TurnFilesEdited == 1 {
			word = "file"
		}
		parts = append(parts, infoStyle.Render(fmt.Sprintf("edits ×%d %s", vm.TurnFilesEdited, word)))
	}
	// Tool-errors-this-turn badge: info at 1-2, warn at 3+. A retry-
	// heavy turn with the model recovering between failures is the
	// "is everything fine?" question users glance at the runtime strip
	// to answer; a visible badge converts a chip-stream-of-failures
	// into one persistent counter instead of the user counting chips.
	if vm.ToolErrorsThisTurn > 0 {
		label := fmt.Sprintf("errs ×%d", vm.ToolErrorsThisTurn)
		if vm.ToolErrorsThisTurn >= 3 {
			parts = append(parts, warnStyle.Render(label))
		} else {
			parts = append(parts, infoStyle.Render(label))
		}
	}
	// Auto-resume progress: show ceiling proximity continuously during a
	// long autonomous run. Style escalates from info → warn as headroom
	// disappears so the user knows when to /continue with a refined
	// scope before the engine refuses further work.
	if badge := autoResumeBadge(vm); badge != "" {
		parts = append(parts, badge)
	}
	// Self-narration: the model's `_reason` on the latest tool call,
	// truncated to fit. Renders as "→ <reason>" so a user can see
	// what the agent is currently trying to accomplish without having
	// to scroll back through chips. Subtle-styled because it's
	// supplementary commentary, not a state indicator.
	if reason := strings.TrimSpace(vm.LastToolReason); reason != "" {
		parts = append(parts, subtleStyle.Render("→ "+truncateSingleLine(reason, 64)))
	}
	// Stuck-loop badge: warn-styled, ahead of last-tool so it lands in
	// the eyeline of someone scanning a long-running run for "is it
	// stuck?" Cleared automatically by the next successful tool result.
	if stuck := strings.TrimSpace(vm.StuckTool); stuck != "" && vm.StuckCount > 0 {
		badge := fmt.Sprintf("stalled: %s ×%d", stuck, vm.StuckCount)
		if cls := strings.TrimSpace(vm.StuckErrClass); cls != "" {
			badge += " · " + cls
		}
		parts = append(parts, warnStyle.Render(badge))
	}
	// Unvalidated-edits badge: lights up when the agent has been editing
	// without running a build/test/vet. info at 1-2, warn at 3+ — three
	// edits without a single validation pass is the quiet failure mode
	// the user explicitly worries about ("don't break the code while
	// running for hours"). Cleared by the next successful validation.
	if vm.UnvalidatedEdits > 0 {
		badge := fmt.Sprintf("unverified: %d edit", vm.UnvalidatedEdits)
		if vm.UnvalidatedEdits != 1 {
			badge += "s"
		}
		if vm.UnvalidatedEdits >= 3 {
			parts = append(parts, warnStyle.Render(badge))
		} else {
			parts = append(parts, infoStyle.Render(badge))
		}
	}
	if tool := strings.TrimSpace(vm.LastTool); tool != "" {
		label := "last tool: " + tool
		if vm.LastStatus != "" {
			label += " " + vm.LastStatus
		}
		if vm.LastDuration > 0 {
			label += fmt.Sprintf(" %dms", vm.LastDuration)
		}
		parts = append(parts, label)
	}
	if vm.ApprovalPending {
		parts = append(parts, warnStyle.Render("approval pending"))
	}
	if vm.Parked {
		parts = append(parts, warnStyle.Render("/continue"))
	}
	if vm.QueuedCount > 0 {
		parts = append(parts, warnStyle.Render(fmt.Sprintf("queue %d", vm.QueuedCount)))
	}
	if vm.PendingNotes > 0 {
		parts = append(parts, infoStyle.Render(fmt.Sprintf("notes %d", vm.PendingNotes)))
	}
	return parts
}

// liveLoopTokensBadge renders the "loop ~47k/250k" indicator when the
// agent loop is active. Style escalates with proximity to the per-turn
// budget cap so the user can see when a force-compact is about to fire:
//
//	<70% → subtle (quiet status counter)
//	70-90% → info (compaction is approaching)
//	>=90% → warn (compaction or budget park imminent)
//
// Returns "" when the loop isn't running (LiveLoopTokens==0) so the
// strip stays clean between turns. When LiveLoopBudgetCap is zero we
// still render the count alone (some configs disable the budget) so
// the user always sees the live working set during a turn.
func liveLoopTokensBadge(vm runtimeViewModel) string {
	if vm.LiveLoopTokens <= 0 {
		return ""
	}
	if vm.LiveLoopBudgetCap <= 0 {
		return subtleStyle.Render(fmt.Sprintf("loop ~%s", formatTokenCount(vm.LiveLoopTokens)))
	}
	pct := (vm.LiveLoopTokens * 100) / vm.LiveLoopBudgetCap
	label := fmt.Sprintf("loop ~%s/%s",
		formatTokenCount(vm.LiveLoopTokens),
		formatTokenCount(vm.LiveLoopBudgetCap),
	)
	switch {
	case pct >= 90:
		return warnStyle.Render(label)
	case pct >= 70:
		return infoStyle.Render(label)
	default:
		return subtleStyle.Render(label)
	}
}

// compactsThisTurnBadge renders "compacts ×N · -Mk" when the engine has
// run at least one auto-compact during the current turn. Surfaces a
// budget-thrashing turn so the user can spot one without watching the
// activity feed event-by-event. Style escalates: 1 = info nudge,
// 2-3 = info bumped, 4+ = warn (the loop is barely keeping up and a
// scope refinement is probably warranted).
//
// Returns "" when the turn hasn't compacted yet (CompactsThisTurn == 0)
// so the strip stays quiet on healthy short turns.
func compactsThisTurnBadge(vm runtimeViewModel) string {
	if vm.CompactsThisTurn <= 0 {
		return ""
	}
	label := fmt.Sprintf("compacts ×%d", vm.CompactsThisTurn)
	if vm.CompactReclaimedThisTurn > 0 {
		label = fmt.Sprintf("compacts ×%d · -%s reclaimed",
			vm.CompactsThisTurn,
			formatTokenCount(vm.CompactReclaimedThisTurn),
		)
	}
	switch {
	case vm.CompactsThisTurn >= 4:
		return warnStyle.Render(label)
	default:
		return infoStyle.Render(label)
	}
}

// autoResumeBadge renders a one-token "auto · S/SCeil" badge when the
// autonomous wrapper has accumulated work across resumes. Style ramps
// from neutral → info → warn as proximity to the ceiling grows: under
// 50% reads as a quiet status counter, 50-80% bumps to info to invite
// attention, ≥80% switches to warn so the user can refine scope before
// the engine refuses. Empty string when no auto-resume has fired
// (StepCeiling==0) so the badge stays hidden in the common case.
func autoResumeBadge(vm runtimeViewModel) string {
	if vm.StepCeiling <= 0 || vm.CumulativeSteps <= 0 {
		return ""
	}
	stepPct := (vm.CumulativeSteps * 100) / vm.StepCeiling
	tokPct := 0
	if vm.TokenCeiling > 0 && vm.CumulativeTokens > 0 {
		tokPct = (vm.CumulativeTokens * 100) / vm.TokenCeiling
	}
	hottest := stepPct
	if tokPct > hottest {
		hottest = tokPct
	}
	label := fmt.Sprintf("auto %d/%d", vm.CumulativeSteps, vm.StepCeiling)
	if vm.TokenCeiling > 0 {
		label = fmt.Sprintf("auto %d/%d · %s/%s",
			vm.CumulativeSteps, vm.StepCeiling,
			formatTokenCount(vm.CumulativeTokens),
			formatTokenCount(vm.TokenCeiling),
		)
	}
	switch {
	case hottest >= 80:
		return warnStyle.Render(label)
	case hottest >= 50:
		return infoStyle.Render(label)
	default:
		return subtleStyle.Render(label)
	}
}

// formatTokenCount renders a token total compactly (1234 → "1.2k",
// 12345 → "12k") for the auto-resume badge. The badge sits in a
// crowded strip; raw five-digit numbers wash out the rest of the line.
func formatTokenCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 10000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}

func runtimeStripWorkParts(vm runtimeViewModel) []string {
	parts := []string{}
	if execution := strings.TrimSpace(vm.WorkflowExecution); execution != "" {
		parts = append(parts, "work: "+humanizeWorkflowText(execution))
	}
	if vm.TodoTotal > 0 {
		label := fmt.Sprintf("todos %d/%d done, %d doing", vm.TodoDone, vm.TodoTotal, vm.TodoDoing)
		if active := strings.TrimSpace(vm.TodoActive); active != "" {
			label += " - " + active
		}
		parts = append(parts, label)
	}
	if vm.ActiveSubagents > 0 {
		label := fmt.Sprintf("agents %d", vm.ActiveSubagents)
		if vm.SubagentLimit > 0 {
			label = fmt.Sprintf("agents %d/%d", vm.ActiveSubagents, vm.SubagentLimit)
		}
		if summary := strings.TrimSpace(vm.SubagentSummary); summary != "" {
			label += " " + summary
		}
		parts = append(parts, label)
		if line := firstUsefulSubagentLine(vm.SubagentLines); line != "" {
			parts = append(parts, "agent: "+line)
		}
	}
	if strings.TrimSpace(vm.DriveRunID) != "" || vm.DriveTotal > 0 {
		label := fmt.Sprintf("drive %d/%d", vm.DriveDone, vm.DriveTotal)
		if vm.DriveRunID != "" {
			label = "drive " + vm.DriveRunID + " " + fmt.Sprintf("%d/%d", vm.DriveDone, vm.DriveTotal)
		}
		if vm.DriveBlocked > 0 {
			label += fmt.Sprintf(", %d blocked", vm.DriveBlocked)
		}
		parts = append(parts, label)
	}
	if len(parts) == 0 && len(vm.WorkflowRecent) > 0 {
		parts = append(parts, "recent: "+humanizeWorkflowText(vm.WorkflowRecent[0]))
	}
	return parts
}

func runtimeStripTaskParts(vm runtimeViewModel) []string {
	parts := []string{}
	if vm.PlanSubtasks > 0 {
		mode := "serial"
		if vm.PlanParallel {
			mode = "parallel"
		}
		label := fmt.Sprintf("plan %d %s", vm.PlanSubtasks, mode)
		if vm.PlanConfidence > 0 {
			label += fmt.Sprintf(" %.2f", vm.PlanConfidence)
		}
		parts = append(parts, label)
	}
	if len(vm.TaskTreeLines) > 0 {
		parts = append(parts, fmt.Sprintf("store %d", len(vm.TaskTreeLines)))
		if line := firstUsefulTaskLine(vm.TaskTreeLines); line != "" {
			parts = append(parts, "task: "+line)
		}
	} else if vm.PlanSubtasks > 0 {
		if line := firstUsefulTaskLine(vm.TaskLines); line != "" {
			parts = append(parts, line)
		}
	}
	return parts
}

func runtimeStripOrchestrationParts(vm runtimeViewModel) []string {
	parts := []string{}
	if vm.TodoTotal > 0 {
		parts = append(parts, fmt.Sprintf("todo %d/%d done", vm.TodoDone, vm.TodoTotal))
	} else {
		parts = append(parts, "todo idle (/todos)")
	}
	if vm.PlanSubtasks > 0 || len(vm.TaskTreeLines) > 0 {
		if vm.PlanSubtasks > 0 {
			mode := "serial"
			if vm.PlanParallel {
				mode = "parallel"
			}
			parts = append(parts, fmt.Sprintf("task plan %d %s", vm.PlanSubtasks, mode))
		} else {
			parts = append(parts, fmt.Sprintf("task store %d", len(vm.TaskTreeLines)))
		}
	} else {
		parts = append(parts, "task idle (/tasks)")
	}
	if strings.TrimSpace(vm.DriveRunID) != "" || vm.DriveTotal > 0 {
		parts = append(parts, fmt.Sprintf("drive %d/%d (F5)", vm.DriveDone, vm.DriveTotal))
	} else {
		parts = append(parts, "drive idle (F5)")
	}
	if vm.ActiveSubagents > 0 {
		label := fmt.Sprintf("subagents %d", vm.ActiveSubagents)
		if vm.SubagentLimit > 0 {
			label = fmt.Sprintf("subagents %d/%d", vm.ActiveSubagents, vm.SubagentLimit)
		}
		parts = append(parts, label)
	} else {
		parts = append(parts, "subagents idle (/subagents)")
	}
	return parts
}

func runtimeStripToolParts(vm runtimeViewModel) []string {
	parts := []string{}
	if vm.ToolsEnabled {
		label := "enabled"
		if vm.ToolCount > 0 {
			label += fmt.Sprintf(", %d registered", vm.ToolCount)
		}
		parts = append(parts, label)
	} else if vm.ToolCount > 0 {
		parts = append(parts, fmt.Sprintf("%d registered", vm.ToolCount))
	}
	if vm.ActiveTools > 0 {
		parts = append(parts, fmt.Sprintf("%d active", vm.ActiveTools))
	}
	if vm.CompressionSavedChars > 0 {
		label := "rtk saved " + compactMetric(vm.CompressionSavedChars)
		if vm.CompressionRawChars > 0 {
			pct := int((int64(vm.CompressionSavedChars) * 100) / int64(vm.CompressionRawChars))
			if pct > 0 {
				label += fmt.Sprintf(" (%d%%)", pct)
			}
		}
		parts = append(parts, label)
	}
	return parts
}

func runtimeStripBudgetParts(vm runtimeViewModel) []string {
	parts := []string{}
	if task := strings.TrimSpace(vm.ContextTask); task != "" {
		parts = append(parts, "task "+task)
	}
	if vm.ContextFileCount > 0 {
		label := fmt.Sprintf("ctx files %d", vm.ContextFileCount)
		if vm.ContextMaxFiles > 0 {
			label += fmt.Sprintf("/%d", vm.ContextMaxFiles)
		}
		parts = append(parts, label)
	}
	if vm.ContextBudgetTokens > 0 {
		parts = append(parts, fmt.Sprintf("evidence %s/%s", compactMetric(vm.ContextTokens), compactMetric(vm.ContextBudgetTokens)))
	}
	if used, remaining := runtimeWindowUsage(vm); used > 0 {
		if vm.MaxContext > 0 {
			parts = append(parts, fmt.Sprintf("window %s/%s", compactMetric(used), compactMetric(vm.MaxContext)))
			if remaining >= 0 {
				parts = append(parts, "left "+compactMetric(remaining))
			} else {
				parts = append(parts, "over "+compactMetric(-remaining))
			}
		} else {
			parts = append(parts, "window "+compactMetric(used))
		}
	}
	if vm.ContextAvailable > 0 {
		parts = append(parts, "available "+compactMetric(vm.ContextAvailable))
	}
	if vm.ContextMaxPerFile > 0 {
		parts = append(parts, "slice "+compactMetric(vm.ContextMaxPerFile))
	}
	if vm.ContextSystemTokens > 0 || vm.ContextHistoryTokens > 0 {
		parts = append(parts, fmt.Sprintf("sys %s hist %s", compactMetric(vm.ContextSystemTokens), compactMetric(vm.ContextHistoryTokens)))
	}
	if vm.ContextResponseTokens > 0 || vm.ContextToolTokens > 0 {
		parts = append(parts, fmt.Sprintf("resp %s tools %s", compactMetric(vm.ContextResponseTokens), compactMetric(vm.ContextToolTokens)))
	}
	if c := strings.TrimSpace(vm.ContextCompression); c != "" {
		parts = append(parts, "zip "+c)
	}
	if file := firstUsefulRuntimeLine(vm.ContextTopFiles); file != "" {
		parts = append(parts, "top "+file)
	}
	if reason := firstUsefulRuntimeLine(vm.ContextReasons); reason != "" {
		parts = append(parts, "why "+reason)
	}
	return parts
}

func runtimeWindowUsage(vm runtimeViewModel) (int, int) {
	used := vm.ContextWindowTokens
	if used <= 0 {
		used = vm.ContextSystemTokens + vm.ContextHistoryTokens + vm.ContextTokens + vm.ContextResponseTokens + vm.ContextToolTokens
	}
	if used <= 0 {
		used = vm.ContextTokens
	}
	if used <= 0 {
		return 0, 0
	}
	if vm.MaxContext <= 0 {
		return used, -1
	}
	return used, vm.MaxContext - used
}

func runtimeContextUsed(vm runtimeViewModel) int {
	if used, _ := runtimeWindowUsage(vm); used > 0 {
		return used
	}
	return vm.ContextTokens
}

func runtimeStripTokenParts(vm runtimeViewModel) []string {
	parts := []string{}
	if vm.LastInputTokens > 0 || vm.LastOutputTokens > 0 || vm.LastTotalTokens > 0 {
		total := vm.LastTotalTokens
		if total <= 0 {
			total = vm.LastInputTokens + vm.LastOutputTokens
		}
		// Include the per-turn cost inline when a price is configured —
		// a user iterating on a question wants to know whether they
		// just spent $0.001 or $0.30 on the last turn, not just on the
		// whole session. Without this they have to subtract before/
		// after of the cumulative number.
		lastSegment := fmt.Sprintf("last in %s out %s total %s",
			compactMetric(vm.LastInputTokens),
			compactMetric(vm.LastOutputTokens),
			compactMetric(total),
		)
		if vm.CostPer1kTokens > 0 && total > 0 {
			lastCost := (float64(total) / 1000) * vm.CostPer1kTokens
			lastSegment += " · " + formatUSDCost(lastCost)
		}
		parts = append(parts, lastSegment)
	}
	sessionTotal := vm.SessionTotalTokens
	if sessionTotal <= 0 {
		sessionTotal = vm.SessionInputTokens + vm.SessionOutputTokens
	}
	if vm.SessionInputTokens > 0 || vm.SessionOutputTokens > 0 || sessionTotal > 0 {
		parts = append(parts, fmt.Sprintf("session in %s out %s total %s", compactMetric(vm.SessionInputTokens), compactMetric(vm.SessionOutputTokens), compactMetric(sessionTotal)))
	}
	if vm.CostPer1kTokens > 0 && sessionTotal > 0 {
		cost := (float64(sessionTotal) / 1000) * vm.CostPer1kTokens
		parts = append(parts, "cost "+formatUSDCost(cost))
	}
	if vm.TranscriptInputTokens > 0 || vm.TranscriptOutputTokens > 0 {
		parts = append(parts, fmt.Sprintf("visible in %s out %s", compactMetric(vm.TranscriptInputTokens), compactMetric(vm.TranscriptOutputTokens)))
	}
	if vm.ComposerTokens > 0 {
		parts = append(parts, "composer "+compactMetric(vm.ComposerTokens))
	}
	return parts
}

func runtimeStripActivityParts(vm runtimeViewModel) []string {
	seen := map[string]struct{}{}
	parts := make([]string, 0, 2)
	add := func(line string) {
		line = strings.TrimSpace(humanizeWorkflowText(line))
		if line == "" {
			return
		}
		key := strings.ToLower(line)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		parts = append(parts, line)
	}
	for _, line := range vm.WorkflowTimeline {
		add(line)
		if len(parts) >= 2 {
			return parts
		}
	}
	for _, line := range vm.WorkflowRecent {
		add(line)
		if len(parts) >= 2 {
			return parts
		}
	}
	return parts
}

func runtimeStripActionParts(vm runtimeViewModel) []string {
	seen := map[string]struct{}{}
	parts := make([]string, 0, 3)
	add := func(action string) {
		action = strings.TrimSpace(action)
		if action == "" {
			return
		}
		key := strings.ToLower(action)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		parts = append(parts, action)
	}

	state := strings.ToLower(strings.TrimSpace(vm.State))
	status := strings.ToLower(strings.Join([]string{
		vm.WorkflowStatus,
		vm.WorkflowExecution,
		vm.LastStatus,
		strings.Join(vm.WorkflowRecent, " "),
	}, " "))
	switch {
	case state == "needs key" || state == "needs provider":
		add("/providers to pick or configure a model")
		add("/reload after updating env")
	case vm.ApprovalPending:
		add("approve or deny the pending tool")
	case vm.Parked:
		add("/continue to resume")
	case vm.QueuedCount > 0:
		add("/queue to inspect pending prompts")
	case strings.Contains(status, "stalled") || strings.Contains(status, "error") || strings.Contains(status, "failed"):
		add("F7 Activity errors for details")
		add("/retry after fixing the cause")
	case strings.Contains(status, "throttled") || strings.Contains(status, "rate limit"):
		add("wait for retry or switch provider")
	case vm.DriveBlocked > 0:
		add("F5 Workflow to unblock TODOs")
	case vm.Dirty || vm.Inserted > 0 || vm.Deleted > 0:
		add("/diff to inspect workspace changes")
	}
	for _, action := range runtimeContextPressureActions(vm) {
		add(action)
	}
	if len(parts) > 3 {
		return parts[:3]
	}
	return parts
}

func runtimeContextPressureActions(vm runtimeViewModel) []string {
	used, _ := runtimeWindowUsage(vm)
	if used <= 0 || vm.MaxContext <= 0 {
		return nil
	}
	pct := int((int64(used) * 100) / int64(vm.MaxContext))
	// Hint copy reflects what actually frees engine context (NOT the
	// TUI-only /compact slash command — that only collapses visible
	// transcript lines, the engine's running working set is untouched).
	// Real reducers: narrow @file mentions, drop pinned files, /chat
	// new for a fresh conversation, or wait for auto-compact to fire.
	switch {
	case pct >= 100:
		return []string{
			fmt.Sprintf("context over window: %s/%s", compactMetric(used), compactMetric(vm.MaxContext)),
			"/conv new or narrow @files / drop pins",
		}
	case pct >= 90:
		return []string{
			fmt.Sprintf("context critical: %d%% full", pct),
			"/conv new for fresh window · Ctrl+I to inspect budget",
		}
	case pct >= 70:
		return []string{
			fmt.Sprintf("context high: %d%% full", pct),
			"Ctrl+I Context budget — narrow @files before the next big ask",
		}
	default:
		return nil
	}
}

func runtimeStateStyle(style string) func(...string) string {
	switch style {
	case "accent":
		return accentStyle.Bold(true).Render
	case "info":
		return infoStyle.Bold(true).Render
	case "warn":
		return warnStyle.Bold(true).Render
	case "fail":
		return failStyle.Bold(true).Render
	default:
		return okStyle.Bold(true).Render
	}
}

func runtimeContextLabel(tokens, maxTokens int) string {
	if maxTokens <= 0 {
		if tokens <= 0 {
			return "ctx unknown"
		}
		return "ctx " + compactMetric(tokens)
	}
	pct := 0
	if tokens > 0 {
		pct = int((int64(tokens) * 100) / int64(maxTokens))
	}
	return fmt.Sprintf("ctx %s/%s %d%%", compactMetric(tokens), compactMetric(maxTokens), pct)
}

func runtimeGitLabel(vm runtimeViewModel) string {
	if strings.TrimSpace(vm.Branch) == "" && !vm.Dirty && vm.Inserted == 0 && vm.Deleted == 0 {
		return ""
	}
	branch := strings.TrimSpace(vm.Branch)
	if branch == "" {
		branch = "worktree"
	}
	if vm.Dirty {
		branch += "*"
	}
	return fmt.Sprintf("git %s +%d -%d", branch, vm.Inserted, vm.Deleted)
}

func firstUsefulSubagentLine(lines []string) string {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.EqualFold(line, "idle") || strings.EqualFold(line, "recent:") {
			continue
		}
		line = strings.TrimPrefix(line, "Subagent ")
		return line
	}
	return ""
}

func firstUsefulTaskLine(lines []string) string {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), "plan ") {
			continue
		}
		return line
	}
	return ""
}

func firstUsefulRuntimeLine(lines []string) string {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}
