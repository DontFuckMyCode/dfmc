// render_patch.go — F4 Patch Lab panel, rebuilt as a 3-pane review
// surface so a user can see "what files · which hunk · what changed"
// at a glance without scrolling. The legacy stack-rendering view in
// patch_view.go (renderPatchView) now delegates to renderPatchViewV2.
//
// Layout strategy (mirrors render_files.go for visual consistency):
//   ≥120 cols → 3 panes (28% files · 44% diff · 28% metadata)
//   80-119    → 2 panes (35% files · 65% diff) + inline footer
//   <80       → 1 pane stack
//
// Panes:
//   FILES        list of files touched by latest assistant patch with
//                per-file +adds/-dels and hunk counts; cursor = active.
//   DIFF         side-by-side rendering of current hunk (red/green) +
//                hunk strip below it (e.g. "hunk 2/4 · @@ -10,3 +10,5").
//   METADATA     panelCards — SUMMARY (totals + status chip), REVIEW
//                (test/fixme/panic flags), ACTIONS (a/c/u/f/n/b/j/k).
//
// The PENDING / APPLIED / CHECKED / EMPTY chip surfaces the latest
// apply attempt outcome (drawn from m.notice + state) — quick read on
// "is the patch live yet?".

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderPatchViewV2 is the rebuilt F4 Patch panel.
func (m Model) renderPatchViewV2(width int) string {
	width = max(width, 50)
	height := 24 // legacy renderPatchView used 18-row sub-panes

	pal := paletteForTab("Patch", false)

	threePane := width >= 120
	twoPane := !threePane && width >= 80

	listW, diffW, metaW := patchPanelWidths(width, threePane, twoPane)

	banner := m.patchTopBanner(width)
	listBlock := m.renderPatchFilesPane(listW, height, pal)
	diffBlock := m.renderPatchDiffPane(diffW, height, pal)
	if threePane {
		metaBlock := m.renderPatchMetaPane(metaW, height, pal)
		body := lipgloss.JoinHorizontal(lipgloss.Top,
			listBlock, "  ", diffBlock, "  ", metaBlock)
		return banner + "\n" + body
	}
	if twoPane {
		footer := m.renderPatchMetaInline(width)
		body := lipgloss.JoinHorizontal(lipgloss.Top, listBlock, "  ", diffBlock)
		return banner + "\n" + body + "\n" + footer
	}
	return banner + "\n" + listBlock + "\n" + diffBlock
}

func patchPanelWidths(total int, threePane, twoPane bool) (listW, diffW, metaW int) {
	if threePane {
		listW = max(total*28/100, 28)
		metaW = max(total*28/100, 28)
		diffW = max(total-listW-metaW-4, 32)
		return
	}
	if twoPane {
		listW = max(total*35/100, 28)
		diffW = max(total-listW-2, 28)
		return
	}
	return total, total, 0
}

// --- BANNER ------------------------------------------------------------------

// patchTopBanner draws a one-line status chip + summary above the
// panes. PENDING (assistant patch loaded, not applied yet) /
// CHECKED (apply --check passed) / APPLIED / EMPTY (no patch).
func (m Model) patchTopBanner(width int) string {
	chip, chipStyle := m.patchStatusChip()
	chipRendered := chipStyle.Render(" " + chip + " ")
	title := titleStyle.Bold(true).Render("⇄ PATCH LAB")
	files := strings.Join(m.patchFilesOrNone(), ", ")
	if files == "(none)" {
		files = subtleStyle.Render("no active patch")
	} else {
		files = subtleStyle.Render(truncateForLine(files, width-32))
	}
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipRendered)-lipgloss.Width(files)-4, 1)
	line := title + "  " + files + strings.Repeat(" ", gap) + chipRendered
	return line + "\n" + subtleStyle.Render(strings.Repeat("─", width-2))
}

func (m Model) patchStatusChip() (string, lipgloss.Style) {
	if strings.TrimSpace(m.patchView.latestPatch) == "" {
		return "EMPTY", subtleStyle
	}
	notice := strings.ToLower(strings.TrimSpace(m.notice))
	switch {
	case strings.Contains(notice, "applied"):
		return "APPLIED", okStyle
	case strings.Contains(notice, "check") || strings.Contains(notice, "ok"):
		return "CHECKED", infoStyle
	case strings.Contains(notice, "error") || strings.Contains(notice, "fail"):
		return "FAILED", warnStyle
	}
	return "PENDING", infoStyle
}

// --- FILES PANE --------------------------------------------------------------

func (m Model) renderPatchFilesPane(width, height int, pal tabPaletteEntry) string {
	header := m.patchFilesHeader(width)
	lines := []string{
		header,
		subtleStyle.Render(strings.Repeat("─", width-2)),
		"",
	}
	if len(m.patchView.set) == 0 {
		lines = append(lines,
			"  "+subtleStyle.Render("No active patch."))
	} else {
		rowBudget := max(height-6, 6)
		start, end := scrollWindow(m.patchView.index, len(m.patchView.set), rowBudget)
		for i := start; i < end; i++ {
			lines = append(lines, m.renderPatchFileRow(i, width, pal))
		}
		lines = append(lines, "",
			"  "+subtleStyle.Render(fmt.Sprintf("%d / %d files",
				m.patchView.index+1, len(m.patchView.set))))
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) patchFilesHeader(width int) string {
	count := len(m.patchView.set)
	chip := okStyle
	chipText := fmt.Sprintf(" %d ", count)
	if count == 0 {
		chip = warnStyle
		chipText = " 0 "
	}
	title := titleStyle.Bold(true).Render(" ◎ FILES")
	chipRendered := chip.Render(chipText)
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipRendered)-2, 1)
	return title + strings.Repeat(" ", gap) + chipRendered
}

func (m Model) renderPatchFileRow(i, width int, pal tabPaletteEntry) string {
	section := m.patchView.set[i]
	selected := i == m.patchView.index
	cursor := "  "
	if selected {
		cursor = accentStyle.Bold(true).Render("· ")
	}
	adds, dels := patchLineCounts(section.Content)
	stat := fmt.Sprintf("+%d -%d", adds, dels)
	statStyle := subtleStyle
	if adds > 0 && dels == 0 {
		statStyle = okStyle
	} else if dels > 0 && adds == 0 {
		statStyle = warnStyle
	}
	statRendered := " " + statStyle.Render(stat)
	hunkBadge := ""
	if section.HunkCount > 1 {
		hunkBadge = " " + subtleStyle.Render(fmt.Sprintf("%dh", section.HunkCount))
	}
	chrome := lipgloss.Width(cursor) + lipgloss.Width(statRendered) + lipgloss.Width(hunkBadge) + 2
	nameWidth := max(width-chrome, 12)
	name := truncatePathHead(section.Path, nameWidth)

	row := cursor + name + statRendered + hunkBadge
	if selected {
		row = lipgloss.NewStyle().
			Background(colorTabActiveBg).
			Foreground(colorTitleFg).
			Bold(true).
			Width(width).
			Render(row)
	}
	return row
}

// DIFF + METADATA pane renderers (renderPatchDiffPane, patchDiffHeader,
// renderPatchMetaPane, patchMetaCards, renderPatchMetaInline) live in
// render_patch_panes.go.
