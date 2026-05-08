package engine

// duplication_heuristics.go — per-line / per-window classifiers that
// keep the duplication detector from flagging idiomatic boilerplate as
// copy-paste. Sibling to duplication.go which owns the rolling-hash
// pipeline (detectDuplication / normalizeForDuplication /
// collapseWhitespace / hashNormalizedWindow / buildDuplicationGroups /
// mergeBucketToGroup) and the report types.

import (
	"path/filepath"
	"strings"
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
