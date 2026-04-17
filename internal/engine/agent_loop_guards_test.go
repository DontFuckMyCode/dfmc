// Tests for the four loop-pathology guards added after a real user
// report: "7 rounds of tool calling, then 139k/120k budget overshoot,
// then auto-resume produced zero visible answer." Each guard is
// individually verifiable in this file.
//
//  1. TOP-of-loop budget gate with headroom — prevents starting a round
//     the budget can't afford (stops the 139k/120k overshoot pattern).
//  2. Synthesis hint injected ONCE at `toolRoundSoftCap` — tells a
//     read-forever model to answer now.
//  3. Hard cap flips `ToolChoice: "none"` at `toolRoundHardCap` — the
//     next call MUST emit text.
//  4. Empty-response recovery — one retry with an explicit nudge, then
//     a visible failure message instead of an empty bubble.

package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// buildGuardTestEngine wires a temp-dir engine around a scripted
// provider so the guard tests don't have to rebuild the boilerplate
// each time. `budget` maps straight to cfg.Agent.MaxToolTokens; 0
// disables the token budget for that test.
func buildGuardTestEngine(t *testing.T, budget int, steps int, responses []scriptedResponse) (*Engine, *scriptedProvider, <-chan Event) {
	t.Helper()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "note.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write note.txt: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub-model",
		MaxTokens:  4096,
		MaxContext: 128000,
	}
	cfg.Agent.MaxToolSteps = steps
	cfg.Agent.MaxToolTokens = budget

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	stub := &scriptedProvider{
		name:       "stub",
		model:      "stub-model",
		hints:      newNativeHints(),
		maxContext: 40, // keeps elastic scaling from erasing the small budget
		responses:  responses,
	}
	router.Register(stub)

	eng := &Engine{
		Config:      cfg,
		EventBus:    NewEventBus(),
		ProjectRoot: tmp,
		Providers:   router,
		Tools:       tools.New(*cfg),
	}
	evCh := eng.EventBus.Subscribe("*")
	t.Cleanup(func() { eng.EventBus.Unsubscribe("*", evCh) })
	return eng, stub, evCh
}

func loopingReadToolCall(id string) provider.ToolCall {
	return provider.ToolCall{
		ID:   id,
		Name: "tool_call",
		Input: toolCallInput(map[string]any{
			"name": "read_file",
			"args": map[string]any{"path": "note.txt", "line_start": 1, "line_end": 1},
		}),
	}
}

// Bug 1 regression: a real run saw the loop consume 139098 tokens
// against a 120000 cap before the post-round gate tripped. With the
// pre-round headroom gate the loop parks BEFORE the overrun round,
// so totalTokens in the park event stays at-or-under MaxTokens.
//
// Setup: budget=70, per-call usage=30, headroom=70/7=10. Expected:
//   - Round 1 pre: 0+10<70 ✓ run → tokens=30
//   - Round 2 pre: 30+10<70 ✓ run → tokens=60
//   - Round 3 pre: 60+10>=70 ✗ PARK (without this fix, round 3 would
//     run and land at 90 — 20 tokens of overshoot).
func TestNativeToolLoop_HeadroomGateParksBeforeOvershoot(t *testing.T) {
	eng, stub, evCh := buildGuardTestEngine(t, 70, 10, []scriptedResponse{
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c1")}},
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c2")}},
		{ToolCalls: []provider.ToolCall{loopingReadToolCall("c3")}}, // would-overshoot
		{Text: "should never reach final answer"},
	})

	answer, err := eng.AskWithMetadata(context.Background(), "headroom gate check")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(answer, "tool budget exhausted") {
		t.Fatalf("want parked notice, got %q", answer)
	}

	stub.mu.Lock()
	reqCount := len(stub.requests)
	stub.mu.Unlock()
	if reqCount != 2 {
		t.Fatalf("want exactly 2 provider requests before headroom park, got %d", reqCount)
	}

	events := collectRecentEvents(evCh, 64, 150*time.Millisecond)
	ev, ok := findEventByType(events, "agent:loop:parked")
	if !ok {
		t.Fatalf("want agent:loop:parked event, got %v", eventTypes(events))
	}
	payload, _ := ev.Payload.(map[string]any)
	tokens, _ := payload["tokens_used"].(int)
	if tokens > 70 {
		t.Fatalf("headroom gate should park at-or-under budget, tokens_used=%d > 70", tokens)
	}
	if reason, _ := payload["reason"].(string); reason != "budget_exhausted" {
		t.Fatalf("want reason=budget_exhausted, got %v", payload["reason"])
	}
}

