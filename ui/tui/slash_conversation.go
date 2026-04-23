// slash_conversation.go — the /conversation slash family. Exposes
// the engine's persistent conversation store (JSONL per convo, branch
// support) through chat so users can list / save / load / search /
// undo / branch without leaving the TUI.
//
//   - conversationSlash: dispatcher (list | active | new | save |
//     load | undo | search | branch ...).
//   - conversationBranchSlash: the branch subdispatcher (list |
//     create | switch).
//
// All engine calls route through the ConversationManager surface on
// engine.Engine; no persistence work happens here.

package tui

import (
	"fmt"
	"sort"
	"strings"
)

// conversationSlash exposes the branch/history surface through chat.
func (m Model) conversationSlash(args []string) string {
	if m.eng == nil {
		return "Engine unavailable."
	}
	sub := "active"
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	rest := args
	if len(args) > 0 {
		rest = args[1:]
	}
	switch sub {
	case "list":
		items, err := m.eng.ConversationList()
		if err != nil {
			return "conversation list: " + err.Error()
		}
		if len(items) == 0 {
			return "No saved conversations."
		}
		var b strings.Builder
		b.WriteString("Conversations:\n")
		for i, item := range items {
			if i >= 20 {
				fmt.Fprintf(&b, "  +%d more\n", len(items)-i)
				break
			}
			fmt.Fprintf(&b, "  %s (%d msgs)\n", item.ID, item.MessageN)
		}
		return strings.TrimRight(b.String(), "\n")
	case "active":
		active := m.eng.ConversationActive()
		if active == nil {
			return "No active conversation."
		}
		return fmt.Sprintf("Active: %s — %d messages, branch %q",
			active.ID, len(active.Messages()), blankFallback(active.Branch, "main"))
	case "new":
		c := m.eng.ConversationStart()
		if c == nil {
			return "Failed to start a new conversation."
		}
		return "Started new conversation: " + c.ID
	case "save":
		if err := m.eng.ConversationSave(); err != nil {
			return "save failed: " + err.Error()
		}
		return "Conversation saved."
	case "load":
		if len(rest) == 0 {
			return "Usage: /conversation load <id>"
		}
		c, err := m.eng.ConversationLoad(strings.TrimSpace(rest[0]))
		if err != nil {
			return "load failed: " + err.Error()
		}
		return fmt.Sprintf("Loaded %s (%d messages).", c.ID, len(c.Messages()))
	case "undo":
		n, err := m.eng.ConversationUndoLast()
		if err != nil {
			return "undo failed: " + err.Error()
		}
		return fmt.Sprintf("Undid %d assistant message(s).", n)
	case "search":
		query := strings.TrimSpace(strings.Join(rest, " "))
		if query == "" {
			return "Usage: /conversation search <query>"
		}
		items, err := m.eng.ConversationSearch(query, 15)
		if err != nil {
			return "search failed: " + err.Error()
		}
		if len(items) == 0 {
			return "No matching conversations."
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Matches (%d):\n", len(items))
		for _, item := range items {
			fmt.Fprintf(&b, "  %s (%d msgs)\n", item.ID, item.MessageN)
		}
		return strings.TrimRight(b.String(), "\n")
	case "branch":
		return conversationBranchSlash(m, rest)
	default:
		return "conversation: unknown subcommand. Try: list | active | new | save | load <id> | undo | search <q> | branch <sub>"
	}
}

func conversationBranchSlash(m Model, args []string) string {
	sub := "list"
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	rest := args
	if len(args) > 0 {
		rest = args[1:]
	}
	switch sub {
	case "list":
		branches := m.eng.ConversationBranchList()
		if len(branches) == 0 {
			return "No branches."
		}
		sort.Strings(branches)
		return "Branches: " + strings.Join(branches, ", ")
	case "create", "new":
		if len(rest) == 0 {
			return "Usage: /conversation branch create <name>"
		}
		name := strings.TrimSpace(rest[0])
		if err := m.eng.ConversationBranchCreate(name); err != nil {
			return "branch create failed: " + err.Error()
		}
		return "Created branch: " + name
	case "switch", "use":
		if len(rest) == 0 {
			return "Usage: /conversation branch switch <name>"
		}
		name := strings.TrimSpace(rest[0])
		if err := m.eng.ConversationBranchSwitch(name); err != nil {
			return "branch switch failed: " + err.Error()
		}
		return "Switched to branch: " + name
	default:
		return "branch: unknown sub. Try: list | create <name> | switch <name>"
	}
}
