package provider

// offline_analyzer.go — heuristic code analyzers the offline provider runs
// locally when no LLM key is configured. The goal is signal, not stubs:
// running `dfmc review` or `dfmc security` offline should surface real
// findings (TODOs, anti-patterns, likely secrets, missing tests, etc.)
// grounded in the context files the engine already ranked.
//
// Every finding cites `path:line` so output stays debuggable. The analyzers
// are regex/word-level — fast and CGO-free — not full static analysis.

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

func offlineReviewReport(chunks []types.ContextChunk) string {
	findings := []offlineFinding{}
	for _, ch := range chunks {
		findings = append(findings, scanCommonIssues(ch)...)
		findings = append(findings, scanLanguageIssues(ch)...)
	}
	findings = dedupeSortFindings(findings)

	if len(findings) == 0 {
		return "No obvious issues caught by heuristic scans.\n" +
			"Residual risk: offline analysis only flags lexical patterns — logic bugs, " +
			"race conditions, and integration failures need an LLM provider or a test run.\n\n" +
			renderFileInventory(chunks)
	}

	return renderFindings(findings) + "\n\n" + renderFileInventory(chunks) +
		"\n\n_Offline heuristics only: configure an LLM provider for semantic review._"
}


// -------------------------------------------------------------------------
// SECURITY

var (
	reSecretAssign = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|secret|password|private[_-]?key|aws[_-]?access)\s*[:=]\s*['"\x60][A-Za-z0-9\-_/+=]{8,}['"\x60]`)
	reSQLConcat    = regexp.MustCompile(`(?i)(select|insert|update|delete)\s+[^\n]*['"\x60]\s*\+`)
	reCmdConcat    = regexp.MustCompile(`(?i)(exec|system|popen|spawn|shell)\s*\([^)]*\+[^)]*\)`)
	reOldCrypto    = regexp.MustCompile(`(?i)(md5|sha1)\s*\(|Hashing\.(MD5|SHA1)|HashAlgorithm\.(Md5|Sha1)`)
	reAWSAccessKey = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	rePEMPrivate   = regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`)
	reHTTPUrl      = regexp.MustCompile(`\bhttp://[^\s"'\x60>]+`)
	reRandWeakSeed = regexp.MustCompile(`(?i)(math\.rand\.Seed|Math\.random|random\.(seed|random)|new\s+Random\()`)
)

func offlineSecurityReport(chunks []types.ContextChunk) string {
	findings := []offlineFinding{}
	for _, ch := range chunks {
		for i, line := range strings.Split(ch.Content, "\n") {
			ln := lineNumber(ch, i)
			stripped := strings.TrimSpace(line)
			if reAWSAccessKey.MatchString(line) {
				findings = append(findings, offlineFinding{
					Severity: "critical", Path: ch.Path, Line: ln,
					Category: "secrets", Message: "AWS access key literal in source",
					Evidence: truncate(stripped, 80),
				})
			}
			if rePEMPrivate.MatchString(line) {
				findings = append(findings, offlineFinding{
					Severity: "critical", Path: ch.Path, Line: ln,
					Category: "secrets", Message: "PEM private key embedded in source",
					Evidence: "-----BEGIN ... -----",
				})
			}
			if reSecretAssign.MatchString(line) {
				findings = append(findings, offlineFinding{
					Severity: "high", Path: ch.Path, Line: ln,
					Category: "secrets", Message: "hardcoded credential-like literal — load from env/secret store",
					Evidence: truncate(stripped, 80),
				})
			}
			if reSQLConcat.MatchString(line) {
				findings = append(findings, offlineFinding{
					Severity: "high", Path: ch.Path, Line: ln,
					Category: "injection", Message: "SQL string concatenation — use parameterized queries",
					Evidence: truncate(stripped, 80),
				})
			}
			if reCmdConcat.MatchString(line) {
				findings = append(findings, offlineFinding{
					Severity: "critical", Path: ch.Path, Line: ln,
					Category: "injection", Message: "shell command built with string concatenation — argv form + allow-list",
					Evidence: truncate(stripped, 80),
				})
			}
			if reOldCrypto.MatchString(line) {
				findings = append(findings, offlineFinding{
					Severity: "medium", Path: ch.Path, Line: ln,
					Category: "crypto", Message: "MD5/SHA1 in use — prefer SHA-256 or stronger for integrity",
					Evidence: stripped,
				})
			}
			if reHTTPUrl.MatchString(line) && !strings.Contains(line, "localhost") && !strings.Contains(line, "127.0.0.1") {
				findings = append(findings, offlineFinding{
					Severity: "low", Path: ch.Path, Line: ln,
					Category: "transport", Message: "plain http:// URL — prefer https://",
					Evidence: truncate(stripped, 80),
				})
			}
			if reRandWeakSeed.MatchString(line) {
				findings = append(findings, offlineFinding{
					Severity: "medium", Path: ch.Path, Line: ln,
					Category: "crypto", Message: "non-crypto RNG — use crypto/rand for tokens, secrets, session IDs",
					Evidence: stripped,
				})
			}
		}
	}
	findings = dedupeSortFindings(findings)
	if len(findings) == 0 {
		return "No obvious security smells caught by heuristic scans.\n" +
			"Residual risk: authn/authz logic, race conditions, and protocol misuse need semantic review.\n\n" +
			renderFileInventory(chunks)
	}
	return renderFindings(findings) + "\n\n_Heuristic scan only — configure an LLM provider for deeper threat modelling._"
}

