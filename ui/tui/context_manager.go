package tui

// context_manager.go — the interactive Context Manager sub-view inside
// the Context panel. Activated with 'm' when the Context panel is
// showing active context. Lets the user browse conversation messages,
// multi-select them with space, and delete selected messages to free
// up context window budget.
//
// Design principles:
//   - Read-heavy: loads once on activation, refreshes on delete
//   - Space bar toggles selection (vim visual-mode feel)
//   - 'x' or 'd' deletes all marked messages (with confirmation)
//   - Single 'D' deletes the message under cursor without marking
//   - 'a' selects all / deselects all (toggle)
//   - Esc exits manager mode, returns to normal context view
//   - Each row shows: [âœ“/ ] #N  role  ~tokens  toolsÃ—N  preview
//
// Companion siblings:
//   - context_panel.go        parent view orchestration
//   - context_panel_keys.go   context-level keyboard routing
//   - context_panel_blocks.go budget/breakdown render helpers

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// contextManagerRow is one message row in the manager list.
type contextManagerRow struct {
	index     int    // 1-based display index
	id        string // message ID (e.g. "u-a3f2")
	role      string // "user", "assistant", "system", "tool"
	tokenEst  int    // rough token count
	toolCalls int    // number of tool calls in this message
	preview   string // truncated content preview
	pinned    bool   // user pinned this row in the manager
	kept      bool   // user marked this row as keep
	action    string // keep/drop/pin/compact suggestion
}

// activateContextManager loads the active conversation's messages into
// the manager state for interactive browsing and deletion.
func (m Model) activateContextManager() Model {
	m.contextPanel.manager.active = true
	m.contextPanel.manager.cursor = 0
	m.contextPanel.manager.scroll = 0
	m.contextPanel.manager.marked = make(map[int]bool)
	if m.contextPanel.manager.pinned == nil {
		m.contextPanel.manager.pinned = make(map[string]bool)
	}
	if m.contextPanel.manager.kept == nil {
		m.contextPanel.manager.kept = make(map[string]bool)
	}
	m.contextPanel.manager.confirmDelete = false
	m.contextPanel.manager.rows = nil
	m.contextPanel.manager.statusMsg = ""

	if m.eng == nil {
		m.contextPanel.manager.statusMsg = "engine not ready"
		return m
	}

	conv := m.eng.ConversationActive()
	if conv == nil {
		m.contextPanel.manager.statusMsg = "no active conversation"
		return m
	}

	msgs := conv.Messages()
	pinned := copyBoolMap(m.contextPanel.manager.pinned)
	kept := copyBoolMap(m.contextPanel.manager.kept)
	rows := make([]contextManagerRow, len(msgs))
	for i, msg := range msgs {
		id := strings.TrimSpace(msg.ID)
		if id == "" {
			id = "(unset)"
		}
		tokenEst := managerMessageTokens(msg)
		toolCalls := len(msg.ToolCalls)
		preview := managerMessagePreview(msg)
		rows[i] = contextManagerRow{
			index:     i + 1,
			id:        id,
			role:      managerRoleLabel(msg.Role),
			tokenEst:  tokenEst,
			toolCalls: toolCalls,
			preview:   preview,
			pinned:    pinned[id],
			kept:      kept[id],
			action:    managerActionSuggestion(msg, i, len(msgs), tokenEst),
		}
	}
	m.contextPanel.manager.pinned = pinned
	m.contextPanel.manager.kept = kept
	m.contextPanel.manager.rows = rows
	if len(rows) > 0 {
		m.contextPanel.manager.statusMsg = fmt.Sprintf("%d messages loaded", len(rows))
	}
	return m
}

// deactivateContextManager returns to the normal context view.
func (m Model) deactivateContextManager() Model {
	m.contextPanel.manager = contextManagerState{}
	return m
}

// refreshContextManager reloads message list after a deletion.
// Preserves the status message set by the caller (e.g. "deleted N/M messages").
func (m Model) refreshContextManager() Model {
	oldMarked := m.contextPanel.manager.marked
	oldRows := m.contextPanel.manager.rows
	oldCursor := m.contextPanel.manager.cursor
	oldStatus := m.contextPanel.manager.statusMsg
	m = m.activateContextManager()
	// Restore caller's status message over the "N messages loaded" that
	// activateContextManager writes.
	if oldStatus != "" {
		m.contextPanel.manager.statusMsg = oldStatus
	}
	// Restore marks for IDs that still exist.
	// Old marks are by index; we resolve through the old rows' IDs
	// to find the matching new index after deletion shifts them.
	if len(oldMarked) > 0 && len(oldRows) > 0 {
		newMarked := make(map[int]bool, len(oldMarked))
		newByID := make(map[string]int, len(m.contextPanel.manager.rows))
		for ni, row := range m.contextPanel.manager.rows {
			newByID[row.id] = ni
		}
		for oldIdx := range oldMarked {
			if oldIdx < len(oldRows) {
				if newIdx, ok := newByID[oldRows[oldIdx].id]; ok {
					newMarked[newIdx] = true
				}
			}
		}
		m.contextPanel.manager.marked = newMarked
	}
	// Clamp cursor
	if oldCursor >= len(m.contextPanel.manager.rows) {
		oldCursor = max(0, len(m.contextPanel.manager.rows)-1)
	}
	m.contextPanel.manager.cursor = oldCursor
	return m
}

