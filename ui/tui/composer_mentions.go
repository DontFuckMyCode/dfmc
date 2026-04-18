package tui

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/tokens"
)

// renderContextStrip summarizes what will be attached to the next message:
// pinned file, inline [[file:...]] markers, fenced code blocks, and the
// provider-facing token estimate/budget percentage when available.
func (m Model) renderContextStrip(width int) string {
	if width < 40 {
		width = 40
	}
	input := m.chat.input

	pinned := strings.TrimSpace(m.filesView.pinned)
	markerCount := countFileMarkers(input)
	fenceCount := countFencedBlocks(input)
	atMentions := countAtMentions(input)

	if pinned == "" && markerCount == 0 && fenceCount == 0 && atMentions == 0 && strings.TrimSpace(input) == "" {
		return ""
	}

	parts := []string{accentStyle.Render("📎 context")}
	if pinned != "" {
		parts = append(parts, subtleStyle.Render("pinned:")+" "+boldStyle.Render(pinned))
	}
	if markerCount > 0 {
		parts = append(parts, subtleStyle.Render("markers:")+" "+boldStyle.Render(fmt.Sprintf("%d", markerCount)))
	}
	if atMentions > 0 {
		parts = append(parts, subtleStyle.Render("@refs:")+" "+boldStyle.Render(fmt.Sprintf("%d", atMentions)))
	}
	if fenceCount > 0 {
		parts = append(parts, subtleStyle.Render("fenced:")+" "+boldStyle.Render(fmt.Sprintf("%d", fenceCount)))
	}
	if trimmed := strings.TrimSpace(input); trimmed != "" {
		chars := len([]rune(trimmed))
		parts = append(parts, subtleStyle.Render("chars:")+" "+boldStyle.Render(fmt.Sprintf("%d", chars)))
		tok := tokens.Estimate(trimmed)
		budget := m.status.ProviderProfile.MaxContext
		if budget <= 0 && m.status.ContextIn != nil {
			budget = m.status.ContextIn.ProviderMaxContext
		}
		tokenLabel := fmt.Sprintf("~%d", tok)
		if budget > 0 {
			pct := int(float64(tok) / float64(budget) * 100)
			tokenLabel = fmt.Sprintf("~%d (%d%% of %d)", tok, pct, budget)
		}
		parts = append(parts, subtleStyle.Render("tokens:")+" "+boldStyle.Render(tokenLabel))
	}

	joined := strings.Join(parts, subtleStyle.Render("  ·  "))
	return "  " + truncateSingleLine(joined, width-2)
}

func countFileMarkers(s string) int {
	return strings.Count(s, "[[file:")
}

func countFencedBlocks(s string) int {
	n := strings.Count(s, "```")
	return n / 2
}

func countAtMentions(s string) int {
	if !strings.Contains(s, "@") {
		return 0
	}
	count := 0
	prevSpace := true
	for _, r := range s {
		if r == '@' && prevSpace {
			count++
		}
		prevSpace = r == ' ' || r == '\t' || r == '\n'
	}
	return count
}

// renderMentionPickerModal frames the @ file picker as a visible bordered box.
func renderMentionPickerModal(s chatSuggestionState, mentionIndex, totalFiles int, width int) string {
	if width < 40 {
		width = 40
	}
	title := accentStyle.Bold(true).Render("◆ File Picker") +
		subtleStyle.Render("  —  ") +
		boldStyle.Render("@"+s.MentionQuery())
	if s.MentionRange() != "" {
		title += subtleStyle.Render(" · range " + s.MentionRange())
	}

	countLine := ""
	switch {
	case len(s.MentionSuggestions()) > 0:
		countLine = subtleStyle.Render(fmt.Sprintf("%d/%d files match", len(s.MentionSuggestions()), totalFiles))
	case totalFiles == 0:
		countLine = warnStyle.Render("file index empty")
	default:
		countLine = warnStyle.Render("no files match")
	}

	bodyLines := []string{}
	switch {
	case len(s.MentionSuggestions()) > 0:
		selected := clampIndex(mentionIndex, len(s.MentionSuggestions()))
		for i, row := range s.MentionSuggestions() {
			label := truncateSingleLine(row.Path, width-6)
			if row.Recent {
				label += " " + subtleStyle.Render("· recent")
			}
			if i == selected {
				bodyLines = append(bodyLines, mentionSelectedRowStyle.Render("▶ "+label))
			} else {
				bodyLines = append(bodyLines, "  "+label)
			}
		}
	case totalFiles == 0:
		bodyLines = append(bodyLines,
			subtleStyle.Render("Indexing project files…"),
			subtleStyle.Render("If this persists, open the Files tab (F3) and press r to reload,"),
			subtleStyle.Render("or confirm you launched dfmc from a project root."),
		)
	case s.MentionQuery() != "":
		bodyLines = append(bodyLines,
			subtleStyle.Render("No files matched '"+s.MentionQuery()+"'."),
			subtleStyle.Render("Refine the query or press esc to cancel."),
		)
	default:
		bodyLines = append(bodyLines,
			subtleStyle.Render("Type a path after @ to filter."),
			subtleStyle.Render("Ranges: auth.go:10-50 or auth.go#L10-L50 attaches that slice."),
		)
	}

	footer := subtleStyle.Render("↑/↓ move · tab/enter insert as [[file:…]] · esc cancel")

	parts := []string{title, countLine, ""}
	parts = append(parts, bodyLines...)
	parts = append(parts, "", footer)
	return mentionPickerStyle.Width(width).Render(strings.Join(parts, "\n"))
}

// MentionQuery and friends expose chatSuggestionState fields to callers in
// other files while keeping the struct fields unexported.
func (s chatSuggestionState) MentionQuery() string { return s.mentionQuery }
func (s chatSuggestionState) MentionRange() string { return s.mentionRange }
func (s chatSuggestionState) MentionSuggestions() []mentionRow {
	return s.mentionSuggestions
}
