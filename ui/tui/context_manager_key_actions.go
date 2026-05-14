package tui

import (
	"fmt"
	"strings"
)

func (m Model) handleContextManagerEscape() Model {
	mgr := &m.contextPanel.manager
	if mgr.confirmDelete {
		mgr.confirmDelete = false
		mgr.statusMsg = "delete cancelled"
		return m
	}
	return m.deactivateContextManager()
}

func (m Model) handleContextManagerEnter() Model {
	mgr := &m.contextPanel.manager
	if mgr.confirmDelete {
		return m.deleteContextManagerSelected()
	}
	mgr.toggleCurrentMark(false)
	return m
}

func (m Model) beginContextManagerDelete() Model {
	mgr := &m.contextPanel.manager
	ids := m.collectDeleteIDs()
	if len(ids) == 0 {
		mgr.statusMsg = "nothing selected \u2014 use space to mark messages"
		return m
	}
	mgr.confirmDelete = true
	mgr.statusMsg = fmt.Sprintf("press Enter to delete %d message(s), Esc to cancel", len(ids))
	return m
}

func (m Model) deleteContextManagerCursor() Model {
	mgr := &m.contextPanel.manager
	if !mgr.hasCursorRow() {
		return m
	}
	row := mgr.rows[mgr.cursor]
	id := strings.TrimSpace(row.id)
	if id == "" || id == "(unset)" {
		mgr.statusMsg = "message has no ID \u2014 cannot delete"
		return m
	}
	if m.eng == nil || m.eng.Conversation == nil {
		mgr.statusMsg = "engine not available"
		return m
	}
	dropped := m.eng.Conversation.RemoveMessagesByID([]string{id})
	mgr.statusMsg = fmt.Sprintf("deleted message #%d (id=%s, dropped=%d)", row.index, id, dropped)
	return m.refreshContextManager()
}

func (m Model) toggleContextManagerPin() Model {
	mgr := &m.contextPanel.manager
	id, ok := mgr.currentRowID()
	if !ok {
		return m
	}
	if mgr.pinned == nil {
		mgr.pinned = make(map[string]bool)
	}
	mgr.pinned[id] = !mgr.pinned[id]
	if !mgr.pinned[id] {
		delete(mgr.pinned, id)
	}
	delete(mgr.marked, mgr.cursor)
	m = m.refreshContextManager()
	m.contextPanel.manager.statusMsg = "pin toggled for " + id
	return m
}

func (m Model) toggleContextManagerKeep() Model {
	mgr := &m.contextPanel.manager
	id, ok := mgr.currentRowID()
	if !ok {
		return m
	}
	if mgr.kept == nil {
		mgr.kept = make(map[string]bool)
	}
	mgr.kept[id] = !mgr.kept[id]
	if !mgr.kept[id] {
		delete(mgr.kept, id)
	}
	delete(mgr.marked, mgr.cursor)
	m = m.refreshContextManager()
	m.contextPanel.manager.statusMsg = "keep toggled for " + id
	return m
}

func (mgr *contextManagerState) moveCursor(delta int) {
	rowCount := len(mgr.rows)
	if rowCount == 0 {
		mgr.cursor = 0
		return
	}
	mgr.cursor = minInt(rowCount-1, maxInt(0, mgr.cursor+delta))
}

func (mgr *contextManagerState) pageCursor(delta int) {
	rowCount := len(mgr.rows)
	if rowCount == 0 {
		mgr.cursor = 0
		return
	}
	mgr.cursor = minInt(rowCount-1, maxInt(0, mgr.cursor+delta))
}

func (mgr *contextManagerState) toggleCurrentMark(autoAdvance bool) {
	if !mgr.hasCursorRow() {
		return
	}
	mgr.marked[mgr.cursor] = !mgr.marked[mgr.cursor]
	if !mgr.marked[mgr.cursor] {
		delete(mgr.marked, mgr.cursor)
	}
	if autoAdvance && mgr.cursor < len(mgr.rows)-1 {
		mgr.cursor++
	}
}

func (mgr *contextManagerState) markCompactDropCandidates() {
	count := 0
	for i, row := range mgr.rows {
		if row.action != "compact" && row.action != "drop" {
			continue
		}
		if mgr.pinned[row.id] || mgr.kept[row.id] {
			continue
		}
		mgr.marked[i] = true
		count++
	}
	mgr.statusMsg = fmt.Sprintf("%d compact/drop candidate(s) marked", count)
}

func (mgr *contextManagerState) toggleAllMarks() {
	rowCount := len(mgr.rows)
	allMarked := len(mgr.marked) == rowCount && rowCount > 0
	if allMarked {
		mgr.marked = make(map[int]bool)
		mgr.statusMsg = "all unmarked"
		return
	}
	for i := 0; i < rowCount; i++ {
		mgr.marked[i] = true
	}
	mgr.statusMsg = fmt.Sprintf("all %d marked", rowCount)
}

func (mgr *contextManagerState) currentRowID() (string, bool) {
	if !mgr.hasCursorRow() {
		return "", false
	}
	id := strings.TrimSpace(mgr.rows[mgr.cursor].id)
	return id, id != "" && id != "(unset)"
}

func (mgr *contextManagerState) hasCursorRow() bool {
	return len(mgr.rows) > 0 && mgr.cursor >= 0 && mgr.cursor < len(mgr.rows)
}
