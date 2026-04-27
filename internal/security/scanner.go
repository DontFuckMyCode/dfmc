package security

import (
	"bufio"
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type SecretFinding struct {
	Pattern  string `json:"pattern"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Match    string `json:"match"`
	Severity string `json:"severity"`
}

type VulnerabilityFinding struct {
	Kind     string `json:"kind"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	CWE      string `json:"cwe,omitempty"`
	OWASP    string `json:"owasp,omitempty"`
	Snippet  string `json:"snippet"`
}

type Report struct {
	FilesScanned    int                    `json:"files_scanned"`
	Secrets         []SecretFinding        `json:"secrets,omitempty"`
	Vulnerabilities []VulnerabilityFinding `json:"vulnerabilities,omitempty"`
}

type secretPattern struct {
	Name       string
	Pattern    *regexp.Regexp
	Severity   string
	MinEntropy float64
}

type vulnPattern struct {
	Kind     string
	Pattern  *regexp.Regexp
	Severity string
	CWE      string
	OWASP    string
}

type Scanner struct {
	secrets []secretPattern
	vulns   []vulnPattern
}

func New() *Scanner {
	return &Scanner{
		secrets: defaultSecretPatterns(),
		vulns:   defaultVulnPatterns(),
	}
}

func (s *Scanner) ScanPaths(paths []string) (Report, error) {
	report := Report{}
	for _, p := range paths {
		if shouldSkipFile(p) {
			continue
		}
		content, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		report.FilesScanned++
		sec, vul := s.ScanContent(p, content)
		report.Secrets = append(report.Secrets, sec...)
		report.Vulnerabilities = append(report.Vulnerabilities, vul...)
	}
	return report, nil
}

func (s *Scanner) ScanContent(path string, content []byte) ([]SecretFinding, []VulnerabilityFinding) {
	var secrets []SecretFinding
	var vulns []VulnerabilityFinding

	lang := detectLanguageFromPath(path)
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Skip pure line comments so a doc comment quoting a sink
		// ("// `exec.Command(\"git\", ...)` is safe because ...")
		// doesn't land as a CWE-78 finding. Block comments are harder
		// to track at line granularity; this handles the 95% case.
		if lang != "" && isCommentLine(trimmed, lang) {
			continue
		}
		if isPatternDefinitionLine(trimmed) {
			continue
		}

		for _, pat := range s.secrets {
			if pat.Pattern.MatchString(line) {
				match := pat.Pattern.FindString(line)
				if shouldSuppressSecretFinding(path, pat, match) {
					continue
				}
				secrets = append(secrets, SecretFinding{
					Pattern:  pat.Name,
					File:     toSlash(path),
					Line:     lineNo,
					Match:    redact(match),
					Severity: pat.Severity,
				})
			}
		}

		for _, pat := range s.vulns {
			if !pat.Pattern.MatchString(line) {
				continue
			}
			// Regex false-positive guard shared with the AST scanner:
			// if every argument in the first call on this line is a
			// literal, the regex match is almost certainly a coincidence
			// (e.g. `exec.Command("git", "-C", "/path", "diff")` flagged
			// as CWE-78 even though nothing is user-controlled).
			if argumentListAllLiterals(trimmed) {
				continue
			}
			vulns = append(vulns, VulnerabilityFinding{
				Kind:     pat.Kind,
				File:     toSlash(path),
				Line:     lineNo,
				Severity: pat.Severity,
				CWE:      pat.CWE,
				OWASP:    pat.OWASP,
				Snippet:  snippet(trimmed, 180),
			})
		}
	}

	// AST-aware pass: higher precision, per-language rules. Findings
	// are appended as-is — dedup is handled by callers via line+CWE
	// on the final report when needed, since the rule kinds differ.
	astFindings := s.ScanASTRules(path, content)
	vulns = append(vulns, astFindings...)

	return secrets, vulns
}

