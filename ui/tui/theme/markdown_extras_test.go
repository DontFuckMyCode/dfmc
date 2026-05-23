package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderMarkdownBlocks_BlockquoteEmitsBarAndItalic(t *testing.T) {
	out := RenderMarkdownBlocks("> the quoted line\nplain prose follows", 80)
	if len(out) < 2 {
		t.Fatalf("expected two rendered lines, got %d: %#v", len(out), out)
	}
	stripped := ansi.Strip(out[0])
	if !strings.Contains(stripped, "▎") {
		t.Errorf("blockquote bar missing, got %q", stripped)
	}
	if !strings.Contains(stripped, "the quoted line") {
		t.Errorf("quoted body missing, got %q", stripped)
	}
	// Plain prose stays plain (no quote bar).
	if strings.Contains(ansi.Strip(out[1]), "▎") {
		t.Errorf("non-quoted line should not carry quote bar, got %q", out[1])
	}
}

func TestRenderMarkdownBlocks_BlockquoteRejectsNestedDoubleAngle(t *testing.T) {
	// `>>` is a markdown nested quote we don't render; treat as plain.
	out := RenderMarkdownBlocks(">> nested wishes", 80)
	if len(out) != 1 {
		t.Fatalf("expected single rendered line, got %d", len(out))
	}
	if strings.Contains(ansi.Strip(out[0]), "▎") {
		t.Errorf("nested-quote should not render as blockquote, got %q", out[0])
	}
}

func TestRenderMarkdownLite_InlineLink(t *testing.T) {
	out := RenderMarkdownLite("see [DFMC](https://dfmc.test) for details")
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "DFMC") {
		t.Errorf("label lost, got %q", stripped)
	}
	if !strings.Contains(stripped, "https://dfmc.test") {
		t.Errorf("URL suffix lost, got %q", stripped)
	}
	// The literal markdown brackets must be GONE — that's the whole
	// reason we render it.
	if strings.Contains(stripped, "[DFMC]") {
		t.Errorf("raw [label] markdown still visible: %q", stripped)
	}
}

func TestRenderMarkdownLite_InlineLink_LabelEqualsURLDropsSuffix(t *testing.T) {
	out := RenderMarkdownLite("[https://x.test](https://x.test)")
	stripped := ansi.Strip(out)
	// URL appears exactly once when label == url so we don't double-print.
	if strings.Count(stripped, "https://x.test") != 1 {
		t.Errorf("expected single URL print, got %q", stripped)
	}
}
