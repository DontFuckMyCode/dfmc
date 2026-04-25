package engine

// terminal_sanitize.go — engine-side filter for terminal-control
// bytes in tool output and assistant text. Closes VULN-038.
//
// Pre-fix, a tool whose output contained ANSI escape sequences
// (e.g. `web_fetch` returning a hostile HTML page, a `run_command`
// invoking a binary that emits ANSI for its own use, or an LLM
// echoing literal `\x1b[...]`) wrote those bytes straight to the
// TUI chip and to every SSE subscriber. Modern terminal emulators
// honour:
//   - CSI sequences (cursor moves, screen clears, color changes)
//   - OSC 0/1/2 (window-title rewrite — "you have been pwned")
//   - OSC 8 (clickable hyperlinks — embed phishing URLs inside
//     plausible "documentation" text the user can't easily inspect)
//   - DCS / APC / PM / SOS (private-message and command sequences)
//
// The filter strips C0 (`0x00-0x1F`) except `\t`, `\n`, `\r` and
// the BEL bytes used in OSC terminators (we strip the surrounding
// OSC anyway), plus C1 (`0x80-0x9F`). It runs once at the engine
// publish boundary so every TUI/web/JSONL consumer sees clean
// bytes — no per-consumer remembering.
//
// We deliberately do NOT strip:
//   - `\t`, `\n`: needed for legitimate multi-line tool output
//   - Plain printable ASCII / UTF-8: the model emits these as
//     normal content
//   - Backspace `\x08`: kept because some older tool outputs use
//     it for over-strike rendering; it cannot rewrite history far
//     enough to forge content under any modern emulator
//
// We DO strip:
//   - `\x1b` (ESC) — the entry byte for every CSI / OSC / DCS
//     sequence. Removing ESC alone neutralises every ANSI exploit
//     because the trailing bytes become inert ASCII.
//   - `\x9b` (CSI in C1), `\x9d` (OSC in C1), `\x9e` (PM), `\x9f`
//     (APC), `\x90` (DCS) — same reasoning, just the 8-bit form.
//   - Other C0 control bytes that have no legitimate use in tool
//     output (`\x00`, vertical tab, form feed, etc.).

import (
	"strings"
	"unicode/utf8"
)

// stripTerminalControlBytes returns a copy of s with every
// terminal-control codepoint removed and every invalid UTF-8 byte
// dropped. Whitelist approach: tab, newline, and carriage-return
// survive; printable Unicode (codepoint >= 0x20 except the C1
// range) survives; everything else is dropped.
//
// We iterate codepoints with utf8.DecodeRuneInString (not Go's
// range-over-string idiom) for two reasons:
//
//  1. The C1 control range (U+0080-U+009F) overlaps the byte
//     range used by UTF-8 continuation bytes. A byte-wise filter
//     would shred legitimate multi-byte UTF-8 (Turkish, Japanese,
//     emoji, ...) that contains a continuation byte in [0x80, 0x9F].
//     As Unicode codepoints, C1 controls only match rune values
//     0x80-0x9F; valid multi-byte UTF-8 decodes to codepoints far
//     outside that window.
//
//  2. A standalone invalid byte (e.g. a raw 0x9B from a hostile
//     binary stream) decodes to U+FFFD with size=1 in Go. We need
//     to strip the underlying byte, not pass through the original
//     0x9B unchanged. DecodeRuneInString gives us the size so the
//     loop can advance one byte AND drop it.
//
// Single-pass; allocates only when the input contains a stripped
// rune. The hot path (clean input) returns the original string.
func stripTerminalControlBytes(s string) string {
	if s == "" {
		return ""
	}
	// Fast path: scan once. A clean string returns its original
	// header — no allocation, no copy. Tool output is plain UTF-8
	// in the steady state so this is the dominant path.
	if !needsTerminalStrip(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// Invalid UTF-8 byte — drop it. This catches the C1
			// control bytes that show up as raw 0x80-0x9F outside
			// any multi-byte sequence.
			i++
			continue
		}
		if isTerminalControlRune(r) {
			i += size
			continue
		}
		b.WriteRune(r)
		i += size
	}
	return b.String()
}

func needsTerminalStrip(s string) bool {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return true
		}
		if isTerminalControlRune(r) {
			return true
		}
		i += size
	}
	return false
}

// isTerminalControlRune reports whether r is a Unicode control
// codepoint that could drive the user's terminal emulator. Decision
// table by Unicode codepoint:
//
//	U+0000-U+0008 → strip (NUL, SOH, ..., BS) — no use in tool output
//	U+0009 \t      → keep
//	U+000A \n      → keep
//	U+000B-U+000C  → strip (VT, FF) — line discipline confusion
//	U+000D \r      → keep (Windows tools emit it; harmless on its own)
//	U+000E-U+001F  → strip (SO, SI, ESC at U+001B, RS, US, ...)
//	U+0080-U+009F  → strip (C1 set: NEL, SS3, CSI, ST, OSC, PM, APC, ...)
//
// Codepoints U+0020-U+007F (printable ASCII) and U+00A0+ (printable
// Unicode) survive. The U+007F DEL byte is intentionally allowed —
// no modern emulator interprets it as a control sequence on its own.
func isTerminalControlRune(r rune) bool {
	switch r {
	case '\t', '\n', '\r':
		return false
	}
	if r < 0x20 {
		return true
	}
	if r >= 0x80 && r <= 0x9f {
		return true
	}
	return false
}
