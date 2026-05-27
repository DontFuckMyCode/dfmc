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
		errSubs string
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

// isResultURLBlocked tests

func TestIsResultURLBlocked_InvalidURL(t *testing.T) {
	cases := []string{"", "   ", "hello world", "http:", "javascript:alert(1)", "data:text/html,<h1>"}
	for _, href := range cases {
		if got := isResultURLBlocked(href); !got {
			t.Errorf("isResultURLBlocked(%q) = false, want true", href)
		}
	}
}

func TestIsResultURLBlocked_SchemeBlocked(t *testing.T) {
	for _, href := range []string{"ftp://example.com", "javascript:alert(1)", "data:text/html,<h1>"} {
		if got := isResultURLBlocked(href); !got {
			t.Errorf("isResultURLBlocked(%q) = false, want true", href)
		}
	}
}

func TestIsResultURLBlocked_ValidHTTP(t *testing.T) {
	if got := isResultURLBlocked("https://example.com/page"); got {
		t.Error("https URL should not be blocked")
	}
	if got := isResultURLBlocked("http://example.com"); got {
		t.Error("http URL should not be blocked")
	}
}

// decodeDuckRedirect tests

func TestDecodeDuckRedirect(t *testing.T) {
	cases := []struct {
		label string
		href  string
		want  string
	}{
		{"plain URL unchanged", "https://example.com/page", "https://example.com/page"},
		{"uddg decodes target", "https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage", "https://example.com/page"},
		{"non-l path not decoded", "https://duckduckgo.com/v/?uddg=https://example.com", "https://duckduckgo.com/v/?uddg=https://example.com"},
		{"empty uddg", "https://duckduckgo.com/l/?uddg=", "https://duckduckgo.com/l/?uddg="},
		{"invalid escape falls through", "https://duckduckgo.com/l/?uddg=%ZZ", "https://duckduckgo.com/l/?uddg=%ZZ"},
	}
	for _, tc := range cases {
		got := decodeDuckRedirect(tc.href)
		if got != tc.want {
			t.Errorf("decodeDuckRedirect(%q) = %q, want %q", tc.href, got, tc.want)
		}
	}
}

// stripTags tests

func TestStripTags(t *testing.T) {
	cases := []struct {
		label string
		input string
		want  string
	}{
		{"plain text unchanged", "hello world", "hello world"},
		{"simple tag removed", "hello <b>world</b>", "hello world"},
		{"nested tags", "a <b><i>c</i></b> d", "a c d"},
		{"script tag and content removed", "foo<script>alert(1)</script>bar", "foobar"},
		{"amp entity decoded", "Tom &amp; Jerry", "Tom & Jerry"},
		{"lt gt entities", "a &lt;b&gt; c", "a <b> c"},
	}
	for _, tc := range cases {
		got := stripTags(tc.input)
		if got != tc.want {
			t.Errorf("stripTags(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// isBlockedHost tests

func TestIsBlockedHost_InvalidHost(t *testing.T) {
	if !isBlockedHost("") {
		t.Error("empty host should be blocked")
	}
	if !isBlockedHost("   ") {
		t.Error("whitespace-only host should be blocked")
	}
}

func TestIsBlockedHost_Loopback(t *testing.T) {
	if !isBlockedHost("127.0.0.1") {
		t.Error("127.0.0.1 should be blocked")
	}
	if !isBlockedHost("127.0.0.1:8080") {
		t.Error("127.0.0.1:8080 should be blocked")
	}
}

// WebFetchTool Execute error paths

func TestWebFetchTool_Execute_InvalidURL(t *testing.T) {
	tool := NewWebFetchTool()
	_, err := tool.Execute(nil, Request{Params: map[string]any{"url": "not-a-url"}})
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestWebFetchTool_Execute_BlockedHost(t *testing.T) {
	tool := NewWebFetchTool()
	_, err := tool.Execute(nil, Request{Params: map[string]any{"url": "https://127.0.0.1:9999"}})
	if err == nil {
		t.Fatal("expected error for blocked host")
	}
}

func TestWebFetchTool_Execute_MissingURL(t *testing.T) {
	tool := NewWebFetchTool()
	_, err := tool.Execute(nil, Request{Params: map[string]any{}})
	if err == nil {
		t.Fatal("expected error for missing URL")
	}
}
