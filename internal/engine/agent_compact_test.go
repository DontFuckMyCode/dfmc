package engine

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// Helpers ------------------------------------------------------------------

// makeRound returns an assistant tool_call turn plus its matching user
// tool_result turn, suitable for feeding into the compactor.
func makeRound(id, tool, args, resultBody string, toolErr bool) []provider.Message {
	return []provider.Message{
		{
			Role: types.RoleAssistant,
			ToolCalls: []provider.ToolCall{
				{
					ID:    id,
					Name:  tool,
					Input: map[string]any{"args": args},
				},
			},
		},
		{
			Role:       types.RoleUser,
			ToolCallID: id,
			ToolName:   tool,
			Content:    resultBody,
			ToolError:  toolErr,
		},
	}
}

func compactorEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := config.DefaultConfig()
	// Keep the tunables explicit so the assertions do not float with the
	// defaults file.
	cfg.Agent.ContextLifecycle = config.ContextLifecycleConfig{
		Enabled:                   true,
		AutoCompactThresholdRatio: 0.01, // ~320 tokens of a 32k default window
		KeepRecentRounds:          1,
		HandoffBriefMaxTokens:     500,
	}
	return &Engine{Config: cfg}
}

// Tests --------------------------------------------------------------------

// TestMaybeCompactNativeLoopHistory_FiresAndCollapses: when the running
// history crosses the configured threshold, the compactor must rewrite the
// message list so the oldest rounds collapse into a single summary assistant
// turn while the user's organic question and the last KeepRecentRounds stay
// verbatim.
func TestMaybeCompactNativeLoopHistory_FiresAndCollapses(t *testing.T) {
	eng := compactorEngine(t)

	bigBlob := strings.Repeat("alpha beta gamma delta epsilon zeta ", 200)

	msgs := []provider.Message{
		{Role: types.RoleSystem, Content: "system preamble"},
		{Role: types.RoleUser, Content: "please audit the repo"},
	}
	// Four complete tool rounds — first three should collapse, last one
	// stays verbatim under KeepRecentRounds=1.
	msgs = append(msgs, makeRound("r1", "read_file", "a.go", bigBlob, false)...)
	msgs = append(msgs, makeRound("r2", "grep_codebase", "Engine", bigBlob, false)...)
	msgs = append(msgs, makeRound("r3", "read_file", "b.go", bigBlob, true)...)
	msgs = append(msgs, makeRound("r4", "read_file", "c.go", "small tail", false)...)

	rebuilt, report := eng.maybeCompactNativeLoopHistory(msgs, "system preamble", nil)
	if report == nil {
		t.Fatalf("expected compaction to fire, got nil report (msgs=%d)", len(msgs))
	}
	if report.RoundsCollapsed != 3 {
		t.Fatalf("expected 3 rounds collapsed (4 rounds - 1 keep), got %d", report.RoundsCollapsed)
	}
	if report.AfterTokens >= report.BeforeTokens {
		t.Fatalf("expected AfterTokens < BeforeTokens, got before=%d after=%d", report.BeforeTokens, report.AfterTokens)
	}
	if report.MessagesRemoved <= 0 {
		t.Fatalf("expected MessagesRemoved > 0, got %d", report.MessagesRemoved)
	}

	// The prefix (system + user question) must survive unchanged.
	if rebuilt[0].Role != types.RoleSystem || rebuilt[0].Content != "system preamble" {
		t.Fatalf("prefix system message lost: %#v", rebuilt[0])
	}
	if rebuilt[1].Role != types.RoleUser || !strings.Contains(rebuilt[1].Content, "audit the repo") {
		t.Fatalf("user question not preserved: %#v", rebuilt[1])
	}

	// The next message must be the auto-compact summary.
	summary := rebuilt[2]
	if summary.Role != types.RoleAssistant {
		t.Fatalf("expected summary message as assistant turn, got role=%s", summary.Role)
	}
	if !strings.Contains(summary.Content, "[auto-compacted prior tool context]") {
		t.Fatalf("summary message should contain compaction header, got:\n%s", summary.Content)
	}
	for _, want := range []string{"read_file", "grep_codebase", "ok=", "fail="} {
		if !strings.Contains(summary.Content, want) {
			t.Fatalf("summary content missing %q:\n%s", want, summary.Content)
		}
	}

	// The last round (r4, "c.go") must remain verbatim at the tail.
	tail := rebuilt[len(rebuilt)-2:]
	if tail[0].Role != types.RoleAssistant || len(tail[0].ToolCalls) != 1 || tail[0].ToolCalls[0].ID != "r4" {
		t.Fatalf("expected tail assistant turn to be r4 intact, got %#v", tail[0])
	}
	if tail[1].Role != types.RoleUser || tail[1].ToolCallID != "r4" || tail[1].Content != "small tail" {
		t.Fatalf("expected tail tool_result for r4 intact, got %#v", tail[1])
	}
}

