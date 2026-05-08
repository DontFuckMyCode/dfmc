package tui

// conversations.go — the Conversations panel surfaces the persisted
// conversations that internal/conversation.Manager maintains per project.
// It's a read-only list view with substring search; selecting an entry
// shows a short preview of the first few messages so the user can find an
// old session without leaving the TUI.
//
// This file owns the load commands, key dispatch, and arrow-driven
// action menu. Rendering (filteredConversations, formatConversationRow,
// formatConversationPreview, renderConversationsView,
// conversationsTopBanner) lives in conversations_render.go.
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
	// deepSearchQuery is the query that produced this result set when
	// the message comes from a full-text search rather than a plain
	// List(). Empty for normal loads. The render path keys off this so
	// the title bar can advertise "search for: <q>" instead of the
	// usual entry count.
	deepSearchQuery string
}

type conversationPreviewMsg struct {
	id   string
	msgs []types.Message
	err  error
	// branches carries the loaded conversation's branch list — name +
	// message count per branch — so the preview pane can advertise
	// the available branches without a second engine round-trip.
	// Empty when the conversation only has the default `main` branch.
	// Phase G item 3 — branch tree visualization.
	branches []conversationBranchSummary
	// activeBranch is the name of the currently-active branch on the
	// loaded conversation; the preview pane highlights this name.
	activeBranch string
}

type conversationBranchSummary struct {
	Name     string
	Messages int
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

// searchConversationsCmd runs an engine-side full-text search across
// message bodies (not just the Summary fields the client-side filter
// matches on). Phase G item 1: lets the user find a past conversation
// by something they remember saying, not just by ID/provider/model.
func searchConversationsCmd(eng *engine.Engine, query string, limit int) tea.Cmd {
	return func() tea.Msg {
		if eng == nil || eng.Conversation == nil {
			return conversationsLoadedMsg{deepSearchQuery: query}
		}
		entries, err := eng.ConversationSearch(query, limit)
		return conversationsLoadedMsg{entries: entries, err: err, deepSearchQuery: query}
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
		// Surface the branch list so the preview can render them.
		// We only build the summary when the conversation has more
		// than one branch — single-branch conversations don't need
		// the noise.
		var branches []conversationBranchSummary
		if conv != nil && len(conv.Branches) > 1 {
			for name, body := range conv.Branches {
				branches = append(branches, conversationBranchSummary{
					Name: name, Messages: len(body),
				})
			}
		}
		return conversationPreviewMsg{
			id:           id,
			msgs:         msgs,
			branches:     branches,
			activeBranch: conv.Branch,
		}
	}
}


// handleConversationsKey dispatches panel keys. Search mode consumes the
// keyboard while active.
// openConversationsActionMenu — arrow-driven action surface for the
// Conversations panel. Enter still loads the preview directly; the
// menu sits behind Right Arrow for users who want to discover the rest.
func (m Model) openConversationsActionMenu() Model {
	actions := []panelAction{
		{Label: "Load preview", Accel: "enter",
			Handler: func(m Model) (Model, tea.Cmd) {
				filtered := filteredConversations(m.conversations.entries, m.conversations.query)
				if len(filtered) == 0 || m.conversations.scroll < 0 || m.conversations.scroll >= len(filtered) {
					return m, nil
				}
				selected := filtered[m.conversations.scroll]
				m.conversations.previewID = selected.ID
				m.conversations.preview = nil
				return m, loadConversationPreviewCmd(m.eng, selected.ID)
			}},
		{Label: "Resume this conversation as the active one", Accel: "L",
			Handler: func(m Model) (Model, tea.Cmd) {
				// Phase G item 2: Load action. Calls eng.ConversationLoad,
				// flips the engine's active conversation to the selected
				// entry, jumps to the Chat tab so the user sees their
				// resumed transcript instead of staring at the picker.
				filtered := filteredConversations(m.conversations.entries, m.conversations.query)
				if len(filtered) == 0 || m.conversations.scroll < 0 || m.conversations.scroll >= len(filtered) {
					return m, nil
				}
				selected := filtered[m.conversations.scroll]
				if m.eng == nil {
					m.notice = "Engine not available — cannot load conversation."
					return m, nil
				}
				conv, err := m.eng.ConversationLoad(selected.ID)
				if err != nil {
					m.notice = "load failed: " + err.Error()
					return m, nil
				}
				// Ride the same activeTab dance other panels use so the
				// jump survives Phase A's tab demotion.
				m.activeTab = m.activityTabIndex("Chat")
				m.notice = fmt.Sprintf("Loaded %s (%d messages) — type to continue.", conv.ID, len(conv.Messages()))
				return m, nil
			}},
		{Label: "Refresh list", Accel: "r",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.conversations.loading = true
				m.conversations.err = ""
				return m, loadConversationsCmd(m.eng)
			}},
		{Label: "Search…", Accel: "/",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.conversations.searchActive = true
				return m, nil
			}},
		{Label: "Deep search across message bodies (uses current query)", Accel: "S",
			Handler: func(m Model) (Model, tea.Cmd) {
				query := strings.TrimSpace(m.conversations.query)
				if query == "" {
					m.notice = "Type a query with / first, then S deep-searches across message bodies."
					return m, nil
				}
				m.conversations.loading = true
				m.conversations.err = ""
				m.conversations.deepSearchActive = true
				m.conversations.deepSearchQuery = query
				return m, searchConversationsCmd(m.eng, query, 50)
			}},
		{Label: "Clear search (drop deep-search results, reload list)", Accel: "c",
			Handler: func(m Model) (Model, tea.Cmd) {
				m.conversations.query = ""
				m.conversations.scroll = 0
				if m.conversations.deepSearchActive {
					m.conversations.deepSearchActive = false
					m.conversations.deepSearchQuery = ""
					m.conversations.loading = true
					return m, loadConversationsCmd(m.eng)
				}
				return m, nil
			}},
	}
	return m.openActionMenu("Conversations", "Conversation actions", actions)
}

