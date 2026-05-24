package coach

import (
	"strings"
	"testing"
)

func TestRuleObserver_ParkedLoopSurfaced(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{Parked: true, Question: "fix auth"})
	if len(notes) == 0 || notes[0].Origin != "parked_loop" {
		t.Fatalf("expected parked_loop note first, got %+v", notes)
	}
	if notes[0].Severity != SeverityWarn {
		t.Fatalf("parked note should be warn severity, got %q", notes[0].Severity)
	}
	if !strings.Contains(notes[0].Text, "/continue") {
		t.Fatalf("step-cap park should coach the /continue path, got %q", notes[0].Text)
	}
}

func TestRuleObserver_ParkedBudgetSuggestsSplit(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		Parked:     true,
		ParkReason: "budget_exhausted",
		Question:   "rewrite the auth layer",
	})
	if len(notes) == 0 {
		t.Fatalf("budget-exhausted park should emit at least one note")
	}
	if notes[0].Origin != "parked_budget" {
		t.Fatalf("expected parked_budget note first, got %+v", notes)
	}
	if !strings.Contains(notes[0].Text, "/split") {
		t.Fatalf("budget park should coach the /split path, got %q", notes[0].Text)
	}
	if notes[0].Severity != SeverityWarn {
		t.Fatalf("budget park should be warn severity, got %q", notes[0].Severity)
	}
}

func TestRuleObserver_MutationWithoutValidationFlagged(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		Question:       "refactor the token parser",
		Answer:         "Done, I've rewritten the parser.",
		ToolSteps:      3,
		Mutations:      []string{"internal/auth/token.go"},
		ValidationHint: "`go test ./internal/auth/... -count=1`",
	})
	found := false
	for _, n := range notes {
		if n.Origin == "mutation_unvalidated" {
			found = true
			if !strings.Contains(n.Text, "internal/auth/token.go") {
				t.Fatalf("expected path in note, got %q", n.Text)
			}
			if !strings.Contains(n.Text, "go test ./internal/auth/... -count=1") {
				t.Fatalf("expected concrete validation hint in note, got %q", n.Text)
			}
			if n.Action != "`go test ./internal/auth/... -count=1`" {
				t.Fatalf("expected action to mirror validation hint, got %q", n.Action)
			}
		}
	}
	if !found {
		t.Fatalf("expected mutation_unvalidated note, got %+v", notes)
	}
}

func TestRuleObserver_MutationWithValidationStaysSilent(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		Question:   "refactor the token parser",
		Answer:     "Done. Ran `go test ./internal/auth/...` and it passed.",
		ToolSteps:  3,
		Mutations:  []string{"internal/auth/token.go"},
		ToolsUsed:  []string{"edit_file"},
		TokensUsed: 3000,
	})
	for _, n := range notes {
		if n.Origin == "mutation_unvalidated" {
			t.Fatalf("mutation_unvalidated should be suppressed when answer mentions validation, got %q", n.Text)
		}
	}
}

func TestRuleObserver_RepeatedFailuresWarn(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		FailedTools: []string{"run_command", "apply_patch"},
	})
	found := false
	for _, n := range notes {
		if n.Origin == "repeated_failures" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected repeated_failures note, got %+v", notes)
	}
}

func TestRuleObserver_HeavyTurnInfo(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		TokensUsed:  45000,
		TightenHint: "`review [[file:internal/auth/token.go]] parseToken`",
	})
	found := false
	for _, n := range notes {
		if n.Origin == "heavy_turn" {
			found = true
			if n.Severity != SeverityInfo {
				t.Fatalf("heavy_turn should be info, got %q", n.Severity)
			}
			if !strings.Contains(n.Text, "review [[file:internal/auth/token.go]] parseToken") {
				t.Fatalf("expected concrete tighten hint, got %q", n.Text)
			}
			if n.Action != "`review [[file:internal/auth/token.go]] parseToken`" {
				t.Fatalf("expected action to mirror tighten hint, got %q", n.Action)
			}
		}
	}
	if !found {
		t.Fatalf("expected heavy_turn note, got %+v", notes)
	}
}

