// scanner_filters.go — false-positive suppression + entropy gate +
// scanner-self-recognition for the regex security scanner. Sibling
// of scanner.go which keeps the public types (SecretFinding,
// VulnerabilityFinding, Report), the Scanner struct + constructor,
// the ScanPaths/ScanContent entry points, the default secret/vuln
// pattern catalogs, and the Summary helper.
//
// Splitting filters out keeps scanner.go scoped to "what does the
// scanner produce" while this file owns the "is this finding real"
// machinery: skip-this-file paths (vendor / node_modules / testdata
// / *_test.go), per-pattern entropy threshold (so a generic API key
// regex doesn't fire on "API_KEY=changeme"), and the scanner's own
// rule-body recognizer (so the CWE-327 rule doesn't flag the very
// literal it's looking for).

package security

import (
	"math"
	"path/filepath"
	"strings"
)

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
// We match on three shapes:
//  1. Explicit rule registration (regexp.MustCompile, Pattern:, Match:)
//  2. Rule bodies that reference the scanner's per-line context
//     (ctx.Trimmed, ctx.RecentJoin, ctx.Line) — a near-perfect tell
//     that this is scanner machinery, not application code. No real
//     vulnerability lives inside a rule matcher body.
//  3. Vulnerability rule definitions: a line that defines a vulnPattern
//     (Kind/CWE/OWASP fields) AND calls regexp.MustCompile — these
//     contain the very literals they look for and would trigger
//     catastrophic backtracking if matched against themselves.
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
	// Vulnerability pattern definitions: a line that calls
	// regexp.MustCompile and also carries vulnerability metadata
	// (Kind, CWE, OWASP). Without this, the scanner would match its
	// own CWE-78/CWE-89/CWE-95 pattern bodies and timeout.
	if strings.Contains(l, "regexp.mustcompile(") &&
		(strings.Contains(l, "kind:") || strings.Contains(l, "cwe:") || strings.Contains(l, "owasp:")) {
		return true
	}
	return false
}
