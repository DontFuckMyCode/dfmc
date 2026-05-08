package context

import (
	"strings"
	"testing"
)

func TestTrajectoryHints_FailureBeatsOtherRules(t *testing.T) {
	fresh := []TraceEntry{
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "auth.go"}, Ok: true, Step: 1},
		{Tool: "tool_call", Inner: "run_command", Args: map[string]any{"command": "go test ./..."}, Ok: false, Err: "go: cannot find main module", Step: 1},
	}
	out := TrajectoryHints(fresh, fresh, nil)
	if out == nil || len(out.Hints) == 0 {
		t.Fatalf("expected at least one hint")
	}
	if !strings.Contains(out.Hints[0], "failed") {
		t.Fatalf("failure hint should come first, got %q", out.Hints[0])
	}
	if !strings.Contains(out.Hints[0], "run_command") {
		t.Fatalf("failure hint should name the tool, got %q", out.Hints[0])
	}
}

func TestTrajectoryHints_MutationRemindsValidation(t *testing.T) {
	fresh := []TraceEntry{
		{Tool: "tool_call", Inner: "write_file", Args: map[string]any{"path": "internal/auth/token.go"}, Ok: true, Step: 1},
	}
	out := TrajectoryHints(fresh, fresh, nil)
	if out == nil || len(out.Hints) != 1 {
		t.Fatalf("expected one hint, got %d: %v", len(out.Hints), out)
	}
	if !strings.Contains(out.Hints[0], "internal/auth/token.go") {
		t.Fatalf("hint should cite the path, got %q", out.Hints[0])
	}
	if !strings.Contains(strings.ToLower(out.Hints[0]), "validate") {
		t.Fatalf("hint should push validation, got %q", out.Hints[0])
	}
}

func TestTrajectoryHints_RepeatedFailuresFlagged(t *testing.T) {
	// Model keeps trying read_file with bad paths — same error class on
	// every miss. The trajectory layer should escalate from "don't retry
	// with same inputs" (Rule 1) to "switch tactic, you're stuck" (Rule 0).
	all := []TraceEntry{
		{Tool: "tool_call", Inner: "read_file", Args: map[string]any{"path": "internal/auth.go"}, Ok: false, Err: "file does not exist", Step: 1},
		{Tool: "tool_call", Inner: "read_file", Args: map[string]any{"path": "internal/auth/auth.go"}, Ok: false, Err: "file does not exist", Step: 2},
		{Tool: "tool_call", Inner: "read_file", Args: map[string]any{"path": "auth/handler.go"}, Ok: false, Err: "file does not exist", Step: 3},
	}
	fresh := all[2:]
	out := TrajectoryHints(fresh, all, nil)
	if out == nil || len(out.Hints) == 0 {
		t.Fatalf("expected at least one hint")
	}
	first := out.Hints[0]
	if !strings.Contains(first, "read_file") {
		t.Errorf("hint should name the stuck tool, got %q", first)
	}
	if !strings.Contains(strings.ToLower(first), "switch tactic") {
		t.Errorf("hint should advise tactic switch, got %q", first)
	}
	if !strings.Contains(first, "3 times") {
		t.Errorf("hint should cite the failure count, got %q", first)
	}
	// Confidence should reflect the all-failures round.
	if out.Confidence > 0.2 {
		t.Errorf("confidence should be low for all-fail round, got %.2f", out.Confidence)
	}
}

func TestTrajectoryHints_RepeatedFailures_NotTriggeredOnDifferentErrors(t *testing.T) {
	// Three failures, three DIFFERENT error classes — not a stuck-loop
	// pattern, so Rule 0 must NOT fire (would mis-advise "stop retrying").
	all := []TraceEntry{
		{Tool: "tool_call", Inner: "run_command", Args: map[string]any{"command": "go test"}, Ok: false, Err: "go: cannot find main module", Step: 1},
		{Tool: "tool_call", Inner: "run_command", Args: map[string]any{"command": "go vet"}, Ok: false, Err: "exit status 2", Step: 2},
		{Tool: "tool_call", Inner: "run_command", Args: map[string]any{"command": "make build"}, Ok: false, Err: "make: command not found", Step: 3},
	}
	fresh := all[2:]
	out := TrajectoryHints(fresh, all, nil)
	if out == nil {
		t.Fatalf("expected non-nil output")
	}
	for _, h := range out.Hints {
		if strings.Contains(strings.ToLower(h), "switch tactic") {
			t.Errorf("Rule 0 should not fire when error fingerprints all differ, got %q", h)
		}
	}
}

