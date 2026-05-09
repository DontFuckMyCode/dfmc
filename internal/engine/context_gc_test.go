package engine

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// asstMsg builds an assistant message stub with the given ID, stripped
// content, and aligned tool-call/result pairs. Tool params come from
// the parallel slices; success flags from a parallel bool slice.
func asstMsg(id, content string, names []string, params []map[string]any, success []bool) types.Message {
	m := types.Message{ID: id, Role: types.RoleAssistant, Content: content}
	for i := range names {
		m.ToolCalls = append(m.ToolCalls, types.ToolCallRecord{Name: names[i], Params: params[i]})
		m.Results = append(m.Results, types.ToolResultRecord{Name: names[i], Success: success[i]})
	}
	return m
}

func userMsg(id, content string) types.Message {
	return types.Message{ID: id, Role: types.RoleUser, Content: content}
}

// Pattern 1: a purely-tool-noise turn whose only tool call failed gets
// dropped when a later turn successfully retries the same tool on the
// same target. The intermediate failure is exactly the "stale noise"
// the model wastes context re-reading every round.
func TestGCFailedRetryDroppedWhenLaterSuccess(t *testing.T) {
	msgs := []types.Message{
		userMsg("u-1", "read foo.go"),
		asstMsg("a-1", "", []string{"read_file"},
			[]map[string]any{{"path": "foo.go"}}, []bool{false}),
		asstMsg("a-2", "got it",
			[]string{"read_file"},
			[]map[string]any{{"path": "foo.go"}}, []bool{true}),
	}
	got := garbageCollectActiveBranch(msgs)
	if len(got.DropIDs) != 1 || got.DropIDs[0] != "a-1" {
		t.Fatalf("expected a-1 to be dropped, got %v", got.DropIDs)
	}
	if got.Reasons["a-1"] != gcReasonFailedRetry {
		t.Errorf("expected failed_retry reason, got %q", got.Reasons["a-1"])
	}
}

// A failed turn with no later success on the same key must NOT be
// dropped — we keep the failure visible so the model knows the call
// is broken and can pick a different approach.
func TestGCFailedTurnKeptWithoutLaterSuccess(t *testing.T) {
	msgs := []types.Message{
		userMsg("u-1", "read foo.go"),
		asstMsg("a-1", "", []string{"read_file"},
			[]map[string]any{{"path": "foo.go"}}, []bool{false}),
		asstMsg("a-2", "still trying",
			[]string{"read_file"},
			[]map[string]any{{"path": "bar.go"}}, []bool{true}),
	}
	got := garbageCollectActiveBranch(msgs)
	if len(got.DropIDs) != 0 {
		t.Errorf("no drops expected, got %v", got.DropIDs)
	}
}

// Pattern 2: an earlier successful read_file with end=20 is dominated
// by a later read_file on the same path with end=80. The earlier slice
// is fully covered.
func TestGCDuplicateReadDroppedWhenWiderLater(t *testing.T) {
	msgs := []types.Message{
		userMsg("u-1", "read foo.go"),
		asstMsg("a-1", "",
			[]string{"read_file"},
			[]map[string]any{{"path": "foo.go", "line_end": 20}}, []bool{true}),
		asstMsg("a-2", "summary text",
			[]string{"read_file"},
			[]map[string]any{{"path": "foo.go", "line_end": 80}}, []bool{true}),
	}
	got := garbageCollectActiveBranch(msgs)
	if len(got.DropIDs) != 1 || got.DropIDs[0] != "a-1" {
		t.Fatalf("expected a-1 to be dropped, got %v", got.DropIDs)
	}
	if got.Reasons["a-1"] != gcReasonDuplicateRead {
		t.Errorf("expected duplicate_read reason, got %q", got.Reasons["a-1"])
	}
}

// A narrower later read does NOT dominate a wider earlier read.
func TestGCDuplicateReadKeptWhenLaterNarrower(t *testing.T) {
	msgs := []types.Message{
		userMsg("u-1", "read foo.go"),
		asstMsg("a-1", "",
			[]string{"read_file"},
			[]map[string]any{{"path": "foo.go", "line_end": 80}}, []bool{true}),
		asstMsg("a-2", "summary",
			[]string{"read_file"},
			[]map[string]any{{"path": "foo.go", "line_end": 20}}, []bool{true}),
	}
	got := garbageCollectActiveBranch(msgs)
	if len(got.DropIDs) != 0 {
		t.Errorf("no drops expected, got %v", got.DropIDs)
	}
}

// A message with non-empty stripped Content must NEVER be dropped,
// even if every tool call inside it is dominated. The model's text
// might still be referenced by the user.
func TestGCKeepsMessagesWithText(t *testing.T) {
	msgs := []types.Message{
		userMsg("u-1", "read foo.go"),
		asstMsg("a-1", "I tried but failed.",
			[]string{"read_file"},
			[]map[string]any{{"path": "foo.go"}}, []bool{false}),
		asstMsg("a-2", "got it",
			[]string{"read_file"},
			[]map[string]any{{"path": "foo.go"}}, []bool{true}),
	}
	got := garbageCollectActiveBranch(msgs)
	if len(got.DropIDs) != 0 {
		t.Errorf("expected no drops (a-1 has text), got %v", got.DropIDs)
	}
}

