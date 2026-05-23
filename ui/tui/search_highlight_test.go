package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

// withColor wraps a fn body in a scoped lipgloss color-profile bump so
// ANSI escapes are emitted. The whole ui/tui package can't enable this
// in TestMain because dozens of legacy tests assert on `strings.Contains(view, "X")`
// which breaks when SGR codes land between every styled rune.
func withColor(t *testing.T, fn func()) {
	t.Helper()
	prev := lipgloss.DefaultRenderer().ColorProfile()
	lipgloss.DefaultRenderer().SetColorProfile(termenv.TrueColor)
	defer lipgloss.DefaultRenderer().SetColorProfile(prev)
	fn()
}

func TestHighlightSearchHits_MarksMatchedSpans(t *testing.T) {
	withColor(t, func() {
		out := highlightSearchHits("the SSE handler closes the stream", "sse")
		if !strings.Contains(out, "\x1b[") {
			t.Fatalf("expected ANSI styling, got %q", out)
		}
		if !strings.Contains(ansi.Strip(out), "SSE handler") {
			t.Fatalf("original casing must be preserved, got %q", ansi.Strip(out))
		}
	})
}

func TestHighlightSearchHits_EmptyQueryNoop(t *testing.T) {
	out := highlightSearchHits("hello world", "")
	if out != "hello world" {
		t.Fatalf("empty query should be no-op, got %q", out)
	}
}

func TestRunHistorySearch_StashesQuery(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: chatRoleAssistant, Content: "the SSE handler closes the stream"},
	}
	m = m.runHistorySearch("SSE")
	if m.chat.lastSearchQuery != "SSE" {
		t.Fatalf("expected lastSearchQuery=SSE, got %q", m.chat.lastSearchQuery)
	}
}

func TestRunHistorySearch_ClearsQueryOnNoHits(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.lastSearchQuery = "stale"
	m.chat.transcript = []chatLine{
		{Role: chatRoleAssistant, Content: "completely unrelated"},
	}
	m = m.runHistorySearch("nonexistent")
	if m.chat.lastSearchQuery != "" {
		t.Fatalf("zero-hits should clear lastSearchQuery, got %q", m.chat.lastSearchQuery)
	}
}

func TestRenderChatView_HighlightsActiveSearchQuery(t *testing.T) {
	withColor(t, func() {
		m := NewModel(context.Background(), nil)
		m.chat.transcript = []chatLine{
			{Role: chatRoleAssistant, Content: "the SSE handler closes the stream after EOF"},
		}
		m.chat.lastSearchQuery = "SSE"
		view := m.renderChatView(140)
		// Highlighted span emits ANSI; the search term itself must still
		// survive after stripping.
		if !strings.Contains(ansi.Strip(view), "SSE") {
			t.Fatalf("search hit lost from output:\n%s", view)
		}
	})
}
