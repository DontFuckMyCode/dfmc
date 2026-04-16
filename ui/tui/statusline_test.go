package tui

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCompactTokensBoundaries(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1_000, "1k"},
		{120_000, "120k"},
		{125_500, "125.5k"},
		{1_000_000, "1M"},
		{1_500_000, "1.5M"},
	}
	for _, c := range cases {
		if got := compactTokens(c.in); got != c.want {
			t.Errorf("compactTokens(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatSessionDurationBuckets(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "0s"},
		{-5 * time.Second, "0s"},
		{15 * time.Second, "15s"},
		{42 * time.Minute, "42m"},
		{time.Hour, "1h"},
		{time.Hour + 23*time.Minute, "1h 23m"},
		{3 * time.Hour, "3h"},
	}
	for _, c := range cases {
		if got := formatSessionDuration(c.in); got != c.want {
			t.Errorf("formatSessionDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderContextBarThresholds(t *testing.T) {
	// unknown max → falls back to the plain meter, no bar brackets.
	if out := renderContextBar(1000, 0, 10); strings.Contains(out, "[") {
		t.Errorf("expected fallback meter when max is 0, got %q", out)
	}

	low := renderContextBar(10_000, 100_000, 10) // 10%
	mid := renderContextBar(70_000, 100_000, 10) // 70% → warn
	hot := renderContextBar(90_000, 100_000, 10) // 90% → fail

	for _, want := range []string{"10k/100k (10%)", "70k/100k (70%)", "90k/100k (90%)"} {
		found := false
		for _, s := range []string{low, mid, hot} {
			if strings.Contains(s, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected one bar to contain %q", want)
		}
	}

	// The filled/empty cells together should equal the requested width.
	// Count runes rather than raw bytes so we don't trip over ANSI styling.
	if strings.Count(low, "█")+strings.Count(low, "░") != 10 {
		t.Errorf("expected 10 bar cells in low bar, got %q", low)
	}
	if strings.Count(hot, "█")+strings.Count(hot, "░") != 10 {
		t.Errorf("expected 10 bar cells in hot bar, got %q", hot)
	}
}

func TestRenderContextBarMinimumCells(t *testing.T) {
	// Sub-minimum widths get clamped to 4 cells so the bar always has shape.
	out := renderContextBar(500, 1000, 2)
	if cells := strings.Count(out, "█") + strings.Count(out, "░"); cells != 4 {
		t.Errorf("expected 4 bar cells when cells<4, got %d in %q", cells, out)
	}
}

func TestParseNumstatAggregates(t *testing.T) {
	raw := "10\t2\tfile_a.go\n5\t0\tfile_b.go\n-\t-\tbin.png\n"
	ins, del, any := parseNumstat(raw)
	if ins != 15 || del != 2 || !any {
		t.Fatalf("parseNumstat returned ins=%d del=%d any=%v, want 15/2/true", ins, del, any)
	}

	ins2, del2, any2 := parseNumstat("")
	if ins2 != 0 || del2 != 0 || any2 {
		t.Fatalf("empty numstat should be zero/false, got %d/%d/%v", ins2, del2, any2)
	}
}

func TestRenderFooterMetricsComposes(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.sessionStart = time.Now().Add(-42 * time.Minute)
	m.gitInfo = gitWorkspaceInfo{
		Branch:   "main",
		Inserted: 255,
		Deleted:  10,
		Dirty:    true,
	}

	out := strings.Join(m.footerSegments(), "  ·  ")
	for _, want := range []string{"ctx", "main", "+255", "-10", "42m"} {
		if !strings.Contains(out, want) {
			t.Errorf("footer metrics missing %q, got:\n%s", want, out)
		}
	}
}

func TestRenderFooterMetricsOmitsEmptyGitSegments(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.sessionStart = time.Now().Add(-5 * time.Second)
	m.gitInfo = gitWorkspaceInfo{} // no branch, no churn

	out := strings.Join(m.footerSegments(), "  ·  ")
	if strings.Contains(out, "⎇") {
		t.Errorf("expected no branch chip when git info is empty, got:\n%s", out)
	}
	if strings.Contains(out, "+0") || strings.Contains(out, "-0") {
		t.Errorf("expected no churn segment when both counters are zero, got:\n%s", out)
	}
	if !strings.Contains(out, "ctx") || !strings.Contains(out, "5s") {
		t.Errorf("expected ctx bar + session time even with empty git info, got:\n%s", out)
	}
}
