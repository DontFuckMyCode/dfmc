// engine_analyze_complexity.go — cyclomatic-complexity scoring pass
// for the analyze pipeline. Two surfaces:
//
//   - computeComplexity: orchestrates per-file + per-function scoring.
//     Parses each path via AST, slices function bodies out with
//     endOfFunctionBody, scores each segment, ranks, and truncates to
//     the top 20 functions / top 10 files.
//   - complexityScore: the actual metric — McCabe-ish keyword + branch
//     counter applied to arbitrary text. Language-agnostic: any
//     branch/loop/jump keyword in any supported language contributes
//     +1 toward the score.
//
// Supporting helpers stay colocated because they only make sense
// together:
//
//   - endOfFunctionBody / endOfBraceBody / endOfPythonBody: carve a
//     function's body out of a line slice. Brace-based langs track
//     `{` / `}` depth (respecting strings, runes, line / block
//     comments); Python walks until the indent drops back to or below
//     the def's own.
//   - skipStringLiteral: advances past a quoted literal so its braces
//     don't count toward depth.
//   - leadingWhitespaceLen: Python-only, measures the def's indent.
//   - complexityBranchRegexes: compiled ONCE at package init. Each
//     regex is word-boundary-anchored on the keyword side and loose
//     on the delimiter side (space / paren / brace / colon). Hot path
//     — every analyze run re-uses the same compiled set.
//   - looksEntrypoint: tags `main` / `init` / `test*` and anything in
//     a `_test.go` file as entrypoints. Used upstream by the dead-code
//     detector, not by complexity itself; colocated because they both
//     classify symbols by "role."

package engine

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func (e *Engine) computeComplexity(ctx context.Context, paths []string) (ComplexityReport, error) {
	report := ComplexityReport{Files: len(paths)}
	functions := make([]FunctionComplexity, 0, 128)
	fileScores := make([]FunctionComplexity, 0, len(paths))
	totalScore := 0
	maxScore := 0
	totalSymbols := 0
	scannedSymbols := 0

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(content)
		fileScore := complexityScore(text)
		fileScores = append(fileScores, FunctionComplexity{
			Name:  filepath.Base(path),
			File:  filepath.ToSlash(path),
			Line:  1,
			Score: fileScore,
		})
		totalScore += fileScore
		if fileScore > maxScore {
			maxScore = fileScore
		}

		if e.AST == nil {
			continue
		}
		res, err := e.AST.ParseContent(ctx, path, content)
		if err != nil {
			continue
		}
		totalSymbols += len(res.Symbols)
		lines := strings.Split(text, "\n")
		for _, sym := range res.Symbols {
			kind := strings.ToLower(string(sym.Kind))
			if kind != "function" && kind != "method" {
				continue
			}
			scannedSymbols++
			start := sym.Line - 1
			if start < 0 || start >= len(lines) {
				continue
			}
			// Slice the function body by tracking brace depth from the
			// declaration line. Works for Go, C, JS/TS, Java. Python
			// (indent-based) falls back to the next-symbol heuristic
			// via endByNextSymbol because Python has no '{'.
			end := endOfFunctionBody(lines, start, res.Language)
			if end <= start {
				end = start + 1
			}
			segment := strings.Join(lines[start:minInt(end, len(lines))], "\n")
			score := complexityScore(segment)
			functions = append(functions, FunctionComplexity{
				Name:  sym.Name,
				File:  filepath.ToSlash(path),
				Line:  sym.Line,
				Score: score,
			})
		}
	}

	report.Max = maxScore
	if len(fileScores) > 0 {
		report.Average = math.Round((float64(totalScore)/float64(len(fileScores)))*100) / 100
	}
	report.TotalSymbols = totalSymbols
	report.ScannedSymbol = scannedSymbols

	sort.Slice(functions, func(i, j int) bool { return functions[i].Score > functions[j].Score })
	sort.Slice(fileScores, func(i, j int) bool { return fileScores[i].Score > fileScores[j].Score })
	if len(functions) > 20 {
		functions = functions[:20]
	}
	if len(fileScores) > 10 {
		fileScores = fileScores[:10]
	}
	report.TopFunctions = functions
	report.TopFiles = fileScores
	return report, nil
}

// complexityScore approximates McCabe cyclomatic complexity. It counts
// decision points using word-boundary regex so the scorer catches
// `if(x)` (no trailing space), tab-indented `\tif`, `}else if{`, etc.
// — all of which the previous space-padded substring variant missed.
// False positives from identifiers containing keyword substrings (e.g.
// `verifyUser`) are avoided by anchoring on `\b`.
//
// The score is language-agnostic: any branch/loop/jump keyword in any
// of the supported languages contributes +1. A function with zero
// branches returns 1 (the single entry path).
func complexityScore(text string) int {
	if text == "" {
		return 1
	}
	score := 1
	for _, re := range complexityBranchRegexes {
		score += len(re.FindAllStringIndex(text, -1))
	}
	return score
}

