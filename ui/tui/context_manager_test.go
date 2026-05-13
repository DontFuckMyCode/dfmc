package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// newContextManagerTestModel returns a minimal Model with initialised
// diagnosticPanelsState so m.contextPanel dereferences are safe.
func newContextManagerTestModel() Model {
	return Model{
		diagnosticPanelsState: newDiagnosticPanelsState(),
	}
}

// newContextManagerEngineModel builds a Model with a real conversation
// manager so delete/refresh round-trips can be tested end-to-end.
func newContextManagerEngineModel(t *testing.T, msgIDs []string) (Model, *conversation.Manager) {
	t.Helper()
	mgr := conversation.New(nil)
	for i, id := range msgIDs {
		role := types.RoleUser
		if i%2 == 1 {
			role = types.RoleAssistant
		}
		mgr.AddMessage("offline", "offline-v1", types.Message{
			ID:      id,
			Role:    role,
			Content: "msg " + id,
		})
	}
	eng := &engine.Engine{
		Config:       config.DefaultConfig(),
		ProjectRoot:  t.TempDir(),
		EventBus:     engine.NewEventBus(),
		Conversation: mgr,
	}
	m := newContextManagerTestModel()
	m.eng = eng
	return m, mgr
}

func TestActivateContextManager_NoEngine(t *testing.T) {
	m := newContextManagerTestModel()
	m.eng = nil
	m = m.activateContextManager()
	if !m.contextPanel.manager.active {
		t.Fatal("expected manager.active=true even without engine")
	}
	if m.contextPanel.manager.statusMsg != "engine not ready" {
		t.Fatalf("expected status 'engine not ready', got %q", m.contextPanel.manager.statusMsg)
	}
}

func TestDeactivateContextManager(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	m.contextPanel.manager.statusMsg = "hello"
	m = m.deactivateContextManager()
	if m.contextPanel.manager.active {
		t.Fatal("expected manager.active=false after deactivate")
	}
	if m.contextPanel.manager.statusMsg != "" {
		t.Fatalf("expected empty status after deactivate, got %q", m.contextPanel.manager.statusMsg)
	}
}

func TestContextManagerKey_UpDown(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	m.contextPanel.manager.rows = []contextManagerRow{
		{index: 1, id: "u-01", role: "user", tokenEst: 10},
		{index: 2, id: "a-01", role: "assistant", tokenEst: 20},
		{index: 3, id: "u-02", role: "user", tokenEst: 30},
	}
	m.contextPanel.manager.marked = make(map[int]bool)

	// Down
	nm, _, handled := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if !handled {
		t.Fatal("expected j handled")
	}
	if nm.contextPanel.manager.cursor != 1 {
		t.Fatalf("expected cursor=1, got %d", nm.contextPanel.manager.cursor)
	}

	// Down again
	nm2, _, _ := nm.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if nm2.contextPanel.manager.cursor != 2 {
		t.Fatalf("expected cursor=2, got %d", nm2.contextPanel.manager.cursor)
	}

	// Down past end — should clamp
	nm3, _, _ := nm2.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if nm3.contextPanel.manager.cursor != 2 {
		t.Fatalf("expected cursor=2 (clamped), got %d", nm3.contextPanel.manager.cursor)
	}

	// Up
	nm4, _, _ := nm3.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if nm4.contextPanel.manager.cursor != 1 {
		t.Fatalf("expected cursor=1, got %d", nm4.contextPanel.manager.cursor)
	}
}

func TestContextManagerKey_SpaceMarks(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	m.contextPanel.manager.rows = []contextManagerRow{
		{index: 1, id: "u-01", role: "user"},
		{index: 2, id: "a-01", role: "assistant"},
	}
	m.contextPanel.manager.marked = make(map[int]bool)

	// Space on row 0 — marks it, advances cursor to 1
	nm, _, _ := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeySpace})
	if !nm.contextPanel.manager.marked[0] {
		t.Fatal("expected row 0 marked")
	}
	if nm.contextPanel.manager.cursor != 1 {
		t.Fatalf("expected cursor=1 after space, got %d", nm.contextPanel.manager.cursor)
	}

	// Space on row 1 — marks it
	nm2, _, _ := nm.handleContextManagerKey(tea.KeyMsg{Type: tea.KeySpace})
	if !nm2.contextPanel.manager.marked[1] {
		t.Fatal("expected row 1 marked")
	}

	// Space on row 1 again — unmarks it
	nm3, _, _ := nm2.handleContextManagerKey(tea.KeyMsg{Type: tea.KeySpace})
	if nm3.contextPanel.manager.marked[1] {
		t.Fatal("expected row 1 unmarked on second space")
	}
}

