package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestChatBubbleContentShowsFullContentByDefault(t *testing.T) {
	item := chatLine{
		Role:    "assistant",
		Content: "Line one\nLine two\nLine three",
	}
	out := chatBubbleContent(item, false)
	for _, want := range []string{"Line one", "Line two", "Line three"} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected full content to include %q, got %q", want, out)
		}
	}
	if strings.Contains(out, "▎") {
		t.Fatalf("non-streaming bubble should not carry a caret: %q", out)
	}
}

func TestChatBubbleContentAppendsCaretWhileStreaming(t *testing.T) {
	item := chatLine{Role: "assistant", Content: "partial text"}
	out := chatBubbleContent(item, true)
	if !strings.HasSuffix(out, "▎") {
		t.Fatalf("streaming bubble should end with caret, got %q", out)
	}
	if !strings.Contains(out, "partial text") {
		t.Fatalf("streaming bubble should still show content, got %q", out)
	}
}

func TestChatBubbleContentShowsThinkingPlaceholderBeforeFirstDelta(t *testing.T) {
	item := chatLine{Role: "assistant", Content: ""}
	out := chatBubbleContent(item, true)
	if !strings.Contains(out, "thinking") {
		t.Fatalf("empty streaming bubble should show thinking placeholder, got %q", out)
	}
}

func TestRenderChatView_StreamingBubbleShowsFullContentAndCaret(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.sending = true
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "what happens next?"},
		{Role: "assistant", Content: "Here is line one.\nAnd line two."},
	}
	m.chat.streamIndex = 1

	view := m.renderChatView(120)
	for _, want := range []string{"Here is line one.", "And line two.", "▎"} {
		if !strings.Contains(view, want) {
			t.Fatalf("expected chat view to include %q, got:\n%s", want, view)
		}
	}
}

func TestRenderChatView_StreamingHeaderShowsInputOutputTokensNotElapsedMs(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.sending = true
	m.chat.streamInputTokens = 42000
	m.chat.streamStartedAt = time.Now().Add(-28 * time.Second)
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "what happens next?"},
		{Role: "assistant", Content: "partial answer", TokenCount: 17},
	}
	m.chat.streamIndex = 1

	view := m.renderChatView(140)
	for _, want := range []string{"in ~42.0k tok", "out ~17 tok", "streaming"} {
		if !strings.Contains(view, want) {
			t.Fatalf("streaming header missing %q, got:\n%s", want, view)
		}
	}
	if strings.Contains(view, "28000ms") || strings.Contains(view, "28") && strings.Contains(view, "ms") {
		t.Fatalf("streaming header should not show elapsed milliseconds, got:\n%s", view)
	}
}

func TestRenderChatView_RoleHeadersUseLiteralColoredRoleNames(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "answer"},
		{Role: "coach", Content: "hint"},
	}

	view := m.renderChatView(140)
	for _, want := range []string{"USER", "ASSISTANT", "COACH"} {
		if !strings.Contains(view, want) {
			t.Fatalf("chat view should surface colored role label %q, got:\n%s", want, view)
		}
	}
}

func TestProviderStreamStartEventUpdatesLiveInputTokens(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.sending = true
	m.chat.streamIndex = 0

	m = m.handleEngineEvent(engine.Event{
		Type:   "provider:stream:start",
		Source: "engine",
		Payload: map[string]any{
			"input_tokens": 12345,
		},
	})

	if m.chat.streamInputTokens != 12345 {
		t.Fatalf("expected live input token count to update, got %d", m.chat.streamInputTokens)
	}
}

func TestStatsPanelShowsLiveStreamingTokenLedger(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.sending = true
	m.chat.streamInputTokens = 42000
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "what happens next?"},
		{Role: "assistant", Content: "partial answer", TokenCount: 17},
	}
	m.chat.streamIndex = 1

	panel := stripANSI(renderStatsPanel(m.statsPanelInfo(), 28))
	for _, want := range []string{"BUDGET", "live input ~42k", "output ~17"} {
		if !strings.Contains(panel, want) {
			t.Fatalf("stats panel missing live budget signal %q, got:\n%s", want, panel)
		}
	}
	if strings.Contains(panel, "estimate until provider done") {
		t.Fatalf("stats panel should not show verbose token ledger copy, got:\n%s", panel)
	}
}

func TestRenderChatView_CompletedBubbleHasNoCaret(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.sending = false
	m.chat.streamIndex = -1
	m.chat.transcript = []chatLine{
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "full multi-line\nanswer"},
	}

	view := m.renderChatView(120)
	if !strings.Contains(view, "full multi-line") || !strings.Contains(view, "answer") {
		t.Fatalf("expected full assistant content in view, got:\n%s", view)
	}
	if strings.Contains(view, "▎ ▎") {
		t.Fatalf("finished bubble must not end with streaming caret, got:\n%s", view)
	}
}
