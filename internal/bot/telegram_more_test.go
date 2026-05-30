package bot

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// New("") rejects an empty token. The other failure paths in New
// reach for the live Telegram API, so they're out of scope here —
// the empty-token guard is the one we can cover without network.
func TestNewRejectsEmptyToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, err := New(ctx, ""); err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
	if _, err := New(ctx, "   "); err == nil {
		t.Fatal("expected error for whitespace token, got nil")
	}
}

func TestSetAllowedUsersReplacesWhitelist(t *testing.T) {
	t.Parallel()
	b := &TelegramBot{allowedUsers: map[int64]struct{}{}}
	b.SetAllowedUsers([]int64{1, 2, 3, -7, 0})
	got := b.AllowedUsers()
	if len(got) != 3 {
		t.Fatalf("expected 3 allowed users (non-positive filtered), got %d: %v", len(got), got)
	}
	for _, id := range []int64{1, 2, 3} {
		if !b.isAllowed(id) {
			t.Errorf("expected %d in whitelist", id)
		}
	}
	if b.isAllowed(99) {
		t.Error("99 should not be allowed")
	}

	b.SetAllowedUsers(nil)
	if len(b.AllowedUsers()) != 0 {
		t.Error("nil slice should clear whitelist")
	}
	if b.isAllowed(1) {
		t.Error("after clear, 1 should not be allowed")
	}
}

func TestIsAllowedFalseWhenWhitelistNil(t *testing.T) {
	t.Parallel()
	b := &TelegramBot{} // allowedUsers map is nil
	if b.isAllowed(42) {
		t.Fatal("nil whitelist must reject everyone")
	}
}

func TestSetLoggerRoutesAndNilSafe(t *testing.T) {
	t.Parallel()
	b := &TelegramBot{}
	var got string
	b.SetLogger(func(format string, args ...any) {
		got = format
		_ = args
	})
	b.logf("hello %s", "world")
	if got != "hello %s" {
		t.Fatalf("expected logger to receive format, got %q", got)
	}

	// Clearing must not crash logf.
	b.SetLogger(nil)
	b.logf("dropped")

	// nil receiver no-ops.
	var nb *TelegramBot
	nb.SetLogger(func(string, ...any) {})
	nb.logf("nil safe")
}

func TestSetOnMessageNilReceiverIsSafe(t *testing.T) {
	t.Parallel()
	var b *TelegramBot
	b.SetOnMessage(func(int64, string, func(string)) {})
	if got := b.messageHandler(); got != nil {
		t.Fatal("nil receiver must yield nil handler")
	}
}

func TestAllowUserActionRespectsWindowAndPerUser(t *testing.T) {
	t.Parallel()
	b := &TelegramBot{}
	if !b.allowUserAction(1) {
		t.Fatal("first call should pass")
	}
	if b.allowUserAction(1) {
		t.Fatal("repeat within window must fail")
	}
	// A different user must not share the same bucket.
	if !b.allowUserAction(2) {
		t.Fatal("different user should pass independently")
	}
	// Force the lastAction into the past — the next call should pass.
	b.mu.Lock()
	b.lastAction[1] = time.Now().Add(-2 * telegramRateLimitWindow)
	b.mu.Unlock()
	if !b.allowUserAction(1) {
		t.Fatal("after window, user 1 should pass")
	}

	// nil receiver always rejects (no panic).
	var nb *TelegramBot
	if nb.allowUserAction(1) {
		t.Fatal("nil receiver must reject")
	}
}

func TestRegisteredUsersCount(t *testing.T) {
	t.Parallel()
	b := &TelegramBot{chatIDs: map[int64]int64{}}
	if got := b.RegisteredUsers(); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
	b.chatIDs[10] = 100
	b.chatIDs[20] = 200
	if got := b.RegisteredUsers(); got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
}

func TestStopCancelsContext(t *testing.T) {
	t.Parallel()
	b := &TelegramBot{}
	// Simulate the cancel function the constructor would have set.
	done := make(chan struct{})
	b.cancel = func() { close(done) }
	b.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not invoke cancel within 1s")
	}
}

func TestTelegramUserIDTextEdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		id     int64
		user   string
		expect []string
		reject []string
	}{
		{
			name:   "with username",
			id:     7,
			user:   "ersin",
			expect: []string{"7", "@ersin", "allowed users"},
		},
		{
			name:   "blank username",
			id:     8,
			user:   "",
			expect: []string{"8", "(no username)", "allowed users"},
			reject: []string{"@"},
		},
		{
			name:   "whitespace-only username trimmed",
			id:     9,
			user:   "   ",
			expect: []string{"9", "(no username)"},
			reject: []string{"@   "},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := telegramUserIDText(tc.id, tc.user)
			for _, want := range tc.expect {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in %q", want, got)
				}
			}
			for _, no := range tc.reject {
				if strings.Contains(got, no) {
					t.Errorf("unexpected %q in %q", no, got)
				}
			}
		})
	}
}

func TestTruncateBoundaries(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"", 5, ""},
		{"abc", 5, "abc"},
		{"abcdef", 6, "abcdef"},
		{"abcdef", 5, "abcde..."},
		{"abcdef", 0, "..."},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.maxLen); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.maxLen, got, c.want)
		}
	}
}

func TestRedactForLogMoreShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want []string
		hide []string
	}{
		{
			name: "secret=value with spaces",
			in:   "Password: hunter2 elsewhere",
			want: []string{"<redacted>"},
			hide: []string{"hunter2"},
		},
		{
			name: "sk- prefixed token in mid-sentence",
			in:   "use sk-aBcDeFgHiJkLm to login",
			want: []string{"<redacted>"},
			hide: []string{"sk-aBcDeFgHiJkLm"},
		},
		{
			name: "no secret passes through",
			in:   "just a normal log line",
			want: []string{"normal log line"},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := redactForLog(tc.in, 200)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in %q", w, got)
				}
			}
			for _, h := range tc.hide {
				if strings.Contains(got, h) {
					t.Errorf("secret %q leaked in %q", h, got)
				}
			}
		})
	}
}

func TestAllowedUsersRaceSafe(t *testing.T) {
	t.Parallel()
	b := &TelegramBot{allowedUsers: map[int64]struct{}{}}
	b.SetAllowedUsers([]int64{1, 2, 3})

	var wg sync.WaitGroup
	var allowed atomic.Int64
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if b.isAllowed(1) {
				allowed.Add(1)
			}
			_ = b.AllowedUsers()
		}()
		go func() {
			defer wg.Done()
			b.SetAllowedUsers([]int64{1, 2, 3})
		}()
	}
	wg.Wait()
	if allowed.Load() == 0 {
		t.Fatal("expected at least some allowed checks to succeed during the race")
	}
}