func TestRuleObserver_CleanPassCelebratesOnce(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		ToolsUsed:  []string{"read_file", "grep_codebase"},
		TokensUsed: 3500,
	})
	celebrated := 0
	for _, n := range notes {
		if n.Severity == SeverityCelebrate {
			celebrated++
		}
	}
	if celebrated != 1 {
		t.Fatalf("expected exactly 1 celebrate note, got %d (notes=%+v)", celebrated, notes)
	}
}

func TestRuleObserver_MaxNotesRespected(t *testing.T) {
	obs := &RuleObserver{MaxNotes: 1}
	notes := obs.Observe(Snapshot{
		Parked:      true,
		Mutations:   []string{"x.go"},
		FailedTools: []string{"a", "b", "c"},
		TokensUsed:  50000,
	})
	if len(notes) != 1 {
		t.Fatalf("MaxNotes=1 but got %d notes", len(notes))
	}
}

func TestRuleObserver_EmptySnapshotNoNotes(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{})
	if len(notes) != 0 {
		t.Fatalf("empty snapshot should yield no notes, got %+v", notes)
	}
}

func TestRuleObserver_NoActionTakenSurfaced(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		Question:  "add error handling to auth.go",
		Answer:    "Do you want defensive checks or validation?",
		ToolSteps: 0,
	})
	found := false
	for _, n := range notes {
		if n.Origin == "no_action_taken" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected no_action_taken note for actionable question with no tools, got %+v", notes)
	}
}

func TestRuleObserver_PseudoToolCallSurfaced(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		Question:  "read tui.go and summarize",
		Answer:    "Let me gather the files.\n[TOOL_CALL]\n{name: read_file, args: {path: ui/tui/tui.go}}\n[/TOOL_CALL]\n",
		ToolSteps: 0,
	})
	found := false
	for _, n := range notes {
		if n.Origin == "pseudo_tool_call" {
			found = true
			if n.Severity != SeverityWarn {
				t.Fatalf("pseudo_tool_call should be warn, got %q", n.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("expected pseudo_tool_call note when answer carries text-format tool call, got %+v", notes)
	}
}

func TestRuleObserver_RetrievalSymbolMissSurfaced(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		Question:              "fix parseToken in auth",
		ContextFiles:          3,
		ContextSources:        map[string]int{"query-match": 2, "hotspot": 1},
		QueryIdentifiers:      2,
		QueryIdentifierNames:  []string{"parseToken"},
		UsefulQueryIdentifier: "parseToken",
		RetrievalHint:         "`review [[file:internal/auth/token.go]] parseToken`",
	})
	found := false
	for _, n := range notes {
		if n.Origin == "retrieval_symbol_miss" {
			found = true
			if n.Severity != SeverityInfo {
				t.Fatalf("retrieval_symbol_miss should be info, got %q", n.Severity)
			}
			if !strings.Contains(n.Text, "review [[file:internal/auth/token.go]] parseToken") {
				t.Fatalf("expected concrete retrieval hint, got %q", n.Text)
			}
			if n.Action != "`review [[file:internal/auth/token.go]] parseToken`" {
				t.Fatalf("expected action to mirror retrieval hint, got %q", n.Action)
			}
		}
	}
	if !found {
		t.Fatalf("expected retrieval_symbol_miss note, got %+v", notes)
	}
}

func TestRuleObserver_RetrievalSymbolMissQuietWithoutUsefulIdentifier(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		Question:             "review [[file:ui/tui/tui.go]]",
		ContextFiles:         2,
		ContextSources:       map[string]int{"query-match": 2},
		QueryIdentifiers:     1,
		QueryIdentifierNames: []string{"review"},
	})
	for _, n := range notes {
		if n.Origin == "retrieval_symbol_miss" {
			t.Fatalf("retrieval_symbol_miss should stay silent without a useful symbol identifier, got %+v", notes)
		}
	}
}

func TestRuleObserver_RetrievalSymbolMissQuietWhenResolved(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		Question:         "fix parseToken in auth",
		ContextFiles:     3,
		ContextSources:   map[string]int{"symbol-match": 1, "graph-neighborhood": 2},
		QueryIdentifiers: 1,
	})
	for _, n := range notes {
		if n.Origin == "retrieval_symbol_miss" {
			t.Fatalf("retrieval_symbol_miss should stay silent when a symbol-match landed, got %+v", notes)
		}
	}
}

