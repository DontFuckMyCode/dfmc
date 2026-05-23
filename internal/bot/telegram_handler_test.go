package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// chatPtr is a helper to create a *tgbotapi.Chat with the given ID.
func chatPtr(id int64) *tgbotapi.Chat {
	return &tgbotapi.Chat{ID: id}
}

// userPtr is a helper to create a *tgbotapi.User.
func userPtr(id int64, username string) *tgbotapi.User {
	return &tgbotapi.User{ID: id, UserName: username}
}

// cmdEntity returns a bot_command MessageEntity covering the first word.
func cmdEntity(cmdLen int) []tgbotapi.MessageEntity {
	return []tgbotapi.MessageEntity{
		{Type: "bot_command", Offset: 0, Length: cmdLen},
	}
}

// fakeTelegramServer returns a test server + TelegramBot wired to it.
func fakeTelegramServer(t *testing.T) (*httptest.Server, *TelegramBot, *[]string) {
	t.Helper()
	var logs []string
	var mu sync.Mutex
	logf := func(format string, args ...any) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf(format, args...))
		mu.Unlock()
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/botTESTTOKEN/getMe", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true,
			"result": map[string]any{
				"id":       12345,
				"is_bot":   true,
				"username": "test_dfmc_bot",
			},
		})
	})

	mux.HandleFunc("/botTESTTOKEN/sendMessage", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok":     true,
			"result": map[string]any{"message_id": 1},
		})
	})

	mux.HandleFunc("/botTESTTOKEN/answerCallbackQuery", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/botTESTTOKEN/getUpdates", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{}})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	api, err := tgbotapi.NewBotAPIWithClient("TESTTOKEN", srv.URL+"/bot%s/%s", srv.Client())
	if err != nil {
		t.Fatalf("NewBotAPIWithClient: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	bot := &TelegramBot{
		api:          api,
		token:        "TESTTOKEN",
		allowedUsers: make(map[int64]struct{}),
		chatIDs:      make(map[int64]int64),
		lastAction:   make(map[int64]time.Time),
		ctx:          ctx,
		cancel:       cancel,
	}
	bot.SetLogger(logf)
	return srv, bot, &logs
}

// --- Health ---

func TestHealthReturnsFields(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	b.SetAllowedUsers([]int64{1, 2})

	h := b.Health()
	if h["bot_username"] != "test_dfmc_bot" {
		t.Errorf("bot_username = %v, want test_dfmc_bot", h["bot_username"])
	}
	if h["registered"] != 0 {
		t.Errorf("registered = %v, want 0", h["registered"])
	}
	if h["allowed_users"] != 2 {
		t.Errorf("allowed_users = %v, want 2", h["allowed_users"])
	}
	if h["token_set"] != true {
		t.Error("token_set should be true")
	}
}

// --- SendToUser / SendToChat / Broadcast ---

func TestSendToUser_NotRegistered(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	err := b.SendToUser(999, "hello")
	if err == nil {
		t.Fatal("expected error for unregistered user")
	}
	if !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSendToUser_Registered(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	b.mu.Lock()
	b.chatIDs[10] = 100
	b.mu.Unlock()
	err := b.SendToUser(10, "hello")
	if err != nil {
		t.Fatalf("SendToUser registered: %v", err)
	}
}

func TestSendToChat_SendsMessage(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	err := b.SendToChat(12345, "test message")
	if err != nil {
		t.Fatalf("SendToChat: %v", err)
	}
}

func TestBroadcast_NoUsers(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	err := b.Broadcast("hello all")
	if err != nil {
		t.Fatalf("Broadcast with no users should succeed, got: %v", err)
	}
}

func TestBroadcast_SendsToAllRegistered(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	b.mu.Lock()
	b.chatIDs[10] = 100
	b.chatIDs[20] = 200
	b.mu.Unlock()
	err := b.Broadcast("announcement")
	if err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
}

// --- Command handlers ---

func TestCmdStart(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	msg := &tgbotapi.Message{Chat: chatPtr(42)}
	b.cmdStart(msg) // shouldn't panic
}

func TestCmdHelp(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	msg := &tgbotapi.Message{Chat: chatPtr(42)}
	b.cmdHelp(msg)
}

func TestCmdStatus(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	msg := &tgbotapi.Message{Chat: chatPtr(42)}
	b.cmdStatus(msg)
}

func TestCmdID(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	msg := &tgbotapi.Message{Chat: chatPtr(42), From: userPtr(77, "tester")}
	b.cmdID(msg)
}

func TestCmdID_NilMsg(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	b.cmdID(nil)
}

func TestCmdID_NilFrom(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	msg := &tgbotapi.Message{Chat: chatPtr(42)}
	b.cmdID(msg)
}

func TestCmdChat_WithArgs(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	msg := &tgbotapi.Message{
		Chat: chatPtr(42),
		From: userPtr(7, "user"),
		Text: "/chat hello world",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: 5},
		},
	}
	b.mu.Lock()
	b.chatIDs[7] = 42
	b.mu.Unlock()

	var called atomic.Int32
	b.SetOnMessage(func(userID int64, text string, replyFn func(string)) {
		called.Add(1)
		if text != "hello world" {
			t.Errorf("onMessage text = %q, want %q", text, "hello world")
		}
		if userID != 7 {
			t.Errorf("onMessage userID = %d, want 7", userID)
		}
	})
	b.cmdChat(msg)
	if called.Load() != 1 {
		t.Fatal("onMessage should have been called once")
	}
}

