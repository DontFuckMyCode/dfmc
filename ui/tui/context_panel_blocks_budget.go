// context_panel_blocks_budget.go — budget + live-breakdown render
// blocks for the Context tab. Sibling of context_panel_blocks.go
// which keeps the small visual primitives (contextRatioBar,
// contextSeverityStyle, formatContextHintRow) and the
// renderContextActiveBlock chunk dump.
//
// Splitting the budget/breakdown blocks out keeps
// context_panel_blocks.go scoped to "what does an active chunk
// look like to the model" while this file owns "what does the
// budget shape look like before the request" and "what does the
// shape look like after the request lands." Both blocks consume
// engine.ContextBudgetInfo / engine.ContextBreakdown directly
// and emit pre-styled string slices the panel orchestrator
// stitches together.

package tui

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// renderContextBudgetBlock renders the provider/model line, the reserve
// vs. available bar, and the budget-caps block. Pulled into its own
// helper so the tests can exercise each section without rendering the
// whole view.
func renderContextBudgetBlock(info engine.ContextBudgetInfo, width int) []string {
	out := []string{}
	if info.Provider != "" || info.Model != "" {
		head := accentStyle.Render(nonEmpty(info.Provider, "?"))
		if info.Model != "" {
			head += subtleStyle.Render("/" + info.Model)
		}
		head += subtleStyle.Render(fmt.Sprintf("  max_context=%d", info.ProviderMaxContext))
		out = append(out, "  "+head)
	}
	if info.Task != "" {
		taskLine := subtleStyle.Render("task: ") + info.Task
		if info.ExplicitFileMentions > 0 {
			taskLine += subtleStyle.Render(fmt.Sprintf("  · %d [[file:]] markers", info.ExplicitFileMentions))
		}
		out = append(out, "  "+taskLine)
	}

	totalBudget := info.ProviderMaxContext
	if totalBudget <= 0 {
		totalBudget = info.ContextAvailableTokens + info.ReserveTotalTokens
	}
	bar := contextRatioBar(info.ReserveTotalTokens, totalBudget)
	reserveLine := subtleStyle.Render("reserve ") + bar + subtleStyle.Render(
		fmt.Sprintf(" %d/%d tokens (prompt=%d history=%d response=%d tool=%d)",
			info.ReserveTotalTokens, totalBudget,
			info.ReservePromptTokens, info.ReserveHistoryTokens,
			info.ReserveResponseTokens, info.ReserveToolTokens,
		),
	)
	out = append(out, "  "+reserveLine)

	availLine := subtleStyle.Render(fmt.Sprintf(
		"available for context: %d tokens",
		info.ContextAvailableTokens,
	))
	out = append(out, "  "+availLine)

	caps := fmt.Sprintf(
		"caps: files=%d  total=%d  per_file=%d  history=%d",
		info.MaxFiles, info.MaxTokensTotal, info.MaxTokensPerFile, info.MaxHistoryTokens,
	)
	out = append(out, "  "+subtleStyle.Render(caps))

	modes := []string{}
	if info.Compression != "" {
		modes = append(modes, "compression="+info.Compression)
	}
	if info.AutoIncludeFiles {
		modes = append(modes, "workspace_files=auto")
	} else {
		modes = append(modes, "workspace_files=explicit/tool")
	}
	if info.IncludeTests {
		modes = append(modes, "tests=on")
	} else {
		modes = append(modes, "tests=off")
	}
	if info.IncludeDocs {
		modes = append(modes, "docs=on")
	} else {
		modes = append(modes, "docs=off")
	}
	out = append(out, "  "+subtleStyle.Render("modes: "+strings.Join(modes, " · ")))

	if info.TaskTotalScale > 0 || info.TaskFileScale > 0 || info.TaskPerFileScale > 0 {
		scales := fmt.Sprintf(
			"task scale: total=%.2f files=%.2f per_file=%.2f",
			info.TaskTotalScale, info.TaskFileScale, info.TaskPerFileScale,
		)
		out = append(out, "  "+subtleStyle.Render(scales))
	}

	_ = width
	return out
}

// renderContextBreakdownBlock renders the real-time context breakdown
// as a visual bar chart + per-row breakdown. This is the "how full is
// my context window" view the user sees after entering a query.
func renderContextBreakdownBlock(bd engine.ContextBreakdown, width int) []string {
	out := []string{}

	// Provider / model / max line
	if bd.Provider != "" || bd.Model != "" {
		head := accentStyle.Render(nonEmpty(bd.Provider, "?"))
		if bd.Model != "" {
			head += subtleStyle.Render("/" + bd.Model)
		}
		head += subtleStyle.Render(fmt.Sprintf("  %dK ctx", bd.MaxContext/1000))
		out = append(out, "  "+head)
	}

	// Main bar: used vs total
	totalUsed := bd.UsedTotal
	bar := contextRatioBar(totalUsed, bd.MaxContext)
	if bd.MaxContext > 0 {
		pct := float64(totalUsed) / float64(bd.MaxContext) * 100
		out = append(out, "  "+bar+subtleStyle.Render(fmt.Sprintf("  %d%%  ·  %d / %d",
			int(pct), totalUsed/1000, bd.MaxContext/1000)))
	} else {
		out = append(out, "  "+bar+subtleStyle.Render("  ??%%"))
	}

	// Per-bucket rows
	rows := []struct {
		label string
		value int
		pct   float64
	}{
		{"system prompt", bd.SystemPrompt, bd.SystemPromptPct},
		{"history", bd.History, bd.HistoryPct},
		{"file context", bd.ContextChunks, bd.ContextChunksPct},
		{"tool reserve", bd.ToolReserve, 0},
		{"response", bd.Response, bd.ResponsePct},
	}
	for _, row := range rows {
		bar := contextRatioBar(int(float64(bd.MaxContext)*row.pct), bd.MaxContext)
		if row.value == 0 && row.pct == 0 {
			bar = strings.Repeat("░", 10)
		}
		pctStr := fmt.Sprintf("%d%%", int(row.pct*100))
		line := fmt.Sprintf("  %-12s %5d  %s  %s",
			subtleStyle.Render(row.label), row.value/1000,
			bar, subtleStyle.Render(pctStr))
		out = append(out, line)
	}

	// Files in context
	if len(bd.FilesInContext) > 0 {
		out = append(out, "")
		out = append(out, "  "+subtleStyle.Render(fmt.Sprintf("files (%d):", len(bd.FilesInContext))))
		for _, f := range bd.FilesInContext {
			if len(out) > 30 { // safety cap to avoid runaway output
				remaining := len(bd.FilesInContext) - (len(out) - 31)
				if remaining > 0 {
					out = append(out, "  "+subtleStyle.Render(fmt.Sprintf("  ... +%d more", remaining)))
				}
				break
			}
			out = append(out, "   "+subtleStyle.Render("▸ "+f))
		}
	}

	// Compression + task footer
	footer := ""
	if bd.Compression != "" {
		footer += "compression: " + bd.Compression
	}
	if bd.Task != "" {
		if footer != "" {
			footer += " · "
		}
		footer += "task: " + bd.Task
	}
	if footer != "" {
		out = append(out, "  "+subtleStyle.Render(footer))
	}

	_ = width
	return out
}