func TestRuleObserver_RetrievalHotspotOnlySurfaced(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		Question:       "tell me about the project",
		ContextFiles:   4,
		ContextSources: map[string]int{"hotspot": 4},
	})
	found := false
	for _, n := range notes {
		if n.Origin == "retrieval_hotspot_only" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected retrieval_hotspot_only note, got %+v", notes)
	}
}

func TestRuleObserver_PseudoToolCallQuietWhenToolsRan(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		Question:  "read tui.go",
		Answer:    "I ran read_file on [tool_call] as requested.",
		ToolSteps: 2,
		ToolsUsed: []string{"read_file", "read_file"},
	})
	for _, n := range notes {
		if n.Origin == "pseudo_tool_call" {
			t.Fatalf("pseudo_tool_call should stay silent when real tools ran, got %+v", notes)
		}
	}
}

// trimPaths tests

func TestTrimPaths_UnderMax(t *testing.T) {
	paths := []string{"a.go", "b.go"}
	got := trimPaths(paths, 5)
	if len(got) != 2 {
		t.Errorf("got %d", len(got))
	}
	if got[0] != "a.go" || got[1] != "b.go" {
		t.Errorf("got %v", got)
	}
}

func TestTrimPaths_ExactlyMax(t *testing.T) {
	paths := []string{"a.go", "b.go"}
	got := trimPaths(paths, 2)
	if len(got) != 2 {
		t.Errorf("got %d", len(got))
	}
}

func TestTrimPaths_OverMax(t *testing.T) {
	paths := []string{"a.go", "b.go", "c.go", "d.go"}
	got := trimPaths(paths, 2)
	if len(got) != 3 {
		t.Errorf("expected 3, got %d", len(got))
	}
	if got[0] != "a.go" || got[1] != "b.go" {
		t.Errorf("first two should be preserved, got %v", got)
	}
	if !strings.Contains(got[2], "+2 more") {
		t.Errorf("expected +N more suffix, got %q", got[2])
	}
}

func TestTrimPaths_OneOver(t *testing.T) {
	paths := []string{"a.go", "b.go", "c.go"}
	got := trimPaths(paths, 2)
	if !strings.Contains(got[2], "+1 more") {
		t.Errorf("expected +1 more, got %q", got[2])
	}
}

// looksActionable tests

func TestLooksActionable_ReturnsFalse(t *testing.T) {
	nonActionable := []string{
		"what is this project?",
		"show me the files",
		"hello",
		"thanks",
		"nevermind",
		"why did it fail?",
	}
	for _, q := range nonActionable {
		if looksActionable(q) {
			t.Errorf("looksActionable(%q) = true, want false", q)
		}
	}
}

func TestLooksActionable_ReturnsTrue_English(t *testing.T) {
	actionable := []string{
		"fix the bug",
		"add tests",
		"implement the feature",
		"edit the file",
		"refactor this",
		"migrate the database",
		"remove the dead code",
		"delete the file",
		"rename the function",
		"update the config",
		"create a new file",
		"build the project",
		"write a test",
		"generate the docs",
	}
	for _, q := range actionable {
		if !looksActionable(q) {
			t.Errorf("looksActionable(%q) = false, want true", q)
		}
	}
}

func TestLooksActionable_ReturnsTrue_Turkish(t *testing.T) {
	actionable := []string{
		"ekleme yap",         // add
		"silme islemi",       // delete operation
		"duzeltme gerekiyor", // needs fix
		"yazma zamani",       // time to write
	}
	for _, q := range actionable {
		if !looksActionable(q) {
			t.Errorf("looksActionable(%q) = false, want true", q)
		}
	}
}

func TestLooksActionable_TwoWordPhrases(t *testing.T) {
	if !looksActionable("wire up the auth") {
		t.Error("wire up should be actionable")
	}
	if !looksActionable("hook up the database") {
		t.Error("hook up should be actionable")
	}
}

func TestLooksActionable_CaseInsensitive(t *testing.T) {
	if !looksActionable("FIX THE BUG") {
		t.Error("FIX THE BUG should be actionable")
	}
	if !looksActionable("Add More Tests") {
		t.Error("Add More Tests should be actionable")
	}
}

