package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderMarkdownLite_LinkifiesURLs(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"see https://example.com for details", "https://example.com"},
		{"trailing dot https://example.com.", "https://example.com"},
		{"paren (https://example.com) yes", "https://example.com"},
		{"file://foo/bar.txt loaded", "file://foo/bar.txt"},
	}
	for _, tc := range cases {
		out := RenderMarkdownLite(tc.in)
		if !strings.Contains(ansi.Strip(out), tc.want) {
			t.Errorf("input %q lost URL %q, stripped=%q", tc.in, tc.want, ansi.Strip(out))
		}
		if !strings.Contains(out, "\x1b[") {
			t.Errorf("input %q produced no ANSI styling: %q", tc.in, out)
		}
	}
}

func TestRenderMarkdownLite_NoFalseLinks(t *testing.T) {
	// Plain prose like "see foo.go for context" should NOT be linkified.
	out := RenderMarkdownLite("see foo.go for context")
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("plain prose should not be styled, got %q", out)
	}
}

func TestLinkify_TrailingPunctuationStaysOutside(t *testing.T) {
	// Sentence-ending punctuation must remain outside the styled span
	// so paragraph terminators don't get an underline.
	out := RenderMarkdownLite("https://x.test.")
	stripped := ansi.Strip(out)
	if !strings.HasSuffix(stripped, ".") {
		t.Fatalf("trailing period lost: %q", stripped)
	}
}
