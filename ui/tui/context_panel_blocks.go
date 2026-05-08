package tui

// context_panel_blocks.go — pure render helpers for the Context tab's
// block sections. Companion siblings:
//
//   - context_panel.go       view orchestration (renderContextView /
//                            renderContextViewInner / Sized) + state
//                            mutators (runContextPreview / load /
//                            openActionMenu) + the top banner
//   - context_panel_keys.go  handleContextKey + handleContextInputKey
//                            keyboard routers
//
// renderContextBudgetBlock paints the budget preview (provider window
// + reserve breakdown + per-bucket caps + mode flags). render
// ContextBreakdownBlock paints the live "how full is my context"
// view. renderContextActiveBlock dumps the exact chunks captured
// from the last LLM request. The small primitives (contextRatioBar,
// contextSeverityStyle, formatContextHintRow) live here too because
// only these block builders use them.

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// contextRatioBar renders a percentage as a 10-wide meter so the eye
// picks out "reserve eats 80% of the window" without reading numbers.
func contextRatioBar(used, total int) string {
	width := 10
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	filled := (used * width) / total
	if filled < 0 {
		filled = 0
	}
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// contextSeverityStyle picks the warn colour for error/warn hints and
// the subtle style for info/note.
func contextSeverityStyle(sev string) string {
	switch strings.ToLower(strings.TrimSpace(sev)) {
	case "error", "critical", "warn", "warning":
		return warnStyle.Render(strings.ToUpper(sev))
	case "info":
		return accentStyle.Render("INFO")
	default:
		return subtleStyle.Render(strings.ToUpper(nonEmpty(sev, "note")))
	}
}

// formatContextHintRow shapes one hint line: `[WARN] code · message`.
func formatContextHintRow(h engine.ContextRecommendation, width int) string {
	tag := contextSeverityStyle(h.Severity)
	line := "  " + tag
	if h.Code != "" {
		line += "  " + subtleStyle.Render(h.Code)
	}
	if h.Message != "" {
		line += "  " + h.Message
	}
	if width > 0 {
		line = truncateSingleLine(line, width)
	}
	return line
}

// renderContextBudgetBlock + renderContextBreakdownBlock live in
// context_panel_blocks_budget.go.

// renderContextActiveBlock shows the exact chunks from the last LLM request.
func renderContextActiveBlock(debug engine.ContextDebugStatus, width int) []string {
	out := []string{}
	meta := []string{}
	if debug.Provider != "" || debug.Model != "" {
		provider := nonEmpty(debug.Provider, "?")
		if debug.Model != "" {
			provider += "/" + debug.Model
		}
		meta = append(meta, provider)
	}
	if !debug.BuiltAt.IsZero() {
		meta = append(meta, "built="+debug.BuiltAt.Format("15:04:05"))
	}
	if debug.Task != "" {
		meta = append(meta, "task="+debug.Task)
	}
	if debug.ProviderMaxContext > 0 {
		meta = append(meta, fmt.Sprintf("window=%d", debug.ProviderMaxContext))
	}
	if debug.MaxTokensTotal > 0 {
		meta = append(meta, fmt.Sprintf("budget=%d", debug.MaxTokensTotal))
	}
	if debug.TokenCount > 0 || debug.FileCount > 0 {
		meta = append(meta, fmt.Sprintf("used=%d tok / %d files", debug.TokenCount, debug.FileCount))
	}
	if len(meta) > 0 {
		out = append(out, "  "+accentStyle.Render(strings.Join(meta, "  |  ")))
	}
	if strings.TrimSpace(debug.Query) != "" {
		out = append(out, "  "+subtleStyle.Render("query: ")+debug.Query)
	}
	if len(debug.Reasons) > 0 {
		out = append(out, "", "  "+subtleStyle.Render("why this context shape:"))
		for _, reason := range debug.Reasons {
			if strings.TrimSpace(reason) == "" {
				continue
			}
			out = append(out, "   "+subtleStyle.Render("- "+reason))
		}
	}
	if len(debug.Files) == 0 {
		out = append(out, "", warnStyle.Render("  no active context chunks captured yet"))
		return out
	}
	out = append(out, "", "  "+subtleStyle.Render("active chunks (exact content sent to the model):"))
	for i, file := range debug.Files {
		out = append(out, "")
		rangeLabel := ""
		if file.LineStart > 0 || file.LineEnd > 0 {
			rangeLabel = fmt.Sprintf(":%d-%d", file.LineStart, file.LineEnd)
		}
		header := fmt.Sprintf("[%02d] %s%s", i+1, nonEmpty(file.Path, "(unknown)"), rangeLabel)
		stats := []string{}
		if file.Language != "" {
			stats = append(stats, "lang="+file.Language)
		}
		if file.TokenCount > 0 {
			stats = append(stats, fmt.Sprintf("tok=%d", file.TokenCount))
		}
		if file.Score > 0 {
			stats = append(stats, fmt.Sprintf("score=%.2f", file.Score))
		}
		if file.Compression != "" {
			stats = append(stats, "compression="+file.Compression)
		}
		if file.Source != "" {
			stats = append(stats, "source="+file.Source)
		}
		if len(stats) > 0 {
			header += "  " + subtleStyle.Render(strings.Join(stats, "  "))
		}
		out = append(out, "  "+accentStyle.Render(header))
		if strings.TrimSpace(file.Reason) != "" {
			out = append(out, "  "+subtleStyle.Render("reason: ")+file.Reason)
		}
		content := strings.ReplaceAll(file.Content, "\r\n", "\n")
		if strings.TrimSpace(content) == "" {
			out = append(out, "  "+warnStyle.Render("(chunk content is empty)"))
			continue
		}
		for lineIdx, line := range strings.Split(content, "\n") {
			if file.LineStart > 0 {
				out = append(out, fmt.Sprintf("  %5d | %s", file.LineStart+lineIdx, line))
			} else {
				out = append(out, "        | "+line)
			}
		}
	}
	_ = width
	return out
}
