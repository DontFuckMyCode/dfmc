package engine

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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

// REPORT.md #8 regression: in the real YAML-merge path a partial
// override that only tunes the ratio must NOT silently disable
// compaction. DefaultConfig() pre-seeds Enabled=true, and YAML
// preserves untouched fields when merging into a populated struct,
// so a `auto_compact_threshold_ratio: 0.5` override with no
// `enabled` key still resolves to Enabled=true.
func TestResolveContextLifecycle_YAMLPartialOverridePreservesEnabled(t *testing.T) {
	cfg := config.DefaultConfig()
	yamlBlob := []byte(`agent:
  context_lifecycle:
    auto_compact_threshold_ratio: 0.5
`)
	if err := yaml.Unmarshal(yamlBlob, cfg); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	eng := &Engine{Config: cfg}
	got := eng.resolveContextLifecycle()
	if !got.Enabled {
		t.Fatal("YAML partial override (only ratio) must keep Enabled=true from defaults")
	}
	if got.AutoCompactThresholdRatio != 0.5 {
		t.Fatalf("ratio override should take effect, got %.2f", got.AutoCompactThresholdRatio)
	}
}

// Inverse of the above: an explicit YAML `enabled: false` MUST disable
// compaction, even if other knobs are set. This is the user opt-out path.
func TestResolveContextLifecycle_YAMLExplicitDisableHonoured(t *testing.T) {
	cfg := config.DefaultConfig()
	yamlBlob := []byte(`agent:
  context_lifecycle:
    enabled: false
    auto_compact_threshold_ratio: 0.5
`)
	if err := yaml.Unmarshal(yamlBlob, cfg); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	eng := &Engine{Config: cfg}
	got := eng.resolveContextLifecycle()
	if got.Enabled {
		t.Fatal("explicit YAML enabled:false must disable compaction")
	}
}

