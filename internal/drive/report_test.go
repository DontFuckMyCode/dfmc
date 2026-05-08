package drive

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestRenderRunReport_HeaderAndCounts(t *testing.T) {
	run := &Run{
		ID:        "abc123",
		Task:      "fix the off-by-one in foo()",
		Status:    RunDone,
		CreatedAt: time.Date(2026, 5, 8, 15, 0, 0, 0, time.UTC),
		EndedAt:   time.Date(2026, 5, 8, 15, 42, 11, 0, time.UTC),
		Todos: []Todo{
			{ID: "1", Title: "fix foo", Status: TodoDone, WorkerClass: "coder", Attempts: 1},
			{ID: "2", Title: "test foo", Status: TodoDone, WorkerClass: "coder", Attempts: 1},
			{ID: "3", Title: "update changelog", Status: TodoBlocked, WorkerClass: "scribe", Attempts: 2, Error: "could not parse heading"},
		},
	}
	out := RenderRunReport(run)
	checks := []string{
		"# Drive Run abc123",
		"**Task**: fix the off-by-one in foo()",
		"**Status**: done · 2 done · 1 blocked · 0 skipped",
		"**Started**: 2026-05-08T15:00:00Z",
		"**Ended**: 2026-05-08T15:42:11Z",
		"**Duration**: 42m11s",
		"## Summary",
		"| 1 | fix foo | done | coder | 1",
		"| 3 | update changelog | blocked | scribe | 2",
		"## Details",
		"### 1. fix foo (done)",
		"### 3. update changelog (blocked)",
		"**Error**: could not parse heading",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n--- report ---\n%s", want, out)
		}
	}
}

func TestRenderRunReport_NilRun(t *testing.T) {
	if got := RenderRunReport(nil); got != "" {
		t.Errorf("nil run should return empty string, got %q", got)
	}
}

func TestRenderRunReport_NoTodos(t *testing.T) {
	run := &Run{ID: "empty1", Task: "no todos here", Status: RunFailed, Reason: "planner returned 0"}
	out := RenderRunReport(run)
	if !strings.Contains(out, "_No TODOs in this run._") {
		t.Errorf("missing empty-marker; got: %s", out)
	}
	if !strings.Contains(out, "**Status**: failed") {
		t.Errorf("missing status; got: %s", out)
	}
	if !strings.Contains(out, "reason: planner returned 0") {
		t.Errorf("missing reason; got: %s", out)
	}
}

func TestCompactCell_StripsPipesAndNewlines(t *testing.T) {
	in := "line one|with pipe\nline two\rline three"
	got := compactCell(in, 100)
	if strings.Contains(got, "|") {
		t.Errorf("pipe survived: %q", got)
	}
	if strings.Contains(got, "\n") {
		t.Errorf("newline survived: %q", got)
	}
}

func TestCompactCell_TruncatesWithEllipsis(t *testing.T) {
	long := strings.Repeat("x", 100)
	got := compactCell(long, 10)
	if len([]rune(got)) != 10 {
		t.Errorf("expected 10 runes after truncation, got %d (%q)", len([]rune(got)), got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix, got %q", got)
	}
}

func TestSetReportDir_NilSafe(t *testing.T) {
	var d *Driver
	d.SetReportDir("/tmp/test") // must not panic
}

func TestWriteRunReport_WritesFileWhenDirSet(t *testing.T) {
	d := &Driver{}
	d.SetReportDir(t.TempDir())
	run := &Run{
		ID:     "writetest",
		Task:   "smoke test",
		Status: RunDone,
		Todos:  []Todo{{ID: "1", Title: "do thing", Status: TodoDone}},
	}
	d.writeRunReport(run)
	// Quick verify the file landed in the configured dir.
	entries, err := readReportDir(d.reportDir)
	if err != nil {
		t.Fatalf("readDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 report file, got %d: %v", len(entries), entries)
	}
	if !strings.HasSuffix(entries[0], ".md") || !strings.Contains(entries[0], "writetest") {
		t.Errorf("unexpected report filename: %s", entries[0])
	}
}

func readReportDir(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out, nil
}
