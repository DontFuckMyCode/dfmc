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
// Sibling: engine_analyze_complexity_body.go owns the function-body
// slicers (endOfFunctionBody / endOfBraceBody / endOfPythonBody +
// skipStringLiteral + leadingWhitespaceLen) — splitting them out
// keeps this file focused on "what is the score" while the sibling
// owns "where does the function actually end so we know what to
// score."
//
// What stays here:
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
	if ctx == nil {
		ctx = context.Background()
	}
	report := ComplexityReport{Files: len(paths)}
	functions := make([]FunctionComplexity, 0, 128)
	fileScores := make([]FunctionComplexity, 0, len(paths))
	totalScore := 0
	maxScore := 0
	totalSymbols := 0
	scannedSymbols := 0

	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return report, err
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
//
// CAUTION: use a negative limit with care. On large texts, patterns
// with high repetition (e.g. `&&`, `||`) can trigger catastrophic
// backtracking in Go's backtracking NFA engine. The short-circuit
// operators are safe only because their literals are single tokens
// — but a generic `.*` anywhere in the pattern would be catastrophic.
// We apply a per-call match cap to bound the worst case.
//
// The file-level score is the simple sum over the entire text; the
// per-function score uses the same scorer on the function body only.
func complexityScore(text string) int {
	if text == "" {
		return 1
	}
	score := 1
	// 500 is a safe upper bound: a pathological function body with
	// 200 consecutive if/else-if chains has <200 branches. The cap
	// bounds backtracking worst-case on large inputs (a whole-file
	// score call sees the entire file at once). Using the same cap
	// for both file-level and per-function scoring avoids a second
	// tuning parameter.
	const perPatternCap = 500
	for _, re := range complexityBranchRegexes {
		score += len(re.FindAllStringIndex(text, perPatternCap))
	}
	return score
}

// endOfFunctionBody + endOfBraceBody + skipStringLiteral +
// endOfPythonBody + leadingWhitespaceLen live in
// engine_analyze_complexity_body.go.

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
