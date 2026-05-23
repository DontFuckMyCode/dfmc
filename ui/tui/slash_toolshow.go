package tui

// slash_toolshow.go — `/toolshow N` slash command: prints the full
// multi-line detail of the N-th tool event back into the chat as a
// system message. The chat console collapses each tool event to a
// 1-line summary (the full stream is still visible in the Activity
// panel, Ctrl+Shift+T), but reaching for the panel breaks the chat
// flow when the user only wants a peek at one event. /toolshow is
// the "expand this one inline without leaving chat" affordance.
//
// Indexing is 1-based across all tool-role transcript rows, in
// appearance order. `/toolshow last` is the same as `/toolshow N`
// for the latest tool event.

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleToolShowSlash(args []string) (tea.Model, tea.Cmd, bool) {
	idxs := m.toolEventIndices()
	if len(idxs) == 0 {
		m.notice = "/toolshow — no tool events in the transcript yet."
		return m.appendSystemMessage("/toolshow: no tool events yet. Run a tool first."), nil, true
	}
	if len(args) == 0 {
		m.notice = "/toolshow: pass an event number or `last`."
		return m.appendSystemMessage(fmt.Sprintf("Usage: /toolshow N | last. %d tool event(s) recorded so far.", len(idxs))), nil, true
	}
	arg := strings.ToLower(strings.TrimSpace(args[0]))
	var pick int
	switch arg {
	case "last":
		pick = len(idxs)
	default:
		n, err := strconv.Atoi(arg)
		if err != nil || n == 0 {
			m.notice = "/toolshow: positive integer or `last` required."
			return m.appendSystemMessage("/toolshow expects a positive integer or `last`."), nil, true
		}
		if n < 0 {
			pick = len(idxs) + n + 1
		} else {
			pick = n
		}
	}
	if pick <= 0 || pick > len(idxs) {
		m.notice = fmt.Sprintf("/toolshow %s — only %d event(s).", arg, len(idxs))
		return m.appendSystemMessage(fmt.Sprintf("/toolshow %s: out of range (only %d tool event(s)).", arg, len(idxs))), nil, true
	}
	row := m.chat.transcript[idxs[pick-1]]
	dump := buildToolShowDump(row, pick, len(idxs))
	m.notice = fmt.Sprintf("/toolshow %d — printed below.", pick)
	return m.appendSystemMessage(dump), nil, true
}

func (m Model) toolEventIndices() []int {
	out := make([]int, 0, 16)
	for i, line := range m.chat.transcript {
		if line.Role.Eq(chatRoleTool) {
			out = append(out, i)
		}
	}
	return out
}

func buildToolShowDump(row chatLine, pick, total int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "▸ tool event %d/%d", pick, total)
	if !row.Timestamp.IsZero() {
		fmt.Fprintf(&b, " · %s", row.Timestamp.Format("15:04:05"))
	}
	if row.DurationMs > 0 {
		fmt.Fprintf(&b, " · %dms", row.DurationMs)
	}
	if row.TokenCount > 0 {
		fmt.Fprintf(&b, " · %d tok", row.TokenCount)
	}
	b.WriteString("\n")
	if len(row.EventLines) > 0 {
		ev := row.EventLines[0]
		if ev.ToolName != "" || ev.Status != "" {
			fmt.Fprintf(&b, "tool: %s · status: %s\n", ev.ToolName, ev.Status)
		}
		if ev.Title != "" {
			fmt.Fprintf(&b, "title: %s\n", ev.Title)
		}
		if ev.ParamsPreview != "" {
			fmt.Fprintf(&b, "params: %s\n", ev.ParamsPreview)
		}
		if ev.Reason != "" {
			fmt.Fprintf(&b, "reason: %s\n", ev.Reason)
		}
		if len(ev.DetailLines) > 0 {
			fmt.Fprintf(&b, "detail:\n")
			for _, line := range ev.DetailLines {
				fmt.Fprintf(&b, "  %s\n", line)
			}
		}
		if len(ev.RunningLog) > 0 {
			fmt.Fprintf(&b, "log:\n")
			for _, line := range ev.RunningLog {
				fmt.Fprintf(&b, "  %s\n", line)
			}
		}
	}
	content := strings.TrimSpace(row.Content)
	if content != "" {
		b.WriteString("content:\n")
		for _, line := range strings.Split(content, "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