func TestContextManagerPinAndKeepExcludeDeleteIDs(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.rows = []contextManagerRow{
		{index: 1, id: "u-01", role: "user"},
		{index: 2, id: "a-01", role: "assistant"},
		{index: 3, id: "a-02", role: "assistant"},
	}
	m.contextPanel.manager.marked = map[int]bool{0: true, 1: true, 2: true}
	m.contextPanel.manager.pinned = map[string]bool{"u-01": true}
	m.contextPanel.manager.kept = map[string]bool{"a-01": true}

	ids := m.collectDeleteIDs()
	if len(ids) != 1 || ids[0] != "a-02" {
		t.Fatalf("pinned/kept messages should be excluded from deletes, got %v", ids)
	}
}

func TestContextManagerCompactCandidatesMarksOnlyUnsafeRows(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	m.contextPanel.manager.rows = []contextManagerRow{
		{index: 1, id: "u-01", role: "user", action: "keep"},
		{index: 2, id: "a-01", role: "assistant", action: "compact"},
		{index: 3, id: "a-02", role: "assistant", action: "drop"},
	}
	m.contextPanel.manager.marked = make(map[int]bool)
	m.contextPanel.manager.pinned = map[string]bool{"a-02": true}
	m.contextPanel.manager.kept = make(map[string]bool)

	nm, _, handled := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'C'}})
	if !handled {
		t.Fatal("expected C handled")
	}
	if !nm.contextPanel.manager.marked[1] {
		t.Fatal("compact candidate should be marked")
	}
	if nm.contextPanel.manager.marked[2] {
		t.Fatal("pinned drop candidate should not be marked")
	}
}

func TestContextManagerKey_EscCloses(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	m.contextPanel.manager.rows = []contextManagerRow{
		{index: 1, id: "u-01", role: "user"},
	}
	m.contextPanel.manager.marked = make(map[int]bool)

	nm, _, handled := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !handled {
		t.Fatal("expected esc handled")
	}
	if nm.contextPanel.manager.active {
		t.Fatal("expected manager deactivated on esc")
	}
}

func TestContextManagerKey_EscCancelsConfirm(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	m.contextPanel.manager.confirmDelete = true
	m.contextPanel.manager.rows = []contextManagerRow{
		{index: 1, id: "u-01"},
	}
	m.contextPanel.manager.marked = make(map[int]bool)

	nm, _, _ := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyEsc})
	if nm.contextPanel.manager.confirmDelete {
		t.Fatal("expected confirm cancelled on esc")
	}
	if !nm.contextPanel.manager.active {
		t.Fatal("expected manager to stay active after esc cancels confirm")
	}
}

func TestContextManagerKey_ToggleAll(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	m.contextPanel.manager.rows = []contextManagerRow{
		{index: 1, id: "u-01"},
		{index: 2, id: "a-01"},
		{index: 3, id: "u-02"},
	}
	m.contextPanel.manager.marked = make(map[int]bool)

	// 'a' — mark all
	nm, _, _ := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if len(nm.contextPanel.manager.marked) != 3 {
		t.Fatalf("expected all 3 marked, got %d", len(nm.contextPanel.manager.marked))
	}

	// 'a' again — unmark all
	nm2, _, _ := nm.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if len(nm2.contextPanel.manager.marked) != 0 {
		t.Fatalf("expected all unmarked, got %d", len(nm2.contextPanel.manager.marked))
	}
}

func TestCollectDeleteIDs_Marked(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.rows = []contextManagerRow{
		{index: 1, id: "u-01"},
		{index: 2, id: "a-01"},
		{index: 3, id: "u-02"},
	}
	m.contextPanel.manager.marked = map[int]bool{0: true, 2: true}
	m.contextPanel.manager.cursor = 1

	ids := m.collectDeleteIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 IDs, got %d", len(ids))
	}
	if ids[0] != "u-01" || ids[1] != "u-02" {
		t.Fatalf("unexpected IDs: %v", ids)
	}
}