func TestCmdChat_EmptyArgs(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	msg := &tgbotapi.Message{
		Chat:     chatPtr(42),
		From:     userPtr(7, "user"),
		Text:     "/chat",
		Entities: cmdEntity(5),
	}
	b.cmdChat(msg)
}

func TestCmdChat_NoEngine(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	msg := &tgbotapi.Message{
		Chat:     chatPtr(42),
		From:     userPtr(7, "user"),
		Text:     "/chat test",
		Entities: cmdEntity(5),
	}
	b.cmdChat(msg)
}

func TestCmdSubscribe(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	msg := &tgbotapi.Message{
		Chat: chatPtr(99),
		From: userPtr(55, "subber"),
	}
	b.cmdSubscribe(msg)
	b.mu.RLock()
	chatID, ok := b.chatIDs[55]
	b.mu.RUnlock()
	if !ok || chatID != 99 {
		t.Fatal("cmdSubscribe should register chat ID")
	}
}

func TestCmdUnsubscribe(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	msg := &tgbotapi.Message{Chat: chatPtr(42)}
	b.cmdUnsubscribe(msg)
}

// --- handleCommand ---

func TestHandleCommand_AllRoutes(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	cmds := []struct {
		text string
	}{
		{"/start"},
		{"/help"},
		{"/status"},
		{"/id"},
		{"/chat hello"},
		{"/subscribe"},
		{"/unsubscribe"},
		{"/unknown_xyz"},
	}
	for _, tc := range cmds {
		cmdLen := strings.Index(tc.text, " ")
		if cmdLen < 0 {
			cmdLen = len(tc.text)
		}
		msg := &tgbotapi.Message{
			Chat:     chatPtr(42),
			From:     userPtr(7, "tester"),
			Text:     tc.text,
			Entities: cmdEntity(cmdLen),
		}
		b.handleCommand(msg) // none should panic
	}
}

// --- handleIncomingMessage ---

func TestHandleIncomingMessage_OpenCommandsBeforeAuth(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	// /help, /start, /id, /whoami should work without allowedUsers
	for _, text := range []string{"/help", "/start", "/id", "/whoami"} {
		cmdLen := strings.Index(text, " ")
		if cmdLen < 0 {
			cmdLen = len(text)
		}
		msg := &tgbotapi.Message{
			Chat:     chatPtr(42),
			From:     userPtr(99, "stranger"),
			Text:     text,
			Entities: cmdEntity(cmdLen),
		}
		update := tgbotapi.Update{Message: msg}
		b.handleIncomingMessage(update)
	}
}

func TestHandleIncomingMessage_Unauthorized(t *testing.T) {
	_, b, logs := fakeTelegramServer(t)
	msg := &tgbotapi.Message{
		Chat: chatPtr(42),
		From: userPtr(99, "stranger"),
		Text: "hello there",
	}
	b.handleIncomingMessage(tgbotapi.Update{Message: msg})
	assertLogContains(t, logs, "unauthorized")
}

func TestHandleIncomingMessage_AuthorizedText(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	b.SetAllowedUsers([]int64{10})
	b.mu.Lock()
	b.chatIDs[10] = 42
	b.mu.Unlock()

	var called atomic.Int32
	b.SetOnMessage(func(userID int64, text string, replyFn func(string)) {
		called.Add(1)
		replyFn("pong")
	})

	msg := &tgbotapi.Message{
		Chat: chatPtr(42),
		From: userPtr(10, "admin"),
		Text: "ping",
	}
	b.handleIncomingMessage(tgbotapi.Update{Message: msg})
	if called.Load() != 1 {
		t.Fatal("onMessage should have been called")
	}
}

func TestHandleIncomingMessage_AuthorizedNoEngine(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	b.SetAllowedUsers([]int64{10})
	b.mu.Lock()
	b.chatIDs[10] = 42
	b.mu.Unlock()

	msg := &tgbotapi.Message{
		Chat: chatPtr(42),
		From: userPtr(10, "admin"),
		Text: "hello",
	}
	b.handleIncomingMessage(tgbotapi.Update{Message: msg})
}

func TestHandleIncomingMessage_NilMessage(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	b.handleIncomingMessage(tgbotapi.Update{})
}

func TestHandleIncomingMessage_NilFrom(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	msg := &tgbotapi.Message{Chat: chatPtr(42), Text: "hello"}
	b.handleIncomingMessage(tgbotapi.Update{Message: msg})
}

