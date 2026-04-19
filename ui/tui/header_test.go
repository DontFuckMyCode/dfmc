package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// TestRenderChatHeader_DriveChipShownWhenRunActive: when telemetry
// has a non-empty DriveRunID, the header must render a "▸ drive
// X/Y · Tn" chip. When DriveRunID is empty the chip must NOT
// appear (no stale "▸ drive 0/0" leftover).
func TestRenderChatHeader_DriveChipShownWhenRunActive(t *testing.T) {
	active := renderChatHeader(chatHeaderInfo{
		Provider:    "anthropic",
		Model:       "sonnet",
		DriveRunID:  "drv-abc",
		DriveTodoID: "T5",
		DriveDone:   3,
		DriveTotal:  12,
	}, 200)
	if !strings.Contains(active, "drive 3/12") {
		t.Fatalf("active drive chip missing: %q", active)
	}
	if !strings.Contains(active, "T5") {
		t.Fatalf("active drive chip should name in-flight TODO: %q", active)
	}

	idle := renderChatHeader(chatHeaderInfo{
		Provider: "anthropic",
		Model:    "sonnet",
	}, 200)
	if strings.Contains(idle, "drive") {
		t.Fatalf("idle header must NOT show drive chip: %q", idle)
	}
}

// TestRenderChatHeader_DriveChipWarnsWhenBlockedNonZero: blocked > 0
// flips the chip to a warn style and surfaces "(blocked N)" so the
// user sees trouble at a glance.
func TestRenderChatHeader_DriveChipWarnsWhenBlockedNonZero(t *testing.T) {
	out := renderChatHeader(chatHeaderInfo{
		Provider:     "anthropic",
		Model:        "sonnet",
		DriveRunID:   "drv-x",
		DriveDone:    2,
		DriveTotal:   8,
		DriveBlocked: 1,
	}, 200)
	if !strings.Contains(out, "blocked 1") {
		t.Fatalf("blocked-count chip missing: %q", out)
	}
}

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
	if !strings.Contains(agent, "tool loop reviewing") || !strings.Contains(agent, "3/12") {
		t.Fatalf("tool-loop header should show phase + step progress, got %q", agent)
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

// TestRenderChatHeaderShowsActivityBadges: when tools or subagents are in
// flight, the header surfaces compact counts so the user sees fan-out live.
func TestRenderChatHeaderShowsActivityBadges(t *testing.T) {
	out := renderChatHeader(chatHeaderInfo{
		Provider:        "anthropic",
		Model:           "claude-opus-4-6",
		Streaming:       true,
		ActiveTools:     3,
		ActiveSubagents: 2,
	}, 200)
	if !strings.Contains(out, "tools 3") {
		t.Fatalf("expected active-tools badge, got %q", out)
	}
	if !strings.Contains(out, "subagents 2") {
		t.Fatalf("expected active-subagents badge, got %q", out)
	}
	// Zero counts must stay off the header so a resting chat isn't cluttered.
	resting := renderChatHeader(chatHeaderInfo{
		Provider: "anthropic", Model: "claude-opus-4-6",
	}, 200)
	if strings.Contains(resting, "tools ") && !strings.Contains(resting, "tools on") && !strings.Contains(resting, "tools off") {
		t.Fatalf("resting header should not render tools-count badge, got %q", resting)
	}
	if strings.Contains(resting, "subagents ") {
		t.Fatalf("resting header should not render subagents-count badge, got %q", resting)
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
	for _, cmd := range []string{"/review", "/explain", "/analyze", "/map", "/scan", "/refactor"} {
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
	if !strings.Contains(view, "/review") || !strings.Contains(view, "/map") {
		t.Fatalf("empty transcript should render starter commands, got:\n%s", view)
	}
}

func TestRenderChatViewShowsStreamingIndicatorWhileSending(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{Provider: "anthropic", Model: "claude-opus-4-6"}
	m.chat.sending = true
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
	if mm.chat.input != "/review" {
		t.Fatalf("digit 1 should load /review, got %q", mm.chat.input)
	}
	if mm.chat.cursor != len([]rune("/review")) {
		t.Fatalf("cursor should sit at end of loaded template, got %d", mm.chat.cursor)
	}

	next2, _ := mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
	mm2, ok := next2.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next2)
	}
	// With a non-empty composer the digit is treated as plain typed input,
	// so it appends rather than replacing the loaded starter.
	if mm2.chat.input != "/review4" {
		t.Fatalf("digit on non-empty composer should append, got %q", mm2.chat.input)
	}
}

func TestStarterDigitHotkeyIgnoredWhenTranscriptNonEmpty(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{Provider: "anthropic", Model: "claude-opus-4-6"}
	m.chat.transcript = []chatLine{{Role: "user", Content: "hi"}}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'1'}})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next)
	}
	if mm.chat.input != "1" {
		t.Fatalf("digit should be typed literally once transcript has content, got %q", mm.chat.input)
	}
}

func TestRenderMessageHeaderShowsTimestampTokensAndDuration(t *testing.T) {
	ts := time.Date(2026, time.April, 16, 14, 32, 5, 0, time.UTC)
	out := renderMessageHeader(messageHeaderInfo{
		Role:       "assistant",
		Timestamp:  ts,
		TokenCount: 1234,
		DurationMs: 2150,
		ToolCalls:  3,
	})
	for _, want := range []string{"14:32:05", "1,234 tok", "2.1s", "⚒ 3"} {
		if !strings.Contains(out, want) {
			t.Fatalf("message header missing %q, got %q", want, out)
		}
	}
}