// TestMaybeCompactNativeLoopHistory_DisabledShortCircuits: when
// context_lifecycle.enabled is false, the compactor must be a no-op even
// when the message list is enormous.
func TestMaybeCompactNativeLoopHistory_DisabledShortCircuits(t *testing.T) {
	eng := compactorEngine(t)
	eng.Config.Agent.ContextLifecycle.Enabled = false

	bigBlob := strings.Repeat("lorem ipsum dolor sit amet ", 400)
	msgs := []provider.Message{
		{Role: types.RoleSystem, Content: "system"},
		{Role: types.RoleUser, Content: "do work"},
	}
	for i := range 5 {
		msgs = append(msgs, makeRound("id"+string(rune('a'+i)), "read_file", "x", bigBlob, false)...)
	}
	before := len(msgs)

	rebuilt, report := eng.maybeCompactNativeLoopHistory(msgs, "system", nil)
	if report != nil {
		t.Fatalf("expected nil report when lifecycle disabled, got %#v", report)
	}
	if len(rebuilt) != before {
		t.Fatalf("expected msgs untouched (%d), got %d", before, len(rebuilt))
	}
}

// TestMaybeCompactNativeLoopHistory_BelowThresholdNoOp: when the footprint
// sits comfortably under the threshold, the compactor must not rewrite
// anything — there is no saving to be had.
func TestMaybeCompactNativeLoopHistory_BelowThresholdNoOp(t *testing.T) {
	eng := compactorEngine(t)
	// Set the threshold so high that the tiny messages below cannot trip it.
	eng.Config.Agent.ContextLifecycle.AutoCompactThresholdRatio = 0.99

	msgs := []provider.Message{
		{Role: types.RoleSystem, Content: "sys"},
		{Role: types.RoleUser, Content: "hi"},
	}
	msgs = append(msgs, makeRound("r1", "read_file", "a", "tiny", false)...)
	msgs = append(msgs, makeRound("r2", "read_file", "b", "tiny", false)...)

	rebuilt, report := eng.maybeCompactNativeLoopHistory(msgs, "sys", nil)
	if report != nil {
		t.Fatalf("expected no compaction below threshold, got %#v", report)
	}
	if len(rebuilt) != len(msgs) {
		t.Fatalf("expected msgs unchanged, got %d -> %d", len(msgs), len(rebuilt))
	}
}

// TestMaybeCompactNativeLoopHistory_KeepsRecentRoundsInvariant: regardless
// of how many rounds fit in the history, exactly KeepRecentRounds complete
// rounds must survive verbatim after the summary.
func TestMaybeCompactNativeLoopHistory_KeepsRecentRoundsInvariant(t *testing.T) {
	eng := compactorEngine(t)
	eng.Config.Agent.ContextLifecycle.KeepRecentRounds = 2

	bigBlob := strings.Repeat("xyzzy ", 600)
	msgs := []provider.Message{
		{Role: types.RoleSystem, Content: "sys"},
		{Role: types.RoleUser, Content: "work"},
	}
	for _, id := range []string{"r1", "r2", "r3", "r4", "r5"} {
		msgs = append(msgs, makeRound(id, "read_file", id, bigBlob, false)...)
	}

	rebuilt, report := eng.maybeCompactNativeLoopHistory(msgs, "sys", nil)
	if report == nil {
		t.Fatalf("expected compaction to fire with 5 rounds of large content")
	}
	if report.RoundsCollapsed != 3 {
		t.Fatalf("expected 3 rounds collapsed (5 rounds - 2 keep), got %d", report.RoundsCollapsed)
	}

	// Walk from the tail: last four messages should be r4+r5 pairs.
	if n := len(rebuilt); n < 4 {
		t.Fatalf("rebuilt too short: %d", n)
	}
	tail := rebuilt[len(rebuilt)-4:]
	wantIDs := []string{"r4", "r4", "r5", "r5"}
	for i, msg := range tail {
		switch i % 2 {
		case 0:
			if msg.Role != types.RoleAssistant || len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != wantIDs[i] {
				t.Fatalf("tail[%d] expected assistant tool_call id=%s, got %#v", i, wantIDs[i], msg)
			}
		case 1:
			if msg.Role != types.RoleUser || msg.ToolCallID != wantIDs[i] {
				t.Fatalf("tail[%d] expected user tool_result id=%s, got %#v", i, wantIDs[i], msg)
			}
		}
	}
}