func TestCollectDeleteIDs_CursorOnly(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.rows = []contextManagerRow{
		{index: 1, id: "u-01"},
		{index: 2, id: "a-01"},
	}
	m.contextPanel.manager.marked = make(map[int]bool)
	m.contextPanel.manager.cursor = 1

	ids := m.collectDeleteIDs()
	if len(ids) != 1 || ids[0] != "a-01" {
		t.Fatalf("expected [a-01], got %v", ids)
	}
}

func TestManagerRoleLabel(t *testing.T) {
	tests := []struct {
		role types.MessageRole
		want string
	}{
		{types.RoleUser, "user"},
		{types.RoleAssistant, "assistant"},
		{types.RoleSystem, "system"},
		{types.RoleTool, "tool"},
	}
	for _, tt := range tests {
		got := managerRoleLabel(tt.role)
		if got != tt.want {
			t.Errorf("managerRoleLabel(%q) = %q, want %q", tt.role, got, tt.want)
		}
	}
}

func TestRenderContextManagerView_Empty(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	m.contextPanel.manager.rows = nil
	m.contextPanel.manager.marked = make(map[int]bool)

	out := m.renderContextManagerView(80, 0)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !containsStr(out, "No messages") {
		t.Fatalf("expected 'No messages' in output, got: %s", out[:min(200, len(out))])
	}
}

func TestRenderContextManagerView_WithRows(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	m.contextPanel.manager.rows = []contextManagerRow{
		{index: 1, id: "u-01", role: "user", tokenEst: 50, preview: "hello world"},
		{index: 2, id: "a-01", role: "assistant", tokenEst: 200, toolCalls: 2, preview: "here is the answer"},
	}
	m.contextPanel.manager.marked = map[int]bool{0: true}
	m.contextPanel.manager.cursor = 1
	m.contextPanel.manager.statusMsg = "2 messages loaded"

	out := m.renderContextManagerView(100, 0)
	if !containsStr(out, "CONTEXT MANAGER") {
		t.Fatal("expected 'CONTEXT MANAGER' banner")
	}
	if !containsStr(out, "marked for deletion") {
		t.Fatal("expected 'marked for deletion' in summary")
	}
}

// ── New tests for previously untested paths ──────────────────────────

func TestRenderContextCockpitShowsNextRequestSections(t *testing.T) {
	m := newContextManagerTestModel()
	m.status.Provider = "minimax"
	m.status.Model = "MiniMax-M2.7"
	m.status.ProviderProfile.MaxContext = 204800
	m.status.ContextIn = &engine.ContextInStatus{
		Provider:           "minimax",
		Model:              "MiniMax-M2.7",
		ProviderMaxContext: 204800,
		TokenCount:         3200,
		MaxTokensTotal:     16000,
		FileCount:          3,
		Reasons:            []string{"task=general profile"},
	}
	m.contextPanel.query = "[[skill:debug]] inspect auth"

	out := strings.Join(m.renderContextCockpitBlock(120), "\n")
	for _, want := range []string{"Next Provider Request", "system", "tools", "memory", "skills", "history", "evidence", "reserves", "empty"} {
		if !strings.Contains(out, want) {
			t.Fatalf("cockpit should contain %q, got:\n%s", want, out)
		}
	}
}

func TestContextManagerKey_HomeEnd(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	m.contextPanel.manager.rows = []contextManagerRow{
		{index: 1, id: "u-01", role: "user"},
		{index: 2, id: "a-01", role: "assistant"},
		{index: 3, id: "u-02", role: "user"},
		{index: 4, id: "a-02", role: "assistant"},
		{index: 5, id: "u-03", role: "user"},
	}
	m.contextPanel.manager.marked = make(map[int]bool)
	m.contextPanel.manager.cursor = 2

	// 'g' -> home
	nm, _, handled := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if !handled {
		t.Fatal("expected g handled")
	}
	if nm.contextPanel.manager.cursor != 0 {
		t.Fatalf("expected cursor=0 after home, got %d", nm.contextPanel.manager.cursor)
	}

	// 'G' -> end
	nm2, _, _ := nm.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	if nm2.contextPanel.manager.cursor != 4 {
		t.Fatalf("expected cursor=4 after end, got %d", nm2.contextPanel.manager.cursor)
	}
}

