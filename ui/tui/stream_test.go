package tui

import (
	"context"
	"strings"
	"testing"
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
