// Near-duplicate code detector. Deterministic, offline, language-aware
// via the existing strip-strings-and-comments helpers. The approach
// is a rolling-hash over sliding windows of normalized non-blank
// source lines:
//
//   1. Strip strings + comments so trivial mentions don't match.
//   2. Collapse runs of whitespace, trim each line, drop blanks.
//   3. For each file, emit every window of `minLines` consecutive
//      normalized lines. Hash with FNV-1a 64.
//   4. Group by hash; bucket with >=2 entries is a duplicate.
//   5. Cluster: merge overlapping windows in the same group into the
//      longest contiguous run so the report doesn't list every
//      sub-window separately (a 12-line clone at min=6 otherwise
//      shows up as 7 overlapping hits per location).
//
// The detector is line-based, NOT AST-based, which means it catches
// copy-paste even when the surrounding syntax differs slightly — but
// misses clones that are semantically identical with renamed
// identifiers. That's an acceptable tradeoff for the first pass; a
// later pass can normalize identifiers for type-2 clone detection.

package engine

import (
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// Minimum run of non-blank normalized lines to consider a clone.
	// 6 is the pmd-cpd/SonarQube default — long enough that matches
	// aren't just "three imports and a brace", short enough to catch
	// real copy-paste.
	duplicationMinLines = 6
	// Hard cap on lines per file — gigantic generated files would
	// dominate the bucket map and aren't actionable anyway.
	duplicationMaxLinesPerFile = 20000
	// Upper bound on reported groups so JSON stays small.
	duplicationMaxGroups = 50
)

// isLowSignalWindow reports whether a normalized window is too
// structural to count as real duplication. The heuristic:
//
//   - A line is "structural" if, after stripping strings + comments
//     and collapsing whitespace, it consists only of braces, parens,
//     commas, a bare `return` / `break` / `continue` / `else`, or is
//     the standard Go `if err != nil {` pattern.
//   - A window is low-signal when at least 75% of its lines are
//     structural — six lines of `}\n}\n}\nreturn err\n}\nif err != nil {`
//     is the kind of shape we want to ignore.
//
// 75% is deliberately lenient: a window with 1-2 real lines and the
// rest closing braces still has actionable content, but a window
// that's 5-of-6 boilerplate doesn't.
func isLowSignalWindow(window []normalizedLine) bool {
	if len(window) == 0 {
		return true
	}
	structural := 0
	for _, line := range window {
		if isStructuralLine(line.text) {
			structural++
		}
	}
	return structural*4 >= len(window)*3
}

// isStructuralLine answers the per-line question for isLowSignalWindow.
// The per-line classifier tries to recognise every "framework" shape
// a source line can take that carries no semantic signal for
// duplicate detection: punctuation runs, bare keywords, package and
// import scaffolding, and bare string-literal lines (import entries
// that look like `"context"`).
func isStructuralLine(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return true
	}
	// Runs of pure punctuation (}, ), },, )},, etc.) are scaffolding.
	allPunct := true
	for i := 0; i < len(t); i++ {
		c := t[i]
		if !(c == '{' || c == '}' || c == '(' || c == ')' ||
			c == '[' || c == ']' || c == ',' || c == ';' ||
			c == ' ' || c == '\t') {
			allPunct = false
			break
		}
	}
	if allPunct {
		return true
	}
	// Common single-keyword scaffolding lines.
	switch t {
	case "return", "break", "continue", "else", "else {",
		"return nil", "return err", "return false", "return true",
		"} else {", "})", "});":
		return true
	}
	// Go error-check boilerplate — idiomatic, not copy-paste.
	if t == "if err != nil {" || t == "if err != nil {}" {
		return true
	}
	// Package and import scaffolding. `package foo`, `import (`,
	// `import "fmt"`, `import . "x"`, `from x import y` (Python),
	// `import y from 'x'` (TS) are file-preamble, not logic.
	if strings.HasPrefix(t, "package ") ||
		strings.HasPrefix(t, "import ") ||
		t == "import (" ||
		strings.HasPrefix(t, "from ") {
		return true
	}
	// Bare string-literal lines, like the entries inside an
	// `import ( ... )` block: `"context"`, `"encoding/json"`, also
	// `"context",` with a trailing comma. One pair of quotes wrapping
	// the entire line content (plus optional trailing `,` / `;`) is
	// a strong import-entry signal.
	if isBareStringLine(t) {
		return true
	}
	return false
}

