package theme

// stats_panel_runtime.go — row builders for runtime/operator state.
// Currently the live surface is providerRows / providerActiveRows /
// loopRows; the wider row family (tools/git/session/context/tokens/
// workflow/todos/tasks/...) was scaffolded but never wired into a
// rendering caller and was removed.

import (
	"fmt"
	"strings"
)

func providerRows(info StatsPanelInfo) []string {
	provider := strings.TrimSpace(info.Provider)
	model := strings.TrimSpace(info.Model)
	// Phase B dedup slice 5: Overview mode's PROVIDER section used to
	// stack provider + model on two bold rows that duplicated the
	// runtime-strip Top row (chat tab) and the dedicated ACTIVE section
	// in Providers mode (alt+p). Compressed to a single line carrying
	// the same signal so the stats panel still names what's active
	// without occupying three rows for content already visible elsewhere.
	// Failure/needs-key states stay loud — those are the cases where the
	// PROVIDER section is the actionable surface, not just a label.
	switch {
	case provider == "":
		return []string{
			FailStyle.Bold(true).Render("no provider"),
			SubtleStyle.Render("/provider to configure runtime"),
		}
	case !info.Configured:
		return []string{
			WarnStyle.Bold(true).Render(provider + " needs key"),
			BoldStyle.Render(blankFallback(model, "-")),
			SubtleStyle.Render("unconfigured - add API key"),
		}
	default:
		label := provider + " / " + blankFallback(model, "-")
		return []string{
			SubtleStyle.Render(label),
		}
	}
}

func providerActiveRows(info StatsPanelInfo) []string {
	rows := providerRows(info)
	// Phase B dedup slice: window/used numbers used to ride here too,
	// but the BUDGET section below carries the same digits as a bar plus
	// an exact `input n/max | free m` row. The only ACTIVE-unique meta
	// is the per-1k cost — keep that when set, drop the rest.
	if info.CostPer1kTokens > 0 {
		rows = append(rows, SubtleStyle.Render(FormatUSDCost(info.CostPer1kTokens)+"/1k tok"))
	}
	return rows
}

func loopRows(info StatsPanelInfo) []string {
	rows := []string{RenderChatModeSegment(ChatHeaderInfo{
		Streaming:    info.Streaming,
		AgentActive:  info.AgentActive,
		AgentPhase:   info.AgentPhase,
		AgentStep:    info.AgentStep,
		AgentMax:     info.AgentMaxSteps,
		SpinnerFrame: info.SpinnerFrame,
	})}
	if info.AgentActive && info.AgentMaxSteps > 0 {
		rows = append(rows, fmt.Sprintf("call budget %d/%d", info.AgentStep, info.AgentMaxSteps))
	}
	if info.ToolRounds > 0 {
		rows = append(rows, fmt.Sprintf("tool rounds %d", info.ToolRounds))
	}
	if tool := strings.TrimSpace(info.LastTool); tool != "" {
		icon, style := ChipIconStyle(info.LastStatus)
		line := icon + " " + tool
		if info.LastDurationMs > 0 {
			line += fmt.Sprintf(" | %dms", info.LastDurationMs)
		}
		rows = append(rows, style.Render(line))
	}
	// When the resume banner is up, the stats panel skips its own
	// "parked" row + "/continue to resume" hint — they would just
	// echo the banner. The single state-line at the top of the panel
	// already reads "parked" so the user keeps the one-line distill.
	if info.Parked && !info.BannerActive {
		rows = append(rows, WarnStyle.Bold(true).Render("parked"), SubtleStyle.Render("/continue to resume"))
	}
	if info.QueuedCount > 0 {
		rows = append(rows, AccentStyle.Bold(true).Render(fmt.Sprintf("queued %d", info.QueuedCount)))
	}
	if info.PendingNotes > 0 {
		rows = append(rows, InfoStyle.Render(fmt.Sprintf("btw notes %d", info.PendingNotes)))
	}
	return rows
}

