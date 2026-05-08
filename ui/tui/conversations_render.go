package tui

// conversations_render.go — rendering surface for the Conversations
// panel. Sibling of conversations.go which keeps the load commands
// (loadConversationsCmd / loadConversationPreviewCmd), key dispatch
// (handleConversationsKey / handleConversationsSearchKey), and the
// arrow-driven action menu (openConversationsActionMenu).
//
// This file is pure render: filteredConversations,
// formatConversationRow, formatConversationPreview,
// renderConversationsView (the column composer), and
// conversationsTopBanner (title + count chip + state chip strip).
// Every function is read-only on Model — they take m by value and
// return strings, so the rendering pass never mutates panel state.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// filteredConversations filters the loaded summaries by the search query.
// We match on ID, provider, and model — the Summary itself doesn't carry
// message bodies (Search() does that job via the store).
func filteredConversations(entries []conversation.Summary, query string) []conversation.Summary {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return entries
	}
	out := entries[:0:0]
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.ID), q) ||
			strings.Contains(strings.ToLower(e.Provider), q) ||
			strings.Contains(strings.ToLower(e.Model), q) {
			out = append(out, e)
		}
	}
	return out
}

// formatConversationRow renders one summary as a single line, clipped to
// width. Shape: `2026-04-16 13:22  12 msgs  provider/model  id`.
// Provider/model is elided when unknown (Manager returns "unknown" for
// entries that were never active in this session).
func formatConversationRow(s conversation.Summary, selected bool, width int) string {
	ts := "               "
	if !s.StartedAt.IsZero() {
		ts = s.StartedAt.Local().Format("2006-01-02 15:04")
	}
	count := fmt.Sprintf("%3d msgs", s.MessageN)
	head := subtleStyle.Render(ts) + "  " + count
	tail := ""
	if s.Provider != "" && s.Provider != "unknown" {
		tail = "  " + accentStyle.Render(s.Provider)
		if s.Model != "" && s.Model != "unknown" {
			tail += subtleStyle.Render("/" + s.Model)
		}
	}
	id := "  " + subtleStyle.Render(s.ID)
	line := head + tail + id
	if selected {
		line = accentStyle.Render("▶ ") + line
	} else {
		line = "  " + line
	}
	if width > 0 {
		line = truncateSingleLine(line, width)
	}
	return line
}

// formatConversationPreview renders the first few messages of the
// highlighted conversation with role tags. Content is collapsed to a
// single line per message to keep the pane compact; the idea is "jog the
// memory", not "replay the session".
func formatConversationPreview(msgs []types.Message, width int) []string {
	if len(msgs) == 0 {
		return []string{subtleStyle.Render("  (empty transcript)")}
	}
	out := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		role := strings.ToUpper(strings.TrimSpace(string(msg.Role)))
		if role == "" {
			role = "???"
		}
		body := oneLine(msg.Content)
		if len(body) > conversationsPreviewChars {
			body = body[:conversationsPreviewChars-1] + "…"
		}
		head := subtleStyle.Render("[" + role + "]")
		line := "  " + head + " " + body
		if width > 0 {
			line = truncateSingleLine(line, width)
		}
		out = append(out, line)
	}
	return out
}