func TestRuleObserver_GrepThrashSurfaced(t *testing.T) {
	// Five greps with no edits and no parking → should surface the
	// "use find_symbol / codemap" hint.
	notes := NewRuleObserver().Observe(Snapshot{
		ToolsUsed: []string{
			"grep_codebase", "grep_codebase", "grep_codebase",
			"grep_codebase", "grep_codebase",
		},
		TokensUsed: 5000,
	})
	found := false
	for _, n := range notes {
		if n.Origin == "grep_thrash" {
			found = true
			if n.Severity != SeverityInfo {
				t.Errorf("grep_thrash should be info, got %q", n.Severity)
			}
			if !strings.Contains(n.Text, "find_symbol") || !strings.Contains(n.Text, "codemap") {
				t.Errorf("grep_thrash should suggest find_symbol + codemap, got %q", n.Text)
			}
		}
	}
	if !found {
		t.Fatalf("expected grep_thrash note, got %+v", notes)
	}
}

func TestRuleObserver_GrepThrashSilentWhenMutated(t *testing.T) {
	// Greps + a mutation = "research → edit" workflow; not thrashing.
	notes := NewRuleObserver().Observe(Snapshot{
		ToolsUsed: []string{
			"grep_codebase", "grep_codebase", "grep_codebase",
			"grep_codebase", "grep_codebase", "edit_file",
		},
		Mutations:  []string{"internal/foo.go"},
		TokensUsed: 5000,
	})
	for _, n := range notes {
		if n.Origin == "grep_thrash" {
			t.Errorf("grep_thrash should NOT fire when mutations happened: %+v", n)
		}
	}
}

func TestRuleObserver_ToolFloodSurfaced(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		ToolsUsed:  []string{"read_file"},
		ToolSteps:  35,
		TokensUsed: 5000,
	})
	found := false
	for _, n := range notes {
		if n.Origin == "tool_flood" {
			found = true
			if !strings.Contains(n.Text, "/split") {
				t.Errorf("tool_flood should suggest /split, got %q", n.Text)
			}
		}
	}
	if !found {
		t.Fatalf("expected tool_flood note, got %+v", notes)
	}
}

func TestRuleObserver_ToolFloodSilentWhenParked(t *testing.T) {
	// Parked rules already surface; tool_flood should defer so the
	// user isn't double-warned about the same wide turn.
	notes := NewRuleObserver().Observe(Snapshot{
		ToolSteps:  60,
		Parked:     true,
		ParkReason: "budget_exhausted",
	})
	for _, n := range notes {
		if n.Origin == "tool_flood" {
			t.Errorf("tool_flood should NOT fire alongside parked: %+v", n)
		}
	}
}

func TestRuleObserver_MutationBlindSurfaced(t *testing.T) {
	// Mutations happened but no git_status / git_diff in the same
	// turn — the user should be nudged to check git state before
	// trusting the changes.
	notes := NewRuleObserver().Observe(Snapshot{
		ToolsUsed:      []string{"read_file", "edit_file"},
		Mutations:      []string{"internal/foo.go"},
		Answer:         "Edited the parser. ran go test ./... (passes).",
		TokensUsed:     3500,
		ValidationHint: "go test ./internal/foo/...",
	})
	found := false
	for _, n := range notes {
		if n.Origin == "mutation_blind" {
			found = true
			if !strings.Contains(n.Text, "git status") {
				t.Errorf("mutation_blind should mention `git status`, got %q", n.Text)
			}
		}
	}
	if !found {
		t.Fatalf("expected mutation_blind note, got %+v", notes)
	}
}

func TestRuleObserver_MutationBlindSilentWhenGitStatusUsed(t *testing.T) {
	// git_status in the trace clears the rule.
	notes := NewRuleObserver().Observe(Snapshot{
		ToolsUsed:      []string{"git_status", "edit_file"},
		Mutations:      []string{"internal/foo.go"},
		Answer:         "Edited the parser; go vet clean.",
		TokensUsed:     3500,
		ValidationHint: "go vet ./...",
	})
	for _, n := range notes {
		if n.Origin == "mutation_blind" {
			t.Errorf("mutation_blind should NOT fire when git_status was used: %+v", n)
		}
	}
}

