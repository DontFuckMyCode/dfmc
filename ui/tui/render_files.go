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
	if threePane {
		metaBlock := m.renderFilesMetaPane(metaW, height, pal)
		return lipgloss.JoinHorizontal(lipgloss.Top,
			listBlock, "  ", previewBlock, "  ", metaBlock)
	}
	if twoPane {
		// Metadata folded into a footer strip under the preview so
		// the most-useful info still shows on medium terminals.
		footer := m.renderFilesMetaInline(width)
		previewBlock = previewBlock + "\n" + footer
		return lipgloss.JoinHorizontal(lipgloss.Top, listBlock, "  ", previewBlock)
	}
	// Single-pane: stack list above preview, drop metadata.
	return listBlock + "\n" + previewBlock
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

// --- METADATA PANE -----------------------------------------------------------

func (m Model) renderFilesMetaPane(width, height int, pal tabPaletteEntry) string {
	cards := m.filesMetaCards()
	if len(cards) == 0 {
		return lipgloss.NewStyle().Width(width).Render(
			subtleStyle.Render("Select a file to see metadata."))
	}
	rendered := make([]string, 0, len(cards)*2)
	for i, c := range cards {
		if i > 0 {
			rendered = append(rendered, "")
		}
		rendered = append(rendered, renderPanelCard(c, width-2, false, pal.Accent))
	}
	body := strings.Join(rendered, "\n")
	// Hard clip to height so the right pane never overflows.
	rows := splitLines(body)
	if len(rows) > height {
		rows = rows[:height]
	}
	return strings.Join(rows, "\n")
}

func (m Model) filesMetaCards() []panelCard {
	path := strings.TrimSpace(m.filesView.path)
	if path == "" {
		return nil
	}
	cards := []panelCard{}

	// FILE card.
	chipStyle := okStyle
	chip := "OPEN"
	rows := []panelCardRow{
		{Key: "Path", Value: truncatePathHead(path, 60)},
		{Key: "Size", Value: humanFileSize(m.filesView.size)},
	}
	lang := languageFromPath(path)
	if lang != "" {
		rows = append(rows, panelCardRow{Key: "Language", Value: lang})
	}
	lineCount := strings.Count(m.filesView.preview, "\n") + 1
	if strings.TrimSpace(m.filesView.preview) == "" {
		lineCount = 0
	}
	rows = append(rows, panelCardRow{Key: "Lines", Value: fmt.Sprintf("%d", lineCount)})
	cards = append(cards, panelCard{
		Icon:            "▦",
		Title:           "File",
		StatusChip:      chip,
		StatusChipStyle: &chipStyle,
		Rows:            rows,
	})

	// STATUS card — pinned + in-context indicators.
	pinned := strings.TrimSpace(m.filesView.pinned) == path
	statusRows := []panelCardRow{}
	if pinned {
		pinStyle := infoStyle
		statusRows = append(statusRows,
			panelCardRow{Key: "Pinned", Value: "yes — survives reloads", Style: &pinStyle})
	} else {
		statusRows = append(statusRows,
			panelCardRow{Key: "Pinned", Value: "no — press p to pin"})
	}
	cards = append(cards, panelCard{
		Icon:       "◉",
		Title:      "Status",
		Rows:       statusRows,
		FooterHint: "p toggle · r reload index",
	})

	// ACTIONS card.
	cards = append(cards, panelCard{
		Icon:  "⚒",
		Title: "Actions",
		Rows: []panelCardRow{
			{Key: "i", Value: "insert [[file:…]] marker into chat"},
			{Key: "e", Value: "open Chat with explain prompt"},
			{Key: "v", Value: "open Chat with review prompt"},
		},
		FooterHint: "Ctrl+W context preview · F4 Patch for diffs",
	})
	return cards
}

func (m Model) renderFilesMetaInline(width int) string {
	path := strings.TrimSpace(m.filesView.path)
	if path == "" {
		return subtleStyle.Render("(no file selected — press enter on a row)")
	}
	parts := []string{
		titleStyle.Render("▦ ") + truncatePathHead(path, width/2),
		subtleStyle.Render(humanFileSize(m.filesView.size)),
	}
	if lang := languageFromPath(path); lang != "" {
		parts = append(parts, subtleStyle.Render(lang))
	}
	if strings.TrimSpace(m.filesView.pinned) == path {
		parts = append(parts, infoStyle.Render("PIN"))
	}
	parts = append(parts, subtleStyle.Render("i/e/v · p pin · r reload"))
	return strings.Join(parts, "  ·  ")
}

// humanFileSize renders bytes as KB/MB/GB to 1dp; sub-1KB stays raw.
func humanFileSize(n int) string {
	if n <= 0 {
		return "0 B"
	}
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// languageFromPath maps file extensions to friendly language labels.
// Empty when extension is unknown so the row can hide cleanly.
func languageFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "Go"
	case ".js", ".cjs", ".mjs":
		return "JavaScript"
	case ".ts":
		return "TypeScript"
	case ".tsx":
		return "TSX"
	case ".jsx":
		return "JSX"
	case ".py":
		return "Python"
	case ".rs":
		return "Rust"
	case ".java":
		return "Java"
	case ".c":
		return "C"
	case ".h", ".hpp":
		return "C/C++ header"
	case ".cpp", ".cc", ".cxx":
		return "C++"
	case ".rb":
		return "Ruby"
	case ".php":
		return "PHP"
	case ".sh", ".bash":
		return "Shell"
	case ".md":
		return "Markdown"
	case ".yaml", ".yml":
		return "YAML"
	case ".json":
		return "JSON"
	case ".toml":
		return "TOML"
	case ".html", ".htm":
		return "HTML"
	case ".css":
		return "CSS"
	case ".sql":
		return "SQL"
	}
	return ""
}
