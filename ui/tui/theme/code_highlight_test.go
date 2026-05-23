package theme

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestMain(m *testing.M) {
	// Force a color profile so styled output emits ANSI escapes in CI /
	// non-TTY test runs. Without this, lipgloss elides all styling and
	// the highlighter tests can't tell apart "no styles applied" from
	// "styles applied but stripped for plaintext terminal".
	lipgloss.DefaultRenderer().SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}

func TestRenderFencedCodeLine_DiffColors(t *testing.T) {
	cases := []struct {
		line, lang, mustContain string
	}{
		{"+added line", "diff", "added line"},
		{"-removed line", "diff", "removed line"},
		{"@@ -1,3 +1,4 @@", "diff", "@@"},
		{"+++ b/foo", "diff", "foo"},
	}
	for _, tc := range cases {
		out := RenderFencedCodeLine(tc.line, tc.lang)
		if !strings.Contains(ansi.Strip(out), tc.mustContain) {
			t.Errorf("RenderFencedCodeLine(%q,%q) missing %q in %q", tc.line, tc.lang, tc.mustContain, out)
		}
		if !strings.Contains(out, "\x1b[") {
			t.Errorf("RenderFencedCodeLine(%q,%q) had no ANSI styling: %q", tc.line, tc.lang, out)
		}
	}
}

func TestRenderFencedCodeLine_GoKeywordsStringsAndComments(t *testing.T) {
	out := RenderFencedCodeLine(`func Greet(name string) { /* skip */ fmt.Println("hi") // trailing`, "go")
	stripped := ansi.Strip(out)
	for _, want := range []string{"func", "Greet", "string", `"hi"`, "trailing"} {
		if !strings.Contains(stripped, want) {
			t.Errorf("expected %q in stripped output, got %q", want, stripped)
		}
	}
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI styling, got plain: %q", out)
	}
}

func TestRenderFencedCodeLine_UnknownLangFallsBackToCodeStyle(t *testing.T) {
	out := RenderFencedCodeLine("plain text", "brainfuck")
	if !strings.Contains(ansi.Strip(out), "plain text") {
		t.Fatalf("plain content must survive fallback, got %q", out)
	}
}

func TestRenderFencedCodeLine_AutoDetectsDiffLine(t *testing.T) {
	// Lang missing/empty but line itself signals diff → still colorize.
	out := RenderFencedCodeLine("+ accepted", "")
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI styling for auto-detected diff line, got %q", out)
	}
}

func TestHighlightCodeLine_PythonComment(t *testing.T) {
	out := RenderFencedCodeLine(`x = "hello"  # trailing note`, "python")
	stripped := ansi.Strip(out)
	if !strings.Contains(stripped, "trailing note") {
		t.Fatalf("comment text lost: %q", stripped)
	}
}

func TestHighlightCodeLine_JSONStringIsColored(t *testing.T) {
	out := RenderFencedCodeLine(`  "key": 42,`, "json")
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI on json line, got %q", out)
	}
}