func TestContextManagerKey_PgUpDown(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	rows := make([]contextManagerRow, 25)
	for i := range rows {
		rows[i] = contextManagerRow{index: i + 1, id: fmt.Sprintf("u-%02d", i), role: "user"}
	}
	m.contextPanel.manager.rows = rows
	m.contextPanel.manager.marked = make(map[int]bool)
	m.contextPanel.manager.cursor = 0

	// pgdown
	nm, _, _ := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyPgDown})
	if nm.contextPanel.manager.cursor != 10 {
		t.Fatalf("expected cursor=10 after pgdown, got %d", nm.contextPanel.manager.cursor)
	}

	// pgup
	nm2, _, _ := nm.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyPgUp})
	if nm2.contextPanel.manager.cursor != 0 {
		t.Fatalf("expected cursor=0 after pgup, got %d", nm2.contextPanel.manager.cursor)
	}
}

func TestContextManagerKey_ConfirmDelete_Enter(t *testing.T) {
	m, mgr := newContextManagerEngineModel(t, []string{"u-11", "a-22", "u-33"})
	m = m.activateContextManager()

	// Mark rows 0 and 2
	m.contextPanel.manager.marked = map[int]bool{0: true, 2: true}
	m.contextPanel.manager.cursor = 1

	// 'x' -> enters confirm mode
	nm, _, _ := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !nm.contextPanel.manager.confirmDelete {
		t.Fatal("expected confirmDelete=true after x")
	}
	if !containsStr(nm.contextPanel.manager.statusMsg, "2 message") {
		t.Fatalf("expected status mentioning 2 messages, got %q", nm.contextPanel.manager.statusMsg)
	}

	// Enter -> executes deletion
	nm2, _, _ := nm.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyEnter})
	if nm2.contextPanel.manager.confirmDelete {
		t.Fatal("expected confirmDelete=false after enter")
	}
	// Should have refreshed - only 1 message left
	msgs := mgr.Active().Messages()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message after delete, got %d", len(msgs))
	}
	if msgs[0].ID != "a-22" {
		t.Fatalf("expected surviving message a-22, got %q", msgs[0].ID)
	}
	if !containsStr(nm2.contextPanel.manager.statusMsg, "deleted") {
		t.Fatalf("expected 'deleted' in status, got %q", nm2.contextPanel.manager.statusMsg)
	}
}

func TestContextManagerKey_ConfirmDelete_Esc(t *testing.T) {
	m, _ := newContextManagerEngineModel(t, []string{"u-11", "a-22"})
	m = m.activateContextManager()
	m.contextPanel.manager.marked = map[int]bool{0: true}

	// 'x' -> confirm
	nm, _, _ := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if !nm.contextPanel.manager.confirmDelete {
		t.Fatal("expected confirmDelete=true")
	}

	// Esc -> cancel confirm
	nm2, _, _ := nm.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyEsc})
	if nm2.contextPanel.manager.confirmDelete {
		t.Fatal("expected confirmDelete=false after esc")
	}
	if !nm2.contextPanel.manager.active {
		t.Fatal("expected manager still active after esc cancels confirm")
	}
	// Messages should be untouched
	if len(nm2.contextPanel.manager.rows) != 2 {
		t.Fatalf("expected 2 rows untouched, got %d", len(nm2.contextPanel.manager.rows))
	}
}

func TestContextManagerKey_QuickDelete(t *testing.T) {
	m, mgr := newContextManagerEngineModel(t, []string{"u-11", "a-22", "u-33"})
	m = m.activateContextManager()
	m.contextPanel.manager.cursor = 1 // cursor on a-22

	// 'D' -> quick-delete message under cursor
	nm, _, handled := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if !handled {
		t.Fatal("expected D handled")
	}
	msgs := mgr.Active().Messages()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages after quick-delete, got %d", len(msgs))
	}
	if !containsStr(nm.contextPanel.manager.statusMsg, "deleted") {
		t.Fatalf("expected 'deleted' in status, got %q", nm.contextPanel.manager.statusMsg)
	}
	// Verify the right message was deleted
	for _, msg := range msgs {
		if msg.ID == "a-22" {
			t.Fatal("a-22 should have been deleted")
		}
	}
}

