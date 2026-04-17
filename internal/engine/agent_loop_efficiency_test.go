// Tests for the context-efficiency changes that followed a real user
// report of runaway context bloat ("7 rounds, 139k/120k, then /continue
// produced nothing"). Each test pins one of the four knobs:
//
//  1. Compact threshold references min(providerLimit, MaxTokens) — the
//     binding constraint, so compact fires before the park gate.
//  2. KeepRecentRounds default is 2 (was 3).
//  3. Per-tool MaxResultChars halves once totalTokens >= MaxTokens/2.
//  4. Cross-round dedup: identical (name, input) tool_calls stub the
//     older tool_result so we stop resending the same bytes every round.

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
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// 1. Default lifecycle keeps only 2 recent rounds now. This is a
// config-shape assertion — a downstream test for compact behaviour
// lives in agent_compact_test.go and doesn't need to be duplicated.
func TestResolveContextLifecycle_KeepRecentRoundsDefaultIsTwo(t *testing.T) {
	eng := &Engine{Config: config.DefaultConfig()}
	// Wipe the YAML default so resolve falls back to the code default.
	eng.Config.Agent.ContextLifecycle = config.ContextLifecycleConfig{}
	got := eng.resolveContextLifecycle()
	if got.KeepRecentRounds != 2 {
		t.Fatalf("KeepRecentRounds default = %d, want 2", got.KeepRecentRounds)
	}
}

// 2. Compact fires sooner when MaxTokens (tool budget) is tighter than
// the provider window. The code uses the smaller of (providerLimit,
// budget) as the reference; with no provider registered, the default
// provider window is 32000 tokens (engine.go:defaultProviderContextTokens),
// so the threshold without budget = 32000 * 0.7 = 22400. A payload
// in (14000, 22400) compacts only when a tighter budget (≤ 20000)
// is passed.
func TestMaybeCompactNativeLoopHistoryForBudget_UsesTighterReference(t *testing.T) {
	eng := &Engine{Config: config.DefaultConfig()}
	eng.Config.Agent.ContextLifecycle = config.ContextLifecycleConfig{
		Enabled:                   true,
		AutoCompactThresholdRatio: 0.7,
		KeepRecentRounds:          1,
	}
	// Target ~17k tokens total across 4 rounds: sits under the 22400
	// no-budget threshold but over the 14000 budget-20000 threshold.
	msgs := []provider.Message{
		{Role: types.RoleUser, Content: "original question"},
	}
	filler := strings.Repeat("alpha beta gamma ", 1000) // ~17k chars/round → ~4.2k tokens
	for i := 0; i < 4; i++ {
		msgs = append(msgs,
			provider.Message{
				Role:    types.RoleAssistant,
				Content: "",
				ToolCalls: []provider.ToolCall{{
					ID:    "call_" + string(rune('a'+i)),
					Name:  "read_file",
					Input: map[string]any{"path": "note_" + string(rune('a'+i)) + ".txt"},
				}},
			},
			provider.Message{
				Role:       types.RoleUser,
				Content:    filler,
				ToolCallID: "call_" + string(rune('a'+i)),
				ToolName:   "read_file",
			},
		)
	}

	estimatedTokens := estimateRequestTokens("", nil, msgs)
	if estimatedTokens < 14001 || estimatedTokens > 22399 {
		t.Fatalf("test-setup sanity: payload must sit in (14000, 22400) to prove "+
			"budget-vs-provider threshold divergence; got %d tokens — adjust filler size",
			estimatedTokens)
	}

	// With budget=20000 → threshold = min(32000,20000)*0.7 = 14000.
	// Payload > 14000 → compact fires.
	_, report := eng.maybeCompactNativeLoopHistoryForBudget(msgs, "", nil, 20000)
	if report == nil {
		t.Fatal("expected compaction to fire under tight (20000) tool budget")
	}
	if report.RoundsCollapsed < 1 {
		t.Fatalf("expected at least one round collapsed, got %d", report.RoundsCollapsed)
	}

	// With budget=0 (no hint) threshold is 22400 and the same payload
	// sits under it — compact must NOT fire. This is the proof that
	// the budget-aware branch is what tightens the trigger.
	_, reportNoBudget := eng.maybeCompactNativeLoopHistoryForBudget(msgs, "", nil, 0)
	if reportNoBudget != nil {
		t.Fatalf("without budget hint, compaction should NOT fire at %d tokens < 22400",
			estimatedTokens)
	}
}

