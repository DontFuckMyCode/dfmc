package bot

import (
	"strings"
	"testing"
)

func TestTelegramRateLimiterRejectsRapidRepeat(t *testing.T) {
	b := &TelegramBot{}
	if !b.allowUserAction(42) {
		t.Fatal("first action should pass")
	}
	if b.allowUserAction(42) {
		t.Fatal("rapid repeat should be rate limited")
	}
	if !b.allowUserAction(99) {
		t.Fatal("different user should not share rate limit bucket")
	}
}

func TestRedactForLogMasksSecretsBeforeTruncation(t *testing.T) {
	got := redactForLog("token=abc123 sk-testSecretValue api_key=xyz", 200)
	for _, secret := range []string{"abc123", "sk-testSecretValue", "xyz"} {
		if strings.Contains(got, secret) {
			t.Fatalf("secret %q leaked in %q", secret, got)
		}
	}
	if !strings.Contains(got, "<redacted>") {
		t.Fatalf("expected redaction marker, got %q", got)
	}
}

func TestTelegramUserIDTextIncludesAllowListHint(t *testing.T) {
	got := telegramUserIDText(4242, "ersin")
	for _, want := range []string{"4242", "@ersin", "allowed users"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in id response, got %q", want, got)
		}
	}
}

func TestTelegramMessageHandlerCanBeCleared(t *testing.T) {
	b := &TelegramBot{}
	b.SetOnMessage(func(int64, string, func(string)) {})
	if b.messageHandler() == nil {
		t.Fatal("expected message handler after SetOnMessage")
	}
	b.SetOnMessage(nil)
	if b.messageHandler() != nil {
		t.Fatal("expected nil handler after clearing SetOnMessage")
	}
}
