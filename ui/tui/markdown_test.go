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
