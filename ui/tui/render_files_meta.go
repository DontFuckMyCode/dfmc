package tui

// render_files_meta.go — metadata pane of the F3 Files panel: panelCard
// rendering for File / Status / Actions (wide-mode 3-pane), the inline
// medium-mode metadata strip, and the small humanFileSize / languageFromPath
// helpers shared with the list-row badge. Sibling to render_files.go which
// keeps the panel layout, list pane, and preview pane.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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
