package security

import (
	"strings"
	"testing"
)

// TestRedactSecrets_ProviderKeys pins the load-bearing
// invariant: every provider's API-key shape must be replaced with
// the redaction marker. New shapes added to redactionPatterns get a
// row here so a regression in pattern ordering or composition is
// caught.
func TestRedactSecrets_ProviderKeys(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"Anthropic", "API key sk-ant-" + strings.Repeat("a", 50) + " in body"},
		{"OpenAI", "OPENAI_API_KEY=sk-" + strings.Repeat("X", 40)},
		{"AWS access key id", "id=AKIAIOSFODNN7EXAMPLE here"},
		{"GitHub PAT", "token: ghp_" + strings.Repeat("a", 36)},
		{"GitHub OAuth", "token: gho_" + strings.Repeat("a", 36)},
		{"GitLab", "token: glpat-" + strings.Repeat("a", 30)},
		{"Slack bot", "xoxb-12345-67890-abcdef"},
		{"Stripe live", "sk_live_" + strings.Repeat("a", 30)},
		{"Google API", "AIza" + strings.Repeat("a", 35)},
		{"Bearer header", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload"},
		{"Bearer bare", "Bearer abcdef0123456789ABCDEF"},
		{"Postgres URL", "postgres://user:hunter2@host/db"},
		{"MongoDB SRV", "mongodb+srv://admin:p@ssw0rd@cluster/db"},
		{"PEM private key", "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIB"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := RedactSecrets(c.in)
			if !IsRedactionMarker(out) {
				t.Errorf("redactor missed %s: in=%q out=%q", c.name, c.in, out)
			}
			if out == c.in {
				t.Errorf("redactor returned input unchanged for %s", c.name)
			}
		})
	}
}

// TestRedactSecrets_PreservesContent confirms surrounding non-
// secret text is preserved so legitimate content (file paths,
// prose, numbers) stays informative.
func TestRedactSecrets_PreservesContent(t *testing.T) {
	in := "The token is sk-" + strings.Repeat("X", 40) + " for the foo service."
	out := RedactSecrets(in)
	if !strings.Contains(out, "The token is") || !strings.Contains(out, "for the foo service.") {
		t.Errorf("non-secret text must survive: got %q", out)
	}
}

// TestRedactSecrets_NoFalsePositives — long ASCII strings that
// don't match any pattern must pass through unchanged. Important
// because tool output is dominated by file paths, source code, and
// prose; a false-positive redaction would shred ordinary content.
func TestRedactSecrets_NoFalsePositives(t *testing.T) {
	cases := []string{
		"package main\n\nimport \"fmt\"",
		"/Users/x/Projects/myapp/internal/handler.go:42",
		"This is a regular sentence with no secrets in it.",
		"123456789-abcdef-test-fixture",
		"function name = handleRequest, line = 99",
		"sha256: abc123def456 (commit hash)",
	}
	for _, in := range cases {
		out := RedactSecrets(in)
		if out != in {
			t.Errorf("clean input modified: in=%q out=%q", in, out)
		}
	}
}

// TestRedactSecrets_Empty checks the empty-string fast path returns
// empty (not panic, not the marker).
func TestRedactSecrets_Empty(t *testing.T) {
	if got := RedactSecrets(""); got != "" {
		t.Errorf("empty input must round-trip, got %q", got)
	}
}

// TestRedactSecretsInValue_NestedMap walks a realistic tool params
// map and confirms secrets buried under nested keys are redacted.
// The shape mirrors what `web_fetch(headers={Authorization: Bearer
// sk-…})` actually publishes on tool:call events.
func TestRedactSecretsInValue_NestedMap(t *testing.T) {
	in := map[string]any{
		"url": "https://api.example.com/v1/resource",
		"headers": map[string]any{
			"Authorization": "Bearer sk-" + strings.Repeat("a", 40),
			"Content-Type":  "application/json",
		},
		"args": []string{"--token=sk-" + strings.Repeat("b", 40), "--verbose"},
	}
	got := RedactSecretsInValue(in)
	gm, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", got)
	}
	headers := gm["headers"].(map[string]any)
	if !IsRedactionMarker(headers["Authorization"].(string)) {
		t.Errorf("Authorization header not redacted: %v", headers["Authorization"])
	}
	if headers["Content-Type"].(string) != "application/json" {
		t.Errorf("benign header should pass through unchanged")
	}
	args := gm["args"].([]string)
	if !IsRedactionMarker(args[0]) {
		t.Errorf("--token= arg not redacted: %v", args[0])
	}
	if args[1] != "--verbose" {
		t.Errorf("benign arg should pass through unchanged: %v", args[1])
	}
}

// TestRedactSecretsInValue_NonStringLeavesPreserved ensures
// numeric/bool/nil leaves don't get string-coerced — they must
// flow through unchanged so the JSON shape on the wire stays
// consistent for clients.
func TestRedactSecretsInValue_NonStringLeavesPreserved(t *testing.T) {
	in := map[string]any{
		"count":   42,
		"enabled": true,
		"ratio":   3.14,
		"nothing": nil,
	}
	got := RedactSecretsInValue(in).(map[string]any)
	if got["count"] != 42 {
		t.Errorf("number changed: %v", got["count"])
	}
	if got["enabled"] != true {
		t.Errorf("bool changed: %v", got["enabled"])
	}
	if got["ratio"] != 3.14 {
		t.Errorf("float changed: %v", got["ratio"])
	}
	if got["nothing"] != nil {
		t.Errorf("nil changed: %v", got["nothing"])
	}
}