func (m Model) renderConversationsView(width int) string {
	width = clampInt(width, 24, 1000)
	banner := m.conversationsTopBanner(width)
	hint := subtleStyle.Render("j/k scroll · enter preview · L resume · / search · S deep-search · r refresh · c clear")
	queryLine := subtleStyle.Render("query ")
	if strings.TrimSpace(m.conversations.query) != "" {
		queryLine += boldStyle.Render(m.conversations.query)
	} else {
		queryLine += subtleStyle.Render("(none)")
	}
	if m.conversations.searchActive {
		queryLine += subtleStyle.Render("  · typing, enter to commit")
	}
	if m.conversations.deepSearchActive {
		// Deep-search results are server-side hits across message
		// bodies — name the mode clearly so the user knows the row
		// count reflects matches, not the full list.
		queryLine += "  " + accentStyle.Render("· deep-search active — c to drop and reload list")
	}
	lines := []string{banner, queryLine, hint, renderDivider(width - 2)}

	if m.conversations.err != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.conversations.err))
		return strings.Join(lines, "\n")
	}
	if m.conversations.loading {
		lines = append(lines, "", subtleStyle.Render("loading..."))
		return strings.Join(lines, "\n")
	}

	filtered := filteredConversations(m.conversations.entries, m.conversations.query)
	if len(filtered) == 0 {
		lines = append(lines, "")
		if len(m.conversations.entries) == 0 {
			lines = append(lines,
				subtleStyle.Render("No conversations persisted yet."),
				subtleStyle.Render("Every chat is auto-saved as JSONL under .dfmc/conversations/ — branches, parked turns, and tool calls all included."),
				subtleStyle.Render("Send a message in /chat to start. Enter on a row resumes that conversation; / searches by title."),
			)
		} else {
			lines = append(lines,
				warnStyle.Render(fmt.Sprintf("No matches for %q in %d conversations.", m.conversations.query, len(m.conversations.entries))),
				subtleStyle.Render("Press c to clear the query, or / to edit it."),
			)
		}
		return strings.Join(lines, "\n")
	}

	scroll := m.conversations.scroll
	if scroll < 0 {
		scroll = 0
	}
	if scroll >= len(filtered) {
		scroll = len(filtered) - 1
	}

	for i, s := range filtered[scroll:] {
		selected := (scroll + i) == m.conversations.scroll
		lines = append(lines, formatConversationRow(s, selected, width-2))
	}

	// Preview pane (only when the highlighted entry's preview is loaded).
	selectedID := ""
	if m.conversations.scroll >= 0 && m.conversations.scroll < len(filtered) {
		selectedID = filtered[m.conversations.scroll].ID
	}
	if selectedID != "" && selectedID == m.conversations.previewID {
		// Preview is read-only — Manager.LoadReadOnly does NOT change the
		// active conversation. The chat tab keeps whatever was running
		// before; switching tabs back to Chat shows the original session.
		lines = append(lines, "",
			subtleStyle.Render("preview · "+selectedID+" · read-only"),
		)
		// Phase G item 3 — branch tree visualization. The store holds
		// branches as flat siblings (map[name][]Message) so the "tree"
		// is a list with the active branch highlighted. Only renders
		// when there's more than one branch — single-branch is the
		// default and would just add chrome.
		if len(m.conversations.previewBranches) > 0 {
			active := m.conversations.previewActiveBranch
			lines = append(lines, subtleStyle.Render("branches:"))
			for _, br := range m.conversations.previewBranches {
				marker := " "
				bodyStyle := subtleStyle
				if br.Name == active {
					marker = accentStyle.Render("●")
					bodyStyle = accentStyle
				}
				lines = append(lines, "  "+marker+" "+bodyStyle.Render(fmt.Sprintf("%s (%d msgs)", br.Name, br.Messages)))
			}
		}
		lines = append(lines, formatConversationPreview(m.conversations.preview, width-2)...)
	}

	lines = append(lines, "", subtleStyle.Render(fmt.Sprintf(
		"%d shown · %d loaded",
		len(filtered), len(m.conversations.entries),
	)))
	out := strings.Join(lines, "\n")
	if m.actionMenu.open && m.actionMenu.owner == "Conversations" {
		out += "\n\n" + m.renderActionMenu(width)
	}
	return out
}

// conversationsTopBanner — title + count chip + state chip on the
// right. State: HEALTHY / EMPTY / ERROR / LOADING.
func (m Model) conversationsTopBanner(width int) string {
	title := titleStyle.Bold(true).Render("⎔ CONVERSATIONS")
	chipText, chipStyle := " HEALTHY ", okStyle
	switch {
	case m.conversations.err != "":
		chipText, chipStyle = " ERROR ", warnStyle
	case m.conversations.loading:
		chipText, chipStyle = " LOADING ", infoStyle
	case len(m.conversations.entries) == 0:
		chipText, chipStyle = " EMPTY ", subtleStyle
	}
	chip := chipStyle.Render(chipText)
	countChip := subtleStyle.Render(fmt.Sprintf(" %d ", len(m.conversations.entries)))
	chipStrip := countChip + " " + chip
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipStrip)-4, 1)
	return title + strings.Repeat(" ", gap) + chipStrip
}