// isBareStringLine reports whether a line is just a single string
// literal (optionally followed by `,` or `;`). Covers the classic
// Go / TS / Python import-entry shape where each line is one module
// path. Matches `"context"`, `"encoding/json",`, `'react';`, but
// NOT `s := "hello"` (that has code before the quote).
func isBareStringLine(t string) bool {
	if t == "" {
		return false
	}
	// Strip trailing `,` / `;`.
	for len(t) > 0 {
		last := t[len(t)-1]
		if last == ',' || last == ';' {
			t = strings.TrimSpace(t[:len(t)-1])
			continue
		}
		break
	}
	if len(t) < 2 {
		return false
	}
	first := t[0]
	last := t[len(t)-1]
	if first != last {
		return false
	}
	if first != '"' && first != '\'' && first != '`' {
		return false
	}
	// Content must not itself contain an unescaped closing quote
	// before the end — avoid matching weird shapes like `"a" + "b"`.
	inner := t[1 : len(t)-1]
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) {
			i++
			continue
		}
		if inner[i] == first {
			return false
		}
	}
	return true
}

// isTestFilePath reports whether a path looks like a test file the
// detector should skip. Go tests (`*_test.go`), Python unit-test
// modules (`test_*.py`, `*_test.py`, `tests/...`), and TS/JS test /
// spec files follow well-known naming conventions. Flagging the
// scaffolding they share produces noise; real refactor targets live
// in product code.
func isTestFilePath(path string) bool {
	p := strings.ToLower(filepath.ToSlash(path))
	base := filepath.Base(p)
	if strings.HasSuffix(p, "_test.go") {
		return true
	}
	if strings.HasSuffix(p, "_test.py") || strings.HasPrefix(base, "test_") {
		return true
	}
	if strings.HasSuffix(p, ".spec.ts") || strings.HasSuffix(p, ".test.ts") ||
		strings.HasSuffix(p, ".spec.tsx") || strings.HasSuffix(p, ".test.tsx") ||
		strings.HasSuffix(p, ".spec.js") || strings.HasSuffix(p, ".test.js") ||
		strings.HasSuffix(p, ".spec.jsx") || strings.HasSuffix(p, ".test.jsx") {
		return true
	}
	// `tests/` or `test/` directory component anywhere in the path
	// — broad but high-signal for Python / JS conventions that group
	// tests by folder. Matches both absolute-ish `/tests/foo.py` and
	// relative `tests/foo.py` / `test/helpers.js`.
	if strings.Contains(p, "/tests/") || strings.Contains(p, "/test/") ||
		strings.HasPrefix(p, "tests/") || strings.HasPrefix(p, "test/") {
		return true
	}
	return false
}

// windowEntry is one occurrence of a normalized window in some file.
type windowEntry struct {
	File      string
	StartLine int // original 1-indexed source line
	EndLine   int // inclusive original source line
}

type normalizedLine struct {
	origLine int // 1-indexed original source line
	text     string
}

func detectDuplication(paths []string, minLines int) DuplicationReport {
	if minLines <= 0 {
		minLines = duplicationMinLines
	}
	report := DuplicationReport{
		MinLines:     minLines,
		FilesScanned: 0,
	}
	if len(paths) == 0 {
		return report
	}

	buckets := map[uint64][]windowEntry{}

	for _, path := range paths {
		// Skip test files — test boilerplate (setup helpers, table
		// initialisation, similar assertion blocks) legitimately
		// repeats across files and flagging it as "duplication"
		// produces noise, not actionable refactor targets. Users
		// who want to dedupe test helpers can still detect them by
		// inspection; the scanner's job is to highlight real
		// product-code copy-paste.
		if isTestFilePath(path) {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		report.FilesScanned++

		// stripCommentsOnly (not stripStringsAndComments): keep string
		// literals intact so that struct-literal tables with different
		// data (Name: "review" vs Name: "explain") don't collapse to
		// the same normalised window. Comments still stripped so a
		// doc-comment block doesn't register as a code clone.
		stripped := stripCommentsOnly(string(content), filepath.Ext(path))
		norm := normalizeForDuplication(stripped)
		if len(norm) > duplicationMaxLinesPerFile {
			norm = norm[:duplicationMaxLinesPerFile]
		}
		if len(norm) < minLines {
			continue
		}

		for i := 0; i+minLines <= len(norm); i++ {
			window := norm[i : i+minLines]
			// Skip windows that are mostly structural scaffolding
			// (closing braces, `return err`, `if err != nil {`). Two
			// files sharing six such lines isn't copy-paste; it's
			// idiomatic Go. The filter keeps windows with real
			// semantic content (function calls, arithmetic, etc.).
			if isLowSignalWindow(window) {
				continue
			}
			h := hashNormalizedWindow(window)
			entry := windowEntry{
				File:      filepath.ToSlash(path),
				StartLine: window[0].origLine,
				EndLine:   window[len(window)-1].origLine,
			}
			buckets[h] = append(buckets[h], entry)
			report.WindowsHashed++
		}
	}

	groups := buildDuplicationGroups(buckets, minLines)
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Length != groups[j].Length {
			return groups[i].Length > groups[j].Length
		}
		if len(groups[i].Locations) != len(groups[j].Locations) {
			return len(groups[i].Locations) > len(groups[j].Locations)
		}
		if len(groups[i].Locations) == 0 || len(groups[j].Locations) == 0 {
			return false
		}
		return groups[i].Locations[0].File < groups[j].Locations[0].File
	})

	dupLines := 0
	for _, g := range groups {
		for _, loc := range g.Locations {
			dupLines += loc.EndLine - loc.StartLine + 1
		}
	}
	report.DuplicatedLines = dupLines

	if len(groups) > duplicationMaxGroups {
		groups = groups[:duplicationMaxGroups]
	}
	report.Groups = groups
	return report
}