func TestContextManagerKey_QuickDelete_EmptyRows(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	m.contextPanel.manager.rows = nil
	m.contextPanel.manager.marked = make(map[int]bool)
	m.contextPanel.manager.cursor = 0

	// 'D' on empty -> no-op
	nm, _, handled := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if !handled {
		t.Fatal("expected D handled even on empty")
	}
	if nm.contextPanel.manager.statusMsg != "" {
		t.Fatalf("expected no status on empty D, got %q", nm.contextPanel.manager.statusMsg)
	}
}

func TestContextManagerKey_DeleteNothingSelected(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	m.contextPanel.manager.rows = []contextManagerRow{
		{index: 1, id: "u-01", role: "user"},
	}
	m.contextPanel.manager.marked = make(map[int]bool)
	m.contextPanel.manager.cursor = -1 // cursor out of range

	// 'x' with nothing selected and cursor out of range
	nm, _, _ := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if nm.contextPanel.manager.confirmDelete {
		t.Fatal("should not enter confirm with nothing to delete")
	}
	if !containsStr(nm.contextPanel.manager.statusMsg, "nothing selected") {
		t.Fatalf("expected 'nothing selected' hint, got %q", nm.contextPanel.manager.statusMsg)
	}
}

func TestContextManagerKey_QuickDelete_NoID(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	m.contextPanel.manager.rows = []contextManagerRow{
		{index: 1, id: "(unset)", role: "user"},
	}
	m.contextPanel.manager.marked = make(map[int]bool)
	m.contextPanel.manager.cursor = 0

	// 'D' on message with no ID
	nm, _, _ := m.handleContextManagerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	if !containsStr(nm.contextPanel.manager.statusMsg, "no ID") {
		t.Fatalf("expected 'no ID' message, got %q", nm.contextPanel.manager.statusMsg)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && searchSubstring(s, sub)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestRenderContextManagerView_ViewportScroll(t *testing.T) {
	m := newContextManagerTestModel()
	m.contextPanel.manager.active = true
	// Create 50 rows — well above any viewport
	rows := make([]contextManagerRow, 50)
	for i := range rows {
		rows[i] = contextManagerRow{
			index: i + 1,
			id:    fmt.Sprintf("m-%02d", i+1),
			role:  "user",
		}
	}
	m.contextPanel.manager.rows = rows
	m.contextPanel.manager.marked = make(map[int]bool)
	m.contextPanel.manager.cursor = 0

	// With small height=15, visibleRows = max(5, 15-12) = 5
	outSmall := m.renderContextManagerView(100, 15)
	// With large height=40, visibleRows = max(5, 40-12) = 28
	outLarge := m.renderContextManagerView(100, 40)

	// Small viewport should show scroll indicator
	if !containsStr(outSmall, "showing") {
		t.Fatal("expected scroll indicator in small viewport output")
	}
	// Small viewport should contain fewer row lines than large
	smallLines := len(strings.Split(outSmall, "\n"))
	largeLines := len(strings.Split(outLarge, "\n"))
	if smallLines >= largeLines {
		t.Fatalf("expected small viewport (%d lines) < large viewport (%d lines)", smallLines, largeLines)
	}

	// Cursor at row 49 with small height — should scroll
	m.contextPanel.manager.cursor = 49
	outCursor := m.renderContextManagerView(100, 15)
	if !containsStr(outCursor, "m-50") {
		t.Fatal("expected cursor row m-50 visible after scroll")
	}
	// Cursor at end: startRow=45, endRow=50==len(rows) → no scroll indicator
	if containsStr(outCursor, "showing") {
		t.Fatal("should NOT show scroll indicator when all remaining rows visible at end")
	}
	// Cursor in middle: startRow=20, endRow=25 < 50 → scroll indicator
	m.contextPanel.manager.cursor = 24
	outMid := m.renderContextManagerView(100, 15)
	if !containsStr(outMid, "showing") {
		t.Fatal("expected scroll indicator when cursor is in the middle")
	}
}