func TestCountUnvalidatedMutations_AccumulatesAcrossRounds(t *testing.T) {
	all := []TraceEntry{
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "a.go"}, Ok: true, Step: 1},
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "b.go"}, Ok: true, Step: 2},
		{Tool: "tool_call", Inner: "write_file", Args: map[string]any{"path": "c.go"}, Ok: true, Step: 3},
	}
	count, paths := countUnvalidatedMutations(all)
	if count != 3 {
		t.Errorf("expected count=3, got %d", count)
	}
	if len(paths) != 3 || paths[0] != "a.go" || paths[2] != "c.go" {
		t.Errorf("unexpected path order: %v", paths)
	}
}

func TestCountUnvalidatedMutations_ResetByValidationCommand(t *testing.T) {
	all := []TraceEntry{
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "a.go"}, Ok: true, Step: 1},
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "b.go"}, Ok: true, Step: 2},
		{Tool: "tool_call", Inner: "run_command", Args: map[string]any{"command": "go test ./..."}, Ok: true, Step: 3},
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "c.go"}, Ok: true, Step: 4},
	}
	count, paths := countUnvalidatedMutations(all)
	if count != 1 {
		t.Errorf("validation should reset; expected count=1 (just c.go), got %d", count)
	}
	if len(paths) != 1 || paths[0] != "c.go" {
		t.Errorf("expected only c.go after validation, got %v", paths)
	}
}

func TestCountUnvalidatedMutations_DedupsRepeatedEditsToSameFile(t *testing.T) {
	all := []TraceEntry{
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "a.go"}, Ok: true, Step: 1},
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "a.go"}, Ok: true, Step: 2},
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "a.go"}, Ok: true, Step: 3},
	}
	count, _ := countUnvalidatedMutations(all)
	if count != 1 {
		t.Errorf("re-editing one file should de-dup to count=1, got %d", count)
	}
}

func TestCountUnvalidatedMutations_FailedEditsDontCount(t *testing.T) {
	all := []TraceEntry{
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "a.go"}, Ok: false, Err: "anchor not found", Step: 1},
		{Tool: "tool_call", Inner: "write_file", Args: map[string]any{"path": "b.go"}, Ok: true, Step: 2},
	}
	count, _ := countUnvalidatedMutations(all)
	if count != 1 {
		t.Errorf("failed edit shouldn't count; expected 1 (just b.go), got %d", count)
	}
}

func TestCountUnvalidatedMutations_NonValidationCmdDoesNotReset(t *testing.T) {
	all := []TraceEntry{
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "a.go"}, Ok: true, Step: 1},
		{Tool: "tool_call", Inner: "run_command", Args: map[string]any{"command": "git status"}, Ok: true, Step: 2},
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "b.go"}, Ok: true, Step: 3},
	}
	count, _ := countUnvalidatedMutations(all)
	if count != 2 {
		t.Errorf("non-validation command should NOT reset; expected 2, got %d", count)
	}
}

func TestTrajectoryHints_MutationEscalates(t *testing.T) {
	// Three+ unvalidated edits across the running history and a fresh
	// edit this round → directive "STOP editing, run a test" hint.
	all := []TraceEntry{
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "a.go"}, Ok: true, Step: 1},
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "b.go"}, Ok: true, Step: 2},
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "c.go"}, Ok: true, Step: 3},
	}
	fresh := all[2:]
	out := TrajectoryHints(fresh, all, nil)
	if out == nil || len(out.Hints) == 0 {
		t.Fatalf("expected at least one hint")
	}
	first := out.Hints[0]
	if !strings.Contains(first, "3 files") {
		t.Errorf("escalated hint should cite count, got %q", first)
	}
	if !strings.Contains(first, "STOP editing") {
		t.Errorf("escalated hint should be directive (STOP editing), got %q", first)
	}
	if !strings.Contains(first, "a.go") {
		t.Errorf("escalated hint should preview paths, got %q", first)
	}
}

