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
	title := titleStyle.Bold(true).Render("◈ PATCH LAB")
	files := strings.Join(m.patchFilesOrNone(), ", ")
	if files == "(none)" {
		files = subtleStyle.Render("no assistant patch loaded — ask DFMC to refactor / fix in Chat")
	} else {
		files = subtleStyle.Render(truncateForLine(files, width-32))
	}
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipRendered)-lipgloss.Width(files)-4, 1)
	line := title + "  " + files + strings.Repeat(" ", gap) + chipRendered
	return line + "\n" + renderDivider(width-2)
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
		renderDivider(width - 2),
		"",
	}
	if len(m.patchView.set) == 0 {
		lines = append(lines,
			subtleStyle.Render("No assistant patch yet."),
			"",
			subtleStyle.Render("Ask DFMC in Chat to:"),
			subtleStyle.Render("  · refactor a function"),
			subtleStyle.Render("  · fix a bug"),
			subtleStyle.Render("  · rewrite a file"),
			"",
			subtleStyle.Render("the generated diff lands here."),
		)
	} else {
		rowBudget := max(height-6, 6)
		start, end := scrollWindow(m.patchView.index, len(m.patchView.set), rowBudget)
		for i := start; i < end; i++ {
			lines = append(lines, m.renderPatchFileRow(i, width, pal))
		}
		lines = append(lines, "",
			subtleStyle.Render(fmt.Sprintf("%d / %d files",
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
	title := titleStyle.Bold(true).Render("⇄ FILES")
	chipRendered := chip.Render(chipText)
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipRendered)-2, 1)
	return title + strings.Repeat(" ", gap) + chipRendered
}

func (m Model) renderPatchFileRow(i, width int, pal tabPaletteEntry) string {
	section := m.patchView.set[i]
	selected := i == m.patchView.index
	cursor := "  "
	if selected {
		cursor = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render("▶ ")
	}
	adds, dels := patchLineCounts(section.Content)
	stat := fmt.Sprintf("+%d/-%d", adds, dels)
	statStyle := subtleStyle
	if adds > 0 && dels == 0 {
		statStyle = okStyle
	} else if dels > 0 && adds == 0 {
		statStyle = warnStyle
	}
	statRendered := " " + statStyle.Render(stat)
	hunkBadge := ""
	if section.HunkCount > 1 {
		hunkBadge = " " + subtleStyle.Render(fmt.Sprintf("·%dh", section.HunkCount))
	}
	chrome := lipgloss.Width(cursor) + lipgloss.Width(statRendered) + lipgloss.Width(hunkBadge) + 1
	nameWidth := max(width-chrome, 12)
	name := truncatePathHead(section.Path, nameWidth)
	if selected {
		name = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render(name)
	}
	return cursor + name + statRendered + hunkBadge
}

// --- DIFF PANE ---------------------------------------------------------------

func (m Model) renderPatchDiffPane(width, height int, pal tabPaletteEntry) string {
	_ = pal // diff colours come from diff_sidebyside.go's red/green styles.
	header := m.patchDiffHeader(width)
	lines := []string{
		header,
		renderDivider(width - 2),
		"",
	}
	body := m.patchPreviewText()
	if strings.TrimSpace(body) == "" {
		// Show worktree diff as the fallback so the panel still has
		// useful content when no assistant patch is queued.
		if wt := strings.TrimSpace(m.patchView.diff); wt != "" {
			lines = append(lines,
				subtleStyle.Render("(no assistant patch — showing worktree diff)"),
				"",
				renderDiffSideBySide(wt, width, max(height-8, 8)))
			return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
		}
		lines = append(lines,
			subtleStyle.Render("No diff to render. Worktree is clean and no assistant patch is queued."))
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}
	rowBudget := max(height-8, 8)
	lines = append(lines, renderDiffSideBySide(body, width, rowBudget))
	// Hunk strip at the bottom so the user sees position-in-section.
	if section := m.currentPatchSection(); section != nil && len(section.Hunks) > 1 {
		strip := subtleStyle.Render(fmt.Sprintf(
			"hunk %d / %d  ·  j/k navigate hunks  ·  n/b navigate files",
			m.patchView.hunk+1, len(section.Hunks)))
		lines = append(lines, "", strip)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) patchDiffHeader(width int) string {
	title := titleStyle.Bold(true).Render("◇ DIFF")
	section := m.currentPatchSection()
	if section == nil {
		return title + "  " + subtleStyle.Render("(select a file with j/k or n)")
	}
	header := strings.TrimSpace(m.patchHunkSummary())
	pathLabel := subtleStyle.Render(truncatePathHead(section.Path, width/3))
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(pathLabel)-lipgloss.Width(header)-6, 1)
	return title + "  " + pathLabel + strings.Repeat(" ", gap) + subtleStyle.Render(header)
}

// --- METADATA PANE -----------------------------------------------------------

func (m Model) renderPatchMetaPane(width, height int, pal tabPaletteEntry) string {
	cards := m.patchMetaCards()
	if len(cards) == 0 {
		return lipgloss.NewStyle().Width(width).Render(
			subtleStyle.Render("No assistant patch loaded."))
	}
	rendered := make([]string, 0, len(cards)*2)
	for i, c := range cards {
		if i > 0 {
			rendered = append(rendered, "")
		}
		rendered = append(rendered, renderPanelCard(c, width-2, false, pal.Accent))
	}
	body := strings.Join(rendered, "\n")
	rows := splitLines(body)
	if len(rows) > height {
		rows = rows[:height]
	}
	return strings.Join(rows, "\n")
}

func (m Model) patchMetaCards() []panelCard {
	if len(m.patchView.set) == 0 && strings.TrimSpace(m.patchView.diff) == "" {
		return nil
	}
	var cards []panelCard

	// SUMMARY card — totals + status chip.
	totalAdds, totalDels := 0, 0
	for _, sec := range m.patchView.set {
		a, d := patchLineCounts(sec.Content)
		totalAdds += a
		totalDels += d
	}
	chip, chipStyle := m.patchStatusChip()
	chipStyleCopy := chipStyle
	cards = append(cards, panelCard{
		Icon:            "Σ",
		Title:           "Summary",
		StatusChip:      chip,
		StatusChipStyle: &chipStyleCopy,
		Rows: []panelCardRow{
			{Key: "Files", Value: fmt.Sprintf("%d touched", len(m.patchView.set))},
			{Key: "Adds", Value: fmt.Sprintf("+%d", totalAdds)},
			{Key: "Deletions", Value: fmt.Sprintf("-%d", totalDels)},
			{Key: "Worktree", Value: fmt.Sprintf("%d changed", len(m.patchView.changed))},
		},
	})

	// REVIEW card — review hints surfaced from patchReviewHints().
	hints := m.patchReviewHints()
	reviewRows := []panelCardRow{}
	if len(hints) == 0 {
		reviewRows = append(reviewRows,
			panelCardRow{Key: "Status", Value: "no flags raised"})
	} else {
		for _, h := range hints {
			reviewRows = append(reviewRows, panelCardRow{Value: "· " + h})
		}
	}
	cards = append(cards, panelCard{
		Icon:       "◉",
		Title:      "Review",
		Rows:       reviewRows,
		FooterHint: "f focus file in Files tab",
	})

	// ACTIONS card.
	cards = append(cards, panelCard{
		Icon:  "⚒",
		Title: "Actions",
		Rows: []panelCardRow{
			{Key: "a", Value: "apply patch"},
			{Key: "c", Value: "check (dry-run apply)"},
			{Key: "u", Value: "undo last conversation"},
			{Key: "n / b", Value: "next / prev file"},
			{Key: "j / k", Value: "next / prev hunk"},
			{Key: "d", Value: "reload worktree diff"},
			{Key: "l", Value: "reload latest patch"},
		},
		FooterHint: "f focus file · ctrl+h keys",
	})
	return cards
}

func (m Model) renderPatchMetaInline(width int) string {
	if len(m.patchView.set) == 0 {
		return subtleStyle.Render("No assistant patch loaded.")
	}
	totalAdds, totalDels := 0, 0
	for _, sec := range m.patchView.set {
		a, d := patchLineCounts(sec.Content)
		totalAdds += a
		totalDels += d
	}
	chip, chipStyle := m.patchStatusChip()
	parts := []string{
		chipStyle.Render(" " + chip + " "),
		subtleStyle.Render(fmt.Sprintf("%d files", len(m.patchView.set))),
		okStyle.Render(fmt.Sprintf("+%d", totalAdds)),
		warnStyle.Render(fmt.Sprintf("-%d", totalDels)),
	}
	if hints := m.patchReviewHints(); len(hints) > 0 {
		parts = append(parts, subtleStyle.Render(strings.Join(hints, " · ")))
	}
	parts = append(parts, subtleStyle.Render("a apply · c check · u undo · n/b file · j/k hunk · f focus"))
	_ = width
	return strings.Join(parts, "  ·  ")
}