// Sanity: an empty/missing context_lifecycle block keeps every default.
func TestResolveContextLifecycle_NoYAMLBlockKeepsAllDefaults(t *testing.T) {
	cfg := config.DefaultConfig()
	eng := &Engine{Config: cfg}
	got := eng.resolveContextLifecycle()
	if !got.Enabled {
		t.Fatal("default config must have Enabled=true")
	}
	if got.AutoCompactThresholdRatio != 0.7 {
		t.Fatalf("ratio default should be 0.7, got %.2f", got.AutoCompactThresholdRatio)
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

// User-visible regression (2026-04-18): the model's "memory" of past
// rounds collapsed to "round 5 · tools=read_file · ok=2 fail=0" —
// useful as a count, useless for reasoning. Post-fix every collapsed
// round emits one indented "↳ <tool> <target> → <result-head>" line
// per call so the model retains a foggy but functional memory of
// what it actually read / ran. This test pins the format because it's
// the contract the model relies on after compaction kicks in.
func TestSummariseSingleRound_PreservesPerCallTargetAndResultExcerpt(t *testing.T) {
	round := toolRound{Messages: []provider.Message{
		{
			Role:    types.RoleAssistant,
			Content: "checking the config loader",
			ToolCalls: []provider.ToolCall{
				{ID: "c1", Name: "read_file", Input: map[string]any{
					"path": "internal/config/config.go", "line_start": 1, "line_end": 80,
				}},
				{ID: "c2", Name: "run_command", Input: map[string]any{
					"command": "go", "args": []any{"build", "./..."},
				}},
			},
		},
		{Role: types.RoleUser, ToolCallID: "c1", ToolName: "read_file", Content: "package config\n\ntype Config struct {\n  Providers ProvidersConfig\n}\n"},
		{Role: types.RoleUser, ToolCallID: "c2", ToolName: "run_command", ToolError: true, Content: "ERROR: command exited with code 1\n\nOUTPUT:\n./foo.go:12:5: undefined: SomeMissingSymbol"},
	}}

	got := summariseSingleRound(7, round)

	// Header keeps the round number and the count summary so trajectory
	// hints can still cite "round 7 · 2 calls · ok=1 fail=1".
	if !strings.Contains(got, "round 7") {
		t.Fatalf("header missing round index: %q", got)
	}
	if !strings.Contains(got, "2 call(s)") || !strings.Contains(got, "ok=1") || !strings.Contains(got, "fail=1") {
		t.Fatalf("header missing call counts: %q", got)
	}
	// Narration line preserved so the model can recall "what was I
	// thinking when I ran these calls".
	if !strings.Contains(got, "checking the config loader") {
		t.Fatalf("assistant narration dropped: %q", got)
	}
	// Per-call target is the lifeblood of the new format. read_file
	// must name the path AND the line range so the model knows
	// what slice of the file it has already seen.
	if !strings.Contains(got, "↳ read_file path=internal/config/config.go (lines 1-80)") {
		t.Fatalf("read_file target missing or misformatted: %q", got)
	}
	// Result excerpt — first non-empty line of the file body so the
	// model gets a "foot in the door" memory of the content.
	if !strings.Contains(got, "package config") {
		t.Fatalf("read_file result excerpt missing: %q", got)
	}
	// run_command's target joins binary + first arg, exactly like
	// the live TUI batch-inner preview.
	if !strings.Contains(got, "↳ run_command command=go build ./...") {
		t.Fatalf("run_command target missing or misformatted: %q", got)
	}
	// Failure tail surfaces the actionable error — the line the model
	// needs to see to know WHY the build broke. ERROR: prefix
	// stripped, FAIL marker prepended.
	if !strings.Contains(got, "FAIL command exited with code 1") {
		t.Fatalf("failed run_command should show error tail with FAIL marker: %q", got)
	}
}

// Inverse: a pure reasoning turn (no tool_calls) keeps its narration
// without collapsing into an empty "round N" stub.
func TestSummariseSingleRound_ReasoningOnlyTurnKeepsNarration(t *testing.T) {
	round := toolRound{Messages: []provider.Message{
		{Role: types.RoleAssistant, Content: "I think we need to refactor the loader before touching the tests."},
	}}
	got := summariseSingleRound(3, round)
	if !strings.Contains(got, "round 3") {
		t.Fatalf("missing header: %q", got)
	}
	if !strings.Contains(got, "refactor the loader") {
		t.Fatalf("reasoning narration dropped: %q", got)
	}
}

// Cross-surface consistency: dedupTargetHint must use the same
// priority order as the live previewBatchTarget so the user sees the
// same identifiers in the live chip and in the deduped stub.
func TestDedupTargetHint_NamesIdentifyingArg(t *testing.T) {
	cases := []struct {
		name string
		call provider.ToolCall
		want string
	}{
		{"read_file uses path", provider.ToolCall{Name: "read_file", Input: map[string]any{"path": "foo.go"}}, " (foo.go)"},
		{"meta-tool unwraps inner args", provider.ToolCall{Name: "tool_call", Input: map[string]any{
			"name": "read_file", "args": map[string]any{"path": "bar.go"},
		}}, " (bar.go)"},
		{"empty input → empty hint", provider.ToolCall{Name: "tool_call", Input: map[string]any{}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dedupTargetHint(c.call); got != c.want {
				t.Fatalf("want %q, got %q", c.want, got)
			}
		})
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

func TestPickInt(t *testing.T) {
	tests := []struct {
		raw    any
		wantOk bool
	}{
		{nil, false},
		{int(42), true},
		{int64(99), true},
		{float64(7.9), true},
		{"not a number", false},
		{true, false},
	}
	for _, tc := range tests {
		got, ok := pickInt(tc.raw)
		if ok != tc.wantOk {
			t.Errorf("pickInt(%v) ok=%v, want %v", tc.raw, ok, tc.wantOk)
		}
		if tc.wantOk && got == 0 && tc.raw != nil {
			// only check non-zero if we expected success
		}
	}
}

func TestPickInt_IntValue(t *testing.T) {
	got, ok := pickInt(int(42))
	if !ok || got != 42 {
		t.Errorf("pickInt(int(42)) = (%d, %v), want (42, true)", got, ok)
	}
}

func TestPickInt_Int64Value(t *testing.T) {
	got, ok := pickInt(int64(99))
	if !ok || got != 99 {
		t.Errorf("pickInt(int64(99)) = (%d, %v), want (99, true)", got, ok)
	}
}

func TestPickInt_FloatValue(t *testing.T) {
	got, ok := pickInt(float64(7))
	if !ok || got != 7 {
		t.Errorf("pickInt(float64(7)) = (%d, %v), want (7, true)", got, ok)
	}
}