// -------------------------------------------------------------------------
// EXPLAIN / GENERAL / DEBUG / TEST / PLANNING

func offlineExplainReport(chunks []types.ContextChunk, question string) string {
	var b strings.Builder
	if question != "" {
		b.WriteString("**Question:** " + question + "\n\n")
	}
	b.WriteString("**What the context shows:**\n")
	for i, ch := range chunks {
		if i >= 6 {
			break
		}
		syms := extractTopSymbols(ch)
		line := fmt.Sprintf("- `%s` (%s, L%d-L%d): %s",
			ch.Path, humanLang(ch.Language), ch.LineStart, ch.LineEnd, briefSymbols(syms))
		b.WriteString(line + "\n")
	}
	b.WriteString("\n**Structure:** ")
	b.WriteString(sketchStructure(chunks))
	b.WriteString("\n\n_Heuristic-only summary: configure an LLM provider for narrative explanation._")
	return b.String()
}

func offlineDebugReport(chunks []types.ContextChunk, question string) string {
	findings := []offlineFinding{}
	for _, ch := range chunks {
		findings = append(findings, scanLanguageIssues(ch)...)
	}
	findings = filterByKeywords(findings, []string{"error", "panic", "nil", "timeout", "race"})
	findings = dedupeSortFindings(findings)
	var b strings.Builder
	if question != "" {
		b.WriteString("**Reported symptom:** " + question + "\n\n")
	}
	if len(findings) > 0 {
		b.WriteString("**Likely suspects (offline heuristics):**\n")
		b.WriteString(renderFindings(findings))
	} else {
		b.WriteString("No obvious suspects from lexical scan.\n")
	}
	b.WriteString("\n**Next steps:** reproduce with a failing test, then configure an LLM provider for root-cause reasoning.\n")
	return b.String()
}

func offlineTestReport(chunks []types.ContextChunk) string {
	var missing []string
	for _, ch := range chunks {
		if strings.Contains(ch.Path, "_test.") || strings.Contains(ch.Path, ".test.") || strings.Contains(ch.Path, "test_") {
			continue
		}
		syms := extractTopSymbols(ch)
		if len(syms) == 0 {
			continue
		}
		missing = append(missing, fmt.Sprintf("- `%s`: candidate tests for %s", ch.Path, briefSymbols(syms)))
	}
	var b strings.Builder
	b.WriteString("**Test coverage signals (offline):**\n")
	if len(missing) == 0 {
		b.WriteString("_No non-test source files in context; nothing to recommend._\n")
	} else {
		for _, m := range missing {
			b.WriteString(m + "\n")
		}
	}
	b.WriteString("\n**Test-writing checklist:**\n")
	b.WriteString("- Cover the golden path + 2 failure modes per public symbol.\n")
	b.WriteString("- Add a regression test if a recent change touched the file.\n")
	b.WriteString("- Avoid mocks for hot paths — prefer real dependencies in a test harness.\n")
	return b.String()
}

func offlinePlanReport(chunks []types.ContextChunk, question string) string {
	var b strings.Builder
	b.WriteString("**Planning scaffold (offline):**\n")
	b.WriteString("_Configure an LLM provider for a real plan. Heuristic outline below._\n\n")
	if question != "" {
		b.WriteString("- Goal: " + question + "\n")
	}
	b.WriteString("- Touch surface (by relevance):\n")
	for i, ch := range chunks {
		if i >= 6 {
			break
		}
		b.WriteString(fmt.Sprintf("  - `%s:%d-%d` (%s)\n", ch.Path, ch.LineStart, ch.LineEnd, humanLang(ch.Language)))
	}
	b.WriteString("\n**Phases:**\n")
	b.WriteString("1. Spike the smallest testable slice (one file / one API).\n")
	b.WriteString("2. Expand to adjacent call sites; add tests per change.\n")
	b.WriteString("3. Backfill docs/migration notes once behaviour is stable.\n")
	return b.String()
}

func offlineGeneralReport(chunks []types.ContextChunk, question string) string {
	var b strings.Builder
	if question != "" {
		b.WriteString("**You asked:** " + question + "\n\n")
	}
	b.WriteString("**Heuristic scan results:**\n")
	findings := []offlineFinding{}
	for _, ch := range chunks {
		findings = append(findings, scanCommonIssues(ch)...)
		findings = append(findings, scanLanguageIssues(ch)...)
	}
	findings = dedupeSortFindings(findings)
	if len(findings) == 0 {
		b.WriteString("_No heuristic findings; context looks clean to the lexical pass._\n")
	} else {
		// Keep it short for general queries — top 6 only.
		if len(findings) > 6 {
			findings = findings[:6]
		}
		b.WriteString(renderFindings(findings))
	}
	b.WriteString("\n\n" + renderFileInventory(chunks))
	b.WriteString("\n\n_Offline assistant — configure an LLM provider for conversational answers._")
	return b.String()
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
