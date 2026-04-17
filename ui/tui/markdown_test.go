package tui

import (
	"strings"
	"testing"
)

func TestRenderMarkdownBlocks_CodeFenceIsolatesLines(t *testing.T) {
	in := "before\n```go\nfunc foo() {}\n```\nafter"
	lines := renderMarkdownBlocks(in)
	if len(lines) != 5 {
		t.Fatalf("expected 5 rendered lines (before, open-fence, code, close-fence, after), got %d: %q", len(lines), lines)
	}
	// Fence markers contain the language tag.
	if !strings.Contains(lines[1], "go") || !strings.Contains(lines[1], "╌") {
		t.Fatalf("expected opening fence marker with lang=go, got %q", lines[1])
	}
	if !strings.Contains(lines[2], "func foo()") || !strings.Contains(lines[2], "│") {
		t.Fatalf("expected code line to carry the │ gutter, got %q", lines[2])
	}
}

func TestRenderMarkdownBlocks_BulletsAndHeaders(t *testing.T) {
	in := "## Summary\n- first\n- second\n1. numbered\nbody text"
	lines := renderMarkdownBlocks(in)
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d: %q", len(lines), lines)
	}
	if !strings.Contains(lines[0], "Summary") || !strings.Contains(lines[0], "##") {
		t.Fatalf("expected header line 0 to contain level-2 marker and label, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "•") || !strings.Contains(lines[1], "first") {
		t.Fatalf("expected bullet on line 1, got %q", lines[1])
	}
	if !strings.Contains(lines[3], "1.") || !strings.Contains(lines[3], "numbered") {
		t.Fatalf("expected numbered bullet on line 3, got %q", lines[3])
	}
	if !strings.Contains(lines[4], "body text") || strings.Contains(lines[4], "•") {
		t.Fatalf("expected body text unmolested on line 4, got %q", lines[4])
	}
}

func TestBulletLineHandlesNumberedAndDashes(t *testing.T) {
	cases := []struct {
		in         string
		wantBullet string
		wantRest   string
	}{
		{"- apple", "•", "apple"},
		{"* apple", "•", "apple"},
		{"+ apple", "•", "apple"},
		{"1. apple", "1.", "apple"},
		{"  - nested", "  •", "nested"},
	}
	for _, c := range cases {
		bullet, rest, ok := bulletLine(c.in)
		if !ok {
			t.Fatalf("bulletLine(%q) = !ok, want ok", c.in)
		}
		if bullet != c.wantBullet {
			t.Fatalf("bulletLine(%q) bullet = %q, want %q", c.in, bullet, c.wantBullet)
		}
		if rest != c.wantRest {
			t.Fatalf("bulletLine(%q) rest = %q, want %q", c.in, rest, c.wantRest)
		}
	}
	if _, _, ok := bulletLine("-noSpace"); ok {
		t.Fatalf("expected missing-space to not match as bullet")
	}
	if _, _, ok := bulletLine("plain body"); ok {
		t.Fatalf("expected plain text to not match as bullet")
	}
}

// TestWrapBubbleLine_WrapsProseAtWordBoundary — long responses previously
// got chopped by truncateSingleLine with a "…" and users lost the tail of
// every sentence beyond the chat width. This is the regression guard: long
// prose should come back as multiple rows instead of one truncated row.
func TestWrapBubbleLine_WrapsProseAtWordBoundary(t *testing.T) {
	line := "You can fix this by updating the configuration to use the new schema version and then restarting the service to pick up the changes."
	rows := wrapBubbleLine(line, 40)
	if len(rows) < 2 {
		t.Fatalf("expected wrap into multiple rows at width=40, got %d: %q", len(rows), rows)
	}
	// No row should exceed the width.
	for i, row := range rows {
		// ansi.StringWidth handles ANSI codes; rows here are plain so len works.
		if n := len(row); n > 40 {
			t.Errorf("row %d width=%d exceeds limit 40: %q", i, n, row)
		}
	}
	// The tail of the sentence must survive — no "…".
	joined := strings.Join(rows, " ")
	if !strings.Contains(joined, "pick up the changes") {
		t.Fatalf("wrap must preserve the sentence tail, got %q", joined)
	}
	if strings.Contains(joined, "…") {
		t.Fatalf("wrap must not use a truncation ellipsis, got %q", joined)
	}
}

// TestWrapBubbleLine_ShortLinesPassThrough — input that already fits in the
// width budget must come back untouched (no forced wrap, no re-join).
func TestWrapBubbleLine_ShortLinesPassThrough(t *testing.T) {
	rows := wrapBubbleLine("short enough", 40)
	if len(rows) != 1 || rows[0] != "short enough" {
		t.Fatalf("short line should pass through untouched, got %q", rows)
	}
}

// TestWrapBubbleLine_BreaksOnPathAndIdentifierSeams — paths and snake/kebab
// identifiers were previously treated as single tokens because the break set
// only knew about whitespace + punctuation. Pin the new seam set so wrapping
// of long file paths and identifiers stays clean.
func TestWrapBubbleLine_BreaksOnPathAndIdentifierSeams(t *testing.T) {
	line := "internal/engine/agent_loop_native.go contains a very_long_snake_case_function_name_here_too"
	rows := wrapBubbleLine(line, 30)
	if len(rows) < 2 {
		t.Fatalf("expected wrap into multiple rows at width=30, got %d: %q", len(rows), rows)
	}
	for i, row := range rows {
		if n := len(row); n > 30 {
			t.Errorf("row %d width=%d exceeds 30: %q", i, n, row)
		}
	}
}

// TestWrapBubbleLine_HardBreaksRunawayTokens — a base64 blob or any
// break-char-free token longer than the limit must hard-break by cells
// instead of bleeding past the bubble's right edge.
func TestWrapBubbleLine_HardBreaksRunawayTokens(t *testing.T) {
	// 80 chars, no break chars at all
	blob := strings.Repeat("A", 80)
	rows := wrapBubbleLine(blob, 20)
	if len(rows) < 4 {
		t.Fatalf("expected at least 4 rows for 80-char blob at width=20, got %d", len(rows))
	}
	for i, row := range rows {
		if n := len(row); n > 20 {
			t.Errorf("row %d width=%d exceeds 20: %q", i, n, row)
		}
	}
}

// TestRenderMessageBubble_WrapsLongProse end-to-end — the bubble layer must
// surface wrapping as multiple bar-prefixed rows, not one "…" cut.
func TestRenderMessageBubble_WrapsLongProse(t *testing.T) {
	long := "This is a fairly long assistant answer that should wrap across multiple rows so the user can read all of it instead of losing the tail of the sentence to an ellipsis."
	out := renderMessageBubble("assistant", long, "assistant · now", 60)
	lines := strings.Split(out, "\n")
	if len(lines) < 3 {
		t.Fatalf("expected header + multiple content rows, got %d: %q", len(lines), lines)
	}
	if strings.Contains(out, "…") {
		t.Fatalf("bubble must not truncate long prose, got:\n%s", out)
	}
	if !strings.Contains(out, "tail of the sentence") {
		t.Fatalf("bubble must preserve the sentence tail, got:\n%s", out)
	}
}

func TestHeaderLevelMatchesPrefixes(t *testing.T) {
	if headerLevel("# H1") != 1 || headerLevel("## H2") != 2 || headerLevel("### H3") != 3 {
		t.Fatalf("header prefixes should resolve to levels 1/2/3")
	}
	if headerLevel("#### too deep") != 0 {
		t.Fatalf("level >3 should fall through as body text")
	}
	if headerLevel("#nospace") != 0 {
		t.Fatalf("hash without space is not a header")
	}
}