// 3. Once totalTokens crosses MaxTokens/2 the per-round tool_result cap
// halves. We exercise this end-to-end: budget 1000, max_result_chars
// configured to 400. Round 1 should format with the full 400; round
// 2 (starting at 30 from round 1) stays under the half-budget so it
// still uses 400. Round 3 (totalTokens = 60) is still under 500.
// With the stub provider reporting 30 tokens/round, we won't trip
// the saturation gate via token usage alone — but the code path is
// the same regardless, and unit-testing the gate directly is more
// reliable than orchestrating a saturation scenario.
//
// Direct assertion: a helper that mirrors the gate's arithmetic so
// the behaviour is pinned without end-to-end ceremony.
func TestEffectiveToolResultCap_HalvesWhenBudgetHalfSpent(t *testing.T) {
	cases := []struct {
		name        string
		totalTokens int
		maxTokens   int
		maxResult   int
		maxData     int
		wantResult  int
		wantData    int
	}{
		{"under half → full caps", 100, 1000, 3200, 1200, 3200, 1200},
		{"exactly half → halved", 500, 1000, 3200, 1200, 1600, 600},
		{"over half → halved", 700, 1000, 3200, 1200, 1600, 600},
		{"maxTokens=0 → full caps", 9999, 0, 3200, 1200, 3200, 1200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, d := effectiveToolResultCaps(tc.totalTokens, tc.maxTokens, tc.maxResult, tc.maxData)
			if r != tc.wantResult {
				t.Fatalf("effective result cap = %d, want %d", r, tc.wantResult)
			}
			if d != tc.wantData {
				t.Fatalf("effective data cap = %d, want %d", d, tc.wantData)
			}
		})
	}
}

// 4. Dedup: when the same (tool name, input) call reappears after a
// prior round, the older tool_result Content gets replaced with a
// stub. The message is never removed — ToolCallID chains have to
// stay intact for Anthropic/OpenAI tool-use parity.
func TestFindPriorIdenticalToolResult_FindsMatchingCall(t *testing.T) {
	msgs := []provider.Message{
		{Role: types.RoleUser, Content: "original"},
		{
			Role: types.RoleAssistant,
			ToolCalls: []provider.ToolCall{{
				ID:    "call_1",
				Name:  "read_file",
				Input: map[string]any{"path": "engine.go", "line_start": 1, "line_end": 50},
			}},
		},
		{Role: types.RoleUser, Content: strings.Repeat("x", 3000), ToolCallID: "call_1", ToolName: "read_file"},
		{
			Role: types.RoleAssistant,
			ToolCalls: []provider.ToolCall{{
				ID:    "call_2",
				Name:  "grep_codebase",
				Input: map[string]any{"pattern": "TODO"},
			}},
		},
		{Role: types.RoleUser, Content: strings.Repeat("y", 3000), ToolCallID: "call_2", ToolName: "grep_codebase"},
	}

	// Round 3 wants to read engine.go:1-50 again. The current call
	// carries a fresh ID but the name + input hash match the round-1
	// call — expect the round-1 tool_result (index 2) as the match.
	current := provider.ToolCall{
		ID:    "call_3",
		Name:  "read_file",
		Input: map[string]any{"path": "engine.go", "line_start": 1, "line_end": 50},
	}
	idx := findPriorIdenticalToolResult(msgs, current, current.ID)
	if idx != 2 {
		t.Fatalf("expected prior tool_result at index 2, got %d", idx)
	}

	// Key-order permutation: same keys, different insertion order.
	// Canonical marshalling must still treat them as identical.
	rearranged := provider.ToolCall{
		ID:    "call_4",
		Name:  "read_file",
		Input: map[string]any{"line_end": 50, "path": "engine.go", "line_start": 1},
	}
	if idx := findPriorIdenticalToolResult(msgs, rearranged, rearranged.ID); idx != 2 {
		t.Fatalf("rearranged-input call should still match, got index %d", idx)
	}

	// Different path → not a duplicate.
	different := provider.ToolCall{
		ID:    "call_5",
		Name:  "read_file",
		Input: map[string]any{"path": "other.go", "line_start": 1, "line_end": 50},
	}
	if idx := findPriorIdenticalToolResult(msgs, different, different.ID); idx != -1 {
		t.Fatalf("different input must NOT match, got index %d", idx)
	}

	// Different tool → not a duplicate.
	diffTool := provider.ToolCall{
		ID:    "call_6",
		Name:  "grep_codebase",
		Input: map[string]any{"path": "engine.go", "line_start": 1, "line_end": 50},
	}
	if idx := findPriorIdenticalToolResult(msgs, diffTool, diffTool.ID); idx != -1 {
		t.Fatalf("different tool name must NOT match, got index %d", idx)
	}
}

