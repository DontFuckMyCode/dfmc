// render_patch_panes.go — DIFF + METADATA panes for the F3 Patch
// Lab panel. Sibling of render_patch.go which keeps the dispatcher
// (renderPatchViewV2 + patchPanelWidths), the top banner
// (patchTopBanner + patchStatusChip), and the FILES pane
// (renderPatchFilesPane + patchFilesHeader + renderPatchFileRow).
//
// Splitting the diff/metadata renderers out keeps render_patch.go
// scannable when adjusting the pane layout, and groups the cards
// (SUMMARY / REVIEW / ACTIONS) together with the inline footer
// fallback used in two-pane mode.

package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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
				renderDiffSideBySide(wt, width, max(height-8, 1)))
			return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
		}
		lines = append(lines,
			subtleStyle.Render("No diff to render. Worktree is clean and no assistant patch is queued."),
			"",
			subtleStyle.Render("Patch Lab is the staging surface for assistant-proposed changes — section by section, hunk by hunk, with apply/check/undo guards."),
			subtleStyle.Render("Ask the agent to make a change (in /chat), then return here. /patch reload pulls the latest from the engine; /patch check dry-runs apply."))
		return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
	}
	rowBudget := max(height-8, 1)
	lines = append(lines, renderDiffSideBySide(body, width, rowBudget))
	// Phase F item 3 — inline review hints. The metadata pane already
	// has a REVIEW card with hints surfaced from `patchReviewHints()`,
	// but at three-pane widths the user reads the diff and never looks
	// at the metadata. Pin the same hints under the diff so they're in
	// the user's reading path. Only render when there's something to
	// say — empty hints would just add noise.
	if hints := m.patchReviewHints(); len(hints) > 0 {
		lines = append(lines, "", subtleStyle.Render("review:"))
		for _, h := range hints {
			lines = append(lines, "  "+warnStyle.Render("·")+" "+subtleStyle.Render(h))
		}
	}
	// Hunk strip at the bottom so the user sees position-in-section.
	if section := m.currentPatchSection(); section != nil && len(section.Hunks) > 1 {
		strip := subtleStyle.Render(fmt.Sprintf(
			"hunk %d / %d  ·  ↑↓ navigate hunks  ·  ←→ navigate files",
			m.patchView.hunk+1, len(section.Hunks)))
		lines = append(lines, "", strip)
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) patchDiffHeader(width int) string {
	title := titleStyle.Bold(true).Render("◇ DIFF")
	section := m.currentPatchSection()
	if section == nil {
		return title + "  " + subtleStyle.Render("(select a file with ↑↓ or ←→)")
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
			{Key: "enter", Value: "apply patch"},
			{Key: "c", Value: "check (dry-run apply)"},
			{Key: "u", Value: "undo last conversation"},
			{Key: "←→", Value: "next / prev file"},
			{Key: "↑↓", Value: "next / prev hunk"},
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
	parts = append(parts, subtleStyle.Render("enter apply · c check · u undo · ←→ file · ↑↓ hunk · f focus"))
	_ = width
	return strings.Join(parts, "  ·  ")
}
