package theme

// render_chat_header.go — chat-tab header bar + workflow focus card.
// Companion siblings:
//
//   - render.go               role helpers, section header, todo strip,
//                             runtime card, TruncateSingleLine
//   - render_message.go       message header, bubble, wrap helpers,
//                             divider, input box
//   - render_starters.go      starter prompt grid, streaming + resume
//                             banners
//
// RenderChatHeader paints the dense one-line CHAT title bar (provider
// pill, model pill, token meter, mode segment, plan/approval/parking
// state, intent + drive badges) plus an optional pinned-file second
// line. RenderChatWorkflowFocusCard renders the F4 Workflow Focus
// card whose body switches on StatsPanelMode.

import (
	"fmt"
	"strings"
)

func RenderChatHeader(info ChatHeaderInfo, width int) string {
	brand := TitleStyle.Render(" CHAT ")
	segments := []string{brand}

	if !info.Slim {
		providerTrim := strings.TrimSpace(info.Provider)
		modelTrim := strings.TrimSpace(info.Model)
		provider := blankFallback(providerTrim, "no-provider")
		model := blankFallback(modelTrim, "no-model")

		providerPill := AccentStyle.Bold(true).Render(provider)
		modelPill := BoldStyle.Render(model)
		switch {
		case providerTrim == "":
			providerPill = FailStyle.Bold(true).Render("⚠ no provider")
			modelPill = SubtleStyle.Render(model)
		case !info.Configured:
			providerPill = WarnStyle.Bold(true).Render(provider + "⚠")
		}
		who := providerPill + SubtleStyle.Render(" / ") + modelPill
		meter := RenderTokenMeter(chatHeaderContextUsed(info), info.MaxContext)

		tools := SubtleStyle.Render("tools off")
		if info.ToolsEnabled {
			tools = OkStyle.Render("tools on")
		}
		segments = append(segments, who, meter)
		segments = append(segments, RenderChatModeSegment(info))
		segments = append(segments, tools)
	} else {
		if info.Streaming || info.AgentActive {
			segments = append(segments, RenderChatModeSegment(info))
		}
	}

	if info.PlanMode {
		segments = append(segments, WarnStyle.Bold(true).Render("◈ PLAN — /code exits"))
	}
	if info.ApprovalPending {
		segments = append(segments, FailStyle.Bold(true).Render("⚠ APPROVAL — y/n"))
	} else if info.ApprovalGated {
		segments = append(segments, WarnStyle.Render("⚠ gate on"))
	}
	// Suppress the parked chip while the resume banner is up — the
	// banner already carries the same message in a more prominent
	// location and the duplicate would be noise. Banner dismissed
	// (esc) clears BannerActive and the chip re-emerges.
	if info.Parked && !info.BannerActive {
		segments = append(segments, WarnStyle.Bold(true).Render("⏸ parked — /continue"))
	}
	if info.ActiveTools > 0 {
		segments = append(segments, InfoStyle.Bold(true).Render(fmt.Sprintf("◌ tools %d", info.ActiveTools)))
	}
	if info.ActiveSubagents > 0 {
		label := fmt.Sprintf("subagents %d", info.ActiveSubagents)
		if summary := strings.TrimSpace(info.SubagentSummary); summary != "" {
			label += " " + summary
		}
		segments = append(segments, AccentStyle.Bold(true).Render(label))
	}
	if info.QueuedCount > 0 {
		segments = append(segments, AccentStyle.Bold(true).Render(fmt.Sprintf("▸ queued %d", info.QueuedCount)))
	}
	if info.PendingNotes > 0 {
		segments = append(segments, InfoStyle.Render(fmt.Sprintf("✎ btw %d", info.PendingNotes)))
	}
	if last := strings.TrimSpace(info.IntentLast); last != "" {
		segments = append(segments, SubtleStyle.Render("⚙ intent "+last))
	}
	if strings.TrimSpace(info.DriveRunID) != "" {
		label := fmt.Sprintf("▸ drive %d/%d", info.DriveDone, info.DriveTotal)
		if id := strings.TrimSpace(info.DriveTodoID); id != "" {
			label += " · " + id
		}
		if info.DriveBlocked > 0 {
			label += fmt.Sprintf(" (blocked %d)", info.DriveBlocked)
			segments = append(segments, WarnStyle.Bold(true).Render(label))
		} else {
			segments = append(segments, AccentStyle.Bold(true).Render(label))
		}
	}
	sep := SubtleStyle.Render("  ·  ")
	head := TruncateSingleLine(strings.Join(segments, sep), width)
	if pinned := strings.TrimSpace(info.Pinned); pinned != "" {
		pinLine := AccentStyle.Render("  ◆ pinned: ") + BoldStyle.Render(FileMarker(pinned))
		return head + "\n" + pinLine
	}
	return head
}

func chatHeaderContextUsed(info ChatHeaderInfo) int {
	if info.ContextWindowTokens > 0 {
		return info.ContextWindowTokens
	}
	return info.ContextTokens
}

// FileMarker returns a rel path with a file:// prefix for display.
// Defined here to avoid a import cycle with chat_helpers. callers in
// this package should use chat_helpers.FileMarker from ui/tui instead.
var FileMarker func(string) string = func(rel string) string { return rel }