func TestRenderMessageHeaderHighlightsToolFailures(t *testing.T) {
	out := renderMessageHeader(messageHeaderInfo{
		Role:         "assistant",
		ToolCalls:    4,
		ToolFailures: 1,
	})
	if !strings.Contains(out, "⚒ 4") || !strings.Contains(out, "✗ 1") {
		t.Fatalf("tool-failure chip missing, got %q", out)
	}
}

func TestRenderStreamingIndicatorAnimatesFrames(t *testing.T) {
	a := renderStreamingIndicator("drafting reply", 0)
	b := renderStreamingIndicator("drafting reply", 5)
	if a == b {
		t.Fatalf("spinner should animate across frames; both outputs identical:\n%s\n%s", a, b)
	}
	if !strings.Contains(a, "drafting reply") || !strings.Contains(b, "drafting reply") {
		t.Fatalf("phase label dropped from indicator, got %q / %q", a, b)
	}
}

func TestRenderResumeBannerMentionsKeysAndProgress(t *testing.T) {
	out := renderResumeBanner(25, 25, 80)
	for _, want := range []string{"parked", "25/25", "enter resumes", "esc dismisses"} {
		if !strings.Contains(out, want) {
			t.Fatalf("resume banner missing %q, got %q", want, out)
		}
	}
}

func TestParkedEventSetsResumePromptAndEnterResumes(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{Provider: "anthropic", Model: "claude-opus-4-6"}

	m = m.handleEngineEvent(engine.Event{
		Type: "agent:loop:parked",
		Payload: map[string]any{
			"step":           25,
			"max_tool_steps": 25,
		},
	})
	if !m.ui.resumePromptActive {
		t.Fatalf("parked event should turn resumePromptActive on")
	}
	if m.agentLoop.maxToolStep != 25 || m.agentLoop.step != 25 {
		t.Fatalf("parked event should record step/max, got %d/%d", m.agentLoop.step, m.agentLoop.maxToolStep)
	}

	// Banner must render in the tail above the input.
	view := m.renderChatView(160)
	if !strings.Contains(view, "parked") || !strings.Contains(view, "enter resumes") {
		t.Fatalf("banner should surface above input while parked, got:\n%s", view)
	}
}

// TestParkedEventBudgetReasonArmsResumeWithoutDuplicateLine guards the
// transcript cleanup: when the engine parks for budget_exhausted, the
// dedicated "exhausted %d/%d" line already carries the context, so the
// parked handler arms the resume banner but skips the generic "parked at
// step N/M" echo to avoid double-logging.
func TestParkedEventBudgetReasonArmsResumeWithoutDuplicateLine(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{Provider: "anthropic", Model: "claude-opus-4-6"}
	m.chat.sending = true
	beforeMsgs := len(m.chat.transcript)

	m = m.handleEngineEvent(engine.Event{
		Type: "agent:loop:parked",
		Payload: map[string]any{
			"step":           12,
			"max_tool_steps": 25,
			"reason":         "budget_exhausted",
		},
	})
	if !m.ui.resumePromptActive {
		t.Fatal("budget-park should still arm the resume banner")
	}
	if m.agentLoop.phase != "parked" {
		t.Fatalf("phase should flip to parked, got %q", m.agentLoop.phase)
	}
	if len(m.chat.transcript) != beforeMsgs {
		t.Fatalf("budget-park should suppress the transcript line, got %d new msgs", len(m.chat.transcript)-beforeMsgs)
	}
}

// TestParkedEventAutonomousPendingSuppressesUIFlip pins the fix for the
// 2026-04-18 race: when the autonomous-resume wrapper will immediately
// re-enter the loop after a budget-exhausted park, the parked event must
// NOT flip the TUI into the parked UI state. Doing so flashed a "press
// Enter to resume" prompt the user could act on before the wrapper got
// to the next round, producing the "No parked agent loop" /continue race.
func TestParkedEventAutonomousPendingSuppressesUIFlip(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.status = engine.Status{Provider: "anthropic", Model: "claude-opus-4-6"}
	m.agentLoop.active = true
	m.agentLoop.phase = "running-tools"

	m = m.handleEngineEvent(engine.Event{
		Type: "agent:loop:parked",
		Payload: map[string]any{
			"step":               12,
			"max_tool_steps":     25,
			"reason":             "budget_exhausted",
			"autonomous_pending": true,
		},
	})
	if m.ui.resumePromptActive {
		t.Fatal("autonomous_pending park MUST NOT arm the resume banner")
	}
	if m.agentLoop.phase == "parked" {
		t.Fatalf("autonomous_pending park MUST NOT flip phase to parked, got %q", m.agentLoop.phase)
	}
}

// TestAutoResumeEventClearsStaleResumePrompt is belt-and-braces: even if
// an old engine emits parked-without-autonomous_pending, the moment
// auto_resume fires the prompt must clear so the user doesn't see "press
// Enter to resume" sitting under an actively-running loop.
func TestAutoResumeEventClearsStaleResumePrompt(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.ui.resumePromptActive = true
	m.agentLoop.active = false
	m.agentLoop.phase = "parked"

	m = m.handleEngineEvent(engine.Event{
		Type: "agent:loop:auto_resume",
		Payload: map[string]any{
			"cumulative_steps": 30,
			"step_ceiling":     600,
		},
	})
	if m.ui.resumePromptActive {
		t.Fatal("auto_resume must clear resumePromptActive")
	}
	if !m.agentLoop.active {
		t.Fatal("auto_resume must mark agent loop active again")
	}
	if m.agentLoop.phase != "auto-resuming" {
		t.Fatalf("phase should flip to auto-resuming, got %q", m.agentLoop.phase)
	}
}

func TestEscDismissesResumePromptWithoutClearingEngineState(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.ui.resumePromptActive = true
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next)
	}
	if mm.ui.resumePromptActive {
		t.Fatalf("esc should clear resumePromptActive")
	}
}