// deleteContextManagerSelected removes all marked messages (or just the
// cursor message if none are marked) from the active conversation.
func (m Model) deleteContextManagerSelected() Model {
	if m.eng == nil || m.eng.Conversation == nil {
		m.contextPanel.manager.statusMsg = "engine not available"
		return m
	}

	ids := m.collectDeleteIDs()
	if len(ids) == 0 {
		m.contextPanel.manager.statusMsg = "nothing selected"
		return m
	}

	dropped := m.eng.Conversation.RemoveMessagesByID(ids)
	m.contextPanel.manager.statusMsg = fmt.Sprintf("deleted %d/%d messages", dropped, len(ids))
	m.contextPanel.manager.confirmDelete = false

	return m.refreshContextManager()
}

// collectDeleteIDs gathers message IDs from marked rows (multi-select)
// or the single cursor row if nothing is marked.
func (m Model) collectDeleteIDs() []string {
	mgr := m.contextPanel.manager
	ids := []string{}

	if len(mgr.marked) > 0 {
		for idx, row := range mgr.rows {
			if mgr.marked[idx] {
				if id := strings.TrimSpace(row.id); id != "" && id != "(unset)" {
					if mgr.pinned[id] || mgr.kept[id] {
						continue
					}
					ids = append(ids, id)
				}
			}
		}
	} else if mgr.cursor >= 0 && mgr.cursor < len(mgr.rows) {
		row := mgr.rows[mgr.cursor]
		if id := strings.TrimSpace(row.id); id != "" && id != "(unset)" {
			if mgr.pinned[id] || mgr.kept[id] {
				return ids
			}
			ids = append(ids, id)
		}
	}
	return ids
}

func copyBoolMap(src map[string]bool) map[string]bool {
	dst := make(map[string]bool)
	for k, v := range src {
		if strings.TrimSpace(k) != "" && v {
			dst[k] = true
		}
	}
	return dst
}

// renderContextManagerView renders the interactive message manager UI.
func (m Model) renderContextManagerView(width, height int) string {
	mgr := m.contextPanel.manager
	width = clampInt(width, 40, 200)

	title := titleStyle.Bold(true).Render("CONTEXT MANAGER - MESSAGES")
	chip := infoStyle.Render(" INTERACTIVE ")
	if mgr.confirmDelete {
		chip = warnStyle.Render(" CONFIRM ")
	}
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chip)-4, 1)
	lines := []string{title + strings.Repeat(" ", gap) + chip}
	lines = append(lines, subtleStyle.Render("↑↓ navigate · space mark · enter confirm · esc back"))
	if mgr.statusMsg != "" {
		lines = append(lines, accentStyle.Render("  > ")+mgr.statusMsg)
	}
	lines = append(lines, renderDivider(width-2))
	if len(mgr.rows) == 0 {
		lines = append(lines, "", subtleStyle.Render("  No messages in active conversation."))
		return strings.Join(lines, "\n")
	}

	totalTokens, markedCount, pinnedCount, keptCount := 0, 0, 0, 0
	for i, row := range mgr.rows {
		totalTokens += row.tokenEst
		if mgr.marked[i] {
			markedCount++
		}
		if row.pinned {
			pinnedCount++
		}
		if row.kept {
			keptCount++
		}
	}
	summary := fmt.Sprintf("  %d msgs · ~%d tokens", len(mgr.rows), totalTokens)
	if markedCount > 0 {
		summary += fmt.Sprintf(" · %d marked for deletion", markedCount)
	}
	if pinnedCount > 0 {
		summary += fmt.Sprintf(" · %d pinned", pinnedCount)
	}
	if keptCount > 0 {
		summary += fmt.Sprintf(" · %d kept", keptCount)
	}
	lines = append(lines, accentStyle.Render(summary))

	header := fmt.Sprintf("  %-3s  %-3s  %-12s  %-9s  %-7s  %-7s  %-8s  %s",
		" ", "#", "ID", "ROLE", "~TOK", "ACTION", "FLAGS", "PREVIEW")
	lines = append(lines, subtleStyle.Render(header))
	lines = append(lines, "  "+strings.Repeat("-", min(width-4, 96)))

	visibleRows := 20
	if height > 0 {
		visibleRows = max(5, height-12)
	}
	startRow := 0
	if mgr.cursor >= visibleRows {
		startRow = mgr.cursor - visibleRows + 1
	}
	endRow := min(startRow+visibleRows, len(mgr.rows))
	for i := startRow; i < endRow; i++ {
		row := mgr.rows[i]
		check := " "
		if mgr.marked[i] {
			check = warnStyle.Render("x")
		}
		rowStr := fmt.Sprintf("  %-3s  %-3d  %-12s  %-9s  %-7d  %-7s  %-8s  %s",
			check,
			row.index,
			truncateStr(row.id, 12),
			row.role,
			row.tokenEst,
			managerActionCell(row),
			managerFlagCell(row),
			truncateStr(row.preview, 50),
		)
		if i == mgr.cursor {
			rowStr = accentStyle.Bold(true).Render("> ") + rowStr[2:]
			rowStr = lipgloss.NewStyle().Background(colorRowCursorBg).Render(rowStr)
		} else if mgr.marked[i] {
			rowStr = lipgloss.NewStyle().Background(colorRowMarkedDeleteBg).Render(rowStr)
		}
		lines = append(lines, rowStr)
	}
	if endRow < len(mgr.rows) {
		lines = append(lines, subtleStyle.Render(
			fmt.Sprintf("  ... showing %d-%d of %d (j/k to scroll)", startRow+1, endRow, len(mgr.rows))))
	}
	if mgr.confirmDelete {
		ids := m.collectDeleteIDs()
		lines = append(lines, "")
		lines = append(lines, warnStyle.Bold(true).Render(
			fmt.Sprintf("  Delete %d unpinned/unkept message(s)? Press Enter to confirm, Esc to cancel.", len(ids))))
	}
	return strings.Join(lines, "\n")
}