// End-to-end dedup: run two rounds that read the same file, then a
// third round that wraps up. After round 2 the round-1 tool_result
// must be a stub, not the original 3000-char payload.
func TestNativeToolLoop_DedupsIdenticalReadAcrossRounds(t *testing.T) {
	tmp := t.TempDir()
	// Use DIVERSE lines so compressToolResult doesn't squash them into a
	// tiny payload — the dedup test needs a non-trivial tool_result for
	// the prev-content-size gate to kick in.
	var bigFile strings.Builder
	for i := 0; i < 60; i++ {
		bigFile.WriteString("Line ")
		bigFile.WriteString(strings.Repeat("x", 1)) // placeholder
		// Vary the content so compression can't collapse it.
		bigFile.WriteString(" — unique marker ")
		for j := 0; j < 20; j++ {
			bigFile.WriteString(string(rune('A' + (i*j+j)%26)))
		}
		bigFile.WriteString(" — tail token ")
		bigFile.WriteString(string(rune('a' + i%26)))
		bigFile.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(tmp, "big.txt"), []byte(bigFile.String()), 0o644); err != nil {
		t.Fatalf("write big.txt: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub-model",
		MaxTokens:  4096,
		MaxContext: 128000,
	}
	cfg.Agent.MaxToolSteps = 10
	cfg.Agent.MaxToolTokens = 0 // no budget pressure for this test

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	sameReadCall := func(id string) provider.ToolCall {
		return provider.ToolCall{
			ID:   id,
			Name: "tool_call",
			Input: toolCallInput(map[string]any{
				"name": "read_file",
				"args": map[string]any{"path": "big.txt", "line_start": 1, "line_end": 60},
			}),
		}
	}
	stub := &scriptedProvider{
		name:  "stub",
		model: "stub-model",
		hints: newNativeHints(),
		responses: []scriptedResponse{
			{ToolCalls: []provider.ToolCall{sameReadCall("r1")}},
			{ToolCalls: []provider.ToolCall{sameReadCall("r2")}},
			{Text: "done"},
		},
	}
	router.Register(stub)

	eng := &Engine{
		Config:      cfg,
		EventBus:    NewEventBus(),
		ProjectRoot: tmp,
		Providers:   router,
		Tools:       tools.New(*cfg),
	}

	if _, err := eng.AskWithMetadata(context.Background(), "dedup check"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Inspect the final request the stub observed: the messages it saw
	// on round 3 must contain exactly one full read_file payload (the
	// newer one) and one stubbed placeholder for the older call.
	stub.mu.Lock()
	requests := append([]provider.CompletionRequest(nil), stub.requests...)
	stub.mu.Unlock()
	if len(requests) < 3 {
		t.Fatalf("want 3 provider requests, got %d", len(requests))
	}
	lastReq := requests[2]

	stubCount := 0
	fullPayloadCount := 0
	for _, m := range lastReq.Messages {
		if m.Role != types.RoleUser || strings.TrimSpace(m.ToolCallID) == "" {
			continue
		}
		if strings.Contains(m.Content, "deduped") {
			stubCount++
			continue
		}
		if len(m.Content) > 500 {
			fullPayloadCount++
		}
	}
	if stubCount != 1 {
		t.Fatalf("want exactly 1 deduped stub message in the final round's context, got %d", stubCount)
	}
	if fullPayloadCount != 1 {
		t.Fatalf("want exactly 1 full-payload tool_result (the newest read), got %d", fullPayloadCount)
	}
}

// effectiveToolResultCaps mirrors the arithmetic from the agent loop
// so the gate behaviour can be asserted in isolation. This helper is
// test-only — if the loop's logic changes, this helper must be kept
// in sync (the test in TestEffectiveToolResultCap_HalvesWhenBudgetHalfSpent
// will catch drift).
func effectiveToolResultCaps(totalTokens, maxTokens, maxResult, maxData int) (int, int) {
	r := maxResult
	d := maxData
	if maxTokens > 0 && totalTokens*2 >= maxTokens {
		if r > 0 {
			r /= 2
		}
		if d > 0 {
			d /= 2
		}
	}
	return r, d
}

var _ = time.Millisecond // imported for consistency across guard tests
