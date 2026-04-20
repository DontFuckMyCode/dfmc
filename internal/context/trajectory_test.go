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
