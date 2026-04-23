package provider

// offline_scanners.go — language-specific and language-agnostic code
// smell scanners used by the offline review pipeline.
//
// scanCommonIssues flags TODOs, FIXMEs, overly-long lines, and long
// functions regardless of language. scanLanguageIssues dispatches into
// the per-language regex scanners (Go, TS/JS, Python, Rust) — each one
// keeps its regex table inline so adding a new language only touches
// this file. Findings are flat []offlineFinding structs with path:line
// + severity + evidence; rendering and dedup lives back in offline
// _analyzer.go alongside the security/explain/debug report entry points.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// scanCommonIssues catches language-agnostic smells: TODOs, FIXMEs, long
// functions, commented-out code, trailing whitespace, overly-long lines.
func scanCommonIssues(ch types.ContextChunk) []offlineFinding {
	out := []offlineFinding{}
	lines := strings.Split(ch.Content, "\n")
	longFunctionStart := 0
	longFunctionName := ""

	for i, line := range lines {
		ln := lineNumber(ch, i)
		stripped := strings.TrimSpace(line)
		lower := strings.ToLower(stripped)

		if strings.Contains(lower, "todo") && (strings.HasPrefix(lower, "//") || strings.HasPrefix(lower, "#") || strings.HasPrefix(lower, "/*")) {
			out = append(out, offlineFinding{
				Severity: "low", Path: ch.Path, Line: ln,
				Category: "maintenance", Message: "TODO marker still present",
				Evidence: stripped,
			})
		}
		if strings.Contains(lower, "fixme") || strings.Contains(lower, "xxx:") || strings.Contains(lower, "hack:") {
			out = append(out, offlineFinding{
				Severity: "medium", Path: ch.Path, Line: ln,
				Category: "maintenance", Message: "FIXME/HACK marker indicates known defect",
				Evidence: stripped,
			})
		}
		if len(line) > 180 {
			out = append(out, offlineFinding{
				Severity: "low", Path: ch.Path, Line: ln,
				Category: "style", Message: fmt.Sprintf("line is %d chars (wrap at ~120)", len(line)),
				Evidence: truncate(stripped, 80),
			})
		}
		if looksLikeFunctionStart(ch.Language, stripped) {
			if longFunctionStart > 0 && i-longFunctionStart > 120 {
				out = append(out, offlineFinding{
					Severity: "medium", Path: ch.Path, Line: lineNumber(ch, longFunctionStart),
					Category: "complexity",
					Message:  fmt.Sprintf("function %s is ~%d lines — consider splitting", longFunctionName, i-longFunctionStart),
				})
			}
			longFunctionStart = i
			longFunctionName = extractFunctionName(ch.Language, stripped)
		}
	}
	return out
}

// scanLanguageIssues layers language-specific checks.
func scanLanguageIssues(ch types.ContextChunk) []offlineFinding {
	lang := strings.ToLower(ch.Language)
	switch lang {
	case "go":
		return scanGoIssues(ch)
	case "typescript", "javascript", "ts", "js", "tsx", "jsx":
		return scanTSIssues(ch)
	case "python", "py":
		return scanPyIssues(ch)
	case "rust", "rs":
		return scanRustIssues(ch)
	}
	return nil
}

var (
	reGoErrIgnored  = regexp.MustCompile(`(?m)^\s*_(\s*,\s*_)*\s*(:?=)\s*[A-Za-z_]+\s*\(`)
	reGoFmtPanicf   = regexp.MustCompile(`panic\s*\(`)
	reGoContextTodo = regexp.MustCompile(`context\.TODO\(\)`)
	reGoDeferInLoop = regexp.MustCompile(`(?m)^\s*for\b`)
)

func scanGoIssues(ch types.ContextChunk) []offlineFinding {
	out := []offlineFinding{}
	lines := strings.Split(ch.Content, "\n")
	inLoop := 0
	for i, line := range lines {
		ln := lineNumber(ch, i)
		stripped := strings.TrimSpace(line)

		if reGoErrIgnored.MatchString(line) && !strings.Contains(line, "// nolint") {
			out = append(out, offlineFinding{
				Severity: "medium", Path: ch.Path, Line: ln,
				Category: "error-handling", Message: "error return discarded with `_`",
				Evidence: stripped,
			})
		}
		if reGoContextTodo.MatchString(line) {
			out = append(out, offlineFinding{
				Severity: "low", Path: ch.Path, Line: ln,
				Category: "api-hygiene", Message: "context.TODO() — thread a real context",
				Evidence: stripped,
			})
		}
		if reGoFmtPanicf.MatchString(line) && !strings.Contains(line, "// recoverable") {
			out = append(out, offlineFinding{
				Severity: "medium", Path: ch.Path, Line: ln,
				Category: "reliability", Message: "panic() — prefer returned error at this level",
				Evidence: stripped,
			})
		}
		if reGoDeferInLoop.MatchString(line) {
			inLoop = 1
		} else if inLoop > 0 && strings.HasPrefix(stripped, "}") {
			inLoop = 0
		}
		if inLoop > 0 && strings.HasPrefix(stripped, "defer ") {
			out = append(out, offlineFinding{
				Severity: "medium", Path: ch.Path, Line: ln,
				Category: "resource-leak", Message: "defer inside loop — deferred calls accumulate until function return",
				Evidence: stripped,
			})
		}
	}
	return out
}