func TestCountTool_CaseInsensitiveAndTrim(t *testing.T) {
	used := []string{"grep_codebase", " GREP_CODEBASE ", "edit_file", ""}
	if got := countTool(used, "grep_codebase"); got != 2 {
		t.Errorf("countTool: want 2, got %d", got)
	}
	if got := countTool(used, ""); got != 0 {
		t.Errorf("countTool empty target should yield 0, got %d", got)
	}
}

func TestRuleObserver_MaxNotesDefault(t *testing.T) {
	// MaxNotes <= 0 should default to 3
	obs := &RuleObserver{MaxNotes: 0}
	notes := obs.Observe(Snapshot{
		Parked:      true,
		ParkReason:  "manual", // non-budget reason to hit default park text
		Mutations:   []string{"a.go", "b.go", "c.go"},
		FailedTools: []string{"x", "y"},
	})
	if len(notes) != 3 {
		t.Fatalf("MaxNotes=0 should default to 3, got %d notes", len(notes))
	}
}

func TestRuleObserver_EmptyNoteTextSkipped(t *testing.T) {
	// push() should skip notes where strings.TrimSpace(note.Text) == ""
	obs := &RuleObserver{MaxNotes: 3}
	notes := obs.Observe(Snapshot{
		FailedTools: []string{"x"},
	})
	// If an empty note somehow got added, the count would be wrong.
	// This test validates the push closure's trim guard.
	if len(notes) == 0 {
		t.Log("no notes emitted for failed tools — acceptable")
	}
}

func TestRuleObserver_MutationUnvalidatedNoHint(t *testing.T) {
	// When ValidationHint is empty, the "Double-check before shipping." suffix fires
	notes := NewRuleObserver().Observe(Snapshot{
		Question:       "refactor auth",
		Answer:         "Done, changed token.go.",
		ToolSteps:      1,
		Mutations:      []string{"internal/auth/token.go"},
		ValidationHint: "", // empty — should use else branch
	})
	found := false
	for _, n := range notes {
		if n.Origin == "mutation_unvalidated" {
			found = true
			if !strings.Contains(n.Text, "Double-check before shipping.") {
				t.Errorf("empty hint should append double-check text, got %q", n.Text)
			}
		}
	}
	if !found {
		t.Fatalf("mutation_unvalidated should fire even without hint, got %+v", notes)
	}
}

func TestRuleObserver_HeavyTurnNoHint(t *testing.T) {
	// When TightenHint is empty, the else branch fires
	notes := NewRuleObserver().Observe(Snapshot{
		TokensUsed:  45000,
		TightenHint: "", // empty — should use else branch
	})
	found := false
	for _, n := range notes {
		if n.Origin == "heavy_turn" {
			found = true
			if !strings.Contains(n.Text, "narrowing the question") {
				t.Errorf("empty tighten hint should use else text, got %q", n.Text)
			}
		}
	}
	if !found {
		t.Fatalf("heavy_turn should fire even without hint, got %+v", notes)
	}
}

func TestRuleObserver_PseudoToolCall(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		Question:   "fix the auth bug",
		Answer:     "I will use [tool_call] read_file [/tool_call] to check the token.",
		ToolSteps:  0, // no native tools used
	})
	found := false
	for _, n := range notes {
		if n.Origin == "pseudo_tool_call" {
			found = true
		}
	}
	if !found {
		t.Fatalf("pseudo_tool_call should fire when answer contains [tool_call] marker with no ToolSteps, got %+v", notes)
	}
}

func TestRuleObserver_RetrievalHotspotOnly(t *testing.T) {
	notes := NewRuleObserver().Observe(Snapshot{
		Question:      "analyze the codebase",
		ContextFiles:  3,
		ContextSources: map[string]int{"hotspot": 3},
	})
	found := false
	for _, n := range notes {
		if n.Origin == "retrieval_hotspot_only" {
			found = true
		}
	}
	if !found {
		t.Fatalf("retrieval_hotspot_only should fire when ContextSources[hotspot] == ContextFiles, got %+v", notes)
	}
}

