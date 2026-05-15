// AST-aware vulnerability scanner.
//
// The regex scanner in scanner.go is fast and broad but produces real
// false positives (observed: `exec.Command("git", "-C", root, "diff")`
// flagged as CWE-78 even though every argument is a literal and the
// Go runtime never spawns a shell). This scanner layers a second,
// smarter pass on top:
//
//   * For each sink (exec, sql query, eval, deserialize), inspect the
//     argument list. A call whose arguments are ALL string / numeric
//     literals is treated as safe — literal commands can't be
//     hijacked by user input.
//   * Concatenation and formatted strings (`+`, `fmt.Sprintf`,
//     template literals, f-strings, `.format(...)`) touching a sink's
//     arguments are kept as findings — they're the real exploit path.
//   * Per-language sinks capture the patterns the regex can't see
//     (e.g. `subprocess.call(..., shell=True)` where shell=True
//     IS the condition, not the command string).
//
// Not a replacement for the regex scanner; both run and their results
// merge through deduplicateFindings so a file/line never appears twice
// for the "same" issue. When a regex rule and an AST rule both match
// at the same line, the AST rule wins because it carries richer
// classification.

package security

import (
	"bufio"
	"bytes"
	"path/filepath"
	"strings"
)

// astRule is one smart-scan rule. The matcher receives the current
// line + a small "recent lines" ring so multi-line constructs like
// `q := "SELECT ... " + \n   userInput` can still be caught.
type astRule struct {
	Name     string
	Severity string
	CWE      string
	OWASP    string
	Langs    []string // applicable languages: "go", "javascript", "typescript", "python"
	Match    func(ctx *scanLineCtx) bool
}

// scanLineCtx is the per-line slice the matcher sees.
type scanLineCtx struct {
	Lang       string
	LineNo     int
	Line       string // current source line
	Trimmed    string // strings.TrimSpace(Line)
	RecentJoin string // last ~3 source lines joined on space, for multi-line patterns
	// Taint is the per-file taint tracker, populated by observeLine on
	// every prior line in the same file. Rules check IsTainted(name) at
	// sink call sites to detect the multi-step `body := r.Body; sink(body)`
	// flow that the concat-only rules can't see. nil-safe: rules that
	// don't need it can ignore the field. See astscan_taint.go.
	Taint *taintTracker
}

// detectLanguageFromPath is a minimal extension-to-language map; the
// real ast.Engine has a richer one, but pulling that in here would
// force a cyclic dep (engine imports security). Keep in sync with
// the rules' Langs values.
func detectLanguageFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".py", ".pyw":
		return "python"
	case ".rb", ".rake":
		return "ruby"
	case ".java":
		return "java"
	}
	// Filename-based detection: a handful of Ruby files have no
	// extension by convention.
	base := filepath.Base(path)
	switch base {
	case "Gemfile", "Rakefile":
		return "ruby"
	}
	return ""
}

// ScanASTRules runs the smart-scan pass and returns findings. The
// regex-based scanner is in scanner.go; this complements it and both
// feed Scanner.ScanContent.
func (s *Scanner) ScanASTRules(path string, content []byte) []VulnerabilityFinding {
	lang := detectLanguageFromPath(path)
	if lang == "" {
		return nil
	}
	var findings []VulnerabilityFinding

	// Ring buffer for "recent lines" context — cheap way to catch
	// multi-line concatenation. Three lines has been enough in
	// practice; patterns that span more are rare and usually dead.
	ring := newLineRing(3)

	// Per-file taint tracker. Sees every non-comment line so rules can
	// query identifier taintedness at sink call sites. See astscan_taint.go.
	taint := newTaintTracker(lang)

	// Function-scope tracker (dfmc_report_ast.md §R8 slice 2). For
	// languages where we can recognise function boundaries cheaply
	// (Go for now; Python / JS / TS in follow-up slices), the
	// scanner pushes a fresh tracker scope on function entry and
	// pops it on function exit. The result is that idents tainted
	// in function A are NOT visible to a sink call in function B
	// even when both share the same source file -- a real precision
	// improvement that eliminates the "name reuse across functions"
	// false-positive class the package header used to call out.
	//
	// Languages we don't yet handle (Python, JS / TS, Ruby, Java)
	// keep the file-scoped behaviour: the balancer never pushes or
	// pops, so the tracker stays at scope depth 1 -- identical to
	// the pre-R8 single-map implementation.
	scope := newScopeBalancer(lang)

	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		ring.push(trimmed)

		if trimmed == "" || isCommentLine(trimmed, lang) {
			continue
		}
		if isPatternDefinitionLine(trimmed) {
			// Rule-definition lines inside our own source shouldn't
			// trigger on themselves. Same guard the regex scanner uses.
			continue
		}

		// Apply scope transitions BEFORE observe + rules. A line that
		// ends a function (lone `}` for brace languages, indent drop
		// for Python) must pop BEFORE we observe so the taint state
		// on the line lives in the outer scope. A line that starts a
		// function pushes AFTER we observe so the declaration itself
		// reads as part of the outer scope (function name / params
		// don't get tainted in the new inner scope).
		//
		// Both `line` (raw) and `trimmed` are passed: brace
		// languages key off `trimmed`, the Python path measures
		// leading whitespace on `line` for indent tracking.
		scope.preObserve(line, trimmed, taint)

		// Update the tracker BEFORE running rules so a rule that
		// queries taintedness on its OWN line sees only prior-line
		// state. Otherwise `x := r.Body` would also count itself as a
		// tainted-arg use on the same line.
		taint.observeLine(trimmed)

		// Post-observe scope entry. A function-declaration line
		// observed in the outer scope, then a new scope opens for
		// the body to flow into.
		scope.postObserve(line, trimmed, taint)

		ctx := &scanLineCtx{
			Lang:       lang,
			LineNo:     lineNo,
			Line:       line,
			Trimmed:    trimmed,
			RecentJoin: ring.joined(),
			Taint:      taint,
		}
		for _, rule := range astRulesForLang(lang) {
			if !rule.Match(ctx) {
				continue
			}
			findings = append(findings, VulnerabilityFinding{
				Kind:     rule.Name,
				File:     toSlash(path),
				Line:     lineNo,
				Severity: rule.Severity,
				CWE:      rule.CWE,
				OWASP:    rule.OWASP,
				Snippet:  snippet(trimmed, 180),
			})
		}
	}
	return findings
}

// astRulesForLang returns the subset of registered rules that apply
// to a given language. Kept as a function call (not a map lookup)
// so tests can override easily without touching a package-level
// mutable state.
func astRulesForLang(lang string) []astRule {
	out := make([]astRule, 0, 16)
	for _, r := range allASTRules() {
		for _, l := range r.Langs {
			if l == lang {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// allASTRules collects every language's rules. Each language file
// (astscan_go.go, astscan_js.go, astscan_python.go) contributes via
// its own registration function.
func allASTRules() []astRule {
	var out []astRule
	out = append(out, goASTRules()...)
	out = append(out, jsASTRules()...)
	out = append(out, pythonASTRules()...)
	out = append(out, rubyASTRules()...)
	out = append(out, javaASTRules()...)
	return out
}

// argumentListAllLiterals + splitArgs + isLiteralArg +
// containsConcatOrFormat + hasBareCall + isIdentChar + isCommentLine
// + lineRing live in astscan_helpers.go.