func TestHandleIncomingMessage_AuthorizedCommand(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	b.SetAllowedUsers([]int64{10})
	b.mu.Lock()
	b.chatIDs[10] = 42
	b.mu.Unlock()

	msg := &tgbotapi.Message{
		Chat:     chatPtr(42),
		From:     userPtr(10, "admin"),
		Text:     "/status",
		Entities: cmdEntity(7),
	}
	b.handleIncomingMessage(tgbotapi.Update{Message: msg})
}

func TestHandleIncomingMessage_RateLimited(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	b.SetAllowedUsers([]int64{10})
	b.mu.Lock()
	b.chatIDs[10] = 42
	b.mu.Unlock()
	b.SetOnMessage(func(int64, string, func(string)) {})

	msg := &tgbotapi.Message{
		Chat: chatPtr(42),
		From: userPtr(10, "admin"),
		Text: "first",
	}
	b.handleIncomingMessage(tgbotapi.Update{Message: msg})

	// Immediate second message should be rate limited
	msg2 := &tgbotapi.Message{
		Chat: chatPtr(42),
		From: userPtr(10, "admin"),
		Text: "second",
	}
	b.handleIncomingMessage(tgbotapi.Update{Message: msg2})
}

func TestHandleIncomingMessage_TrackedUsersCap(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	b.SetAllowedUsers([]int64{10})
	// Fill chatIDs to max
	b.mu.Lock()
	for i := int64(0); i < int64(maxTrackedUsers); i++ {
		b.chatIDs[i] = i * 10
	}
	b.mu.Unlock()

	msg := &tgbotapi.Message{
		Chat: chatPtr(9999),
		From: userPtr(10, "admin"),
		Text: "hello",
	}
	// The user 10 is not yet in chatIDs (we filled with 0..maxTrackedUsers-1)
	// but if it exceeds the cap, the new entry won't be stored.
	b.handleIncomingMessage(tgbotapi.Update{Message: msg})
}

// --- handleTextMessage ---

func TestHandleTextMessage_WithEngine(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	b.mu.Lock()
	b.chatIDs[10] = 42
	b.mu.Unlock()

	var gotText string
	b.SetOnMessage(func(_ int64, text string, _ func(string)) {
		gotText = text
	})

	msg := &tgbotapi.Message{From: userPtr(10, "user"), Text: "hello engine"}
	b.handleTextMessage(msg)
	if gotText != "hello engine" {
		t.Fatalf("handleTextMessage text = %q, want %q", gotText, "hello engine")
	}
}

func TestHandleTextMessage_NoEngine(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	msg := &tgbotapi.Message{From: userPtr(10, "user"), Chat: chatPtr(42), Text: "hello"}
	b.handleTextMessage(msg)
}

// --- handleCallbackQuery ---

func TestHandleCallbackQuery_Refresh(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "cb1",
			Data: "refresh",
			Message: &tgbotapi.Message{
				Chat: chatPtr(42),
			},
		},
	}
	b.handleCallbackQuery(update)
}

func TestHandleCallbackQuery_Status(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "cb2",
			Data: "status",
			Message: &tgbotapi.Message{
				Chat: chatPtr(42),
			},
		},
	}
	b.handleCallbackQuery(update)
}

func TestHandleCallbackQuery_Unknown(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{
			ID:   "cb3",
			Data: "custom_action",
			Message: &tgbotapi.Message{
				Chat: chatPtr(42),
			},
		},
	}
	b.handleCallbackQuery(update)
}

func TestHandleCallbackQuery_NilCallback(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	b.handleCallbackQuery(tgbotapi.Update{})
}

func TestHandleCallbackQuery_NilMessage(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	update := tgbotapi.Update{
		CallbackQuery: &tgbotapi.CallbackQuery{ID: "cb4", Data: "refresh"},
	}
	b.handleCallbackQuery(update)
}

// --- reply ---

func TestReply_SendsMessage(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)
	b.reply(42, "test reply")
}

// --- allowUserAction cap ---

func TestAllowUserAction_CapEnforced(t *testing.T) {
	b := &TelegramBot{lastAction: make(map[int64]time.Time)}
	for i := int64(0); i < maxTrackedUsers; i++ {
		b.lastAction[i] = time.Now().Add(-time.Hour)
	}
	newUser := int64(maxTrackedUsers + 1)
	if b.allowUserAction(newUser) {
		t.Fatal("new user past cap should be rejected")
	}
	if !b.allowUserAction(0) {
		t.Fatal("existing user past window should pass")
	}
}

// --- Start/Stop lifecycle ---

func TestStartStopsOnCancel(t *testing.T) {
	_, b, _ := fakeTelegramServer(t)

	done := make(chan error, 1)
	go func() {
		done <- b.Start()
	}()

	time.Sleep(200 * time.Millisecond)
	b.Stop()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error after Stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after Stop within 5s")
	}
}

// --- helpers ---

func assertLogContains(t *testing.T, logs *[]string, substr string) {
	t.Helper()
	for _, l := range *logs {
		if strings.Contains(l, substr) {
			return
		}
	}
	t.Errorf("expected log containing %q, got %v", substr, *logs)
}
