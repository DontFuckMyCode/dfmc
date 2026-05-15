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

	blocks := renderMarkdownBlocks(src, 80)
	// Layout (top-down): top border ┌─┬─┐, header row, divider ├─┼─┤,
	// body row(s), bottom border └─┴─┘. We want at least 5 lines so
	// the body row is reachable; the borders carry no labels so we
	// scan past them for the labelled rows.
	if len(blocks) < 5 {
		t.Fatalf("expected at least 5 output lines (top + header + rule + body + bottom), got %d:\n%v", len(blocks), blocks)
	}
	plain := make([]string, 0, len(blocks))
	for _, b := range blocks {
		plain = append(plain, stripANSI(b))
	}
	// Header row must still contain the column names (somewhere past
	// the top border). Underline row must be a dash / box rule. Body
	// rows must no longer start with a raw `|`.
	var header, under, body string
	for _, line := range plain {
		if header == "" && strings.Contains(line, "Dosya") {
			header = line
			continue
		}
		if header != "" && under == "" && strings.Contains(line, "─") && !strings.Contains(line, "Dosya") {
			under = line
			continue
		}
		if under != "" && body == "" && strings.Contains(line, "engine.go") {
			body = line
			break
		}
	}
	if header == "" {
		t.Fatalf("header row should retain column labels, got blocks:\n%v", plain)
	}
	if under == "" {
		t.Fatalf("expected a dash-rule separator after header, got:\n%v", plain)
	}
	if body == "" {
		t.Fatalf("body row should keep cell contents, got blocks:\n%v", plain)
	}
	if strings.Contains(body, " | ") && strings.HasPrefix(strings.TrimSpace(body), "|") {
		t.Fatalf("body row should be aligned, not raw pipes, got: %q", body)
	}
}

