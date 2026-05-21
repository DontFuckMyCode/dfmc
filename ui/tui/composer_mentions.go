package tui

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/tokens"
)

// renderContextStrip summarizes the next send in the same vocabulary as
// code-agent context managers: conversation window first, workspace evidence
// second. Pinned files, [[file:...]] markers, fenced code, and paste blocks
// are explicit evidence; broad workspace retrieval is not implied by this
// strip.
func (m Model) renderContextStrip(width int) string {
	if width < 40 {
		width = 40
	}
	input := m.chat.input
	statsInput := input
	if len(m.chat.pasteBlocks) > 0 {
		statsInput = m.composeInput()
	}

	pinned := strings.TrimSpace(m.filesView.pinned)
	markerCount := countFileMarkers(statsInput)
	fenceCount := countFencedBlocks(statsInput)
	atMentions := countAtMentions(statsInput)
	pasteBlocks, pasteLines, pasteBytes := pasteBlockTotals(m.chat.pasteBlocks)

	if pinned == "" && markerCount == 0 && fenceCount == 0 && atMentions == 0 && strings.TrimSpace(statsInput) == "" {
		return ""
	}

	lines := []string{}
	if trimmed := strings.TrimSpace(statsInput); trimmed != "" {
		chars := len([]rune(trimmed))
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
		parts := []string{
			accentStyle.Render("CTX conversation"),
			subtleStyle.Render("chars:") + " " + boldStyle.Render(fmt.Sprintf("%d", chars)),
			subtleStyle.Render("tokens:") + " " + boldStyle.Render(tokenLabel),
		}
		if live := m.liveContextSnapshot(); live.ok {
			if live.maxContext > 0 && live.windowTokens > 0 {
				pct := int((int64(live.windowTokens) * 100) / int64(live.maxContext))
				parts = append(parts, subtleStyle.Render("window:")+" "+boldStyle.Render(fmt.Sprintf("~%s/%s (%d%%)", compactMetric(live.windowTokens), compactMetric(live.maxContext), pct)))
			} else if live.windowTokens > 0 {
				parts = append(parts, subtleStyle.Render("window:")+" "+boldStyle.Render("~"+compactMetric(live.windowTokens)))
			}
			if live.available > 0 {
				parts = append(parts, subtleStyle.Render("left:")+" "+boldStyle.Render(compactMetric(live.available)))
			}
		}
		lines = append(lines, "  "+truncateSingleLine(strings.Join(parts, subtleStyle.Render("  |  ")), width-2))
	}

	evidence := []string{
		accentStyle.Render("CTX evidence"),
		subtleStyle.Render("mode:") + " " + boldStyle.Render(m.contextStripEvidenceMode()),
	}
	if pinned != "" {
		evidence = append(evidence, subtleStyle.Render("pinned:")+" "+boldStyle.Render(pinned))
	}
	if markerCount > 0 {
		evidence = append(evidence, subtleStyle.Render("markers:")+" "+boldStyle.Render(fmt.Sprintf("%d", markerCount)))
	}
	if atMentions > 0 {
		evidence = append(evidence, subtleStyle.Render("@refs:")+" "+boldStyle.Render(fmt.Sprintf("%d", atMentions)))
	}
	if fenceCount > 0 {
		evidence = append(evidence, subtleStyle.Render("fenced:")+" "+boldStyle.Render(fmt.Sprintf("%d", fenceCount)))
	}
	if pasteBlocks > 0 {
		evidence = append(evidence, subtleStyle.Render("pasted:")+" "+boldStyle.Render(fmt.Sprintf("%d blocks / %d lines / %s bytes", pasteBlocks, pasteLines, compactMetric(pasteBytes))))
	}
	lines = append(lines, "  "+truncateSingleLine(strings.Join(evidence, subtleStyle.Render("  |  ")), width-2))
	return strings.Join(lines, "\n")
}

func (m Model) contextStripEvidenceMode() string {
	if m.status.ContextIn != nil && m.status.ContextIn.FileCount > 0 {
		return fmt.Sprintf("workspace %d file(s)", m.status.ContextIn.FileCount)
	}
	if m.status.ContextIn != nil {
		for _, reason := range m.status.ContextIn.Reasons {
			if strings.Contains(strings.ToLower(reason), "conversation history only") {
				return "explicit/tool"
			}
		}
	}
	return "explicit/tool"
}

func pasteBlockTotals(blocks []pasteBlock) (count, lines, bytes int) {
	for _, block := range blocks {
		if strings.TrimSpace(block.content) == "" {
			continue
		}
		count++
		lines += block.lineCount
		bytes += len([]byte(block.content))
	}
	return count, lines, bytes
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
		subtleStyle.Render("  -  ") +
		boldStyle.Render("@"+s.MentionQuery())
	if s.MentionRange() != "" {
		title += subtleStyle.Render(" | range " + s.MentionRange())
	}

	countLine := ""
	switch {
	case len(s.MentionSuggestions()) > 0:
		countLine = subtleStyle.Render(fmt.Sprintf("%d/%d files match", len(s.MentionSuggestions()), totalFiles))
	case totalFiles == 0 && s.MentionQuery() == "":
		countLine = subtleStyle.Render("indexing project files...")
	case totalFiles == 0:
		countLine = warnStyle.Render("file index empty")
	default:
		countLine = warnStyle.Render("no files match")
	}

	bodyLines := []string{}
	switch {
	case totalFiles == 0 && s.MentionQuery() == "":
		bodyLines = append(bodyLines,
			subtleStyle.Render("Project files are still being indexed..."),
			subtleStyle.Render("If this persists, press Ctrl+T or use /file to reopen the picker after the index loads."),
		)
	case len(s.MentionSuggestions()) > 0:
		selected := clampIndex(mentionIndex, len(s.MentionSuggestions()))
		query := s.MentionQuery()
		for i, row := range s.MentionSuggestions() {
			truncated := truncateSingleLine(row.Path, width-6)
			label := highlightMentionMatch(truncated, query)
			if row.Recent {
				label += " " + subtleStyle.Render("| recent")
			}
			if i == selected {
				bodyLines = append(bodyLines, mentionSelectedRowStyle.Render("▶ ")+label)
			} else {
				bodyLines = append(bodyLines, "  "+label)
			}
		}
	case totalFiles == 0:
		bodyLines = append(bodyLines,
			subtleStyle.Render("Indexing project files..."),
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

	footer := subtleStyle.Render("↑↓ move · tab/enter insert · esc cancel")

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
