package engine

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func newJournalEngine(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &Engine{Storage: store}
}

func TestSubagentJournal_RoundTripAndCap(t *testing.T) {
	eng := newJournalEngine(t)

	// First write — appears verbatim on read.
	eng.appendSubagentJournal("Researcher", subagentJournalEntry{
		Task:    "survey provider router",
		Summary: "found three protocols: anthropic, openai, openai-compatible",
	})
	got := eng.loadSubagentJournal("researcher") // case-insensitive lookup
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if !strings.Contains(got[0].Summary, "three protocols") {
		t.Errorf("summary not preserved: %q", got[0].Summary)
	}
	if got[0].Timestamp.IsZero() {
		t.Errorf("timestamp should be auto-stamped")
	}

	// Cap enforcement — write subagentJournalCap+3 entries, expect newest cap kept.
	for i := range subagentJournalCap + 3 {
		eng.appendSubagentJournal("researcher", subagentJournalEntry{
			Summary: "entry-" + itoaInt(i),
		})
	}
	got = eng.loadSubagentJournal("researcher")
	if len(got) != subagentJournalCap {
		t.Fatalf("want cap=%d entries, got %d", subagentJournalCap, len(got))
	}
	// Oldest survivor must be entry-3 (we wrote 0..7 plus the original;
	// list trims to the most recent 5 = entries 3..7).
	if !strings.Contains(got[0].Summary, "entry-3") {
		t.Errorf("oldest survivor wrong: %q", got[0].Summary)
	}
	if !strings.Contains(got[len(got)-1].Summary, "entry-7") {
		t.Errorf("newest entry wrong: %q", got[len(got)-1].Summary)
	}
}

func TestSubagentJournal_EmptyRoleSkips(t *testing.T) {
	eng := newJournalEngine(t)
	// Empty role is the "no useful key" case — must not write.
	eng.appendSubagentJournal("", subagentJournalEntry{Summary: "should not persist"})
	eng.appendSubagentJournal("   ", subagentJournalEntry{Summary: "should not persist"})
	if got := eng.loadSubagentJournal(""); got != nil {
		t.Errorf("empty role load should return nil, got %v", got)
	}
}

func TestSubagentJournal_EmptySummarySkips(t *testing.T) {
	eng := newJournalEngine(t)
	eng.appendSubagentJournal("reviewer", subagentJournalEntry{Summary: ""})
	eng.appendSubagentJournal("reviewer", subagentJournalEntry{Summary: "   "})
	if got := eng.loadSubagentJournal("reviewer"); len(got) != 0 {
		t.Errorf("empty-summary writes should not persist, got %d entries", len(got))
	}
}

func TestSubagentJournal_TruncatesLongSummary(t *testing.T) {
	eng := newJournalEngine(t)
	long := strings.Repeat("x", subagentJournalSummaryMax+500)
	eng.appendSubagentJournal("coder", subagentJournalEntry{Summary: long})
	got := eng.loadSubagentJournal("coder")
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	if len(got[0].Summary) > subagentJournalSummaryMax+3 { // +3 for "..."
		t.Errorf("summary not truncated: len=%d", len(got[0].Summary))
	}
	if !strings.HasSuffix(got[0].Summary, "...") {
		t.Errorf("expected ellipsis suffix on truncated summary")
	}
}

func TestSubagentJournal_NilSafe(t *testing.T) {
	var e *Engine
	e.appendSubagentJournal("x", subagentJournalEntry{Summary: "y"}) // must not panic
	if got := e.loadSubagentJournal("x"); got != nil {
		t.Errorf("nil engine load should return nil, got %v", got)
	}
	// Engine without Storage must also be safe.
	bare := &Engine{}
	bare.appendSubagentJournal("x", subagentJournalEntry{Summary: "y"})
	if got := bare.loadSubagentJournal("x"); got != nil {
		t.Errorf("storage-less engine load should return nil, got %v", got)
	}
}

func TestFormatSubagentJournalSection_RendersEntries(t *testing.T) {
	if got := formatSubagentJournalSection(nil); got != "" {
		t.Errorf("nil entries should render empty, got %q", got)
	}
	out := formatSubagentJournalSection([]subagentJournalEntry{
		{
			Timestamp: time.Date(2026, 5, 8, 12, 30, 0, 0, time.UTC),
			Task:      "first task",
			Summary:   "first finding",
		},
		{
			Timestamp: time.Date(2026, 5, 8, 13, 0, 0, 0, time.UTC),
			Task:      "second task",
			Summary:   "second finding",
			Parked:    true,
		},
	})
	for _, want := range []string{
		"Prior delegations to this role",
		"#1",
		"#2",
		"first finding",
		"second finding",
		"first task",
		"second task",
		"[parked]",
		"2026-05-08 12:30 UTC",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered section missing %q\n--- output ---\n%s", want, out)
		}
	}
}

func TestBuildSubagentPrompt_IncludesJournalSection(t *testing.T) {
	prompt := buildSubagentPrompt(
		tools.SubagentRequest{Task: "do the thing", Role: "researcher"},
		nil,
		subagentPromptEnvironment{
			ProjectRoot:    "/tmp/proj",
			JournalSection: "Prior delegations to this role (oldest first):\n- #1 task: foo\n  → bar\n\n",
		},
	)
	if !strings.Contains(prompt, "Prior delegations to this role") {
		t.Errorf("prompt missing journal block:\n%s", prompt)
	}
	if !strings.Contains(prompt, "→ bar") {
		t.Errorf("prompt missing journal entry body:\n%s", prompt)
	}
}
