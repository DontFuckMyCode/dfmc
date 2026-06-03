package tools

// apply_patch_hunks.go — applyHunks runs each parsed hunk against the
// original file content with anchor-based splice + small ±10-line
// fuzz window for line-drift tolerance, while preserving the file's
// dominant line ending so an LF patch on a CRLF source doesn't flip
// every line. Companion siblings:
//
//   - apply_patch.go        ApplyPatchTool + Execute
//   - apply_patch_parse.go  parseUnifiedDiff + diffFile/Hunk/Line
//                           types + stripPathPrefix + parseHunkHeader
//                           + parseRange + atoiSafe

import (
	"strings"
)

// applyHunks runs each hunk against the original text. Strategy:
//  1. Use OldStart as the first guess; if context + deletions match there,
//     apply.
//  2. Otherwise, scan forward/backward a small window looking for a match.
//  3. If still no match, mark the hunk rejected (don't force).
func applyHunks(original string, hunks []diffHunk, isNew bool) (string, int, int, []int, error) {
	if isNew {
		// For a new file, all context/removal lines should be zero; just
		// emit the '+' lines.
		var b strings.Builder
		applied := 0
		for _, h := range hunks {
			for _, l := range h.Lines {
				if l.Kind == '+' {
					b.WriteString(l.Text)
					b.WriteByte('\n')
				}
			}
			applied++
		}
		out := b.String()
		return out, applied, 0, nil, nil
	}

	lines := splitKeepNewline(original)
	// Detect the source file's dominant newline so emitted replacement
	// lines keep the same style as the rest of the file. Without this,
	// an LF patch applied to a CRLF source produced mixed line endings
	// (or, when the hunk spanned the whole file, a silent EOL flip),
	// which showed up in git as unwanted whole-file diff noise.
	fileEnding := "\n"
	if strings.Contains(original, "\r\n") {
		fileEnding = "\r\n"
	}
	applied, rejected := 0, 0
	fuzzyOffsets := make([]int, 0, len(hunks))
	// Running offset between original-file line coords (what each
	// hunk's OldStart describes) and the current lines-buffer coords
	// (what findHunkAnchor probes). Every applied hunk that replaces
	// len(want) lines with len(middle) lines shifts every subsequent
	// hunk's true anchor by (len(middle) - len(want)). Without this,
	// the second+ hunks of a patch that net-adds or net-removes more
	// than findHunkAnchor's +-10 fuzz window were silently rejected
	// (the outer Result reports success but with hunks_rejected>0),
	// producing a partially-applied patch the caller couldn't easily
	// notice until the compile/test step blew up.
	offset := 0

	for _, h := range hunks {
		// Build the sequence of lines that must be present in the source
		// (context + '-' lines), and the replacement sequence ('+' + context).
		var want []string
		var replace []string
		for _, l := range h.Lines {
			switch l.Kind {
			case ' ':
				want = append(want, l.Text)
				replace = append(replace, l.Text)
			case '-':
				want = append(want, l.Text)
			case '+':
				replace = append(replace, l.Text)
			}
		}
		if len(want) == 0 && len(replace) == 0 {
			// A hunk with no context, additions, or deletions carries no
			// change and can only come from a malformed patch (a well-formed
			// unified-diff hunk always has a body). Count it as rejected
			// rather than silently skipping: otherwise applied+rejected fails
			// to account for every hunk, and a patch made entirely of empty
			// hunks reports "0 rejected" — a false success the agent loop
			// would treat as "my edit landed" when nothing changed.
			rejected++
			continue
		}

		anchor := h.OldStart - 1 + offset
		if anchor < 0 {
			anchor = 0
		}
		idx, fuzzyOffset := findHunkAnchor(lines, want, anchor)
		if idx < 0 {
			rejected++
			continue
		}
		if fuzzyOffset != 0 {
			fuzzyOffsets = append(fuzzyOffsets, fuzzyOffset)
		}
		// Splice: replace lines[idx : idx+len(want)] with `replace` (preserve
		// trailing newlines per original line).
		before := append([]string{}, lines[:idx]...)
		after := append([]string{}, lines[idx+len(want):]...)
		middle := make([]string, 0, len(replace))
		for _, r := range replace {
			// Strip any trailing CR the patch carried (e.g. a CRLF patch
			// pasted from a Windows IDE) so we don't leak CR bytes into an
			// LF source, then append the file's own ending detected above.
			r = strings.TrimRight(r, "\r")
			middle = append(middle, r+fileEnding)
		}
		// If the last original line lacked a trailing newline and the replace
		// region touches the end, preserve that.
		if idx+len(want) == len(lines) && len(lines) > 0 && !strings.HasSuffix(lines[len(lines)-1], "\n") && len(middle) > 0 {
			middle[len(middle)-1] = strings.TrimSuffix(middle[len(middle)-1], fileEnding)
		}
		lines = append(before, append(middle, after...)...)
		offset += len(middle) - len(want)
		applied++
	}

	return strings.Join(lines, ""), applied, rejected, fuzzyOffsets, nil
}

// findHunkAnchor searches for the contiguous sequence `want` in `lines`,
// preferring the hint anchor, then expanding outward.
func findHunkAnchor(lines, want []string, hint int) (int, int) {
	if len(want) == 0 {
		// Pure insertion (only '+' lines): there is no anchor sequence to
		// match, so the hint IS the insertion point. Clamp it into
		// [0, len(lines)] — an out-of-range OldStart (e.g. a patch inserting
		// past EOF on a short or empty file) would otherwise make the
		// caller's lines[:idx] / lines[idx:] splice panic with a slice-bounds
		// error.
		if hint < 0 {
			hint = 0
		}
		if hint > len(lines) {
			hint = len(lines)
		}
		return hint, 0
	}
	// Normalize both sides: drop trailing CR+LF so CRLF-ended source
	// files match against LF-normalized hunks (and vice-versa). Without
	// the \r strip, `foo\r\n` trimmed to `foo\r` would never equal a
	// hunk's `foo` — a silent "all hunks rejected" on Windows files.
	trim := func(s string) string { return strings.TrimRight(s, "\r\n") }

	match := func(at int) bool {
		if at < 0 || at+len(want) > len(lines) {
			return false
		}
		for i, w := range want {
			if trim(lines[at+i]) != trim(w) {
				return false
			}
		}
		return true
	}

	if match(hint) {
		return hint, 0
	}
	// Fuzzy anchor search: expand outward a small window around the hint.
	// Previously we expanded ±200 lines and then linear-scanned the whole
	// file as a last resort — that turned a stale-context patch into a
	// silently-misplaced edit far from the intended site (REPORT H1, the
	// "patch landed in the wrong function" class of bug). A tight window
	// keeps the fuzz forgiving for normal drift (a few intervening edits)
	// while letting truly stale hunks fail loudly so the caller re-reads
	// the file.
	const maxFuzz = 10
	for delta := 1; delta <= maxFuzz; delta++ {
		if match(hint + delta) {
			return hint + delta, delta
		}
		if match(hint - delta) {
			return hint - delta, -delta
		}
	}
	return -1, 0
}

// splitKeepNewline splits content on '\n' but keeps the trailing newline on
// each line so reassembly preserves byte identity.
func splitKeepNewline(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i+1])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
