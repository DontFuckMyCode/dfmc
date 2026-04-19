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
