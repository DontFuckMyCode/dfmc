// render_files.go — the F3 Files panel, rebuilt as a 3-pane explorer
// (file list | preview | metadata cards). Replaces the old 2-pane
// implementation in render_panels.go's renderFilesViewSized.
//
// Layout strategy:
//   ≥120 cols → 3 panes (24% list · 44% preview · 32% metadata)
//   80-119    → 2 panes (35% list · 65% preview)
//   <80       → 1 pane stack
//
// The list shows file rows with:
//   - selection cursor
//   - pin indicator
//   - language badge
//   - right-aligned file extension
// The preview shows numbered lines.
// The metadata pane shows panelCard-style boxes:
//   - FILE      path / size / language / line count
//   - STATUS    pinned? in context? recent edit?
//   - ACTIONS   i/e/v key hints + Ctrl+W jump

package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderFilesViewV2 is the new 3-pane Files panel. The legacy
// renderFilesViewSized in render_panels.go now delegates here.
func (m Model) renderFilesViewV2(width, height int) string {
	width = max(width, 50)
	height = max(height, 12)

	pal := paletteForTab("Files", false)

	// Layout breakpoints. The "metadata" pane only appears on wide
	// terminals; on medium widths we collapse it into a footer strip
	// under the preview to keep the list and preview readable.
	threePane := width >= 120
	twoPane := !threePane && width >= 80

	listW, previewW, metaW := filesPanelWidths(width, threePane, twoPane)

	listBlock := m.renderFilesListPane(listW, height, pal)
	previewBlock := m.renderFilesPreviewPane(previewW, height, pal)
	var body string
	if threePane {
		metaBlock := m.renderFilesMetaPane(metaW, height, pal)
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			listBlock, "  ", previewBlock, "  ", metaBlock)
	} else if twoPane {
		// Metadata folded into a footer strip under the preview so
		// the most-useful info still shows on medium terminals.
		footer := m.renderFilesMetaInline(width)
		previewBlock = previewBlock + "\n" + footer
		body = lipgloss.JoinHorizontal(lipgloss.Top, listBlock, "  ", previewBlock)
	} else {
		// Single-pane: stack list above preview, drop metadata.
		body = listBlock + "\n" + previewBlock
	}
	// Discoverable arrow-key path: when the action menu is open it
	// stacks under the panel as a visible overlay so the user sees
	// every action (pin/explain/review/...) without memorising keys.
	if m.actionMenu.open && m.actionMenu.owner == "Files" {
		body += "\n\n" + m.renderActionMenu(width)
	}
	return body
}

// filesPanelWidths splits the available width into list/preview/meta
// columns honouring minimums. Three-pane gets a 24/44/32 split,
// two-pane a 35/65 split, one-pane the full width on each.
func filesPanelWidths(total int, threePane, twoPane bool) (listW, previewW, metaW int) {
	if threePane {
		listW = max(total*24/100, 28)
		metaW = max(total*32/100, 30)
		previewW = max(total-listW-metaW-4, 30)
		return
	}
	if twoPane {
		listW = max(total*35/100, 28)
		previewW = max(total-listW-2, 24)
		return
	}
	return total, total, 0
}

// --- LIST PANE ---------------------------------------------------------------

func (m Model) renderFilesListPane(width, height int, pal tabPaletteEntry) string {
	header := m.filesListHeader(width)
	lines := []string{
		header,
		renderDivider(width - 2),
		"",
	}
	if len(m.filesView.entries) == 0 {
		lines = append(lines,
			warnStyle.Render("No indexed project files yet."),
			"",
			subtleStyle.Render("Try one of these:"),
			subtleStyle.Render("  · /analyze in Chat"),
			subtleStyle.Render("  · press 'r' to refresh"),
			subtleStyle.Render("  · launch dfmc from the project root"),
		)
	} else {
		// Reserve rows for header (3) + footer (3).
		rowBudget := max(height-6, 6)
		start, end := scrollWindow(m.filesView.index, len(m.filesView.entries), rowBudget)
		for i := start; i < end; i++ {
			row := m.renderFilesListRow(i, width, pal)
			lines = append(lines, row)
		}
		// Scroll-position indicator + count.
		lines = append(lines, "",
			subtleStyle.Render(fmt.Sprintf("%d / %d files",
				m.filesView.index+1, len(m.filesView.entries))))
		if pinned := strings.TrimSpace(m.filesView.pinned); pinned != "" {
			lines = append(lines,
				infoStyle.Render("📌 ")+subtleStyle.Render(truncateForLine(pinned, width-6)))
		}
		// Always-visible keyboard contract — same shape the other panels use.
		lines = append(lines,
			subtleStyle.Render("j/k scroll · enter/→ action menu · p pin · i insert · e explain · v review · r reload"))
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) filesListHeader(width int) string {
	count := len(m.filesView.entries)
	chip := okStyle
	chipText := fmt.Sprintf(" %d ", count)
	if count == 0 {
		chip = warnStyle
		chipText = " 0 "
	}
	title := titleStyle.Bold(true).Render("▦ FILES")
	chipRendered := chip.Render(chipText)
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipRendered)-2, 1)
	return title + strings.Repeat(" ", gap) + chipRendered
}