// endOfFunctionBody returns the (0-indexed) line AFTER the closing
// delimiter of the function that STARTS at `start`. For brace-based
// languages it tracks `{` / `}` depth while respecting strings, runes,
// and line/block comments. For Python (indent-based) it walks until a
// non-blank line's indentation drops to or below the function's own
// indent. If neither strategy finds a clean end, returns `len(lines)`
// so the caller still gets a sensible segment (whole rest of file).
func endOfFunctionBody(lines []string, start int, language string) int {
	if start < 0 || start >= len(lines) {
		return len(lines)
	}
	lang := strings.ToLower(strings.TrimSpace(language))
	if lang == "python" {
		return endOfPythonBody(lines, start)
	}
	return endOfBraceBody(lines, start)
}

// endOfBraceBody walks lines from `start`, counting balanced braces
// outside strings/comments. Stops one line past the line where depth
// returns to zero AFTER having been positive at least once. This is
// resilient to nested closures — the body of an outer function
// legitimately contains many `{}` pairs and only the outermost match
// closes it.
func endOfBraceBody(lines []string, start int) int {
	depth := 0
	opened := false
	inBlockComment := false
	for i := start; i < len(lines); i++ {
		line := lines[i]
		j := 0
		for j < len(line) {
			if inBlockComment {
				if j+1 < len(line) && line[j] == '*' && line[j+1] == '/' {
					inBlockComment = false
					j += 2
					continue
				}
				j++
				continue
			}
			// Line comment — rest of the line is not code.
			if j+1 < len(line) && line[j] == '/' && line[j+1] == '/' {
				break
			}
			// Block comment start.
			if j+1 < len(line) && line[j] == '/' && line[j+1] == '*' {
				inBlockComment = true
				j += 2
				continue
			}
			// Skip string / rune literals so their braces don't count.
			if c := line[j]; c == '"' || c == '\'' || c == '`' {
				j = skipStringLiteral(line, j)
				continue
			}
			if line[j] == '{' {
				depth++
				opened = true
			} else if line[j] == '}' {
				depth--
				if opened && depth <= 0 {
					return i + 1
				}
			}
			j++
		}
	}
	return len(lines)
}

// skipStringLiteral returns the index of the character AFTER the
// closing quote of a string/rune/backtick literal starting at
// `line[start]`. Respects escape sequences for "" and ''. Backtick
// (raw) strings don't honour escapes in Go. If the literal doesn't
// close on this line (multi-line raw strings), returns len(line).
func skipStringLiteral(line string, start int) int {
	if start >= len(line) {
		return start
	}
	quote := line[start]
	j := start + 1
	for j < len(line) {
		c := line[j]
		if quote != '`' && c == '\\' {
			j += 2
			continue
		}
		if c == quote {
			return j + 1
		}
		j++
	}
	return len(line)
}

// endOfPythonBody walks until a non-blank line whose indentation is
// ≤ the def's indentation. That line belongs to the enclosing scope,
// so the function ends the line before.
func endOfPythonBody(lines []string, start int) int {
	defIndent := leadingWhitespaceLen(lines[start])
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if leadingWhitespaceLen(line) <= defIndent {
			return i
		}
	}
	return len(lines)
}

func leadingWhitespaceLen(s string) int {
	n := 0
	for n < len(s) && (s[n] == ' ' || s[n] == '\t') {
		n++
	}
	return n
}

// complexityBranchRegexes is compiled once and reused; compiling per
// call is expensive and shows up in profiles for big codebases. Each
// regex is word-boundary-anchored on the keyword side and loose on
// the delimiter side (accepts space / paren / brace / line end).
var complexityBranchRegexes = func() []*regexp.Regexp {
	keywords := []string{
		"if", "else if", "elif",
		"for", "while", "do",
		"switch", "case",
		"catch", "except", "rescue", "finally",
		"goto",
	}
	out := make([]*regexp.Regexp, 0, len(keywords)+3)
	for _, kw := range keywords {
		// Keyword must be preceded by non-word OR start-of-string,
		// and followed by a space/paren/brace/colon. `\b...\b` alone
		// would match inside identifiers when followed by whitespace
		// only, which is why we also require the trailing-char class.
		out = append(out, regexp.MustCompile(`(^|\W)`+regexp.QuoteMeta(kw)+`[\s(:{]`))
	}
	// Short-circuit boolean operators — one decision per && / ||.
	out = append(out,
		regexp.MustCompile(`&&`),
		regexp.MustCompile(`\|\|`),
	)
	// Ternary: match `?` followed by non-punct so we don't count
	// `foo?.bar` (JS optional chaining) or `type?` annotations.
	out = append(out, regexp.MustCompile(`\?\s`))
	return out
}()

func looksEntrypoint(name, file string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "main" || n == "init" {
		return true
	}
	if strings.HasPrefix(n, "test") {
		return true
	}
	base := strings.ToLower(filepath.Base(file))
	return strings.HasSuffix(base, "_test.go")
}