func (m Model) handleConversationsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.conversations.searchActive {
		return m.handleConversationsSearchKey(msg)
	}
	if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
		return nm, cmd
	}
	if s := msg.String(); s == "right" || s == "l" {
		return m.openConversationsActionMenu(), nil
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
	case "L":
		// Phase G item 2: Load action — flip the engine's active
		// conversation to the highlighted entry and jump to Chat. The
		// previous in-memory conversation persists on disk (auto-save
		// fired at every turn) so this is reversible by loading the
		// other one back.
		filtered := filteredConversations(m.conversations.entries, m.conversations.query)
		if len(filtered) == 0 || m.conversations.scroll < 0 || m.conversations.scroll >= len(filtered) {
			return m, nil
		}
		selected := filtered[m.conversations.scroll]
		if m.eng == nil {
			m.notice = "Engine not available — cannot load conversation."
			return m, nil
		}
		conv, err := m.eng.ConversationLoad(selected.ID)
		if err != nil {
			m.notice = "load failed: " + err.Error()
			return m, nil
		}
		m.activeTab = m.activityTabIndex("Chat")
		m.notice = fmt.Sprintf("Loaded %s (%d messages) — type to continue.", conv.ID, len(conv.Messages()))
		return m, nil
	case "S":
		// Phase G item 1: deep full-text search across message bodies.
		// `/` filters the loaded summaries client-side (ID/provider/
		// model substrings); `S` calls eng.ConversationSearch which
		// loads each conversation and matches against assistant/user
		// content. Slower but actually finds "the chat where I asked
		// about <X>" when X isn't in the title.
		query := strings.TrimSpace(m.conversations.query)
		if query == "" {
			m.notice = "Type a query with / first, then S deep-searches message bodies."
			return m, nil
		}
		m.conversations.loading = true
		m.conversations.err = ""
		m.conversations.deepSearchActive = true
		m.conversations.deepSearchQuery = query
		return m, searchConversationsCmd(m.eng, query, 50)
	case "c":
		m.conversations.query = ""
		m.conversations.scroll = 0
		if m.conversations.deepSearchActive {
			m.conversations.deepSearchActive = false
			m.conversations.deepSearchQuery = ""
			m.conversations.loading = true
			return m, loadConversationsCmd(m.eng)
		}
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
