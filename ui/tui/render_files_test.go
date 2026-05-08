package tui

import (
	"strings"
	"testing"
)

// TestFilesViewV2_RendersAllThreePanesOnWideTerminal pins the
// 3-pane layout: list (FILES), preview (PREVIEW), and metadata
// cards (FILE / STATUS / ACTIONS). All must appear at width≥120.
func TestFilesViewV2_RendersAllThreePanesOnWideTerminal(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{
		entries: []string{"cmd/dfmc/main.go", "internal/engine/engine.go", "ui/tui/tui.go"},
		index:   1,
		path:    "internal/engine/engine.go",
		preview: "package engine\n\nfunc Init() {}\n",
		size:    1500,
	}
	view := stripANSI(m.renderFilesViewV2(140, 30))
	for _, want := range []string{
		"FILES", "PREVIEW",
		"FILE", "STATUS", "ACTIONS",
		"engine.go",
		"package engine",
		"Path:", "Size:", "Language:", "Lines:",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("wide files view missing %q. Got:\n%s", want, view)
		}
	}
}

// TestFilesViewV2_TwoPaneCollapsesMetaIntoFooter — at medium widths
// (80–119) the metadata pane folds into a single inline strip.
func TestFilesViewV2_TwoPaneCollapsesMetaIntoFooter(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{
		entries: []string{"a.go", "b.py"},
		index:   0,
		path:    "a.go",
		preview: "package a\n",
		size:    256,
	}
	view := stripANSI(m.renderFilesViewV2(100, 24))
	if !strings.Contains(view, "FILES") || !strings.Contains(view, "PREVIEW") {
		t.Errorf("two-pane layout missing core panes. Got:\n%s", view)
	}
	// Metadata cards are NOT separately rendered at this width.
	if strings.Contains(view, "ACTIONS") {
		t.Errorf("two-pane layout should fold metadata, but ACTIONS card rendered:\n%s", view)
	}
	// Inline footer surfaces the size + language summary.
	if !strings.Contains(view, "Go") {
		t.Errorf("inline footer missing language label. Got:\n%s", view)
	}
}

// TestFilesViewV2_EmptyStateOffersGuidance — when no files indexed,
// the list pane should print actionable hints rather than blank.
func TestFilesViewV2_EmptyStateOffersGuidance(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{}
	view := stripANSI(m.renderFilesViewV2(140, 24))
	for _, want := range []string{
		"No indexed project files",
		"/analyze",
		"refresh",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("empty state missing %q. Got:\n%s", want, view)
		}
	}
}

// TestFilesViewV2_PinnedFileShowsBadge — a pinned file should show
// the PIN chip in the list and a "Pinned: yes" row in the STATUS card.
func TestFilesViewV2_PinnedFileShowsBadge(t *testing.T) {
	m := newCoverageModel(t)
	m.filesView = filesViewState{
		entries: []string{"main.go"},
		index:   0,
		pinned:  "main.go",
		path:    "main.go",
		preview: "package main\n",
		size:    100,
	}
	view := stripANSI(m.renderFilesViewV2(140, 24))
	if !strings.Contains(view, "PIN") {
		t.Errorf("pinned chip missing in list. Got:\n%s", view)
	}
	if !strings.Contains(view, "yes") {
		t.Errorf("STATUS card should report pinned=yes. Got:\n%s", view)
	}
}

func TestHumanFileSize(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1 << 20, "1.0 MB"},
		{int(2.5 * float64(1<<30)), "2.5 GB"},
	}
	for _, tc := range cases {
		if got := humanFileSize(tc.in); got != tc.want {
			t.Errorf("humanFileSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLanguageFromPath(t *testing.T) {
	cases := map[string]string{
		"foo.go":         "Go",
		"foo.js":         "JavaScript",
		"foo.ts":         "TypeScript",
		"foo.py":         "Python",
		"foo.unknown":    "",
		"path/to/foo.md": "Markdown",
	}
	for path, want := range cases {
		if got := languageFromPath(path); got != want {
			t.Errorf("languageFromPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestTruncatePathHead(t *testing.T) {
	// Short path is returned unchanged.
	if got := truncatePathHead("a.go", 20); got != "a.go" {
		t.Errorf("short path: got %q", got)
	}
	// Long path keeps the tail (filename) visible.
	long := "very/deeply/nested/directory/structure/with/lots/of/segments/main.go"
	got := truncatePathHead(long, 20)
	if !strings.HasSuffix(got, "main.go") {
		t.Errorf("truncated path should keep filename, got %q", got)
	}
	if len([]rune(got)) > 20 {
		t.Errorf("truncated path exceeded width: %q (len=%d)", got, len([]rune(got)))
	}
}

func TestScrollWindow(t *testing.T) {
	cases := []struct {
		name                  string
		cursor, total, budget int
		wantStart, wantEnd    int
	}{
		{"empty", 0, 0, 5, 0, 0},
		{"top", 0, 20, 6, 0, 6},
		{"middle centres cursor", 10, 20, 6, 7, 13},
		{"bottom clamps", 19, 20, 6, 14, 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, e := scrollWindow(tc.cursor, tc.total, tc.budget)
			if s != tc.wantStart || e != tc.wantEnd {
				t.Errorf("scrollWindow(%d, %d, %d) = (%d, %d), want (%d, %d)",
					tc.cursor, tc.total, tc.budget, s, e, tc.wantStart, tc.wantEnd)
			}
		})
	}
}