func TestRenderMarkdownBlocks_NonTablePipesPassThrough(t *testing.T) {
	// A lone line with pipes but no separator must render normally —
	// this is the pipe-in-prose case.
	src := "this | pipe | is just text"
	blocks := renderMarkdownBlocks(src, 80)
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

	blocks := renderMarkdownBlocks(src, 80)
	if len(blocks) < 4 {
		t.Fatalf("expected 4+ output lines, got %d:\n%v", len(blocks), blocks)
	}

	// After alignment, every non-separator row's delimiter positions
	// must match the header's. Use ASCII "│" (U+2502) as the anchor.
	plain := make([]string, 0, len(blocks))
	for _, b := range blocks {
		plain = append(plain, stripANSI(b))
	}
	rows := pipeRows(plain)
	if len(rows) < 2 {
		t.Fatalf("expected header + body rows in output, got %d │-rows:\n%v", len(rows), plain)
	}
	anchors := pipePositions(rows[0])
	if len(anchors) < 2 {
		t.Fatalf("header must contain at least 2 delimiters, got %q", rows[0])
	}
	for i, row := range rows[1:] {
		got := pipePositions(row)
		if len(got) != len(anchors) {
			t.Fatalf("body row %d delimiter count = %d, want %d\nrow: %q\nheader: %q",
				i, len(got), len(anchors), row, rows[0])
		}
		for j := range got {
			if got[j] != anchors[j] {
				t.Fatalf("body row %d delim %d at col %d, want %d\nrow:    %q\nheader: %q",
					i, j, got[j], anchors[j], row, rows[0])
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

// pipeRows filters a rendered-table block down to only the rows that
// carry │ delimiters (i.e. the header + body cells). Top/bottom
// borders (┌─┐, └─┘) and dash rules (├─┤) are skipped so the test can
// treat row 0 as "header" and row 1+ as "body" regardless of the
// surrounding chrome.
func pipeRows(plain []string) []string {
	out := make([]string, 0, len(plain))
	for _, line := range plain {
		if strings.ContainsRune(line, '│') {
			out = append(out, line)
		}
	}
	return out
}

// Regression: when body cells contain backticked code or **bold** the
// markers get stripped by renderMarkdownLite. Before this fix the
// widths were computed on the raw cell (with markers), padding was
// added to reach colwidth, then the markers were removed — leaving
// body cells 2–4 visible chars short and misaligning the column with
// its own header. The fix: measure widths on the rendered cell, not
// the raw markdown.
func TestRenderMarkdownBlocks_AlignsTableWithBacktickedBody(t *testing.T) {
	src := strings.Join([]string{
		"| Özellik | Kaynak | Durum |",
		"|---------|--------|-------|",
		"| Go sembol çıkarma | `go_extract.go:23-114` | Tam |",
		"| Tree-sitter Python | `treesitter_cgo.go:215-253` | Tam |",
	}, "\n")

	blocks := renderMarkdownBlocks(src, 80)
	if len(blocks) < 4 {
		t.Fatalf("expected 4+ output lines, got %d", len(blocks))
	}
	plain := make([]string, 0, len(blocks))
	for _, b := range blocks {
		plain = append(plain, stripANSI(b))
	}
	rows := pipeRows(plain)
	if len(rows) < 2 {
		t.Fatalf("expected header + body rows, got %d │-rows:\n%v", len(rows), plain)
	}
	header := pipePositions(rows[0])
	if len(header) < 2 {
		t.Fatalf("header row should have at least 2 delimiters, got %q", rows[0])
	}
	for i, row := range rows[1:] {
		got := pipePositions(row)
		if len(got) != len(header) {
			t.Fatalf("body row %d delim count = %d, want %d\nrow: %q\nheader: %q",
				i, len(got), len(header), row, rows[0])
		}
		for j := range got {
			if got[j] != header[j] {
				t.Fatalf("body row %d delim %d at col %d, want %d\nrow:    %q\nheader: %q",
					i, j, got[j], header[j], row, rows[0])
			}
		}
	}
}

// Same regression but with **bold** markers in the body instead of
// backticks. Bold is stripped by renderMarkdownLite too; any markdown
// inline marker that consumes literal characters has to be measured
// post-render for alignment to hold.
func TestRenderMarkdownBlocks_AlignsTableWithBoldBody(t *testing.T) {
	src := strings.Join([]string{
		"| Name | Severity | Note |",
		"|------|----------|------|",
		"| **Critical bug** | High | fix today |",
		"| Style drift | Low | later |",
	}, "\n")

	blocks := renderMarkdownBlocks(src, 80)
	plain := make([]string, 0, len(blocks))
	for _, b := range blocks {
		plain = append(plain, stripANSI(b))
	}
	rows := pipeRows(plain)
	if len(rows) < 2 {
		t.Fatalf("expected header + body rows, got %d │-rows:\n%v", len(rows), plain)
	}
	header := pipePositions(rows[0])
	for i, row := range rows[1:] {
		got := pipePositions(row)
		for j := range got {
			if got[j] != header[j] {
				t.Fatalf("body row %d delim %d at col %d, want %d\nrow:    %q\nheader: %q",
					i, j, got[j], header[j], row, rows[0])
			}
		}
	}
}

func TestRenderMarkdownBlocks_TableFollowedByProseKeepsBoth(t *testing.T) {
	src := strings.Join([]string{
		"| A | B |",
		"|---|---|",
		"| 1 | 2 |",
		"",
		"some prose below the table",
	}, "\n")
	blocks := renderMarkdownBlocks(src, 80)
	joined := stripANSI(strings.Join(blocks, "\n"))
	if !strings.Contains(joined, "some prose below the table") {
		t.Fatalf("prose after table must survive, got:\n%s", joined)
	}
	if !strings.Contains(joined, "A") || !strings.Contains(joined, "1") {
		t.Fatalf("table cells must survive, got:\n%s", joined)
	}
}

func TestRenderMarkdownBlocks_RaggedTableDoesNotPanic(t *testing.T) {
	src := strings.Join([]string{
		"| A | B | C |",
		"|---|---|---|",
		"| 1 | 2 |",
		"| 3 | 4 | 5 |",
	}, "\n")

	blocks := renderMarkdownBlocks(src, 80)
	joined := stripANSI(strings.Join(blocks, "\n"))
	if !strings.Contains(joined, "1") || !strings.Contains(joined, "5") {
		t.Fatalf("ragged table cells must survive, got:\n%s", joined)
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