func blankFallback(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func RenderChatModeSegment(info ChatHeaderInfo) string {
	glyph := SpinnerFrame(info.SpinnerFrame)
	switch {
	case info.Streaming:
		return InfoStyle.Bold(true).Render(glyph + " streaming")
	case info.AgentActive:
		phase := blankFallback(strings.TrimSpace(info.AgentPhase), "working")
		if info.AgentStep > 0 && info.AgentMax > 0 {
			return AccentStyle.Bold(true).Render(fmt.Sprintf("%s tool loop %s - %d/%d", glyph, phase, info.AgentStep, info.AgentMax))
		}
		if info.AgentStep > 0 {
			return AccentStyle.Bold(true).Render(fmt.Sprintf("%s tool loop %s - step %d", glyph, phase, info.AgentStep))
		}
		return AccentStyle.Bold(true).Render(glyph + " tool loop " + phase)
	default:
		return OkStyle.Render("● ready")
	}
}

func RenderChatWorkflowFocusCard(info StatsPanelInfo, width int) string {
	if width < 36 {
		width = 36
	}
	mode := info.Mode
	if string(mode) == "" || mode == StatsPanelModeOverview {
		return ""
	}
	title := "Workflow Focus"
	switch mode {
	case StatsPanelModeTodos:
		title += " · TODOS"
	case StatsPanelModeTasks:
		title += " · TASKS"
	case StatsPanelModeSubagents:
		title += " · SUBAGENTS"
	case StatsPanelModeProviders:
		title += " · PROVIDERS"
	}
	lines := []string{SectionHeader("»", title)}
	if status := info.WorkflowStatus; status != "" {
		lines = append(lines, "  "+TruncateSingleLine(status, width))
	}
	if meter := info.WorkflowMeter; meter != "" {
		lines = append(lines, "  "+TruncateSingleLine(meter, width))
	}
	if execution := info.WorkflowExecution; execution != "" {
		lines = append(lines, "  "+AccentStyle.Render(TruncateSingleLine(execution, width)))
	}
	appendBlock := func(items []string, fallback string) {
		if len(items) == 0 {
			if fallback != "" {
				lines = append(lines, "  "+TruncateSingleLine(fallback, width))
			}
			return
		}
		for i, line := range items {
			if i >= 4 {
				lines = append(lines, "  ...")
				break
			}
			lines = append(lines, "  "+TruncateSingleLine(line, width))
		}
	}
	switch mode {
	case StatsPanelModeTodos:
		appendBlock(info.TodoLines, "No shared todo list yet.")
	case StatsPanelModeTasks:
		appendBlock(info.TaskLines, "No active task graph yet.")
	case StatsPanelModeSubagents:
		appendBlock(info.SubagentLines, "No subagent activity yet.")
	case StatsPanelModeProviders:
		if len(info.Providers) == 0 {
			appendBlock(nil, "No providers registered.")
		} else {
			var providerLines []string
			for i, row := range info.Providers {
				var prefix string
				if i == info.ProvidersSelectedIndex {
					prefix = "» "
				}
				line := prefix + row.Name
				if len(row.Models) > 0 {
					line += " · " + strings.Join(row.Models, " › ")
				}
				if row.Status == "no-key" {
					line += " ⚠ no-key"
				} else if row.Status == "offline" {
					line += " ○ offline"
				} else {
					line += " ● ready"
				}
				providerLines = append(providerLines, line)
			}
			appendBlock(providerLines, "")

			// Detail pane for the selected provider
			if info.ProvidersSelectedIndex >= 0 && info.ProvidersSelectedIndex < len(info.Providers) {
				sel := info.Providers[info.ProvidersSelectedIndex]
				detail := []string{
					AccentStyle.Bold(true).Render("▸ " + sel.Name),
				}
				if sel.Primary {
					detail = append(detail, SubtleStyle.Render("  primary"))
				}
				if sel.Active {
					detail = append(detail, AccentStyle.Render("  ◉ active"))
				}
				if len(sel.Models) > 0 {
					detail = append(detail, SubtleStyle.Render("  models:    ")+strings.Join(sel.Models, " › "))
				}
				if len(sel.FallbackModels) > 0 {
					detail = append(detail, SubtleStyle.Render("  fallback:  ")+strings.Join(sel.FallbackModels, " › "))
				}
				detail = append(detail, SubtleStyle.Render("  protocol:  "+sel.Protocol))
				detail = append(detail, SubtleStyle.Render(fmt.Sprintf("  max_ctx:   %d", sel.MaxContext)))
				if sel.HasAPIKey {
					detail = append(detail, OkStyle.Render("  api_key:   ● set"))
				} else {
					detail = append(detail, FailStyle.Render("  api_key:   ⚠ missing"))
				}
				lines = append(lines, strings.Join(detail, "\n"))
				lines = append(lines, "")
				lines = append(lines, SubtleStyle.Render("  enter:switch · m:model · f:fallback · s:save"))
			}
		}
	}
	if len(info.WorkflowTimeline) > 0 {
		lines = append(lines, "  live log:")
		for i, line := range info.WorkflowTimeline {
			if i >= 4 {
				lines = append(lines, "    ...")
				break
			}
			lines = append(lines, "    "+TruncateSingleLine(line, width-2))
		}
	}
	if len(info.WorkflowRecent) > 0 {
		lines = append(lines, "  recent:")
		for i, line := range info.WorkflowRecent {
			if i >= 2 {
				break
			}
			lines = append(lines, "    "+TruncateSingleLine(line, width-2))
		}
	}
	return strings.Join(lines, "\n")
}
