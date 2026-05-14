package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestTelegramOverlayRoutesSetupKeys(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.ui.panelOverlayKind = "telegram"

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm := modelValue(t, next)
	if mm.telegram.setupSelected != 1 {
		t.Fatalf("telegram setup down key should select allowed-users row, got %d", mm.telegram.setupSelected)
	}

	next, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm = modelValue(t, next)
	if !mm.telegram.formActive {
		t.Fatal("telegram setup enter should open config form")
	}
	if !mm.telegram.editingUsers || mm.telegram.editingToken {
		t.Fatalf("enter on selected users row should edit users, token=%v users=%v", mm.telegram.editingToken, mm.telegram.editingUsers)
	}
}

func modelValue(t *testing.T, got tea.Model) Model {
	t.Helper()
	switch v := got.(type) {
	case Model:
		return v
	case *Model:
		return *v
	default:
		t.Fatalf("expected TUI model, got %T", got)
		return Model{}
	}
}

func TestTelegramMessageAddedOnlyUpdatesTelegramPanel(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{newChatLine(chatRoleUser, "chat stays clean")}
	m.activityLog = []string{"activity stays clean"}
	m.notice = "keep notice"

	next, cmd := m.Update(telegramMessageAddedMsg{
		msg: telegramMessageItem{from: "User 42", text: "hello from telegram", time: "12:00"},
	})
	mm := modelValue(t, next)
	if cmd == nil {
		t.Fatal("telegram message handler should keep listening for the next panel event")
	}
	if len(mm.telegram.messages) != 1 {
		t.Fatalf("telegram panel should receive message, got %#v", mm.telegram.messages)
	}
	if got := mm.telegram.messages[0].text; got != "hello from telegram" {
		t.Fatalf("unexpected telegram panel text %q", got)
	}
	if len(mm.chat.transcript) != 1 || mm.chat.transcript[0].Content != "chat stays clean" {
		t.Fatalf("telegram message leaked into chat transcript: %#v", mm.chat.transcript)
	}
	if len(mm.activityLog) != 1 || mm.activityLog[0] != "activity stays clean" {
		t.Fatalf("telegram message leaked into activity log: %#v", mm.activityLog)
	}
	if mm.notice != "keep notice" {
		t.Fatalf("telegram message should not overwrite notice, got %q", mm.notice)
	}
}

func TestTelegramLogFormattingStripsPrefix(t *testing.T) {
	got := formatTelegramLog("[telegram] user=%d rate limited", 42)
	if got != "user=42 rate limited" {
		t.Fatalf("unexpected telegram log text %q", got)
	}
}

// Regression: pressing 'q' while in the Telegram token form must
// NOT close the panel — it must be typed into the token field.
// Bug: panel overlay q-handler at update_keypress.go:145 would intercept
// 'q' before routing to handleTelegramFormKey.
func TestTelegramFormAcceptsQLetter(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.ui.panelOverlayKind = "telegram"

	// Open form in token-edit mode
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = modelValue(t, next)
	if !m.telegram.formActive {
		t.Fatal("form should be active after enter")
	}
	if !m.telegram.editingToken {
		t.Fatal("should be editing token after opening from empty-state setup")
	}

	// Type 'q' — it must land in tokenInput, not close the panel
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = modelValue(t, next)

	if m.telegram.formActive == false {
		t.Fatal("BUG: 'q' closed the form — panel overlay q-handler intercepted it")
	}
	if m.telegram.tokenInput != "q" {
		t.Fatalf("expected tokenInput='q', got %q", m.telegram.tokenInput)
	}
	if m.ui.panelOverlayKind != "telegram" {
		t.Fatal("panel should still be open")
	}
}

