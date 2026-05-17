package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// activeMentionQuery extracts the file query and optional range suffix from
// the `@token` currently under the cursor. Returns (query, rangeSuffix, ok):
//   - query: the file path prefix to rank against, stripped of any range
//   - rangeSuffix: normalized `#L10[-L50]` form (empty when no range was typed)
//   - ok: true only when the current token starts with `@` and has at least
//     one character of query body
func activeMentionQuery(input string) (string, string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", false
	}
	lastSpace := strings.LastIndexAny(input, " \t\n")
	token := input
	if lastSpace >= 0 {
		token = input[lastSpace+1:]
	}
	if !strings.HasPrefix(token, "@") {
		return "", "", false
	}
	body := strings.TrimPrefix(token, "@")
	query, rangeSuffix := splitMentionToken(body)
	return query, rangeSuffix, true
}

// mentionRow is a render-ready picker entry. Recent flags files the engine's
// working memory has recently touched so the UI can badge them without
// re-querying the engine at draw time.
type mentionRow struct {
	Path   string
	Recent bool
}

// highlightMentionMatch wraps the matched substring of `path` (case-insensitive
// against `query`) with the picker's accent style so users can see why a row
// ranked. Empty/whitespace queries return the path unchanged. Multi-byte
// characters are preserved by indexing on byte offsets returned by
// strings.Index over the lower-cased copy — both sides have identical byte
// length because ToLower preserves byte width for ASCII; for non-ASCII paths
// we fall back to plain rendering rather than risk slicing inside a rune.
func highlightMentionMatch(path, query string) string {
	q := strings.TrimSpace(query)
	if q == "" || path == "" {
		return path
	}
	low := strings.ToLower(path)
	lq := strings.ToLower(q)
	// ASCII fast path: ToLower preserves byte length so byte slicing is
	// safe. For non-ASCII queries (rare for file paths), bail out — the
	// raw path still renders, just without highlight.
	if len(low) != len(path) || len(lq) != len(q) {
		return path
	}
	idx := strings.Index(low, lq)
	if idx < 0 {
		return path
	}
	end := idx + len(lq)
	return path[:idx] + accentStyle.Bold(true).Render(path[idx:end]) + path[end:]
}

func (m Model) mentionSuggestions(query string, limit int) []mentionRow {
	ranker := newMentionRanker(m.filesView.entries, m.engineRecentFiles())
	ranked := ranker.rank(query, limit)
	out := make([]mentionRow, 0, len(ranked))
	for _, c := range ranked {
		out = append(out, mentionRow{Path: c.path, Recent: c.recent})
	}
	return out
}

func replaceActiveMention(input, path, rangeSuffix string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return input
	}
	lastSpace := strings.LastIndexAny(input, " \t\n")
	prefix := ""
	tokenStart := 0
	if lastSpace >= 0 {
		prefix = input[:lastSpace+1]
		tokenStart = lastSpace + 1
	}
	token := input[tokenStart:]
	if !strings.HasPrefix(token, "@") {
		return input
	}
	return prefix + fileMarkerRange(path, rangeSuffix)
}

func expandAtFileMentionsWithRecent(input string, files, recent []string) string {
	tokens := strings.Fields(input)
	if len(tokens) == 0 {
		return input
	}
	changed := false
	for i, token := range tokens {
		if !strings.HasPrefix(token, "@") || len(token) < 2 {
			continue
		}
		body := filepath.ToSlash(strings.TrimSpace(strings.TrimPrefix(token, "@")))
		if body == "" {
			continue
		}
		query, rangeSuffix := splitMentionToken(body)
		if resolved, ok := resolveMentionQuery(files, recent, query); ok {
			tokens[i] = fileMarkerRange(resolved, rangeSuffix)
			changed = true
		}
	}
	if !changed {
		return input
	}
	return strings.Join(tokens, " ")
}

func indexOfString(items []string, target string) int {
	target = strings.TrimSpace(target)
	for i, item := range items {
		if strings.TrimSpace(item) == target {
			return i
		}
	}
	return -1
}

func clampIndex(index, length int) int {
	if length <= 0 {
		return 0
	}
	if index < 0 {
		return 0
	}
	if index >= length {
		return length - 1
	}
	return index
}

func truncateCommandBlock(text string, max int) string {
	trimmed := strings.TrimSpace(text)
	if max <= 0 || len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "\n... [truncated]"
}

func (m Model) selectedFile() string {
	entries := m.visibleFilesEntries()
	if len(entries) == 0 {
		return ""
	}
	if m.filesView.index < 0 {
		return entries[0]
	}
	if m.filesView.index >= len(entries) {
		return entries[len(entries)-1]
	}
	return entries[m.filesView.index]
}

// truncateSingleLine clips `text` to at most `width` visible terminal cells.
// We approximate cells with rune count here because the panel copy is plain
// ASCII-heavy status text; this keeps the helper cheap and deterministic.
func truncateSingleLine(text string, width int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if width <= 0 {
		return trimmed
	}
	if ansi.StringWidth(trimmed) <= width {
		return trimmed
	}
	if width <= 3 {
		return ansi.Truncate(trimmed, width, "")
	}
	return ansi.Truncate(trimmed, width, "…")
}

func formatCommandPickerItem(item commandPickerItem) string {
	value := strings.TrimSpace(item.Value)
	desc := strings.TrimSpace(item.Description)
	meta := strings.TrimSpace(item.Meta)
	switch {
	case desc != "" && meta != "":
		return value + " - " + desc + " - " + meta
	case desc != "":
		return value + " - " + desc
	case meta != "":
		return value + " - " + meta
	default:
		return value
	}
}

func fitPanelContentHeight(content string, maxLines int) string {
	if maxLines <= 0 {
		return content
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > maxLines {
		if maxLines >= 2 {
			lines = append(lines[:maxLines-1], subtleStyle.Render("..."))
		} else {
			lines = lines[:maxLines]
		}
	}
	return strings.Join(lines, "\n")
}

// fitPanelContentScrollable is the scroll-aware sibling of
// fitPanelContentHeight. Used by overlays whose body is longer than
// the viewport — the caller passes the user's scroll offset (lines from
// the top) and the helper slices the body to fit, surfacing a `↑ N
// earlier` / `↓ N more` hint at the edges so the user can tell how
// much remains. Returns the clamped scroll value so the caller can
// keep its state honest when the content shrinks under the cursor.
func fitPanelContentScrollable(content string, maxLines, scroll int) (string, int) {
	if maxLines <= 0 {
		return content, 0
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	total := len(lines)
	if total <= maxLines {
		return strings.Join(lines, "\n"), 0
	}
	if scroll < 0 {
		scroll = 0
	}
	maxScroll := total - maxLines
	if scroll > maxScroll {
		scroll = maxScroll
	}
	end := scroll + maxLines
	if end > total {
		end = total
	}
	window := append([]string{}, lines[scroll:end]...)
	if scroll > 0 && len(window) > 0 {
		window[0] = subtleStyle.Render(fmt.Sprintf("  ↑ %d earlier · k/pgup/g to scroll", scroll))
	}
	if end < total && len(window) > 0 {
		window[len(window)-1] = subtleStyle.Render(fmt.Sprintf("  ↓ %d more · j/pgdn/G to scroll", total-end))
	}
	return strings.Join(window, "\n"), scroll
}
