package provider

// offline_analyzer.go — core pipeline + shared helpers for the offline
// provider's heuristic code analysis. Running `dfmc review` or
// `dfmc security` offline should surface real findings (TODOs, anti-
// patterns, likely secrets, missing tests, etc.) grounded in the
// context files the engine already ranked, not return stubs.
//
// This file holds:
//   - detectOfflineTask / offlineTaskFromText — task routing
//   - analyzeOffline / offlineHeader         — pipeline entry + header
//   - offlineFinding struct                  — shared finding shape
//   - renderFindings / renderFileInventory   — output formatters
//   - dedupeSortFindings / filterByKeywords  — finding collection helpers
//   - lineNumber / truncate / looksLikeFunctionStart / extractFunctionName
//     extractTopSymbols / briefSymbols / sketchStructure / humanLang
//     — content helpers shared across scanners and reports
//
// The per-task report generators (review/security/explain/debug/test/
// plan/general) live in offline_reports.go; the per-language scanners
// live in offline_scanners.go. Every finding cites `path:line` so
// output stays debuggable. The analyzers are regex/word-level — fast
// and CGO-free — not full static analysis.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// detectOfflineTask reads the merged system prompt (or the user question as
// a fallback) and decides which heuristic pipeline to run. Matches the
// `Task: <name>` stamp the engine writes in system.base, with keyword
// fallbacks so a raw `/review this file` in chat still routes correctly.
//
// Slash-command markers are anchored at the start of a line (after
// optional leading whitespace). An unanchored Contains("/debug")
// would fire on a doc comment like "// XXX: debug me" in source.
// Anchored matching makes the trigger position-determined rather
// than soup-like.
func detectOfflineTask(systemPrompt, question string) string {
	// M2 fix: don't lowercase + concatenate the whole system prompt
	// on every offline query. System prompts routinely run to several
	// KB (templates + project brief + tool catalog + injected context);
	// pre-fix `strings.ToLower(systemPrompt + "\n" + question)` paid
	// the full alloc twice on every call. Now we (a) check the question
	// (small, cheap) first, then (b) lowercase only a bounded prefix of
	// the system prompt — enough to catch the "Task:" header stamp and
	// any slash command typed at the top, while leaving the bulk of a
	// long brief untouched.
	qLower := strings.ToLower(strings.TrimSpace(question))
	if t := offlineTaskFromText(qLower); t != "" {
		return t
	}
	const sysScanWindow = 1024
	prefix := systemPrompt
	if len(prefix) > sysScanWindow {
		prefix = prefix[:sysScanWindow]
	}
	pLower := strings.ToLower(prefix)
	// system.base writes "Task: <name>" near the top of the prompt;
	// canonical fast path.
	if idx := strings.Index(pLower, "task:"); idx >= 0 {
		tail := strings.TrimSpace(pLower[idx+len("task:"):])
		for _, cand := range []string{"security", "review", "planning", "refactor", "debug", "test", "doc", "explain"} {
			if strings.HasPrefix(tail, cand) {
				return cand
			}
		}
	}
	if t := offlineTaskFromText(pLower); t != "" {
		return t
	}
	return "general"
}

// offlineTaskFromText runs the slash-command + natural-language triggers
// against an already-lowercased input. Pulled out of detectOfflineTask
// so the question and system-prompt-prefix paths share the same logic
// without re-allocating a combined string.
func offlineTaskFromText(s string) string {
	if s == "" {
		return ""
	}
	// Slash command anchored at line-start (with optional leading
	// whitespace). Bound by \b so /review-team isn't mistaken for
	// /review.
	if cmd := offlineLeadingSlashTask.FindStringSubmatch(s); len(cmd) >= 2 {
		switch cmd[1] {
		case "security":
			return "security"
		case "review":
			return "review"
		case "refactor":
			return "refactor"
		case "debug":
			return "debug"
		case "test":
			return "test"
		case "explain":
			return "explain"
		case "plan":
			return "planning"
		case "doc":
			return "doc"
		}
	}
	// Natural-language fallbacks — phrases unlikely to occur as
	// stray substrings in source code or paths. The "explain" trigger
	// is gated on a leading space so it doesn't match `/explain`
	// (already covered by the slash branch above) or identifiers
	// like `explain_results`.
	switch {
	case strings.Contains(s, "security audit"), strings.Contains(s, "vulnerab"):
		return "security"
	case strings.Contains(s, "code review"):
		return "review"
	case strings.Contains(s, "root cause"):
		return "debug"
	case strings.Contains(s, "walk me through"),
		strings.HasPrefix(s, "explain "), strings.Contains(s, " explain "):
		return "explain"
	case strings.Contains(s, "plan for"):
		return "planning"
	}
	return ""
}

// Anchored at line start (with optional leading spaces) so we ignore
// `/review` mentions inside paths, comments, code blocks, or quoted
// strings. Multiline mode lets a multi-paragraph system prompt still
// match if the slash command starts a new line.
var offlineLeadingSlashTask = regexp.MustCompile(`(?m)^\s*/(security|review|refactor|debug|test|explain|plan|doc)\b`)

// analyzeOffline is the entry point used by OfflineProvider.Complete when
// it has context chunks to work with. Routes to a task-specific analyzer
// and always returns something useful (never just "offline mode is on").
func analyzeOffline(task, question string, chunks []types.ContextChunk) string {
	task = strings.ToLower(strings.TrimSpace(task))
	q := strings.TrimSpace(question)

	var body string
	switch task {
	case "security":
		body = offlineSecurityReport(chunks)
	case "review", "refactor":
		body = offlineReviewReport(chunks)
	case "explain":
		body = offlineExplainReport(chunks, q)
	case "test":
		body = offlineTestReport(chunks)
	case "debug":
		body = offlineDebugReport(chunks, q)
	case "planning":
		body = offlinePlanReport(chunks, q)
	default:
		body = offlineGeneralReport(chunks, q)
	}

	header := offlineHeader(task, len(chunks))
	return header + "\n\n" + body
}

func offlineHeader(task string, files int) string {
	label := task
	if label == "" || label == "general" {
		label = "assistant"
	}
	return fmt.Sprintf("# DFMC offline %s — %d files analyzed", label, files)
}

// -------------------------------------------------------------------------
// REVIEW / REFACTOR

// offlineReviewFinding carries a single ranked finding with a file:line cite.
type offlineFinding struct {
	Severity string // "critical" | "high" | "medium" | "low"
	Path     string
	Line     int
	Category string
	Message  string
	Evidence string
}

// Renderers + finding aggregation + per-line shape detection
// (renderFindings / renderFileInventory / dedupeSortFindings /
// filterByKeywords / lineNumber / truncate / looksLikeFunctionStart /
// extractFunctionName / extractTopSymbols / briefSymbols /
// sketchStructure / humanLang) live in offline_analyzer_helpers.go.
