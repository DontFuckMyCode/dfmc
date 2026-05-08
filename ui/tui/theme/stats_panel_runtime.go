package theme

// stats_panel_runtime.go — row builders for runtime/operator state
// (provider, context, tokens, tool loop, registered tools, git, session,
// provider list, provider routing). Splits the live-state half of
// stats_panel.go away from the workflow/todo/task half so adding a new
// runtime row only touches this file.

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
			SubtleStyle.Render("alt+p providers · /provider"),
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
			SubtleStyle.Render(label + " · alt+p deep view"),
		}
	}
}

func providerActiveRows(info StatsPanelInfo) []string {
	rows := providerRows(info)
	meta := []string{}
	if info.MaxContext > 0 {
		meta = append(meta, "window "+CompactTokens(info.MaxContext))
	}
	if info.CostPer1kTokens > 0 {
		meta = append(meta, FormatUSDCost(info.CostPer1kTokens)+"/1k tok")
	}
	if info.ContextWindowTokens > 0 {
		meta = append(meta, "used "+CompactTokens(info.ContextWindowTokens))
	}
	if len(meta) > 0 {
		rows = append(rows, SubtleStyle.Render(strings.Join(meta, " | ")))
	}
	return rows
}

// contextRows + tokenRows live in stats_panel_runtime_context.go.

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
		icon, style := chipIconStyle(info.LastStatus)
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

func toolsRows(info StatsPanelInfo) []string {
	rows := []string{}
	if info.ToolsEnabled {
		line := OkStyle.Render("enabled")
		if info.ToolCount > 0 {
			line += SubtleStyle.Render(fmt.Sprintf(" | %d registered", info.ToolCount))
		}
		rows = append(rows, line)
	} else {
		rows = append(rows, SubtleStyle.Render("off"))
	}
	if info.CompressionSavedChars > 0 {
		pct := 0
		if info.CompressionRawChars > 0 {
			pct = int((int64(info.CompressionSavedChars) * 100) / int64(info.CompressionRawChars))
		}
		label := fmt.Sprintf("rtk saved %s chars", CompactTokens(info.CompressionSavedChars))
		if pct > 0 {
			label += fmt.Sprintf(" (%d%%)", pct)
		}
		rows = append(rows, OkStyle.Render(label))
	}
	return rows
}

func providerListRows(info StatsPanelInfo) []string {
	if len(info.Providers) == 0 {
		return []string{
			"No providers registered.",
			"Configure providers in .dfmc/config.yaml or dfmc providers setup.",
		}
	}
	rows := []string{SubtleStyle.Render("* active | + primary | - available")}
	for i, row := range info.Providers {
		cursor := "  "
		if i == info.ProvidersSelectedIndex {
			cursor = "> "
		}
		marker := "-"
		style := SubtleStyle
		switch {
		case row.Active:
			marker = "*"
			style = OkStyle
		case row.Primary:
			marker = "+"
			style = AccentStyle
		}
		line := cursor + marker + " " + style.Bold(row.Active).Render(row.Name)
		if len(row.Models) > 0 {
			line += SubtleStyle.Render(" | " + strings.Join(row.Models, " > "))
		}
		rows = append(rows, line)
		meta := []string{}
		if row.Protocol != "" {
			meta = append(meta, row.Protocol)
		}
		if row.MaxContext > 0 {
			meta = append(meta, "ctx "+CompactTokens(row.MaxContext))
		}
		if row.Status != "" {
			meta = append(meta, row.Status)
		}
		if len(meta) > 0 {
			rows = append(rows, SubtleStyle.Render("    "+strings.Join(meta, " | ")))
		}
		if row.Status == "no-key" {
			rows = append(rows, SubtleStyle.Render("    no API key: providers.profiles."+row.Name+".api_key"))
		}
		if len(row.FallbackModels) > 0 {
			rows = append(rows, SubtleStyle.Render("    fallback: "+strings.Join(row.FallbackModels, " > ")))
		}
	}
	return rows
}

func providerRoutingRows(info StatsPanelInfo) []string {
	rows := []string{}
	if provider := strings.TrimSpace(info.Provider); provider != "" {
		rows = append(rows, "active: "+provider+" / "+blankFallback(strings.TrimSpace(info.Model), "-"))
	} else {
		rows = append(rows, FailStyle.Render("active: none"))
	}
	primary := ""
	fallbacks := []string{}
	for _, row := range info.Providers {
		if row.Primary {
			primary = row.Name
		}
		if !row.Active && !row.Primary && row.Status != "no-key" {
			fallbacks = append(fallbacks, row.Name)
		}
	}
	if primary != "" {
		rows = append(rows, "primary: "+primary)
	}
	if len(fallbacks) > 0 {
		rows = append(rows, "ready fallback: "+strings.Join(firstNNonEmpty(fallbacks, 3), ", "))
	}
	rows = append(rows,
		SubtleStyle.Render("switch model:    "+AccentStyle.Render("alt+m")+SubtleStyle.Render(" — picker overlay")),
		SubtleStyle.Render("switch provider: "+AccentStyle.Render("alt+P")+SubtleStyle.Render(" — picker overlay")),
		SubtleStyle.Render("inline edit:     j/k select · enter switch · m cycle model"),
	)
	return rows
}

func gitRows(info StatsPanelInfo) []string {
	branch := strings.TrimSpace(info.Branch)
	if branch == "" {
		return nil
	}
	chip := BoldStyle.Render(branch)
	if info.Dirty {
		chip += WarnStyle.Render("*")
	}
	if info.Detached {
		chip += SubtleStyle.Render(" (detached)")
	}
	rows := []string{chip}
	if info.Inserted > 0 || info.Deleted > 0 {
		rows = append(rows, OkStyle.Render(fmt.Sprintf("+%d", info.Inserted))+
			SubtleStyle.Render(" / ")+
			FailStyle.Render(fmt.Sprintf("-%d", info.Deleted)))
	}
	return rows
}

func sessionRows(info StatsPanelInfo) []string {
	head := BoldStyle.Render(formatSessionDuration(info.SessionElapsed))
	if info.MessageCount > 0 {
		head += SubtleStyle.Render(fmt.Sprintf(" | %d msgs", info.MessageCount))
	}
	rows := []string{head}
	if pinned := strings.TrimSpace(info.Pinned); pinned != "" {
		rows = append(rows, AccentStyle.Render("pinned ")+BoldStyle.Render(FileMarker(pinned)))
	}
	return rows
}