// TestSplitNativeLoopRounds_KeepsAssistantToolResultPairing: the round
// splitter underpins the compaction invariant that an assistant tool_use
// turn is never separated from its matching user tool_result replies.
func TestSplitNativeLoopRounds_KeepsAssistantToolResultPairing(t *testing.T) {
	msgs := []provider.Message{}
	msgs = append(msgs, makeRound("r1", "read_file", "a", "A", false)...)
	// Assistant round with no tool results attached — a pure reasoning turn.
	msgs = append(msgs,
		provider.Message{Role: types.RoleAssistant, Content: "thinking..."},
	)
	// Another tool round, this time with two tool results for a batch call.
	msgs = append(msgs,
		provider.Message{
			Role: types.RoleAssistant,
			ToolCalls: []provider.ToolCall{
				{ID: "batch_a", Name: "read_file"},
				{ID: "batch_b", Name: "read_file"},
			},
		},
		provider.Message{Role: types.RoleUser, ToolCallID: "batch_a", Content: "A"},
		provider.Message{Role: types.RoleUser, ToolCallID: "batch_b", Content: "B"},
	)

	rounds := splitNativeLoopRounds(msgs)
	if len(rounds) != 3 {
		t.Fatalf("expected 3 rounds (r1, reasoning, batch), got %d", len(rounds))
	}
	if len(rounds[0].Messages) != 2 {
		t.Fatalf("round 0 should have assistant+tool_result, got %d", len(rounds[0].Messages))
	}
	if len(rounds[1].Messages) != 1 {
		t.Fatalf("round 1 (reasoning) should be a lone assistant turn, got %d", len(rounds[1].Messages))
	}
	if len(rounds[2].Messages) != 3 {
		t.Fatalf("round 2 should bundle assistant+2 tool_results, got %d", len(rounds[2].Messages))
	}
}

// TestForceCompactNativeLoopHistory_IgnoresThreshold: force-compact on the
// resume path must fire even when the current footprint sits below the
// configured threshold. Otherwise a parked loop with lots of rounds but
// moderate byte size would not compact, and the first resume step would
// blow the budget the same way that parked the loop.
func TestForceCompactNativeLoopHistory_IgnoresThreshold(t *testing.T) {
	eng := compactorEngine(t)
	// Set the threshold absurdly high so maybeCompact would never fire.
	eng.Config.Agent.ContextLifecycle.AutoCompactThresholdRatio = 0.99
	eng.Config.Agent.ContextLifecycle.KeepRecentRounds = 1

	msgs := []provider.Message{
		{Role: types.RoleSystem, Content: "sys"},
		{Role: types.RoleUser, Content: "do the work"},
	}
	// 4 rounds — even though individually small, force-compact should still
	// collapse 3 of them because rounds > KeepRecentRounds.
	for _, id := range []string{"r1", "r2", "r3", "r4"} {
		msgs = append(msgs, makeRound(id, "read_file", id, strings.Repeat("payload ", 50), false)...)
	}

	if _, report := eng.maybeCompactNativeLoopHistory(msgs, "sys", nil); report != nil {
		t.Fatalf("maybeCompact should be below threshold here, got report=%#v", report)
	}

	rebuilt, report := eng.forceCompactNativeLoopHistory(msgs, "sys", nil)
	if report == nil {
		t.Fatal("forceCompact should fire unconditionally when rounds > keep")
	}
	if report.RoundsCollapsed != 3 {
		t.Fatalf("expected 3 rounds collapsed, got %d", report.RoundsCollapsed)
	}
	if len(rebuilt) >= len(msgs) {
		t.Fatalf("rebuilt should be shorter than original, got %d vs %d", len(rebuilt), len(msgs))
	}
}

// TestFindNativeLoopPrefixEnd_IgnoresToolResultUsers: the prefix boundary is
// the last *organic* user turn — a user message with ToolCallID set is a
// tool_result, not a prefix element.
func TestFindNativeLoopPrefixEnd_IgnoresToolResultUsers(t *testing.T) {
	msgs := []provider.Message{
		{Role: types.RoleSystem, Content: "sys"},
		{Role: types.RoleUser, Content: "earlier turn"},
		{Role: types.RoleAssistant, Content: "earlier reply"},
		{Role: types.RoleUser, Content: "current question"},
	}
	msgs = append(msgs, makeRound("r1", "read_file", "a", "A", false)...)
	// The assistant+tool_result pair above contains a user message with
	// ToolCallID set — it must *not* advance the prefix boundary.

	end := findNativeLoopPrefixEnd(msgs)
	if end != 4 {
		t.Fatalf("expected prefix end at index 4 (after current question), got %d", end)
	}
}
