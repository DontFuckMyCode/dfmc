package security

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// False-positive suppression (shouldSkipFile + shouldSuppressSecretFinding
// + secretEntropyCandidate + shannonEntropy + isPatternDefinitionLine)
// and the redact/snippet/toSlash output helpers live in
// scanner_filters.go.

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

func (r Report) Summary() string {
	return fmt.Sprintf("files=%d secrets=%d vulns=%d", r.FilesScanned, len(r.Secrets), len(r.Vulnerabilities))
}