// Bug 2 fix: after `toolRoundSoftCap` rounds the loop injects one
// synthesis nudge. Verified via the `agent:loop:synthesize_hint`
// event — it must fire exactly once, no matter how many rounds run
// afterwards.
func TestNativeToolLoop_SynthesisHintFiresOnceAtSoftCap(t *testing.T) {
	// Seven tool rounds, then a final text answer. The hint fires
	// when len(traces) first reaches toolRoundSoftCap (5).
	responses := make([]scriptedResponse, 0, 8)
	for i := 1; i <= 7; i++ {
		responses = append(responses, scriptedResponse{
			ToolCalls: []provider.ToolCall{loopingReadToolCall("soft_" + padCallID(i))},
		})
	}
	responses = append(responses, scriptedResponse{Text: "synthesized answer"})

	eng, _, evCh := buildGuardTestEngine(t, 0, 20, responses) // budget disabled

	answer, err := eng.AskWithMetadata(context.Background(), "soft cap hint check")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(answer, "synthesized answer") {
		t.Fatalf("want synthesized answer to reach the user, got %q", answer)
	}

	events := collectRecentEvents(evCh, 256, 200*time.Millisecond)
	hintCount := 0
	for _, e := range events {
		if e.Type == "agent:loop:synthesize_hint" {
			hintCount++
		}
	}
	if hintCount != 1 {
		t.Fatalf("synthesis hint should fire exactly once, fired %d time(s)", hintCount)
	}
}

// Bug 2 fix, harder: after `toolRoundHardCap` rounds the loop flips
// ToolChoice to "none" so the next provider call MUST emit text.
// We verify by inspecting the ToolChoice of the last request recorded
// by the scripted provider.
func TestNativeToolLoop_HardCapForcesToolChoiceNone(t *testing.T) {
	// toolRoundHardCap (7) + one final text turn = 8 scripted rounds.
	responses := make([]scriptedResponse, 0, 8)
	for i := 1; i <= 7; i++ {
		responses = append(responses, scriptedResponse{
			ToolCalls: []provider.ToolCall{loopingReadToolCall("hard_" + padCallID(i))},
		})
	}
	responses = append(responses, scriptedResponse{Text: "had to answer"})

	eng, stub, evCh := buildGuardTestEngine(t, 0, 20, responses) // budget disabled

	if _, err := eng.AskWithMetadata(context.Background(), "hard cap check"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	stub.mu.Lock()
	requests := append([]provider.CompletionRequest(nil), stub.requests...)
	stub.mu.Unlock()
	if len(requests) != 8 {
		t.Fatalf("want 8 provider requests, got %d", len(requests))
	}
	// Requests 1..7 should keep ToolChoice="auto"; request 8 (the one
	// after hitting the hard cap) must be "none".
	for i, req := range requests[:7] {
		if req.ToolChoice != "auto" {
			t.Fatalf("round %d ToolChoice = %q, want auto", i+1, req.ToolChoice)
		}
	}
	if requests[7].ToolChoice != "none" {
		t.Fatalf("round 8 (after hard cap) ToolChoice = %q, want none", requests[7].ToolChoice)
	}

	events := collectRecentEvents(evCh, 256, 200*time.Millisecond)
	if _, ok := findEventByType(events, "agent:loop:tools_force_stop"); !ok {
		t.Fatalf("want agent:loop:tools_force_stop event, got %v", eventTypes(events))
	}
}

// Bug 3 fix: when the model returns no tool_calls AND no text, retry
// ONCE with an explicit nudge. If the model now answers, the user
// sees the answer — not a ghost empty bubble.
func TestNativeToolLoop_EmptyResponseRecoveryProducesAnswer(t *testing.T) {
	eng, stub, evCh := buildGuardTestEngine(t, 0, 10, []scriptedResponse{
		{Text: ""}, // empty — triggers recovery
		{Text: "recovered answer"},
	})

	answer, err := eng.AskWithMetadata(context.Background(), "empty recovery check")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(answer, "recovered answer") {
		t.Fatalf("want recovered answer, got %q", answer)
	}

	stub.mu.Lock()
	reqCount := len(stub.requests)
	stub.mu.Unlock()
	if reqCount != 2 {
		t.Fatalf("want 2 provider requests (empty + retry), got %d", reqCount)
	}

	events := collectRecentEvents(evCh, 64, 150*time.Millisecond)
	if _, ok := findEventByType(events, "agent:loop:empty_recovery"); !ok {
		t.Fatalf("want agent:loop:empty_recovery event, got %v", eventTypes(events))
	}
}

// Bug 3 fix, failure path: when the retry ALSO returns empty, surface
// a visible failure notice instead of silently returning an empty
// answer. The old code returned Answer="" and left the user staring
// at a blank assistant bubble.
func TestNativeToolLoop_TwoEmptyResponsesSurfaceFailureNotice(t *testing.T) {
	eng, _, evCh := buildGuardTestEngine(t, 0, 10, []scriptedResponse{
		{Text: ""}, // first empty → recovery nudge
		{Text: ""}, // second empty → give up with visible notice
	})

	answer, err := eng.AskWithMetadata(context.Background(), "double-empty check")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(answer) == "" {
		t.Fatal("double-empty must surface a non-empty failure notice, not a blank")
	}
	if !strings.Contains(strings.ToLower(answer), "empty") {
		t.Fatalf("failure notice should mention emptiness, got %q", answer)
	}

	events := collectRecentEvents(evCh, 64, 150*time.Millisecond)
	if _, ok := findEventByType(events, "agent:loop:empty_final"); !ok {
		t.Fatalf("want agent:loop:empty_final event, got %v", eventTypes(events))
	}
}

// padCallID keeps tool-call IDs deterministic and unique so provider
// implementations that key on ID don't get confused.
func padCallID(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