// renderContextManagerViewSized renders with explicit height constraint.
func (m Model) renderContextManagerViewSized(width, h int) string {
	full := m.renderContextManagerView(width, h)
	return renderContextPanelLines(strings.Split(full, "\n"), 0, h)
}

// â”€â”€ Helper functions â”€â”€

func managerRoleLabel(role types.MessageRole) string {
	switch role {
	case types.RoleUser:
		return "user"
	case types.RoleAssistant:
		return "assistant"
	case types.RoleSystem:
		return "system"
	case types.RoleTool:
		return "tool"
	}
	r := strings.TrimSpace(string(role))
	if r == "" {
		return "?"
	}
	return r
}

func managerMessageTokens(msg types.Message) int {
	if msg.TokenCnt > 0 {
		return msg.TokenCnt
	}
	return tokens.Estimate(msg.Content) + len(msg.ToolCalls)*20
}

func managerMessagePreview(msg types.Message) string {
	content := msg.Content
	// Strip thinking blocks for preview
	if idx := strings.Index(content, "</thinking>"); idx >= 0 {
		content = strings.TrimSpace(content[idx+len("</thinking>"):])
	}
	content = strings.ReplaceAll(content, "\n", " ")
	content = strings.TrimSpace(content)
	if len([]rune(content)) > 60 {
		return string([]rune(content)[:57]) + "..."
	}
	if content == "" {
		return "(empty)"
	}
	return content
}

func managerToolCell(count int) string {
	if count == 0 {
		return "-"
	}
	return fmt.Sprintf("Ã—%d", count)
}

func managerActionSuggestion(msg types.Message, index, total, tokenEst int) string {
	content := strings.ToLower(strings.TrimSpace(msg.Content))
	switch {
	case msg.Role == types.RoleSystem:
		return "pin"
	case index >= max(0, total-4):
		return "keep"
	case content == "" && len(msg.ToolCalls) == 0 && len(msg.Results) == 0:
		return "drop"
	case strings.Contains(content, "tool round hard cap") || strings.Contains(content, "unverified edits"):
		return "drop"
	case tokenEst >= 1800 || len(msg.Results) > 0 || len(msg.ToolCalls) > 0:
		return "compact"
	default:
		return "keep"
	}
}

func managerActionCell(row contextManagerRow) string {
	action := strings.TrimSpace(row.action)
	if action == "" {
		action = "keep"
	}
	return action
}

func managerFlagCell(row contextManagerRow) string {
	flags := []string{}
	if row.pinned {
		flags = append(flags, "pin")
	}
	if row.kept {
		flags = append(flags, "keep")
	}
	if row.toolCalls > 0 {
		flags = append(flags, managerToolCell(row.toolCalls))
	}
	if len(flags) == 0 {
		return "-"
	}
	return strings.Join(flags, ",")
}

func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}