func TestTrajectoryHints_UnverifiedFieldsExposed(t *testing.T) {
	// Even when the count is low (gentle wording), the structured
	// UnverifiedCount/Paths must still be set so engine telemetry sees
	// the running streak, not just the escalation point.
	all := []TraceEntry{
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "a.go"}, Ok: true, Step: 1},
	}
	out := TrajectoryHints(all, all, nil)
	if out == nil {
		t.Fatal("expected output")
	}
	if out.UnverifiedCount != 1 {
		t.Errorf("UnverifiedCount = %d, want 1", out.UnverifiedCount)
	}
	if len(out.UnverifiedPaths) != 1 || out.UnverifiedPaths[0] != "a.go" {
		t.Errorf("UnverifiedPaths = %v, want [a.go]", out.UnverifiedPaths)
	}
	if out.UnverifiedEscalated {
		t.Errorf("count=1 must NOT mark UnverifiedEscalated")
	}
}

func TestTrajectoryHints_UnverifiedEscalatedFlag(t *testing.T) {
	// Count >= 3 with a fresh edit → directive form fires AND
	// UnverifiedEscalated flips so the engine can publish a structured
	// agent:coach:unverified event in the same round.
	all := []TraceEntry{
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "a.go"}, Ok: true, Step: 1},
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "b.go"}, Ok: true, Step: 2},
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "c.go"}, Ok: true, Step: 3},
	}
	fresh := all[2:]
	out := TrajectoryHints(fresh, all, nil)
	if out == nil {
		t.Fatal("expected output")
	}
	if !out.UnverifiedEscalated {
		t.Errorf("count=3 with fresh edit must flip UnverifiedEscalated")
	}
	if out.UnverifiedCount != 3 {
		t.Errorf("UnverifiedCount = %d, want 3", out.UnverifiedCount)
	}
	if len(out.UnverifiedPaths) != 3 {
		t.Errorf("UnverifiedPaths len = %d, want 3", len(out.UnverifiedPaths))
	}
}

func TestTrajectoryHints_UnverifiedNotEscalatedWithoutFreshEdit(t *testing.T) {
	// Count is high but the current round had no mutation — the
	// directive hint must NOT fire (it's tied to a fresh edit), but
	// the count should still appear on the structured fields so the
	// TUI badge stays accurate.
	all := []TraceEntry{
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "a.go"}, Ok: true, Step: 1},
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "b.go"}, Ok: true, Step: 2},
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "c.go"}, Ok: true, Step: 3},
		{Tool: "tool_call", Inner: "read_file", Args: map[string]any{"path": "d.go"}, Ok: true, Step: 4},
	}
	fresh := all[3:] // just a read this round
	out := TrajectoryHints(fresh, all, nil)
	if out == nil {
		t.Fatal("expected output")
	}
	if out.UnverifiedEscalated {
		t.Errorf("no fresh edit → must NOT escalate, got UnverifiedEscalated=true")
	}
	if out.UnverifiedCount != 3 {
		t.Errorf("UnverifiedCount = %d, want 3", out.UnverifiedCount)
	}
}

func TestTrajectoryHints_MutationGentleAtCountOne(t *testing.T) {
	// Exactly one unvalidated edit → the gentle "validate this" wording,
	// NOT the directive "STOP editing" form.
	all := []TraceEntry{
		{Tool: "tool_call", Inner: "edit_file", Args: map[string]any{"path": "auth/token.go"}, Ok: true, Step: 1},
	}
	out := TrajectoryHints(all, all, nil)
	if out == nil || len(out.Hints) == 0 {
		t.Fatalf("expected at least one hint")
	}
	first := out.Hints[0]
	if strings.Contains(first, "STOP editing") {
		t.Errorf("count=1 should use gentle wording, got %q", first)
	}
	if !strings.Contains(first, "auth/token.go") {
		t.Errorf("gentle hint should still cite the path, got %q", first)
	}
}