func (m Model) renderFilesListRow(i, width int, pal tabPaletteEntry) string {
	path := m.filesView.entries[i]
	pinned := path == strings.TrimSpace(m.filesView.pinned)
	selected := i == m.filesView.index

	// Layout per row:
	//   ▶/space  filename                      ext  pin?
	cursor := "  "
	if selected {
		cursor = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render("▶ ")
	}
	pinChip := ""
	if pinned {
		pinChip = " " + infoStyle.Render("PIN")
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != "" {
		ext = strings.TrimPrefix(ext, ".")
	}
	extBadge := ""
	if ext != "" {
		extBadge = " " + subtleStyle.Render("·"+ext)
	}
	// Reserve room for cursor (2) + ext badge + pin chip + 1 padding.
	chrome := lipgloss.Width(cursor) + lipgloss.Width(extBadge) + lipgloss.Width(pinChip) + 1
	nameWidth := max(width-chrome, 12)
	name := truncatePathHead(path, nameWidth)
	if selected {
		name = lipgloss.NewStyle().Foreground(pal.Accent).Bold(true).Render(name)
	}
	return cursor + name + extBadge + pinChip
}

// truncatePathHead trims long paths from the FRONT (keeping the
// filename visible) — better for code-explorer rows than the
// generic truncateSingleLine which trims the tail.
func truncatePathHead(path string, max int) string {
	if max < 1 {
		return ""
	}
	if lipgloss.Width(path) <= max {
		return path
	}
	if max <= 3 {
		return strings.Repeat(".", max)
	}
	runes := []rune(path)
	keep := max - 1
	return "…" + string(runes[len(runes)-keep:])
}

// scrollWindow centres the cursor in the visible window when
// possible, clamping at the list boundaries.
func scrollWindow(cursor, total, rowBudget int) (start, end int) {
	if rowBudget <= 0 || total <= 0 {
		return 0, 0
	}
	half := rowBudget / 2
	start = max(cursor-half, 0)
	end = start + rowBudget
	if end > total {
		end = total
		start = max(end-rowBudget, 0)
	}
	return start, end
}

// --- PREVIEW PANE ------------------------------------------------------------

func (m Model) renderFilesPreviewPane(width, height int, pal tabPaletteEntry) string {
	header := m.filesPreviewHeader(width, pal)
	lines := []string{
		header,
		renderDivider(width - 2),
		"",
	}
	rowBudget := max(height-6, 6)
	if strings.TrimSpace(m.filesView.preview) == "" {
		lines = append(lines, subtleStyle.Render("Select a file with j/k or enter to load preview."))
	} else {
		// Number each visible line so the user can talk about specific
		// lines ("look at line 42") without counting from zero.
		previewLines := splitLines(m.filesView.preview)
		if len(previewLines) > rowBudget {
			previewLines = previewLines[:rowBudget]
		}
		gutter := digitsForCount(len(previewLines))
		for i, line := range previewLines {
			ln := fmt.Sprintf("%*d", gutter, i+1)
			rendered := truncateSingleLine(line, width-gutter-3)
			lines = append(lines,
				subtleStyle.Render(ln+" │ ")+rendered)
		}
	}
	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

func (m Model) filesPreviewHeader(width int, _ tabPaletteEntry) string {
	title := titleStyle.Bold(true).Render("❐ PREVIEW")
	path := strings.TrimSpace(m.filesView.path)
	if path == "" {
		return title + "  " + subtleStyle.Render("(no file selected)")
	}
	pathLabel := subtleStyle.Render(truncatePathHead(path, width-lipgloss.Width(title)-4))
	return title + "  " + pathLabel
}

func digitsForCount(n int) int {
	if n <= 0 {
		return 1
	}
	d := 0
	for n > 0 {
		d++
		n /= 10
	}
	return d
}

