package theme

// stats_panel_runtime_context.go — context + token row builders for
// the runtime stats panel. Sibling of stats_panel_runtime.go which
// keeps the provider/loop/tools/git/session/provider-list/provider-
// routing builders. Splitting context+tokens into a sibling keeps
// the per-row dial logic out of the dispatcher file so the
// budget-and-window arithmetic is auditable in isolation.

import (
	"fmt"
	"strings"
)

func contextRows(info StatsPanelInfo) []string {
	rows := []string{RenderContextBarFrame(statsPanelContextUsed(info), info.MaxContext, 12, info.SpinnerFrame)}
	workspaceEvidenceOff := contextReasonContains(info.ContextReasons, "conversation history only")
	if workspaceEvidenceOff {
		rows = append(rows, InfoStyle.Render("conversation history only"))
		rows = append(rows, SubtleStyle.Render("workspace evidence off"))
	} else if info.ContextFileCount > 0 || info.ContextBudgetTokens > 0 {
		files := fmt.Sprintf("files %d", info.ContextFileCount)
		if info.ContextMaxFiles > 0 {
			files += fmt.Sprintf("/%d", info.ContextMaxFiles)
		}
		rows = append(rows, InfoStyle.Render(files))
		if info.ContextBudgetTokens > 0 {
			rows = append(rows, InfoStyle.Render(fmt.Sprintf(
				"evidence %s/%s tok",
				CompactTokens(info.ContextTokens),
				CompactTokens(info.ContextBudgetTokens),
			)))
		}
	} else {
		rows = append(rows, SubtleStyle.Render("no context build reported yet"))
	}
	dials := []string{}
	if task := strings.TrimSpace(info.ContextTask); task != "" {
		dials = append(dials, "task "+task)
	}
	if compression := strings.TrimSpace(info.ContextCompression); compression != "" {
		dials = append(dials, "zip "+compression)
	}
	if info.ContextMaxTokensPerFile > 0 {
		dials = append(dials, fmt.Sprintf("slice %s", CompactTokens(info.ContextMaxTokensPerFile)))
	}
	if len(dials) > 0 {
		rows = append(rows, SubtleStyle.Render(strings.Join(dials, " | ")))
	}
	if info.ContextAvailableTokens > 0 {
		rows = append(rows, SubtleStyle.Render(fmt.Sprintf("available %s tok", CompactTokens(info.ContextAvailableTokens))))
	}
	if used, remaining := statsPanelWindowUsage(info); used > 0 {
		if info.MaxContext > 0 {
			line := fmt.Sprintf("window %s/%s tok", CompactTokens(used), CompactTokens(info.MaxContext))
			if remaining >= 0 {
				line += " | left " + CompactTokens(remaining)
			} else {
				line += " | over " + CompactTokens(-remaining)
			}
			rows = append(rows, SubtleStyle.Render(line))
		} else {
			rows = append(rows, SubtleStyle.Render(fmt.Sprintf("window %s tok", CompactTokens(used))))
		}
	}
	if info.ContextSystemTokens > 0 || info.ContextHistoryTokens > 0 || info.ContextResponseTokens > 0 || info.ContextToolTokens > 0 {
		rows = append(rows, SubtleStyle.Render(fmt.Sprintf(
			"window sys %s | hist %s",
			CompactTokens(info.ContextSystemTokens),
			CompactTokens(info.ContextHistoryTokens),
		)))
		rows = append(rows, SubtleStyle.Render(fmt.Sprintf(
			"evidence %s | resp %s | tools %s",
			CompactTokens(info.ContextTokens),
			CompactTokens(info.ContextResponseTokens),
			CompactTokens(info.ContextToolTokens),
		)))
	}
	if len(info.ContextTopFiles) > 0 {
		files := make([]string, 0, len(info.ContextTopFiles))
		for _, path := range info.ContextTopFiles {
			if path = strings.TrimSpace(path); path != "" {
				files = append(files, TruncateSingleLine(path, 28))
			}
		}
		if len(files) > 0 {
			rows = append(rows, AccentStyle.Render("top: "+strings.Join(files, ", ")))
		}
	}
	if len(info.ContextReasons) > 0 {
		rows = append(rows, SubtleStyle.Render("why: "+TruncateSingleLine(info.ContextReasons[0], 42)))
	}
	return rows
}

func tokenRows(info StatsPanelInfo) []string {
	rows := []string{}
	if info.Streaming && (info.LiveInputTokens > 0 || info.LiveOutputTokens > 0 || info.LiveTotalTokens > 0) {
		total := info.LiveTotalTokens
		if total <= 0 {
			total = info.LiveInputTokens + info.LiveOutputTokens
		}
		rows = append(rows, InfoStyle.Bold(true).Render(fmt.Sprintf(
			"live input ~%s | output ~%s",
			CompactTokens(info.LiveInputTokens),
			CompactTokens(info.LiveOutputTokens),
		)))
		rows = append(rows, InfoStyle.Render(fmt.Sprintf(
			"live total ~%s | estimating",
			CompactTokens(total),
		)))
		rows = append(rows, SubtleStyle.Render("estimate until provider done"))
	}
	if info.LastInputTokens > 0 || info.LastOutputTokens > 0 || info.LastTotalTokens > 0 {
		rows = append(rows, InfoStyle.Render(fmt.Sprintf(
			"last input %s | output %s",
			CompactTokens(info.LastInputTokens),
			CompactTokens(info.LastOutputTokens),
		)))
		if info.LastTotalTokens > 0 {
			rows = append(rows, SubtleStyle.Render("last total "+CompactTokens(info.LastTotalTokens)))
		}
	}
	if info.SessionInputTokens > 0 || info.SessionOutputTokens > 0 || info.SessionTotalTokens > 0 {
		rows = append(rows, SubtleStyle.Render(fmt.Sprintf(
			"session input %s | output %s",
			CompactTokens(info.SessionInputTokens),
			CompactTokens(info.SessionOutputTokens),
		)))
		if info.SessionTotalTokens > 0 {
			rows = append(rows, SubtleStyle.Render("session total "+CompactTokens(info.SessionTotalTokens)))
		}
		if info.CostPer1kTokens > 0 && info.SessionTotalTokens > 0 {
			cost := (float64(info.SessionTotalTokens) / 1000) * info.CostPer1kTokens
			rows = append(rows, SubtleStyle.Render(fmt.Sprintf(
				"cost %s @ %s/1k",
				FormatUSDCost(cost),
				FormatUSDCost(info.CostPer1kTokens),
			)))
		}
	}
	if info.TranscriptInputTokens > 0 || info.TranscriptOutputTokens > 0 || info.ComposerTokens > 0 {
		rows = append(rows, SubtleStyle.Render(fmt.Sprintf(
			"visible user %s | assistant %s",
			CompactTokens(info.TranscriptInputTokens),
			CompactTokens(info.TranscriptOutputTokens),
		)))
		if info.ComposerTokens > 0 {
			rows = append(rows, SubtleStyle.Render("composer "+CompactTokens(info.ComposerTokens)))
		}
	}
	return rows
}
