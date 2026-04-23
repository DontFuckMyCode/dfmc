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
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// detectOfflineTask reads the merged system prompt (or the user question as
// a fallback) and decides which heuristic pipeline to run. Matches the
// `Task: <name>` stamp the engine writes in system.base, with keyword
// fallbacks so a raw `/review this file` in chat still routes correctly.
//
// Slash-command markers are anchored at the start of a line (after
// optional leading whitespace). The previous unanchored
// strings.Contains check would fire on `/explain` inside a doc
// comment, `/plan` inside a path like `tests/plans/`, or `/debug`
// inside an `// XXX: debug me` note — and `/debug` was checked
// before `/review` so the order also mattered. Anchored matching
// makes the trigger position-determined rather than soup-like.
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


// -------------------------------------------------------------------------
// RENDERERS + HELPERS

func renderFindings(findings []offlineFinding) string {
	groups := map[string][]offlineFinding{}
	order := []string{"critical", "high", "medium", "low"}
	for _, f := range findings {
		sev := strings.ToLower(strings.TrimSpace(f.Severity))
		if sev == "" {
			sev = "low"
		}
		groups[sev] = append(groups[sev], f)
	}
	var b strings.Builder
	for _, sev := range order {
		bucket := groups[sev]
		if len(bucket) == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("\n**%s** (%d)\n", strings.ToUpper(sev), len(bucket)))
		for _, f := range bucket {
			b.WriteString(fmt.Sprintf("- `%s:%d` · %s · %s", f.Path, f.Line, f.Category, f.Message))
			if f.Evidence != "" {
				b.WriteString("\n  > " + f.Evidence)
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func renderFileInventory(chunks []types.ContextChunk) string {
	if len(chunks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("**Files in scope:**\n")
	limit := len(chunks)
	if limit > 8 {
		limit = 8
	}
	for i := 0; i < limit; i++ {
		ch := chunks[i]
		b.WriteString(fmt.Sprintf("- `%s` · %s · L%d-L%d · score %.2f\n",
			ch.Path, humanLang(ch.Language), ch.LineStart, ch.LineEnd, ch.Score))
	}
	if len(chunks) > limit {
		b.WriteString(fmt.Sprintf("- … +%d more\n", len(chunks)-limit))
	}
	return strings.TrimSpace(b.String())
}

func dedupeSortFindings(in []offlineFinding) []offlineFinding {
	seen := map[string]struct{}{}
	out := make([]offlineFinding, 0, len(in))
	for _, f := range in {
		key := f.Severity + "|" + f.Path + "|" + fmt.Sprintf("%d", f.Line) + "|" + f.Message
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	sevRank := map[string]int{"critical": 0, "high": 1, "medium": 2, "low": 3}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := sevRank[out[i].Severity], sevRank[out[j].Severity]
		if ri != rj {
			return ri < rj
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func filterByKeywords(in []offlineFinding, keys []string) []offlineFinding {
	if len(keys) == 0 {
		return in
	}
	out := make([]offlineFinding, 0, len(in))
	for _, f := range in {
		hay := strings.ToLower(f.Message + " " + f.Category + " " + f.Evidence)
		for _, k := range keys {
			if strings.Contains(hay, k) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

func lineNumber(ch types.ContextChunk, idx int) int {
	if ch.LineStart > 0 {
		return ch.LineStart + idx
	}
	return idx + 1
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func looksLikeFunctionStart(lang, line string) bool {
	switch strings.ToLower(lang) {
	case "go":
		return strings.HasPrefix(line, "func ") || strings.HasPrefix(line, "func(")
	case "python", "py":
		return strings.HasPrefix(line, "def ") || strings.HasPrefix(line, "async def ")
	case "typescript", "javascript", "ts", "js", "tsx", "jsx":
		return strings.Contains(line, "function ") || strings.Contains(line, " => {") || strings.Contains(line, " => (")
	case "rust", "rs":
		return strings.HasPrefix(line, "fn ") || strings.HasPrefix(line, "pub fn ")
	}
	return false
}

func extractFunctionName(lang, line string) string {
	switch strings.ToLower(lang) {
	case "go":
		if strings.HasPrefix(line, "func ") {
			rest := strings.TrimPrefix(line, "func ")
			if i := strings.IndexAny(rest, "( "); i >= 0 {
				return strings.TrimSpace(rest[:i])
			}
			return rest
		}
	case "python", "py":
		rest := strings.TrimPrefix(strings.TrimPrefix(line, "async "), "def ")
		if i := strings.Index(rest, "("); i >= 0 {
			return strings.TrimSpace(rest[:i])
		}
	case "rust", "rs":
		rest := strings.TrimPrefix(strings.TrimPrefix(line, "pub "), "fn ")
		if i := strings.Index(rest, "("); i >= 0 {
			return strings.TrimSpace(rest[:i])
		}
	}
	return "<anon>"
}

func extractTopSymbols(ch types.ContextChunk) []string {
	var out []string
	for _, line := range strings.Split(ch.Content, "\n") {
		stripped := strings.TrimSpace(line)
		if name := extractFunctionName(ch.Language, stripped); name != "<anon>" && name != "" && looksLikeFunctionStart(ch.Language, stripped) {
			out = append(out, name)
			if len(out) >= 6 {
				break
			}
		}
	}
	return out
}

func briefSymbols(syms []string) string {
	if len(syms) == 0 {
		return "no top-level symbols detected"
	}
	if len(syms) <= 4 {
		return strings.Join(syms, ", ")
	}
	return strings.Join(syms[:4], ", ") + fmt.Sprintf(" (+%d)", len(syms)-4)
}

func sketchStructure(chunks []types.ContextChunk) string {
	langs := map[string]int{}
	for _, ch := range chunks {
		langs[humanLang(ch.Language)]++
	}
	if len(langs) == 0 {
		return "no structural data"
	}
	var parts []string
	for l, n := range langs {
		parts = append(parts, fmt.Sprintf("%d %s", n, l))
	}
	sort.Strings(parts)
	return fmt.Sprintf("%d files — %s.", len(chunks), strings.Join(parts, ", "))
}

func humanLang(l string) string {
	if l == "" {
		return "text"
	}
	return l
}
