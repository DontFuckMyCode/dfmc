// Pipe-table rendering tests. The assistant routinely emits GitHub-
// style markdown tables; prior to this module they rendered as raw
// `|` walls with zero column alignment. These tests pin the detector
// (so code blocks with pipes don't get mis-classified) and the output
// shape (so a regression doesn't silently bring back the wall-of-pipes
// look).

package tui

import (
	"strings"
	"testing"
)

func TestIsTableHeader_AcceptsPipeRows(t *testing.T) {
	cases := []string{
		"| Col A | Col B |",
		"  | Col A | Col B | Col C |",
		"|Col A|Col B|",
	}
	for _, in := range cases {
		if !isTableHeader(in) {
			t.Fatalf("%q should be recognized as a table header", in)
		}
	}
}

func TestIsTableHeader_RejectsNonTable(t *testing.T) {
	cases := []string{
		"no pipes at all",
		"one | pipe only", // no leading pipe
		"|only one|",      // only two pipes total
		"",
	}
	for _, in := range cases {
		if isTableHeader(in) {
			t.Fatalf("%q must not be flagged as table header", in)
		}
	}
}

func TestIsTableSeparator_AcceptsDashRuns(t *testing.T) {
	cases := []string{
		"|---|---|",
		"| --- | --- |",
		"|:---|---:|:---:|",
	}
	for _, in := range cases {
		if !isTableSeparator(in) {
			t.Fatalf("%q should be a valid separator", in)
		}
	}
}

func TestIsTableSeparator_RejectsText(t *testing.T) {
	if isTableSeparator("| foo | bar |") {
		t.Fatal("text rows must not be treated as separators")
	}
	if isTableSeparator("| --- text | --- |") {
		t.Fatal("mixed text + dashes must not be a separator")
	}
}

func TestRenderMarkdownBlocks_AlignsTableColumns(t *testing.T) {
	src := strings.Join([]string{
		"| Dosya | Satır | Sorumluluk |",
		"|-------|-------|-------------|",
		"| engine.go | ~390 | Engine struct, ParseFile |",
		"| backend.go | ~40 | BackendStatus |",
	}, "\n")

	blocks := renderMarkdownBlocks(src)
	if len(blocks) < 4 {
		t.Fatalf("expected at least 4 output lines (header + underline + 2 rows), got %d:\n%v", len(blocks), blocks)
	}
	// Header row must still contain the column names, underline row
	// must be all dash / box glyphs, and body rows must no longer
	// start with a raw `|`.
	header := stripANSI(blocks[0])
	if !strings.Contains(header, "Dosya") || !strings.Contains(header, "Sorumluluk") {
		t.Fatalf("header row should retain column labels, got: %q", header)
	}
	under := stripANSI(blocks[1])
	if !strings.Contains(under, "─") {
		t.Fatalf("second line should be a dash-rule separator, got: %q", under)
	}
	body := stripANSI(blocks[2])
	if strings.Contains(body, " | ") && strings.HasPrefix(strings.TrimSpace(body), "|") {
		t.Fatalf("body row should be aligned, not raw pipes, got: %q", body)
	}
	if !strings.Contains(body, "engine.go") {
		t.Fatalf("body row should keep cell contents, got: %q", body)
	}
}

func TestRenderMarkdownBlocks_NonTablePipesPassThrough(t *testing.T) {
	// A lone line with pipes but no separator must render normally —
	// this is the pipe-in-prose case.
	src := "this | pipe | is just text"
	blocks := renderMarkdownBlocks(src)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 line out, got %d", len(blocks))
	}
	if !strings.Contains(stripANSI(blocks[0]), "this | pipe | is just text") {
		t.Fatalf("prose line must not be munged, got: %q", blocks[0])
	}
}

