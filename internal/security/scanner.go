package security

import (
	"bufio"
	"bytes"
	"fmt"
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
	Name     string
	Pattern  *regexp.Regexp
	Severity string
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
		if isPatternDefinitionLine(trimmed) {
			continue
		}

		for _, pat := range s.secrets {
			if pat.Pattern.MatchString(line) {
				match := pat.Pattern.FindString(line)
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
		{"AWS Access Key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`), "critical"},
		{"GitHub Token", regexp.MustCompile(`ghp_[A-Za-z0-9_]{36}`), "critical"},
		{"GitHub OAuth", regexp.MustCompile(`gho_[A-Za-z0-9_]{36}`), "high"},
		{"GitLab Token", regexp.MustCompile(`glpat-[A-Za-z0-9\-_]{20,}`), "critical"},
		{"Private Key", regexp.MustCompile(`-----BEGIN (RSA|EC|DSA|OPENSSH) PRIVATE KEY-----`), "critical"},
		{"JWT Token", regexp.MustCompile(`eyJ[A-Za-z0-9-_]+\.eyJ[A-Za-z0-9-_]+\.[A-Za-z0-9-_.+/=]+`), "high"},
		{"Generic API Key", regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[:=]\s*["']?[A-Za-z0-9-_]{20,}["']?`), "high"},
		{"Database URL", regexp.MustCompile(`(?i)(postgres|mysql|mongodb|redis)://[^\s]+@[^\s]+`), "critical"},
		{"Slack Token", regexp.MustCompile(`xox[bpras]-[A-Za-z0-9-]+`), "high"},
		{"Stripe Key", regexp.MustCompile(`sk_live_[A-Za-z0-9]{24,}`), "critical"},
		{"Anthropic API Key", regexp.MustCompile(`sk-ant-[A-Za-z0-9-_]{40,}`), "critical"},
		{"OpenAI API Key", regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`), "critical"},
		{"Google API Key", regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`), "high"},
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
	if strings.HasSuffix(p, "_test.go") || strings.HasSuffix(p, "_test.py") || strings.HasSuffix(p, ".spec.ts") {
		return true
	}
	return false
}

func isPatternDefinitionLine(line string) bool {
	l := strings.ToLower(line)
	if strings.Contains(l, "regexp.mustcompile(") || strings.Contains(l, "pattern:") {
		return true
	}
	return false
}

func (r Report) Summary() string {
	return fmt.Sprintf("files=%d secrets=%d vulns=%d", r.FilesScanned, len(r.Secrets), len(r.Vulnerabilities))
}
