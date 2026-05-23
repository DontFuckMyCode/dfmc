package tui

// slash_expand.go — `/expand` and `/collapse` slash commands for the
// per-turn long-message collapse. The transcript renderer truncates
// any assistant turn taller than chatCollapseThreshold to a head
// preview plus a "… +N hidden · /expand X" footer; these slash
// commands flip the opt-in flag.
//
//   /expand N        opens turn #N (1-based, matches the #N chip)
//   /expand all      opens every turn currently open
//   /collapse N      re-collapses a previously opened turn
//   /collapse all    re-collapses every turn (clears the map)
//
// The expanded-turn map lives in chat state (chatState.expandedAssistantTurns)
// and is reset by /chat new, same lifetime as pinnedAssistantTurns.

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleExpandSlash(args []string) (tea.Model, tea.Cmd, bool) {
	return m.handleExpandCollapse(args, true)
}

func (m Model) handleCollapseSlash(args []string) (tea.Model, tea.Cmd, bool) {
	return m.handleExpandCollapse(args, false)
}

func (m Model) handleExpandCollapse(args []string, expand bool) (tea.Model, tea.Cmd, bool) {
	verb := "collapse"
	if expand {
		verb = "expand"
	}
	if len(args) == 0 {
		m.notice = "/" + verb + ": pass a turn number or `all`."
		return m.appendSystemMessage("Usage: /" + verb + " N  |  /" + verb + " all. The #N chip on each assistant header is the turn number."), nil, true
	}
	arg := strings.ToLower(strings.TrimSpace(args[0]))
	if arg == "all" {
		idxs := m.assistantIndices()
		if expand {
			if m.chat.expandedAssistantTurns == nil {
				m.chat.expandedAssistantTurns = make(map[int]bool, len(idxs))
			}
			for i := range idxs {
				m.chat.expandedAssistantTurns[i+1] = true
			}
			m.notice = fmt.Sprintf("/expand all — %d turn(s) opened.", len(idxs))
		} else {
			m.chat.expandedAssistantTurns = nil
			m.notice = "/collapse all — every turn back to head preview."
		}
		return m, nil, true
	}
	n, err := strconv.Atoi(arg)
	if err != nil || n <= 0 {
		m.notice = "/" + verb + ": positive integer or `all` required."
		return m.appendSystemMessage("/" + verb + " expects a positive integer (the #N chip in an assistant header) or `all`."), nil, true
	}
	idxs := m.assistantIndices()
	if n > len(idxs) {
		m.notice = fmt.Sprintf("/%s %d — only %d assistant turn(s).", verb, n, len(idxs))
		return m.appendSystemMessage(fmt.Sprintf("/%s %d: out of range (only %d assistant turn(s)).", verb, n, len(idxs))), nil, true
	}
	if expand {
		if m.chat.expandedAssistantTurns == nil {
			m.chat.expandedAssistantTurns = map[int]bool{}
		}
		m.chat.expandedAssistantTurns[n] = true
		m.notice = fmt.Sprintf("/expand %d — turn opened.", n)
	} else {
		delete(m.chat.expandedAssistantTurns, n)
		m.notice = fmt.Sprintf("/collapse %d — turn back to head preview.", n)
	}
	return m, nil, true
}