// Models that pre-render tables emit box-drawing glyphs (│ ─ ┼)
// instead of ASCII pipes. Before this fix those passed through the
// markdown renderer verbatim and the widths were whatever the model
// happened to compute — usually wrong. The renderer now recognises
// box-drawing delimiters and realigns the columns itself.
func TestIsTableHeader_AcceptsBoxDrawingRows(t *testing.T) {
	cases := []string{
		"│ Col A │ Col B │",
		"  │ Dosya │ Satır │ Sorumluluk │",
	}
	for _, in := range cases {
		if !isTableHeader(in) {
			t.Fatalf("%q should be recognized as a box-drawing table header", in)
		}
	}
}

func TestIsTableSeparator_AcceptsBoxDrawingRuns(t *testing.T) {
	cases := []string{
		"─────┼─────┼─────",
		"──────────┼──────────",
		"  ────┼────┼────",
	}
	for _, in := range cases {
		if !isTableSeparator(in) {
			t.Fatalf("%q should be a valid box-drawing separator", in)
		}
	}
}

func TestIsTableSeparator_RejectsBoxRowsWithText(t *testing.T) {
	if isTableSeparator("── foo ┼── bar ──") {
		t.Fatal("content cells must not be treated as a separator")
	}
}

func TestRenderMarkdownBlocks_AlignsBoxDrawingTable(t *testing.T) {
	// Input mimics what a model emits when it pre-renders a table —
	// notice the column widths are inconsistent between header (wider)
	// and body (narrower). The renderer must recompute widths and
	// produce rows where every body delimiter lines up.
	src := strings.Join([]string{
		"│ Dosya            │ Satır │ Durum    │",
		"─────────────────┼───────┼──────────",
		"│ graph.go       │ 260   │ Tam      │",
		"│ algorithms.go  │ 75    │ Tam      │",
	}, "\n")

	blocks := renderMarkdownBlocks(src)
	if len(blocks) < 4 {
		t.Fatalf("expected 4+ output lines, got %d:\n%v", len(blocks), blocks)
	}

	// After alignment, every non-separator row's delimiter positions
	// must match the header's. Use ASCII "│" (U+2502) as the anchor.
	plain := make([]string, 0, len(blocks))
	for _, b := range blocks {
		plain = append(plain, stripANSI(b))
	}
	anchors := pipePositions(plain[0])
	if len(anchors) < 2 {
		t.Fatalf("header must contain at least 2 delimiters, got %q", plain[0])
	}
	for i, row := range plain[2:] { // skip header + underline
		got := pipePositions(row)
		if len(got) != len(anchors) {
			t.Fatalf("body row %d delimiter count = %d, want %d\nrow: %q\nheader: %q",
				i, len(got), len(anchors), row, plain[0])
		}
		for j := range got {
			if got[j] != anchors[j] {
				t.Fatalf("body row %d delim %d at col %d, want %d\nrow:    %q\nheader: %q",
					i, j, got[j], anchors[j], row, plain[0])
			}
		}
	}
}

// pipePositions returns the 0-indexed column where each │ glyph lives
// in a rendered row (after ANSI stripping). Used to verify that body
// rows line up with the header in box-drawing tables.
func pipePositions(line string) []int {
	var out []int
	col := 0
	for _, r := range line {
		if r == '│' {
			out = append(out, col)
		}
		col++
	}
	return out
}

func TestRenderMarkdownBlocks_TableFollowedByProseKeepsBoth(t *testing.T) {
	src := strings.Join([]string{
		"| A | B |",
		"|---|---|",
		"| 1 | 2 |",
		"",
		"some prose below the table",
	}, "\n")
	blocks := renderMarkdownBlocks(src)
	joined := stripANSI(strings.Join(blocks, "\n"))
	if !strings.Contains(joined, "some prose below the table") {
		t.Fatalf("prose after table must survive, got:\n%s", joined)
	}
	if !strings.Contains(joined, "A") || !strings.Contains(joined, "1") {
		t.Fatalf("table cells must survive, got:\n%s", joined)
	}
}

// stripANSI removes ANSI escape sequences so tests can assert on the
// plain-text content without tripping over lipgloss styling bytes.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' || r == 'K' || r == 'J' || r == 'H' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
