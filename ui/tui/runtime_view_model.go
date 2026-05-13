package tui

// runtime_view_model.go — runtimeViewModel type and the (m Model)
// builder that fills it from statsPanelInfo + agentLoopState. Render
// helpers split into siblings:
//
//   runtime_strip.go         — renderRuntimeStrip + runtimeStrip*Parts
//                              (top/now/work/task/orchestration/tool/
//                              budget/token/activity/action rows).
//   runtime_strip_helpers.go — badges (liveLoopTokensBadge,
//                              compactsThisTurnBadge, autoResumeBadge),
//                              window usage helpers, state-style
//                              dispatcher, context-pressure actions,
//                              git label, "first useful line" filters.
//   runtime_formatters.go    — pure number/time formatters
//                              (computeTurnElapsedSec, toolPacePerMinute,
//                              formatTurnElapsed, formatTokenCount).
//
// runtimeStateLabel stays here because it's the bridge from
// statsPanelInfo to the State/StateStyle pair the runtime card
// renders — co-located with the ViewModel builder that reads it.

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
)

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
	ContextPayload         theme.ContextPayloadSnapshot
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
		ContextPayload:         theme.ContextPayloadFromStats(info),
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
		StuckTool:                m.agentLoop.stuckTool,
		StuckCount:               m.agentLoop.stuckCount,
		StuckErrClass:            m.agentLoop.stuckErrClass,
		CumulativeSteps:          m.agentLoop.cumulativeSteps,
		StepCeiling:              m.agentLoop.stepCeiling,
		CumulativeTokens:         m.agentLoop.cumulativeTokens,
		TokenCeiling:             m.agentLoop.tokenCeiling,
		UnvalidatedEdits:         len(m.agentLoop.unvalidatedEdits),
		Parked:                   info.Parked,
		ApprovalPending:          m.pendingApproval != nil,
		QueuedCount:              info.QueuedCount,
		PendingNotes:             info.PendingNotes,
		ToolsEnabled:             info.ToolsEnabled,
		ToolCount:                info.ToolCount,
		CompressionSavedChars:    info.CompressionSavedChars,
		CompressionRawChars:      info.CompressionRawChars,
		TodoTotal:                info.TodoTotal,
		TodoDone:                 info.TodoDone,
		TodoDoing:                info.TodoDoing,
		TodoPending:              info.TodoPending,
		TodoActive:               info.TodoActive,
		TaskLines:                append([]string(nil), info.TaskLines...),
		TaskTreeLines:            append([]string(nil), info.TaskTreeLines...),
		ActiveSubagents:          info.ActiveSubagents,
		SubagentLimit:            info.SubagentLimit,
		SubagentSummary:          info.SubagentSummary,
		SubagentLines:            append([]string(nil), info.SubagentLines...),
		DriveRunID:               info.DriveRunID,
		DriveDone:                info.DriveDone,
		DriveTotal:               info.DriveTotal,
		DriveBlocked:             info.DriveBlocked,
		PlanSubtasks:             info.PlanSubtasks,
		PlanParallel:             info.PlanParallel,
		PlanConfidence:           info.PlanConfidence,
		Branch:                   info.Branch,
		Dirty:                    info.Dirty,
		Inserted:                 info.Inserted,
		Deleted:                  info.Deleted,
		MessageCount:             info.MessageCount,
		Pinned:                   info.Pinned,
		SpinnerFrame:             info.SpinnerFrame,
	}
}

// runtimeStateLabel returns the (label, style-tag) for the chat state
// chip. Phase E item 3 — compose state precedence:
//
//	Streaming > AgentActive > Parked > Drive > Working > Queued >
//	Needs-key > Needs-provider > Ready
//
// Why Streaming first: when a new turn is being received, that fact
// supersedes the previous parked / queued / drive states even if their
// flags briefly co-exist during a transition. The chat header chip
// must reflect what the user is waiting on RIGHT NOW. AgentActive
// (tool loop) is the same family — also "model is doing live work" —
// so it sits second. Parked is a paused state and yields to anything
// active. Queued is the quietest of the live-work states and goes at
// the back so a queued note doesn't drown out a parked banner.
func runtimeStateLabel(info statsPanelInfo) (string, string) {
	switch {
	case info.Streaming:
		return "waiting", "info"
	case info.AgentActive:
		return "running", "accent"
	case info.Parked:
		return "parked", "warn"
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
