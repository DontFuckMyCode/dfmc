package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestFormatThousandsGroupsDigits(t *testing.T) {
	cases := map[int]string{
		0:       "0",
		7:       "7",
		999:     "999",
		1000:    "1,000",
		12450:   "12,450",
		200000:  "200,000",
		1000000: "1,000,000",
	}
	for n, want := range cases {
		if got := formatThousands(n); got != want {
			t.Fatalf("formatThousands(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestRenderTokenMeterThresholdsAndFormat(t *testing.T) {
	out := renderTokenMeter(12450, 200000)
	if !strings.Contains(out, "12,450") || !strings.Contains(out, "200,000") {
		t.Fatalf("meter should show thousands-separated used/max, got %q", out)
	}
	if !strings.Contains(out, "6%") {
		t.Fatalf("meter should include percent usage, got %q", out)
	}

	// Zero-max falls back to plain count.
	fallback := renderTokenMeter(0, 0)
	if !strings.Contains(fallback, "ctx") {
		t.Fatalf("zero-max meter should still label with ctx, got %q", fallback)
	}
}

func TestRenderChatHeaderShowsProviderModelAndMode(t *testing.T) {
	info := chatHeaderInfo{
		Provider:      "anthropic",
		Model:         "claude-opus-4-6",
		Configured:    true,
		MaxContext:    200_000,
		ContextTokens: 12_450,
		ToolsEnabled:  true,
	}
	out := renderChatHeader(info, 160)
	for _, want := range []string{"CHAT", "anthropic", "claude-opus-4-6", "12,450", "200,000", "ready", "tools on"} {
		if !strings.Contains(out, want) {
			t.Fatalf("chat header missing %q, got %q", want, out)
		}
	}
}

func TestRenderChatHeaderSwitchesToStreamingAndAgent(t *testing.T) {
	streaming := renderChatHeader(chatHeaderInfo{
		Provider: "openai", Model: "gpt-5.4", Streaming: true,
	}, 160)
	if !strings.Contains(streaming, "streaming") {
		t.Fatalf("streaming header should say streaming, got %q", streaming)
	}

	agent := renderChatHeader(chatHeaderInfo{
		Provider: "openai", Model: "gpt-5.4",
		AgentActive: true, AgentPhase: "reviewing", AgentStep: 3, AgentMax: 12,
	}, 160)
	if !strings.Contains(agent, "agent reviewing") || !strings.Contains(agent, "3/12") {
		t.Fatalf("agent header should show phase + step progress, got %q", agent)
	}
}

func TestRenderChatHeaderUnconfiguredProviderGetsWarn(t *testing.T) {
	out := renderChatHeader(chatHeaderInfo{
		Provider: "openai", Model: "gpt-5.4", Configured: false,
	}, 160)
	if !strings.Contains(out, "openai") {
		t.Fatalf("header should still show provider name when unconfigured, got %q", out)
	}
	if !strings.Contains(out, "⚠") {
		t.Fatalf("unconfigured provider should carry ⚠ indicator, got %q", out)
	}
}

func TestRenderChatHeaderMovesPinnedToSecondLine(t *testing.T) {
	out := renderChatHeader(chatHeaderInfo{
		Provider: "anthropic", Model: "claude-opus-4-6", Pinned: "internal/foo.go",
	}, 120)
	if !strings.Contains(out, "\n") {
		t.Fatalf("pinned file should go on a second line, got single-line output: %q", out)
	}
	if !strings.Contains(out, "pinned: [[file:internal/foo.go]]") {
		t.Fatalf("pinned line should include file marker, got %q", out)
	}
}

func TestRenderStarterPromptsListsSixActions(t *testing.T) {
	out := renderStarterPrompts(120, true)
	joined := strings.Join(out, "\n")
	for _, cmd := range []string{"/review", "/explain", "/analyze", "/codemap", "/security", "/refactor"} {
		if !strings.Contains(joined, cmd) {
			t.Fatalf("starter prompts missing %q, got:\n%s", cmd, joined)
		}
	}
	if !strings.Contains(joined, "Welcome") {
		t.Fatalf("starter prompts should open with a welcome line, got:\n%s", joined)
	}
	if strings.Contains(joined, "No provider configured") {
		t.Fatalf("setup banner should stay hidden when configured=true, got:\n%s", joined)
	}
}

func TestRenderStarterPromptsShowsSetupBannerWhenUnconfigured(t *testing.T) {
	out := renderStarterPrompts(120, false)
	joined := strings.Join(out, "\n")
	for _, want := range []string{"No provider configured", "f5", "/provider"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("unconfigured welcome should mention %q, got:\n%s", want, joined)
		}
	}
}

func TestRenderChatViewUsesStarterPromptsWhenEmpty(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{Provider: "anthropic", Model: "claude-opus-4-6"}
	view := m.renderChatView(120)
	if !strings.Contains(view, "Welcome") {
		t.Fatalf("empty transcript should render welcome block, got:\n%s", view)
	}
	if !strings.Contains(view, "/review") || !strings.Contains(view, "/codemap") {
		t.Fatalf("empty transcript should render starter commands, got:\n%s", view)
	}
}

func TestRenderChatViewShowsStreamingIndicatorWhileSending(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{Provider: "anthropic", Model: "claude-opus-4-6"}
	m.sending = true
	view := m.renderChatView(120)
	if !strings.Contains(view, "drafting reply") && !strings.Contains(view, "streaming") {
		t.Fatalf("streaming state should surface a phase indicator, got:\n%s", view)
	}
}

func TestStarterDigitHotkeyLoadsTemplateOnEmptyChat(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{Provider: "anthropic", Model: "claude-opus-4-6"}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next)
	}
	if mm.input != "/review" {
		t.Fatalf("digit 1 should load /review, got %q", mm.input)
	}
	if mm.chatCursor != len([]rune("/review")) {
		t.Fatalf("cursor should sit at end of loaded template, got %d", mm.chatCursor)
	}

	next2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	mm2, ok := next2.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next2)
	}
	// With a non-empty composer the digit is treated as plain typed input,
	// so it appends rather than replacing the loaded starter.
	if mm2.input != "/review4" {
		t.Fatalf("digit on non-empty composer should append, got %q", mm2.input)
	}
}

func TestStarterDigitHotkeyIgnoredWhenTranscriptNonEmpty(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{Provider: "anthropic", Model: "claude-opus-4-6"}
	m.transcript = []chatLine{{Role: "user", Content: "hi"}}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next)
	}
	if mm.input != "1" {
		t.Fatalf("digit should be typed literally once transcript has content, got %q", mm.input)
	}
}
