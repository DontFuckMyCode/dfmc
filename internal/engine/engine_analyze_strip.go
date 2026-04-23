// engine_analyze_strip.go — language-aware text strippers used by the
// dead-code, complexity, and duplication passes. Two surfaces:
//
//   - stripStringsAndComments: blanks out BOTH string/rune literals
//     AND comments. Used by the dead-code detector so a symbol name
//     mentioned inside a help-text string ISN'T counted as a real
//     reference.
//   - stripCommentsOnly: blanks out comments only, leaves string
//     literals intact. Used by the duplication detector so struct
//     literals that differ only in their string data ("review" vs
//     "explain") don't collapse into false clones.
//
// Both dispatch to C-family (`//`, `/* */`, single/double/backtick
// quotes with `\` escapes) or Python (`#`, `"""..."""` /
// `”'...”'`, single-line string handling) implementations. All
// replacements are byte-for-space: line numbers and column alignment
// survive stripping so downstream line-oriented passes stay accurate.

package engine

import "strings"

// stripStringsAndComments removes string literals and comments from
// source so a symbol's name occurring inside them does not inflate
// the "looks used" count. Extension-keyed language heuristics:
//
//   - .go / .js / .jsx / .ts / .tsx / .java / .c / .cpp / .cs: `//`
//     line comments, `/* ... */` block comments (cross-line),
//     double-quoted strings with `\` escapes, single-quoted runes,
//     backtick raw strings.
//   - .py: `#` line comments, `"""..."""` / `”'...”'` triple-quoted
//     docstrings (cross-line), single-line "..." and '...' strings.
//   - others: no stripping — a conservative choice that may leave
//     false-usage mentions for unknown languages.
//
// Replaced characters become spaces so line numbers and whitespace
// alignment stay intact for any downstream line-oriented analysis.
func stripStringsAndComments(text, ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "go", "js", "jsx", "mjs", "cjs", "ts", "tsx",
		"java", "c", "cpp", "cc", "h", "hpp", "cs", "rs":
		return stripCFamily(text)
	case "py", "pyw":
		return stripPython(text)
	}
	return text
}

// stripCommentsOnly removes comments but preserves string literals.
// Used by callers (duplication detector) that need to distinguish
// struct-literal tables with different data from real copy-paste —
// without string content, `Name: "review"` and `Name: "explain"`
// collapse to the same normalised line and report as a clone even
// though the semantic content differs. Dead-code and similar
// occurrence-counting passes still use stripStringsAndComments
// because a symbol merely mentioned in a help-text string ISN'T a
// real usage.
func stripCommentsOnly(text, ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "go", "js", "jsx", "mjs", "cjs", "ts", "tsx",
		"java", "c", "cpp", "cc", "h", "hpp", "cs", "rs":
		return stripCFamilyComments(text)
	case "py", "pyw":
		return stripPythonComments(text)
	}
	return text
}

// stripCFamilyComments blanks out line (`//`) and block (`/* ... */`)
// comments while leaving string / rune / backtick literals intact.
// Mirrors stripCFamily's structure so the behaviour is easy to
// reason about.
func stripCFamilyComments(text string) string {
	b := []byte(text)
	out := make([]byte, len(b))
	copy(out, b)
	i := 0
	for i < len(out) {
		c := out[i]
		if c == '/' && i+1 < len(out) && out[i+1] == '/' {
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(out) && out[i+1] == '*' {
			for i < len(out) {
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i] = ' '
					out[i+1] = ' '
					i += 2
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote := c
			i++
			for i < len(out) {
				if quote != '`' && out[i] == '\\' && i+1 < len(out) {
					i += 2
					continue
				}
				if out[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		i++
	}
	return string(out)
}

// stripPythonComments blanks out `#` line comments and `"""..."""` /
// `”'...”'` docstrings while leaving ordinary string literals
// intact. Single-quoted / double-quoted single-line strings are
// preserved — they carry the data we need to distinguish struct /
// dict entries.
func stripPythonComments(text string) string {
	b := []byte(text)
	out := make([]byte, len(b))
	copy(out, b)
	i := 0
	for i < len(out) {
		c := out[i]
		if c == '#' {
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		if (c == '"' || c == '\'') && i+2 < len(out) && out[i+1] == c && out[i+2] == c {
			quote := c
			out[i] = ' '
			out[i+1] = ' '
			out[i+2] = ' '
			i += 3
			for i < len(out) {
				if i+2 < len(out) && out[i] == quote && out[i+1] == quote && out[i+2] == quote {
					out[i] = ' '
					out[i+1] = ' '
					out[i+2] = ' '
					i += 3
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		// Leave single-line strings alone.
		if c == '"' || c == '\'' {
			quote := c
			i++
			for i < len(out) && out[i] != '\n' {
				if out[i] == '\\' && i+1 < len(out) {
					i += 2
					continue
				}
				if out[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		i++
	}
	return string(out)
}

func stripCFamily(text string) string {
	b := []byte(text)
	out := make([]byte, len(b))
	copy(out, b)
	i := 0
	for i < len(out) {
		c := out[i]
		// Line comment
		if c == '/' && i+1 < len(out) && out[i+1] == '/' {
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		// Block comment
		if c == '/' && i+1 < len(out) && out[i+1] == '*' {
			for i < len(out) {
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i] = ' '
					out[i+1] = ' '
					i += 2
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		// String / rune / raw-string literal
		if c == '"' || c == '\'' || c == '`' {
			quote := c
			out[i] = ' '
			i++
			for i < len(out) {
				if quote != '`' && out[i] == '\\' && i+1 < len(out) {
					out[i] = ' '
					out[i+1] = ' '
					i += 2
					continue
				}
				if out[i] == quote {
					out[i] = ' '
					i++
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		i++
	}
	return string(out)
}

func stripPython(text string) string {
	b := []byte(text)
	out := make([]byte, len(b))
	copy(out, b)
	i := 0
	for i < len(out) {
		c := out[i]
		if c == '#' {
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		// Triple-quoted docstrings.
		if (c == '"' || c == '\'') && i+2 < len(out) && out[i+1] == c && out[i+2] == c {
			quote := c
			out[i] = ' '
			out[i+1] = ' '
			out[i+2] = ' '
			i += 3
			for i < len(out) {
				if i+2 < len(out) && out[i] == quote && out[i+1] == quote && out[i+2] == quote {
					out[i] = ' '
					out[i+1] = ' '
					out[i+2] = ' '
					i += 3
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		// Single-quoted / double-quoted strings (single-line).
		if c == '"' || c == '\'' {
			quote := c
			out[i] = ' '
			i++
			for i < len(out) && out[i] != '\n' {
				if out[i] == '\\' && i+1 < len(out) {
					out[i] = ' '
					out[i+1] = ' '
					i += 2
					continue
				}
				if out[i] == quote {
					out[i] = ' '
					i++
					break
				}
				out[i] = ' '
				i++
			}
			continue
		}
		i++
	}
	return string(out)
}