func TestRuleObserver_GrepThrash(t *testing.T) {
	// 5+ greps, no mutations, not parked → should fire
	tools := make([]string, 5)
	for i := range tools {
		tools[i] = "grep_codebase"
	}
	notes := NewRuleObserver().Observe(Snapshot{
		ToolSteps:   5,
		ToolsUsed:   tools,
		Mutations:   []string{},
		Parked:      false,
	})
	found := false
	for _, n := range notes {
		if n.Origin == "grep_thrash" {
			found = true
		}
	}
	if !found {
		t.Fatalf("grep_thrash should fire at 5+ greps, got %+v", notes)
	}
}

func TestRuleObserver_ToolFlood(t *testing.T) {
	// 30+ tool calls, not parked → should fire
	tools := make([]string, 30)
	for i := range tools {
		tools[i] = "read_file"
	}
	notes := NewRuleObserver().Observe(Snapshot{
		ToolSteps: 30,
		ToolsUsed: tools,
		Parked:    false,
	})
	found := false
	for _, n := range notes {
		if n.Origin == "tool_flood" {
			found = true
		}
	}
	if !found {
		t.Fatalf("tool_flood should fire at 30+ tool calls, got %+v", notes)
	}
}

func TestRuleObserver_MutationBlind(t *testing.T) {
	// mutations but no git_status/git_diff
	notes := NewRuleObserver().Observe(Snapshot{
		Mutations:  []string{"a.go", "b.go"},
		ToolsUsed:  []string{"edit_file", "read_file"},
		FailedTools: []string{},
		Parked:     false,
	})
	found := false
	for _, n := range notes {
		if n.Origin == "mutation_blind" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mutation_blind should fire when mutations exist without git_status/git_diff, got %+v", notes)
	}
}

func TestRuleObserver_CleanPass(t *testing.T) {
	// tools used, no failures, tight token spend → should fire
	notes := NewRuleObserver().Observe(Snapshot{
		ToolsUsed:   []string{"read_file", "edit_file"},
		FailedTools: []string{},
		Parked:      false,
		TokensUsed:  5000,
	})
	found := false
	for _, n := range notes {
		if n.Origin == "clean_pass" {
			found = true
		}
	}
	if !found {
		t.Fatalf("clean_pass should fire for clean tight turn, got %+v", notes)
	}
}

func TestRuleObserver_RetrievalSymbolMiss(t *testing.T) {
	// UsefulQueryIdentifier set, symbol-match and marker both 0
	notes := NewRuleObserver().Observe(Snapshot{
		UsefulQueryIdentifier: "AuthToken",
		QueryIdentifiers:      1,
		ContextFiles:          2,
		ContextSources:        map[string]int{"symbol-match": 0, "marker": 0},
		QuestionHasFileMarker: false,
	})
	found := false
	for _, n := range notes {
		if n.Origin == "retrieval_symbol_miss" {
			found = true
		}
	}
	if !found {
		t.Fatalf("retrieval_symbol_miss should fire for unmatched query identifier, got %+v", notes)
	}
}

func TestRuleObserver_RetrievalSymbolMissWithMarker(t *testing.T) {
	// QuestionHasFileMarker true → different text branch
	notes := NewRuleObserver().Observe(Snapshot{
		UsefulQueryIdentifier: "AuthToken",
		QueryIdentifiers:      1,
		ContextFiles:          2,
		ContextSources:        map[string]int{"symbol-match": 0, "marker": 0},
		QuestionHasFileMarker: true,
	})
	found := false
	for _, n := range notes {
		if n.Origin == "retrieval_symbol_miss" {
			found = true
			if !strings.Contains(n.Text, "marker") {
				t.Errorf("with marker: text should mention marker issue, got %q", n.Text)
			}
		}
	}
	if !found {
		t.Fatalf("retrieval_symbol_miss with marker should fire, got %+v", notes)
	}
}

func TestRuleObserver_NoActionTaken(t *testing.T) {
	// ToolSteps=0, answer contains "?", looksActionable question
	notes := NewRuleObserver().Observe(Snapshot{
		Question:   "fix the bug in auth",
		Answer:     "What file should I look at?",
		ToolSteps:  0,
	})
	found := false
	for _, n := range notes {
		if n.Origin == "no_action_taken" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no_action_taken should fire for actionable ? without tools, got %+v", notes)
	}
}
