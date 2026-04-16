package tui

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestFormatProviderSwitchNotice_ConfiguredAnthropic(t *testing.T) {
	got := formatProviderSwitchNotice(engine.ProviderProfileStatus{
		Name:       "anthropic",
		Model:      "claude-sonnet-4-6",
		Protocol:   "anthropic",
		BaseURL:    "https://api.anthropic.com/v1",
		Configured: true,
	})
	for _, want := range []string{"provider → anthropic", "model: claude-sonnet-4-6", "endpoint: https://api.anthropic.com/v1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected notice to contain %q, got %q", want, got)
		}
	}
}

// When a well-known profile has no API key, the notice must name the
// exact env var the user is missing. Generic "not configured" was the
// regression that prompted this test — users couldn't tell which var to
// set.
func TestFormatProviderSwitchNotice_UnconfiguredNamesEnvVar(t *testing.T) {
	cases := []struct {
		name     string
		profile  string
		wantVars []string
	}{
		{"anthropic", "anthropic", []string{"ANTHROPIC_API_KEY", "anthropic"}},
		{"openai", "openai", []string{"OPENAI_API_KEY"}},
		{"deepseek", "deepseek", []string{"DEEPSEEK_API_KEY"}},
		{"kimi", "kimi", []string{"KIMI_API_KEY"}},
		{"minimax", "minimax", []string{"MINIMAX_API_KEY"}},
		{"zai", "zai", []string{"ZAI_API_KEY"}},
		{"alibaba", "alibaba", []string{"ALIBABA_API_KEY"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatProviderSwitchNotice(engine.ProviderProfileStatus{
				Name:       tc.profile,
				Configured: false,
			})
			if !strings.Contains(got, "falling back to offline") {
				t.Fatalf("notice must mention offline fallback, got %q", got)
			}
			for _, v := range tc.wantVars {
				if !strings.Contains(got, v) {
					t.Fatalf("notice for %s should mention %q, got %q", tc.profile, v, got)
				}
			}
		})
	}
}

// Unknown provider should still produce a usable hint without hardcoding
// an env var name — falls back to config.yaml path guidance.
func TestFormatProviderSwitchNotice_UnknownProviderStillHelpful(t *testing.T) {
	got := formatProviderSwitchNotice(engine.ProviderProfileStatus{
		Name:       "custom-proxy",
		Configured: false,
	})
	if !strings.Contains(got, "providers.profiles.custom-proxy.api_key") {
		t.Fatalf("notice should reference config path for unknown provider, got %q", got)
	}
	if !strings.Contains(got, "falling back to offline") {
		t.Fatalf("notice should still mention offline fallback, got %q", got)
	}
}
