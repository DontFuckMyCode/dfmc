package tui

// runtime_strip.go — turns a runtimeViewModel into the multi-line
// runtime strip rendered above the chat composer. Each row is built
// from a runtimeStrip*Parts function so the strip can be slimmed
// (`slim=true` drops everything after the "now" line) without the
// renderer caring about per-row contents.
//
// File layout: this file owns renderRuntimeStrip dispatcher + Top
// and Now row builders. Work / Task / Orchestration / Tool row
// builders live in runtime_strip_work.go; Budget / Token / Activity
// / Action row builders live in runtime_strip_budget.go. Helpers /
// badges live in runtime_strip_helpers.go; pure number/time
// formatters live in runtime_formatters.go. The view model + builder
// stay in runtime_view_model.go.

import (
	"fmt"
	"strings"
)

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
	// Phase B dedup slice 4 dropped the context-budget label (`45k/200k`)
	// from this row — the composer chip + footer chip already render the
	// same number as a bar. The provider/model segment stays here:
	// `theme.RenderChatHeader` is defined but not wired into the chat
	// timeline view, so this row is the only always-visible carrier of
	// provider/model on the Chat tab. Source-of-truth for provider/model
	// dedup is enforced inside the stats panel instead — Overview mode's
	// PROVIDER section drops to a one-line pointer when the active row
	// already names them, and the Providers-mode ACTIVE section is the
	// deep view.
	parts := []string{
		titleStyle.Render("DFMC CHAT"),
		runtimeStateStyle(vm.StateStyle)(state),
		subtleStyle.Render(provider + "/" + model),
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
	if badge := liveLoopTokensBadge(vm); badge != "" {
		parts = append(parts, badge)
	}
	if badge := compactsThisTurnBadge(vm); badge != "" {
		parts = append(parts, badge)
	}
	if vm.CacheHitsThisTurn > 0 {
		parts = append(parts, infoStyle.Render(fmt.Sprintf("cache ×%d", vm.CacheHitsThisTurn)))
	}
	if vm.TurnElapsedSec > 0 {
		label := "running " + formatTurnElapsed(vm.TurnElapsedSec)
		switch {
		case vm.TurnElapsedSec >= 600:
			parts = append(parts, warnStyle.Render(label))
		case vm.TurnElapsedSec >= 120:
			parts = append(parts, infoStyle.Render(label))
		default:
			parts = append(parts, subtleStyle.Render(label))
		}
	}
	if pace := toolPacePerMinute(vm); pace > 0 {
		parts = append(parts, subtleStyle.Render(fmt.Sprintf("≈%d/min", pace)))
	}
	if vm.TurnFilesEdited > 0 {
		word := "files"
		if vm.TurnFilesEdited == 1 {
			word = "file"
		}
		parts = append(parts, infoStyle.Render(fmt.Sprintf("edits ×%d %s", vm.TurnFilesEdited, word)))
	}
	if vm.ToolErrorsThisTurn > 0 {
		label := fmt.Sprintf("errs ×%d", vm.ToolErrorsThisTurn)
		if vm.ToolErrorsThisTurn >= 3 {
			parts = append(parts, warnStyle.Render(label))
		} else {
			parts = append(parts, infoStyle.Render(label))
		}
	}
	if vm.ActiveSubagents > 0 {
		label := fmt.Sprintf("agents ×%d", vm.ActiveSubagents)
		if vm.SubagentLimit > 0 {
			label = fmt.Sprintf("agents ×%d/%d", vm.ActiveSubagents, vm.SubagentLimit)
		}
		parts = append(parts, accentStyle.Render(label))
	}
	if strings.TrimSpace(vm.DriveRunID) != "" || vm.DriveTotal > 0 {
		label := fmt.Sprintf("drive %d/%d", vm.DriveDone, vm.DriveTotal)
		if vm.DriveBlocked > 0 {
			label += fmt.Sprintf(" · %d blocked", vm.DriveBlocked)
		}
		if vm.DriveBlocked > 0 {
			parts = append(parts, warnStyle.Render(label))
		} else {
			parts = append(parts, accentStyle.Render(label))
		}
	}
	if badge := autoResumeBadge(vm); badge != "" {
		parts = append(parts, badge)
	}
	if reason := strings.TrimSpace(vm.LastToolReason); reason != "" {
		parts = append(parts, subtleStyle.Render("→ "+truncateSingleLine(reason, 64)))
	}
	if stuck := strings.TrimSpace(vm.StuckTool); stuck != "" && vm.StuckCount > 0 {
		badge := fmt.Sprintf("stalled: %s ×%d", stuck, vm.StuckCount)
		if cls := strings.TrimSpace(vm.StuckErrClass); cls != "" {
			badge += " · " + cls
		}
		parts = append(parts, warnStyle.Render(badge))
	}
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

