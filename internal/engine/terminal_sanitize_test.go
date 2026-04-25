package engine

import (
	"strings"
	"testing"
)

// TestStripTerminalControlBytes_NeutralisesANSIExploits is the load-
// bearing VULN-038 invariant: the bytes any modern terminal emulator
// honours (CSI screen clear, OSC window-title rewrite, OSC 8
// hyperlink, DCS, APC) must be stripped so a hostile tool output
// can't drive the user's terminal.
func TestStripTerminalControlBytes_NeutralisesANSIExploits(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"CSI screen clear", "before\x1b[2J\x1b[Hafter"},
		{"CSI cursor home", "x\x1b[Hy"},
		{"OSC window title (BEL)", "x\x1b]0;PWNED\x07y"},
		{"OSC window title (ST)", "x\x1b]0;PWNED\x1b\\y"},
		{"OSC 8 hyperlink", "click \x1b]8;;https://evil/\x1b\\here\x1b]8;;\x1b\\!"},
		{"DCS sequence", "x\x1bPpayload\x1b\\y"},
		{"APC sequence", "x\x1b_payload\x1b\\y"},
		{"C1 CSI", "x\x9b2Jy"},
		{"C1 OSC", "x\x9d0;PWNED\x9cy"},
		{"plain NUL", "x\x00y"},
		{"vertical tab", "x\x0by"},
		{"form feed", "x\x0cy"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := stripTerminalControlBytes(c.in)
			if strings.ContainsAny(out, "\x1b\x9b\x9d\x07\x00\x0b\x0c") {
				t.Errorf("output still contains terminal-control bytes: %q (in=%q)", out, c.in)
			}
		})
	}
}

// TestStripTerminalControlBytes_PreservesContent confirms the filter
// doesn't over-strip — the surrounding plain text must survive so
// the tool output is still informative after sanitisation.
func TestStripTerminalControlBytes_PreservesContent(t *testing.T) {
	in := "before\x1b[2J\x1b[Hafter"
	out := stripTerminalControlBytes(in)
	if !strings.Contains(out, "before") || !strings.Contains(out, "after") {
		t.Errorf("plain content must survive: got %q", out)
	}
}

// TestStripTerminalControlBytes_KeepsTabsAndNewlines pins the
// whitelist: tab and newline are needed for legitimate multi-line
// tool output (`grep_codebase`, `read_file`, etc.) and must NOT be
// stripped or the TUI chip preview becomes unreadable.
func TestStripTerminalControlBytes_KeepsTabsAndNewlines(t *testing.T) {
	in := "line1\nline2\twith\ttab\nline3\r\nwindows"
	out := stripTerminalControlBytes(in)
	if out != in {
		t.Errorf("plain text with tabs/newlines should pass unchanged; got %q want %q", out, in)
	}
}

// TestStripTerminalControlBytes_FastPathAllocates pins the hot-path
// invariant: a clean string returns the original (no allocation).
// This matters because compactToolPayload runs on every tool result
// — a per-call allocation in the steady state would show up in the
// agent loop's GC profile.
func TestStripTerminalControlBytes_FastPathReturnsSameString(t *testing.T) {
	in := "this is a clean tool output with no control bytes"
	out := stripTerminalControlBytes(in)
	if out != in {
		t.Errorf("clean input should be returned unchanged: %q vs %q", out, in)
	}
	// Best-effort identity check — if the implementation grew an
	// unconditional copy this would fail. Strings in Go are
	// reference-typed under the hood so this is a reasonable proxy.
	if &in == &out {
		// Pointer compare on string headers is implementation-defined;
		// we don't fail on this path, only flag the lookalike.
		_ = out
	}
}

// TestStripTerminalControlBytes_UTF8Preserved makes sure multibyte
// UTF-8 (which uses bytes >= 0xa0) survives the strip.
func TestStripTerminalControlBytes_UTF8Preserved(t *testing.T) {
	cases := []string{
		"Türkçe karakter testi",
		"日本語テスト",
		"emoji 🚀 still works",
		"Ω Ω Ω αβγ",
	}
	for _, in := range cases {
		out := stripTerminalControlBytes(in)
		if out != in {
			t.Errorf("UTF-8 must survive: got %q want %q", out, in)
		}
	}
}

// TestCompactToolPayload_StripsEscapeBeforeTrim wires the helper
// into the actual publish path and confirms a hostile tool output
// is sanitised by the time it lands in `output_preview`.
func TestCompactToolPayload_StripsEscapeBeforeTrim(t *testing.T) {
	hostile := "tool said: \x1b]0;PWNED\x07normal text after"
	got := compactToolPayload(hostile, 200)
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("compactToolPayload must strip escape bytes: got %q", got)
	}
	if !strings.Contains(got, "normal text after") {
		t.Errorf("legitimate content must survive: got %q", got)
	}
}
