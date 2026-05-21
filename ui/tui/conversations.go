package tui

// conversations.go owns the Conversations panel state messages, load
// commands, and action menu. Keyboard dispatch lives in
// conversations_keys.go; rendering lives in conversations_render.go.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

const (
	conversationsPreviewMessages = 6
	conversationsPreviewChars    = 280
)

type conversationsLoadedMsg struct {
	entries         []conversation.Summary
	err             error
	deepSearchQuery string
}

type conversationPreviewMsg struct {
	id           string
	msgs         []types.Message
	err          error
	branches     []conversationBranchSummary
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
		conv, err := eng.Conversation.LoadReadOnly(id)
		if err != nil {
			return conversationPreviewMsg{id: id, err: err}
		}
		msgs := conv.Messages()
		if len(msgs) > conversationsPreviewMessages {
			msgs = msgs[:conversationsPreviewMessages]
		}
		var branches []conversationBranchSummary
		if conv != nil && len(conv.Branches) > 1 {
			for name, body := range conv.Branches {
				branches = append(branches, conversationBranchSummary{Name: name, Messages: len(body)})
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

func (m Model) openConversationsActionMenu() Model {
	actions := []panelAction{
		{Label: "Load preview", Handler: func(m Model) (Model, tea.Cmd) {
			selected, ok := m.selectedConversationSummary()
			if !ok {
				return m, nil
			}
			m.conversations.previewID = selected.ID
			m.conversations.preview = nil
			return m, loadConversationPreviewCmd(m.eng, selected.ID)
		}},
		{Label: "Resume this conversation as the active one", Accel: "L", Handler: func(m Model) (Model, tea.Cmd) {
			return m.loadSelectedConversation()
		}},
		{Label: "Refresh list", Accel: "r", Handler: func(m Model) (Model, tea.Cmd) {
			m.conversations.loading = true
			m.conversations.err = ""
			return m, loadConversationsCmd(m.eng)
		}},
		{Label: "Search...", Accel: "/", Handler: func(m Model) (Model, tea.Cmd) {
			m.conversations.searchActive = true
			return m, nil
		}},
		{Label: "Deep search across message bodies (uses current query)", Accel: "S", Handler: func(m Model) (Model, tea.Cmd) {
			return m.startConversationDeepSearch()
		}},
		{Label: "Clear search (drop deep-search results, reload list)", Accel: "c", Handler: func(m Model) (Model, tea.Cmd) {
			return m.clearConversationSearch()
		}},
	}
	return m.openActionMenu("Conversations", "Conversation actions", actions)
}

func conversationLoadedNotice(id string, messageCount int) string {
	return fmt.Sprintf("Loaded %s (%d messages) - type to continue.", id, messageCount)
}
