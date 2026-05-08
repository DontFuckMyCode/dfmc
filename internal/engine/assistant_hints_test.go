package engine

import (
	"strings"
	"testing"
)

func TestParseAssistantHints_BothBlocks(t *testing.T) {
	answer := strings.TrimSpace(`
Here is the fix.

[next:
- run go test ./...
- review the new helper for edge cases
- add a regression test]

[cleanup: u-3f29a1, a-7b40c2]
`)
	cleanup, next, stripped := parseAssistantHints(answer)
	if got := strings.Join(cleanup, ","); got != "u-3f29a1,a-7b40c2" {
		t.Errorf("cleanup IDs: got %q", got)
	}
	if len(next) != 3 {
		t.Fatalf("expected 3 next-action lines, got %d (%v)", len(next), next)
	}
	if next[0] != "run go test ./..." {
		t.Errorf("first next-action: got %q", next[0])
	}
	if strings.Contains(stripped, "[next:") || strings.Contains(stripped, "[cleanup:") {
		t.Errorf("stripped answer still contains marker block:\n%s", stripped)
	}
	if !strings.HasPrefix(stripped, "Here is the fix") {
		t.Errorf("stripped answer head: %q", stripped)
	}
}

func TestParseAssistantHints_OnlyCleanup_EmptyOK(t *testing.T) {
	cleanup, next, stripped := parseAssistantHints("done.\n[cleanup: ]")
	if len(cleanup) != 0 {
		t.Errorf("expected zero cleanup IDs, got %v", cleanup)
	}
	if len(next) != 0 {
		t.Errorf("expected zero next actions, got %v", next)
	}
	if strings.Contains(stripped, "[cleanup") {
		t.Errorf("marker leaked: %q", stripped)
	}
}

func TestParseAssistantHints_NoMarkers_PassThrough(t *testing.T) {
	cleanup, next, stripped := parseAssistantHints("just some text")
	if len(cleanup)+len(next) != 0 {
		t.Errorf("unexpected hints in plain text")
	}
	if stripped != "just some text" {
		t.Errorf("stripped text changed: %q", stripped)
	}
}

func TestParseAssistantHints_TolerantOfBulletStyles(t *testing.T) {
	answer := "[next:\n* alpha\n• beta\n1. gamma\n2) delta]"
	_, next, _ := parseAssistantHints(answer)
	want := []string{"alpha", "beta", "gamma", "delta"}
	if len(next) != len(want) {
		t.Fatalf("got %d entries, want %d (%v)", len(next), len(want), next)
	}
	for i, w := range want {
		if next[i] != w {
			t.Errorf("entry %d: got %q want %q", i, next[i], w)
		}
	}
}

func TestParseAssistantHints_DedupesIDs(t *testing.T) {
	cleanup, _, _ := parseAssistantHints("ok\n[cleanup: u-aaaaaa, a-bbbbbb, u-aaaaaa]")
	if len(cleanup) != 2 {
		t.Errorf("expected dedup → 2 unique IDs, got %v", cleanup)
	}
}

func TestParseAssistantHints_DoneMarker(t *testing.T) {
	cases := []struct {
		name      string
		answer    string
		wantSet   bool
		wantDone  bool
		wantStrip string
	}{
		{"done_true", "ok\n[done: true]", true, true, "ok"},
		{"done_yes", "ok\n[done: yes]", true, true, "ok"},
		{"done_completed", "ok\n[done: completed]", true, true, "ok"},
		{"done_false", "ok\n[done: false]", true, false, "ok"},
		{"done_no", "ok\n[done: no]", true, false, "ok"},
		{"done_absent", "ok", false, false, "ok"},
		{"done_with_other_markers", "ok\n[next:\n- a]\n[done: true]", true, true, "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := parseAssistantHintsFull(tc.answer)
			if h.DoneSet != tc.wantSet {
				t.Errorf("DoneSet: got %v want %v", h.DoneSet, tc.wantSet)
			}
			if h.Done != tc.wantDone {
				t.Errorf("Done: got %v want %v", h.Done, tc.wantDone)
			}
			_, _, stripped := parseAssistantHints(tc.answer)
			if stripped != tc.wantStrip {
				t.Errorf("stripped: got %q want %q", stripped, tc.wantStrip)
			}
		})
	}
}

func TestParseAssistantHints_UnknownMarkerPreserved(t *testing.T) {
	// `[plan: foo]` is not a recognised marker — the parser must
	// leave it intact rather than swallowing arbitrary blocks. This
	// keeps room for future markers without retroactively losing
	// content from existing logs.
	_, _, stripped := parseAssistantHints("hello [plan: future] world")
	if !strings.Contains(stripped, "[plan: future]") {
		t.Errorf("unknown marker was stripped: %q", stripped)
	}
}