var (
	reTSAny        = regexp.MustCompile(`\b(:|as)\s*any\b`)
	reTSConsoleLog = regexp.MustCompile(`\bconsole\.(log|debug)\s*\(`)
	reTSTsIgnore   = regexp.MustCompile(`@ts-(ignore|nocheck)`)
)

func scanTSIssues(ch types.ContextChunk) []offlineFinding {
	out := []offlineFinding{}
	lines := strings.Split(ch.Content, "\n")
	for i, line := range lines {
		ln := lineNumber(ch, i)
		stripped := strings.TrimSpace(line)
		if reTSAny.MatchString(line) {
			out = append(out, offlineFinding{
				Severity: "medium", Path: ch.Path, Line: ln,
				Category: "type-safety", Message: "use of `any` weakens type coverage",
				Evidence: stripped,
			})
		}
		if reTSTsIgnore.MatchString(line) {
			out = append(out, offlineFinding{
				Severity: "high", Path: ch.Path, Line: ln,
				Category: "type-safety", Message: "@ts-ignore / @ts-nocheck suppresses the typechecker",
				Evidence: stripped,
			})
		}
		if reTSConsoleLog.MatchString(line) && !strings.Contains(line, "// keep") {
			out = append(out, offlineFinding{
				Severity: "low", Path: ch.Path, Line: ln,
				Category: "debug-leak", Message: "console.log/debug likely leftover from development",
				Evidence: stripped,
			})
		}
	}
	return out
}

var (
	rePyBareExcept = regexp.MustCompile(`^\s*except\s*:\s*$`)
	rePyPrint      = regexp.MustCompile(`(?m)^\s*print\s*\(`)
	rePyMutDefault = regexp.MustCompile(`def\s+\w+\([^)]*=\s*(\[\]|\{\})`)
	rePyEval       = regexp.MustCompile(`\b(eval|exec)\s*\(`)
)

func scanPyIssues(ch types.ContextChunk) []offlineFinding {
	out := []offlineFinding{}
	for i, line := range strings.Split(ch.Content, "\n") {
		ln := lineNumber(ch, i)
		stripped := strings.TrimSpace(line)
		if rePyBareExcept.MatchString(line) {
			out = append(out, offlineFinding{
				Severity: "medium", Path: ch.Path, Line: ln,
				Category: "error-handling", Message: "bare except swallows all exceptions (including KeyboardInterrupt)",
				Evidence: stripped,
			})
		}
		if rePyMutDefault.MatchString(line) {
			out = append(out, offlineFinding{
				Severity: "high", Path: ch.Path, Line: ln,
				Category: "bug-risk", Message: "mutable default argument — shared across calls",
				Evidence: stripped,
			})
		}
		if rePyEval.MatchString(line) {
			out = append(out, offlineFinding{
				Severity: "critical", Path: ch.Path, Line: ln,
				Category: "security", Message: "eval/exec — arbitrary code execution risk",
				Evidence: stripped,
			})
		}
		if rePyPrint.MatchString(line) && !strings.Contains(line, "# keep") {
			out = append(out, offlineFinding{
				Severity: "low", Path: ch.Path, Line: ln,
				Category: "debug-leak", Message: "print() likely leftover from development",
				Evidence: stripped,
			})
		}
	}
	return out
}

var (
	reRustUnwrap = regexp.MustCompile(`\.unwrap\(\)`)
	reRustUnsafe = regexp.MustCompile(`\bunsafe\b`)
)

func scanRustIssues(ch types.ContextChunk) []offlineFinding {
	out := []offlineFinding{}
	for i, line := range strings.Split(ch.Content, "\n") {
		ln := lineNumber(ch, i)
		stripped := strings.TrimSpace(line)
		if reRustUnwrap.MatchString(line) && !strings.Contains(line, "// safe:") {
			out = append(out, offlineFinding{
				Severity: "medium", Path: ch.Path, Line: ln,
				Category: "error-handling", Message: "`.unwrap()` panics — prefer `?` or `.expect(\"why\")`",
				Evidence: stripped,
			})
		}
		if reRustUnsafe.MatchString(line) && !strings.Contains(line, "// safety:") {
			out = append(out, offlineFinding{
				Severity: "high", Path: ch.Path, Line: ln,
				Category: "memory-safety", Message: "unsafe block — document the invariants you rely on",
				Evidence: stripped,
			})
		}
	}
	return out
}