func TestErrorFingerprint_NormalizesClass(t *testing.T) {
	cases := map[string]string{
		"":                                      "",
		"  File not Found":                      "file not found",
		"file   not\tfound":                     "file not found",
		"FILE NOT FOUND: /tmp/zzz/aaa/path.go":  "file not found: /tmp/zzz/aaa/p",
		"permission denied: /var/run/something": "permission denied: /var/run/so",
	}
	for in, want := range cases {
		if got := errorFingerprint(in); got != want {
			t.Errorf("errorFingerprint(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTrajectoryHints_RepeatedCallsFlagged(t *testing.T) {
	all := []TraceEntry{
		{Tool: "tool_call", Inner: "read_file", Args: map[string]any{"path": "auth.go"}, Ok: true, Step: 1},
		{Tool: "tool_call", Inner: "read_file", Args: map[string]any{"path": "auth.go"}, Ok: true, Step: 2},
		{Tool: "tool_call", Inner: "read_file", Args: map[string]any{"path": "auth.go"}, Ok: true, Step: 3},
	}
	fresh := all[2:]
	out := TrajectoryHints(fresh, all, nil)
	// Should see a consolidation hint somewhere in the returned list.
	found := false
	for _, h := range out.Hints {
		if strings.Contains(h, "read_file") && strings.Contains(strings.ToLower(h), "consolidate") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected consolidate hint for repeated read_file calls, got %v", out.Hints)
	}
}

func TestTrajectoryHints_PreferDedicatedTool(t *testing.T) {
	fresh := []TraceEntry{
		{Tool: "tool_call", Inner: "run_command", Args: map[string]any{"command": "grep -rn 'TODO' ."}, Ok: true, Step: 1},
	}
	out := TrajectoryHints(fresh, fresh, nil)
	if out == nil || len(out.Hints) == 0 {
		t.Fatalf("expected a hint steering grep → grep_codebase")
	}
	found := false
	for _, h := range out.Hints {
		if strings.Contains(h, "grep_codebase") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected grep_codebase steering, got %v", out.Hints)
	}
}

func TestTrajectoryHints_DedupsAgainstRecent(t *testing.T) {
	fresh := []TraceEntry{
		{Tool: "tool_call", Inner: "write_file", Args: map[string]any{"path": "x.go"}, Ok: true, Step: 1},
	}
	firstRun := TrajectoryHints(fresh, fresh, nil)
	if firstRun == nil || len(firstRun.Hints) == 0 {
		t.Fatalf("expected first-turn mutation hint")
	}
	secondRun := TrajectoryHints(fresh, fresh, firstRun.Hints)
	for _, h := range secondRun.Hints {
		for _, f := range firstRun.Hints {
			if h == f {
				t.Fatalf("dedup failed: hint %q reappeared", h)
			}
		}
	}
}

func TestTrajectoryHints_NoHintsForIdleTurn(t *testing.T) {
	out := TrajectoryHints(nil, nil, nil)
	if out != nil && len(out.Hints) != 0 {
		t.Fatalf("idle turn should yield no hints, got %v", out.Hints)
	}
}

func TestFormatTrajectoryHints_EmitsCoachBlock(t *testing.T) {
	out := FormatTrajectoryHints(&TrajectoryOutput{Hints: []string{"hint A", "hint B"}})
	if !strings.HasPrefix(out, "[trajectory coach]") {
		t.Fatalf("output must start with coach tag, got %q", out)
	}
	if !strings.Contains(out, "• hint A") || !strings.Contains(out, "• hint B") {
		t.Fatalf("output must contain bulleted hints, got %q", out)
	}
}

func TestFormatTrajectoryHints_EmptyReturnsEmpty(t *testing.T) {
	if got := FormatTrajectoryHints(nil); got != "" {
		t.Fatalf("empty hints must format to empty string, got %q", got)
	}
}