// The most recent assistant message must NEVER be dropped — the model
// just produced it and will reference it next round.
func TestGCNeverDropsLatestAssistant(t *testing.T) {
	msgs := []types.Message{
		userMsg("u-1", "read foo.go"),
		asstMsg("a-1", "first",
			[]string{"read_file"},
			[]map[string]any{{"path": "foo.go", "line_end": 80}}, []bool{true}),
		asstMsg("a-2", "",
			[]string{"read_file"},
			[]map[string]any{{"path": "foo.go", "line_end": 10}}, []bool{true}),
	}
	got := garbageCollectActiveBranch(msgs)
	if len(got.DropIDs) != 0 {
		t.Errorf("latest assistant must never be dropped, got %v", got.DropIDs)
	}
}

// A turn with a single non-dominated tool call disqualifies the WHOLE
// message — partial dominance is not enough.
func TestGCPartialDominanceKeepsMessage(t *testing.T) {
	msgs := []types.Message{
		userMsg("u-1", "do work"),
		asstMsg("a-1", "",
			[]string{"read_file", "read_file"},
			[]map[string]any{
				{"path": "foo.go", "line_end": 20},
				{"path": "bar.go"},
			},
			[]bool{true, true}),
		asstMsg("a-2", "done",
			[]string{"read_file"},
			[]map[string]any{{"path": "foo.go", "line_end": 80}}, []bool{true}),
	}
	got := garbageCollectActiveBranch(msgs)
	if len(got.DropIDs) != 0 {
		t.Errorf("expected no drops (bar.go not dominated), got %v", got.DropIDs)
	}
}

// Unknown tools (run_command, custom plugins) are intentionally
// non-keyable, so a turn containing them is always kept — the engine
// can't prove dominance from the trace alone.
func TestGCUnknownToolsAreNeverDominated(t *testing.T) {
	msgs := []types.Message{
		userMsg("u-1", "run script"),
		asstMsg("a-1", "",
			[]string{"run_command"},
			[]map[string]any{{"command": "ls"}}, []bool{false}),
		asstMsg("a-2", "ok",
			[]string{"run_command"},
			[]map[string]any{{"command": "ls"}}, []bool{true}),
	}
	got := garbageCollectActiveBranch(msgs)
	if len(got.DropIDs) != 0 {
		t.Errorf("unknown tools must not be dominated, got %v", got.DropIDs)
	}
}

// Messages without IDs (legacy entries from before the ID field
// existed) cannot be dropped — RemoveMessagesByID can't target them.
func TestGCSkipsMessagesWithoutID(t *testing.T) {
	msgs := []types.Message{
		userMsg("u-1", "read foo.go"),
		asstMsg("", "",
			[]string{"read_file"},
			[]map[string]any{{"path": "foo.go"}}, []bool{false}),
		asstMsg("a-2", "got it",
			[]string{"read_file"},
			[]map[string]any{{"path": "foo.go"}}, []bool{true}),
	}
	got := garbageCollectActiveBranch(msgs)
	if len(got.DropIDs) != 0 {
		t.Errorf("messages without ID must not be dropped, got %v", got.DropIDs)
	}
}

// Two successful non-read calls on the same key (grep, find_symbol,
// list_dir, glob) are NOT mutually dominating — an intervening
// write_file could shift what the second call returns, so we have no
// cheap way to prove the first is redundant from the trace alone.
// Failed-retry-then-success and read_file cover the safe cases.
func TestGCSuccessOnNonReadToolNotDominated(t *testing.T) {
	msgs := []types.Message{
		userMsg("u-1", "search foo"),
		asstMsg("a-1", "",
			[]string{"grep_codebase"},
			[]map[string]any{{"pattern": "foo"}}, []bool{true}),
		asstMsg("a-2", "later look",
			[]string{"grep_codebase"},
			[]map[string]any{{"pattern": "foo"}}, []bool{true}),
	}
	got := garbageCollectActiveBranch(msgs)
	if len(got.DropIDs) != 0 {
		t.Errorf("two successful greps must NOT be dominated (intervening writes can shift results), got %v", got.DropIDs)
	}
}

// Single-message branches are no-ops — there's nothing to compare.
func TestGCEmptyBranchNoOp(t *testing.T) {
	got := garbageCollectActiveBranch(nil)
	if len(got.DropIDs) != 0 {
		t.Error("nil branch should be no-op")
	}
	got = garbageCollectActiveBranch([]types.Message{userMsg("u-1", "hi")})
	if len(got.DropIDs) != 0 {
		t.Error("single-message branch should be no-op")
	}
}
