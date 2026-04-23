package provider

// offline_reports.go — task-specific report generators for the offline
// analyzer pipeline. Each offlineXxxReport function consumes the ranked
// context chunks (+ optional user question) and returns a markdown
// string the engine hands back to the caller when no LLM is reachable.
//
// Split out of offline_analyzer.go so adding a new task flavor
// ("security-lite," "performance," etc.) is a one-file change instead
// of scrolling past a mixed bag of helpers. The security-specific
// regex table lives here too so the rules a scan can fire on stay
// next to the scan itself.
//
// Language scanners live in offline_scanners.go; rendering helpers
// (renderFindings, renderFileInventory, briefSymbols, sketchStructure,
// humanLang) stay in offline_analyzer.go alongside the utility core.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

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
