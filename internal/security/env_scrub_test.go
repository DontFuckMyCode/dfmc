package security

import (
	"sort"
	"strings"
	"testing"
)

// TestIsSecretEnvKey covers the classifier directly. New secret-key
// shapes go in the data table here so a future regression in
// ScrubEnv pins exactly which key would have leaked.
func TestIsSecretEnvKey(t *testing.T) {
	cases := []struct {
		key  string
		want bool
	}{
		// Provider API keys.
		{"ANTHROPIC_API_KEY", true},
		{"OPENAI_API_KEY", true},
		{"DEEPSEEK_API_KEY", true},
		{"KIMI_API_KEY", true},
		{"ZAI_API_KEY", true},
		{"ALIBABA_API_KEY", true},
		{"MINIMAX_API_KEY", true},
		{"GOOGLE_AI_API_KEY", true},

		// DFMC-internal tokens.
		{"DFMC_WEB_TOKEN", true},
		{"DFMC_REMOTE_TOKEN", true},

		// Generic shapes.
		{"GH_TOKEN", true},
		{"GITHUB_TOKEN", true},
		{"NPM_TOKEN", true},
		{"AWS_SECRET_ACCESS_KEY", true},
		{"AWS_ACCESS_KEY_ID", true},
		{"DB_PASSWORD", true},
		{"SOMETHING_PRIVATE_KEY", true},
		{"ANY_CREDENTIALS", true},
		{"FOO_CREDS", true},
		{"x_apikey", true},

		// Case insensitivity.
		{"anthropic_api_key", true},

		// Things that look secret-like but aren't.
		{"PATH", false},
		{"HOME", false},
		{"USER", false},
		{"LANG", false},
		{"GOPATH", false},
		{"PWD", false},
		{"TERM", false},
		{"DFMC_PROJECT_ROOT", false}, // payload-shaped — operationally fine
		{"DFMC_EVENT", false},

		// Edge cases.
		{"", false},
		{"   ", false},
	}
	for _, c := range cases {
		got := IsSecretEnvKey(c.key)
		if got != c.want {
			t.Errorf("IsSecretEnvKey(%q) = %v, want %v", c.key, got, c.want)
		}
	}
}

// TestScrubEnv_DropsSecretKeys exercises the actual filter on a
// realistic os.Environ() snapshot. The "kept" set must include
// non-secret system vars; the "dropped" set must include every
// secret-shaped key.
func TestScrubEnv_DropsSecretKeys(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"HOME=/home/user",
		"USER=user",
		"ANTHROPIC_API_KEY=sk-fake",
		"DFMC_WEB_TOKEN=tok",
		"AWS_SECRET_ACCESS_KEY=aws",
		"GITHUB_TOKEN=ghp_fake",
		"DB_PASSWORD=hunter2",
		"NOT_PRIVATE=fine",
	}
	out := ScrubEnv(in, nil)

	dropped := []string{
		"ANTHROPIC_API_KEY",
		"DFMC_WEB_TOKEN",
		"AWS_SECRET_ACCESS_KEY",
		"GITHUB_TOKEN",
		"DB_PASSWORD",
	}
	for _, key := range dropped {
		for _, entry := range out {
			if strings.HasPrefix(entry, key+"=") {
				t.Errorf("ScrubEnv must drop %s, but found in output: %q", key, entry)
			}
		}
	}
	// NOTE: classifier is over-eager by design — NOT_SECRET would
	// match the _SECRET suffix, NOT_TOKEN would match _TOKEN, etc.
	// Operators who hit a false positive add the key to
	// env_passthrough (covered in TestScrubEnv_AllowlistOverridesBlock).
	kept := []string{"PATH=/usr/bin", "HOME=/home/user", "USER=user", "NOT_PRIVATE=fine"}
	sort.Strings(out)
	for _, want := range kept {
		found := false
		for _, entry := range out {
			if entry == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ScrubEnv dropped %q which should have been kept; out=%v", want, out)
		}
	}
}

// TestScrubEnv_AllowlistOverridesBlock confirms the operator opt-in
// surface: a secret-shaped key the operator named in env_passthrough
// is forwarded.
func TestScrubEnv_AllowlistOverridesBlock(t *testing.T) {
	in := []string{
		"GITHUB_TOKEN=ghp_fake",
		"OPENAI_API_KEY=sk-openai",
		"PATH=/usr/bin",
	}
	out := ScrubEnv(in, []string{"GITHUB_TOKEN"})

	// GITHUB_TOKEN must survive (allowlisted)
	gotGithub := false
	for _, entry := range out {
		if entry == "GITHUB_TOKEN=ghp_fake" {
			gotGithub = true
		}
	}
	if !gotGithub {
		t.Errorf("allowlisted GITHUB_TOKEN must be forwarded, got out=%v", out)
	}
	// OPENAI_API_KEY must be dropped (not allowlisted)
	for _, entry := range out {
		if strings.HasPrefix(entry, "OPENAI_API_KEY=") {
			t.Errorf("OPENAI_API_KEY must be dropped without explicit allowlist, got %q", entry)
		}
	}
}

// TestScrubEnv_AllowlistCaseInsensitive matches operator config —
// users might write `env_passthrough: [github_token]` in lowercase.
func TestScrubEnv_AllowlistCaseInsensitive(t *testing.T) {
	in := []string{"GITHUB_TOKEN=ghp_fake"}
	out := ScrubEnv(in, []string{"github_token"}) // lowercase
	if len(out) != 1 || out[0] != "GITHUB_TOKEN=ghp_fake" {
		t.Errorf("case-insensitive allowlist failed; got %v", out)
	}
}

// TestScrubEnv_PreservesMalformedEntries — entries without `=` are
// kept as-is so we don't silently swallow them on misformatted env
// arrays.
func TestScrubEnv_PreservesMalformedEntries(t *testing.T) {
	in := []string{"PATH=/usr/bin", "MALFORMED", "=onlyvalue"}
	out := ScrubEnv(in, nil)
	if len(out) != 3 {
		t.Errorf("expected 3 entries (incl. malformed), got %d: %v", len(out), out)
	}
}
