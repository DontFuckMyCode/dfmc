package tui

// conversations.go — the Conversations panel surfaces the persisted
// conversations that internal/conversation.Manager maintains per project.
// It's a read-only list view with substring search; selecting an entry
// shows a short preview of the first few messages so the user can find an
// old session without leaving the TUI.
//
// Shape: a list of conversation.Summary, a search query, a scroll offset,
// and an optional preview of the currently-highlighted entry. Refresh is
// manual — the store doesn't publish mutation events for past files, so
// `r` re-runs List and tab-switch triggers an initial load.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

const (
	// conversationsPreviewMessages caps how many messages the preview pane
	// shows when an entry is highlighted. Enough to jog the user's memory;
	// less than a full replay.
	conversationsPreviewMessages = 6
	// conversationsPreviewChars clips each message body when rendered in
	// the preview so one verbose turn can't push the rest off-screen.
	conversationsPreviewChars = 280
)

type conversationsLoadedMsg struct {
	entries []conversation.Summary
	err     error
}

type conversationPreviewMsg struct {
	id   string
	msgs []types.Message
	err  error
}

func loadConversationsCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil || eng.Conversation == nil {
			return conversationsLoadedMsg{}
		}
		entries, err := eng.Conversation.List()
		return conversationsLoadedMsg{entries: entries, err: err}
	}
}

func loadConversationPreviewCmd(eng *engine.Engine, id string) tea.Cmd {
	return func() tea.Msg {
		if eng == nil || eng.Conversation == nil || strings.TrimSpace(id) == "" {
			return conversationPreviewMsg{id: id}
		}
		// Read-only load — we only need msgs[] for the preview pane and
		// must NOT silently switch the user's active conversation while
		// they're scrolling the Conversations tab.
		conv, err := eng.Conversation.LoadReadOnly(id)
		if err != nil {
			return conversationPreviewMsg{id: id, err: err}
		}
		msgs := conv.Messages()
		if len(msgs) > conversationsPreviewMessages {
			msgs = msgs[:conversationsPreviewMessages]
		}
		return conversationPreviewMsg{id: id, msgs: msgs}
	}
}

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
	hint := subtleStyle.Render("j/k scroll · enter preview (read-only) · / search · r refresh · c clear search")
	header := sectionHeader("⎔", "Conversations")
	queryLine := subtleStyle.Render("query: ")
	if strings.TrimSpace(m.conversations.query) != "" {
		queryLine += m.conversations.query
	} else {
		queryLine += subtleStyle.Render("(none)")
	}
	if m.conversations.searchActive {
		queryLine += subtleStyle.Render("  · typing, enter to commit")
	}
	lines := []string{header, hint, queryLine, renderDivider(width - 2)}

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
		lines = append(lines, "",
			subtleStyle.Render("No conversations persisted yet."),
			subtleStyle.Render("Start a chat and DFMC will persist it under .dfmc/conversations/."),
		)
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
		lines = append(lines, formatConversationPreview(m.conversations.preview, width-2)...)
	}

	lines = append(lines, "", subtleStyle.Render(fmt.Sprintf(
		"%d shown · %d loaded",
		len(filtered), len(m.conversations.entries),
	)))
	return strings.Join(lines, "\n")
}

// handleConversationsKey dispatches panel keys. Search mode consumes the
// keyboard while active.
func (m Model) handleConversationsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.conversations.searchActive {
		return m.handleConversationsSearchKey(msg)
	}
	total := len(filteredConversations(m.conversations.entries, m.conversations.query))
	step := 1
	pageStep := 10
	switch msg.String() {
	case "j", "down":
		if m.conversations.scroll+step < total {
			m.conversations.scroll += step
		}
	case "k", "up":
		if m.conversations.scroll >= step {
			m.conversations.scroll -= step
		} else {
			m.conversations.scroll = 0
		}
	case "pgdown":
		if m.conversations.scroll+pageStep < total {
			m.conversations.scroll += pageStep
		} else if total > 0 {
			m.conversations.scroll = total - 1
		}
	case "pgup":
		if m.conversations.scroll >= pageStep {
			m.conversations.scroll -= pageStep
		} else {
			m.conversations.scroll = 0
		}
	case "g":
		m.conversations.scroll = 0
	case "G":
		if total > 0 {
			m.conversations.scroll = total - 1
		}
	case "enter":
		// Load the preview for the currently highlighted entry.
		filtered := filteredConversations(m.conversations.entries, m.conversations.query)
		if len(filtered) == 0 || m.conversations.scroll < 0 || m.conversations.scroll >= len(filtered) {
			return m, nil
		}
		id := filtered[m.conversations.scroll].ID
		if id == "" || id == m.conversations.previewID {
			return m, nil
		}
		return m, loadConversationPreviewCmd(m.eng, id)
	case "r":
		m.conversations.loading = true
		m.conversations.err = ""
		return m, loadConversationsCmd(m.eng)
	case "/":
		m.conversations.searchActive = true
		return m, nil
	case "c":
		m.conversations.query = ""
		m.conversations.scroll = 0
	}
	return m, nil
}

func (m Model) handleConversationsSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.conversations.searchActive = false
		m.conversations.scroll = 0
		return m, nil
	case tea.KeyEsc:
		m.conversations.searchActive = false
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.conversations.query); len(r) > 0 {
			m.conversations.query = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.conversations.query += msg.String()
		return m, nil
	}
	return m, nil
}