func TestTelegramConnectedScreenShowsActionsMenu(t *testing.T) {
	m := telegramConnectedModel()
	view := m.renderTelegramPanel()
	for _, want := range []string{"Connected", "u users", "enter/a actions"} {
		if !strings.Contains(view, want) {
			t.Fatalf("connected telegram panel should expose %q, got:\n%s", want, view)
		}
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = modelValue(t, next)
	view = m.renderTelegramPanel()
	if !m.actionMenu.open || m.actionMenu.owner != "Telegram" {
		t.Fatalf("enter should open telegram action menu, got %#v", m.actionMenu)
	}
	if !strings.Contains(view, "Edit allowed users") {
		t.Fatalf("telegram action menu should be rendered in connected panel, got:\n%s", view)
	}
}

func TestTelegramConnectedUsersShortcutOpensUsersEditor(t *testing.T) {
	m := telegramConnectedModel()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = modelValue(t, next)

	if !m.telegram.formActive {
		t.Fatal("u should open telegram config form")
	}
	if !m.telegram.editingUsers || m.telegram.editingToken {
		t.Fatalf("u should focus allowed users, token=%v users=%v", m.telegram.editingToken, m.telegram.editingUsers)
	}
	if m.telegram.tokenInput != "token" || m.telegram.allowedUsersInput != "42" {
		t.Fatalf("form should preload current config, token=%q users=%q", m.telegram.tokenInput, m.telegram.allowedUsersInput)
	}
}

func TestTelegramFormTabSwitchesFields(t *testing.T) {
	m := telegramConnectedModel()
	m = m.openTelegramForm()
	if !m.telegram.editingToken {
		t.Fatal("regular config form should start on token field")
	}

	next, _ := m.handleTelegramFormKey(tea.KeyMsg{Type: tea.KeyTab})
	m = modelValue(t, next)
	if !m.telegram.editingUsers || m.telegram.editingToken {
		t.Fatalf("tab should switch to users field, token=%v users=%v", m.telegram.editingToken, m.telegram.editingUsers)
	}

	next, _ = m.handleTelegramFormKey(tea.KeyMsg{Type: tea.KeyUp})
	m = modelValue(t, next)
	if !m.telegram.editingToken || m.telegram.editingUsers {
		t.Fatalf("up should focus token field, token=%v users=%v", m.telegram.editingToken, m.telegram.editingUsers)
	}
}

func TestTelegramMessageHistoryWrapsLongLogs(t *testing.T) {
	m := telegramConnectedModel()
	m.appendTelegramLog("Config", strings.Repeat("saved-token-and-user-log ", 12), true)
	rows := renderTelegramMessageRows(m.telegram.messages[0], 48)
	if len(rows) < 2 {
		t.Fatalf("expected wrapped telegram log rows, got %#v", rows)
	}
	for _, row := range rows {
		if got := lipgloss.Width(row); got > 48 {
			t.Fatalf("telegram log row should fit panel width, width=%d row=%q rows=%#v", got, row, rows)
		}
	}
}

func TestTelegramFormTruncatesLongTokenAndUsers(t *testing.T) {
	m := telegramConnectedModel()
	m.telegram.formActive = true
	m.telegram.editingToken = true
	m.telegram.tokenInput = strings.Repeat("token", 40)
	m.telegram.allowedUsersInput = strings.Repeat("123456789,", 20)

	view := m.renderTelegramForm(60)
	if strings.Contains(view, m.telegram.tokenInput) {
		t.Fatalf("long telegram token should not render unbounded:\n%s", view)
	}
	if strings.Contains(view, m.telegram.allowedUsersInput) {
		t.Fatalf("long allowed-user list should not render unbounded:\n%s", view)
	}
}

func telegramConnectedModel() Model {
	cfg := config.DefaultConfig()
	cfg.Telegram.Enabled = true
	cfg.Telegram.Token = "token"
	cfg.Telegram.AllowedUsers = []int64{42}

	m := NewModel(context.Background(), &engine.Engine{Config: cfg})
	m.ui.panelOverlayKind = "telegram"
	m.width = 100
	m.height = 30
	return m
}