func defaultSecretPatterns() []secretPattern {
	return []secretPattern{
		{"AWS Access Key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "critical", 0},
		{"GitHub Token", regexp.MustCompile(`ghp_[A-Za-z0-9_]{36}`), "critical", 0},
		{"GitHub OAuth", regexp.MustCompile(`gho_[A-Za-z0-9_]{36}`), "high", 0},
		{"GitLab Token", regexp.MustCompile(`glpat-[A-Za-z0-9\-_]{20,}`), "critical", 0},
		{"Private Key", regexp.MustCompile(`-----BEGIN (RSA|EC|DSA|OPENSSH) PRIVATE KEY-----`), "critical", 0},
		{"JWT Token", regexp.MustCompile(`eyJ[A-Za-z0-9-_]+\.eyJ[A-Za-z0-9-_]+\.[A-Za-z0-9-_.+/=]+`), "high", 0},
		{"Generic API Key", regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[:=]\s*["']?[A-Za-z0-9-_]{20,}["']?`), "high", 3.5},
		{"Database URL", regexp.MustCompile(`(?i)(postgres|mysql|mongodb|redis)://[^\s]+@[^\s]+`), "critical", 0},
		{"Slack Token", regexp.MustCompile(`xox[bpras]-[A-Za-z0-9-]+`), "high", 0},
		{"Stripe Key", regexp.MustCompile(`sk_live_[A-Za-z0-9]{24,}`), "critical", 0},
		{"Anthropic API Key", regexp.MustCompile(`sk-ant-[A-Za-z0-9-_]{40,}`), "critical", 0},
		{"OpenAI API Key", regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`), "critical", 0},
		{"Google API Key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`), "high", 0},
	}
}

func defaultVulnPatterns() []vulnPattern {
	return []vulnPattern{
		{
			Kind:     "Potential SQL Injection",
			Pattern:  regexp.MustCompile(`(?i)(SELECT|INSERT|UPDATE|DELETE).*\+.*(req|input|param|user|query)`),
			Severity: "high",
			CWE:      "CWE-89",
			OWASP:    "A03:2021 Injection",
		},
		{
			Kind:     "Potential Command Injection",
			Pattern:  regexp.MustCompile(`(?i)(os\.exec|exec\.command|subprocess\.(popen|run|call)).*(\+|format\(|f\")`),
			Severity: "high",
			CWE:      "CWE-78",
			OWASP:    "A03:2021 Injection",
		},
		{
			Kind:     "Potential Insecure Eval",
			Pattern:  regexp.MustCompile(`(?i)\beval\s*\(`),
			Severity: "high",
			CWE:      "CWE-95",
			OWASP:    "A03:2021 Injection",
		},
		{
			Kind:     "Potential Insecure Deserialization",
			Pattern:  regexp.MustCompile(`(?i)(pickle\.loads|yaml\.load\(|ObjectInputStream|unserialize\()`),
			Severity: "high",
			CWE:      "CWE-502",
			OWASP:    "A08:2021 Software and Data Integrity Failures",
		},
		{
			Kind:     "Potential SSRF",
			Pattern:  regexp.MustCompile(`(?i)(http\.Get|axios\.get|requests\.get)\s*\([^)]*(url|endpoint|target)`),
			Severity: "medium",
			CWE:      "CWE-918",
			OWASP:    "A10:2021 Server-Side Request Forgery",
		},
	}
}

func redact(value string) string {
	v := strings.TrimSpace(value)
	if len(v) <= 8 {
		return "****"
	}
	return v[:4] + strings.Repeat("*", len(v)-8) + v[len(v)-4:]
}

func snippet(value string, max int) string {
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "..."
}

func toSlash(path string) string {
	return filepath.ToSlash(path)
}

func shouldSkipFile(path string) bool {
	p := strings.ToLower(filepath.ToSlash(path))
	if strings.Contains(p, "/vendor/") || strings.Contains(p, "/node_modules/") {
		return true
	}
	if strings.Contains(p, "/testdata/") || strings.Contains(p, "/fixtures/") || strings.Contains(p, "/examples/") {
		return true
	}
	if strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, "_test.py") || strings.HasSuffix(p, ".spec.ts") {
		return true
	}
	return false
}

func shouldSuppressSecretFinding(path string, pat secretPattern, match string) bool {
	p := strings.ToLower(filepath.ToSlash(path))
	if strings.Contains(p, "/testdata/") || strings.Contains(p, "/fixtures/") || strings.Contains(p, "/examples/") {
		return true
	}
	if pat.MinEntropy <= 0 {
		return false
	}
	candidate := secretEntropyCandidate(match)
	if len(candidate) == 0 {
		return true
	}
	return shannonEntropy(candidate) < pat.MinEntropy
}

func secretEntropyCandidate(match string) string {
	fields := strings.FieldsFunc(match, func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z':
			return false
		case r >= 'A' && r <= 'Z':
			return false
		case r >= '0' && r <= '9':
			return false
		case r == '_' || r == '-':
			return false
		default:
			return true
		}
	})
	best := ""
	for _, field := range fields {
		if len(field) > len(best) {
			best = field
		}
	}
	return best
}

func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	counts := make(map[rune]int, len(s))
	for _, r := range s {
		counts[r]++
	}
	total := float64(len(s))
	var entropy float64
	for _, count := range counts {
		p := float64(count) / total
		entropy -= p * math.Log2(p)
	}
	return entropy
}

// isPatternDefinitionLine recognises the scanner's OWN rule-definition
// lines so the security scanner doesn't fire against itself. The rule
// bodies naturally contain the very literals they look for — e.g.
// `strings.Contains(ctx.Trimmed, "md5.New")` has "md5.New" sitting in
// the source, which the CWE-327 rule would then flag.
//
// We match on two shapes:
//  1. Explicit rule registration (regexp.MustCompile, Pattern:, Match:)
//  2. Rule bodies that reference the scanner's per-line context
//     (ctx.Trimmed, ctx.RecentJoin, ctx.Line) — a near-perfect tell
//     that this is scanner machinery, not application code. No real
//     vulnerability lives inside a rule matcher body.
func isPatternDefinitionLine(line string) bool {
	l := strings.ToLower(line)
	if strings.Contains(l, "regexp.mustcompile(") ||
		strings.Contains(l, "pattern:") ||
		strings.Contains(l, "match:") {
		return true
	}
	// Scanner-internal: the rule matchers operate on scanLineCtx,
	// which exposes these three fields. Any line touching them is a
	// rule body, not code under review.
	if strings.Contains(l, "ctx.trimmed") ||
		strings.Contains(l, "ctx.recentjoin") ||
		strings.Contains(l, "ctx.line") {
		return true
	}
	return false
}

func (r Report) Summary() string {
	return fmt.Sprintf("files=%d secrets=%d vulns=%d", r.FilesScanned, len(r.Secrets), len(r.Vulnerabilities))
}
