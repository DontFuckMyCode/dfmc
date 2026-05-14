package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
)

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
	switch msg.String() {
	case "j", "down":
		m.conversations.scroll = scrollIndexDown(m.conversations.scroll, total, 1)
	case "k", "up":
		m.conversations.scroll = scrollIndexUp(m.conversations.scroll, 1)
	case "pgdown":
		m.conversations.scroll = scrollIndexDown(m.conversations.scroll, total, 10)
	case "pgup":
		m.conversations.scroll = scrollIndexUp(m.conversations.scroll, 10)
	case "g":
		m.conversations.scroll = 0
	case "G":
		m.conversations.scroll = lastScrollIndex(total)
	case "enter":
		return m.previewSelectedConversation()
	case "r":
		m.conversations.loading = true
		m.conversations.err = ""
		return m, loadConversationsCmd(m.eng)
	case "/":
		m.conversations.searchActive = true
	case "L":
		return m.loadSelectedConversation()
	case "S":
		return m.startConversationDeepSearch()
	case "c":
		return m.clearConversationSearch()
	}
	return m, nil
}

func (m Model) previewSelectedConversation() (Model, tea.Cmd) {
	selected, ok := m.selectedConversationSummary()
	if !ok || selected.ID == "" || selected.ID == m.conversations.previewID {
		return m, nil
	}
	return m, loadConversationPreviewCmd(m.eng, selected.ID)
}

func (m Model) loadSelectedConversation() (Model, tea.Cmd) {
	selected, ok := m.selectedConversationSummary()
	if !ok {
		return m, nil
	}
	if m.eng == nil {
		m.notice = "Engine not available - cannot load conversation."
		return m, nil
	}
	conv, err := m.eng.ConversationLoad(selected.ID)
	if err != nil {
		m.notice = "load failed: " + err.Error()
		return m, nil
	}
	m.activeTab = m.activityTabIndex("Chat")
	m.notice = conversationLoadedNotice(conv.ID, len(conv.Messages()))
	return m, nil
}

func (m Model) startConversationDeepSearch() (Model, tea.Cmd) {
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
}

func (m Model) clearConversationSearch() (Model, tea.Cmd) {
	m.conversations.query = ""
	m.conversations.scroll = 0
	if !m.conversations.deepSearchActive {
		return m, nil
	}
	m.conversations.deepSearchActive = false
	m.conversations.deepSearchQuery = ""
	m.conversations.loading = true
	return m, loadConversationsCmd(m.eng)
}

func (m Model) selectedConversationSummary() (conversation.Summary, bool) {
	filtered := filteredConversations(m.conversations.entries, m.conversations.query)
	if len(filtered) == 0 || m.conversations.scroll < 0 || m.conversations.scroll >= len(filtered) {
		return conversation.Summary{}, false
	}
	return filtered[m.conversations.scroll], true
}

func (m Model) handleConversationsSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.conversations.searchActive = false
		m.conversations.scroll = 0
	case tea.KeyEsc:
		m.conversations.searchActive = false
	default:
		if query, ok := applyInlineSearchTextKey(m.conversations.query, msg); ok {
			m.conversations.query = query
		}
	}
	return m, nil
}
