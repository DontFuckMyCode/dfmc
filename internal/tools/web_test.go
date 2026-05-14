package tools

import (
	"strings"
	"testing"
)

func TestNormalizeResolverHost_StripsIPv6ZoneID(t *testing.T) {
	if got := normalizeResolverHost("fe80::1%eth0"); got != "fe80::1" {
		t.Fatalf("expected zone-stripped IPv6 literal, got %q", got)
	}
	if got := normalizeResolverHost("::1"); got != "::1" {
		t.Fatalf("plain IPv6 literal should remain unchanged, got %q", got)
	}
	if got := normalizeResolverHost("example.com"); got != "example.com" {
		t.Fatalf("hostname should remain unchanged, got %q", got)
	}
}

// TestHostAllowed_EgressAllowlist pins the web_fetch defense-in-depth
// allowlist semantics: empty list → permissive (opt-in policy);
// non-empty → strict exact + *.suffix matching, case-insensitive on
// the host. Tied directly to the prompt-injection-to-exfiltration
// chain so the matrix below covers the patterns a hostile LLM might
// emit (typo'd brand domains, raw IPs, scheme-only variants).
func TestHostAllowed_EgressAllowlist(t *testing.T) {
	cases := []struct {
		label   string
		host    string
		allow   []string
		want    bool
		errSubs string // expected substring in the rejection reason (empty when want=true)
	}{
		{"empty list allows anything", "evil.example", nil, true, ""},
		{"empty entries ignored", "evil.example", []string{"", "  "}, false, "not on the web_fetch allowlist"},
		{"exact match", "docs.python.org", []string{"docs.python.org"}, true, ""},
		{"exact mismatch", "evil.example", []string{"docs.python.org"}, false, "not on the web_fetch allowlist"},
		{"case-insensitive host", "Docs.Python.Org", []string{"docs.python.org"}, true, ""},
		{"case-insensitive entry", "docs.python.org", []string{"DOCS.PYTHON.ORG"}, true, ""},
		{"wildcard matches subdomain", "api.github.com", []string{"*.github.com"}, true, ""},
		{"wildcard matches deep subdomain", "raw.githubusercontent.com", []string{"*.githubusercontent.com"}, true, ""},
		{"wildcard does NOT match bare host", "github.com", []string{"*.github.com"}, false, "not on the web_fetch allowlist"},
		{"wildcard with bare-host coexists", "github.com", []string{"*.github.com", "github.com"}, true, ""},
		{"wildcard does NOT spoof-match", "github.com.attacker.example", []string{"*.github.com"}, false, "not on the web_fetch allowlist"},
		{"empty host rejected", "", []string{"github.com"}, false, "host is empty"},
		{"trimmed host", "  docs.python.org  ", []string{"docs.python.org"}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			ok, reason := hostAllowed(tc.host, tc.allow)
			if ok != tc.want {
				t.Fatalf("hostAllowed(%q, %v) = %v (reason=%q), want %v", tc.host, tc.allow, ok, reason, tc.want)
			}
			if !tc.want && tc.errSubs != "" && !strings.Contains(reason, tc.errSubs) {
				t.Fatalf("rejection reason %q does not contain %q", reason, tc.errSubs)
			}
		})
	}
}