// normalizeForDuplication collapses a file's lines into a slice of
// (origLine, normalizedText) pairs with blanks skipped. We keep the
// original line number so the final report can point at a real
// location in the source, even after comment stripping rewrote lines
// to whitespace.
func normalizeForDuplication(text string) []normalizedLine {
	lines := strings.Split(text, "\n")
	out := make([]normalizedLine, 0, len(lines))
	for i, raw := range lines {
		norm := collapseWhitespace(raw)
		if norm == "" {
			continue
		}
		out = append(out, normalizedLine{origLine: i + 1, text: norm})
	}
	return out
}

func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true // leading whitespace is dropped
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\r' {
			if prevSpace {
				continue
			}
			b.WriteByte(' ')
			prevSpace = true
			continue
		}
		b.WriteByte(c)
		prevSpace = false
	}
	return strings.TrimSuffix(b.String(), " ")
}

func hashNormalizedWindow(window []normalizedLine) uint64 {
	h := fnv.New64a()
	for i, line := range window {
		if i > 0 {
			_, _ = h.Write([]byte{'\n'})
		}
		_, _ = h.Write([]byte(line.text))
	}
	return h.Sum64()
}

// buildDuplicationGroups turns raw hash buckets into DuplicationGroups.
// Overlapping windows inside the same file are folded into one span,
// so a 12-line clone at minLines=6 shows up as one group with spans
// of length 12 — not seven overlapping sub-windows.
func buildDuplicationGroups(buckets map[uint64][]windowEntry, minLines int) []DuplicationGroup {
	groups := make([]DuplicationGroup, 0, len(buckets))
	for _, entries := range buckets {
		if len(entries) < 2 {
			continue
		}
		g := mergeBucketToGroup(entries)
		if len(g.Locations) < 2 {
			// All overlapping hits were in a single file — one copy.
			continue
		}
		length := 0
		for _, loc := range g.Locations {
			span := loc.EndLine - loc.StartLine + 1
			if span > length {
				length = span
			}
		}
		if length < minLines {
			length = minLines
		}
		g.Length = length
		groups = append(groups, g)
	}
	return groups
}

// mergeBucketToGroup folds overlapping / adjacent windows in the same
// file into their outer span. The caller decides whether the result
// qualifies as a clone (>= 2 locations).
func mergeBucketToGroup(entries []windowEntry) DuplicationGroup {
	byFile := map[string][]windowEntry{}
	for _, e := range entries {
		byFile[e.File] = append(byFile[e.File], e)
	}
	locs := make([]DuplicationLocation, 0, len(byFile))
	for file, es := range byFile {
		sort.Slice(es, func(i, j int) bool {
			return es[i].StartLine < es[j].StartLine
		})
		curStart := es[0].StartLine
		curEnd := es[0].EndLine
		for i := 1; i < len(es); i++ {
			if es[i].StartLine <= curEnd+1 {
				if es[i].EndLine > curEnd {
					curEnd = es[i].EndLine
				}
				continue
			}
			locs = append(locs, DuplicationLocation{File: file, StartLine: curStart, EndLine: curEnd})
			curStart = es[i].StartLine
			curEnd = es[i].EndLine
		}
		locs = append(locs, DuplicationLocation{File: file, StartLine: curStart, EndLine: curEnd})
	}
	sort.Slice(locs, func(i, j int) bool {
		if locs[i].File != locs[j].File {
			return locs[i].File < locs[j].File
		}
		return locs[i].StartLine < locs[j].StartLine
	})
	return DuplicationGroup{Locations: locs}
}
