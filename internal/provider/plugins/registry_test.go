package plugins

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestConfig_BestModel(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"explicit model wins", Config{Model: "gpt-4o", Models: []string{"gpt-3.5"}}, "gpt-4o"},
		{"falls back to first of Models", Config{Models: []string{"claude-x", "claude-y"}}, "claude-x"},
		{"empty when nothing set", Config{}, ""},
		{"empty Models slice is empty", Config{Models: []string{}}, ""},
	}
	for _, tc := range cases {
		if got := tc.cfg.BestModel(); got != tc.want {
			t.Errorf("%s: BestModel() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestNormalizedProtocol(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		want     string
	}{
		// Explicit protocol overrides the name mapping (and is lowercased/trimmed).
		{"anything", "OpenAI-Compatible", ProtocolOpenAICompatible},
		{"ignored", "  Anthropic  ", ProtocolAnthropic},
		// Name-based mapping when protocol is empty.
		{"anthropic", "", ProtocolAnthropic},
		{"minimax", "", ProtocolAnthropic},
		{"openai", "", ProtocolOpenAI},
		{"google", "", ProtocolGoogle},
		{"gemini", "", ProtocolGoogle},
		{"deepseek", "", ProtocolOpenAICompatible},
		{"kimi", "", ProtocolOpenAICompatible},
		{"zai", "", ProtocolOpenAICompatible},
		{"alibaba", "", ProtocolOpenAICompatible},
		{"ollama", "", ProtocolOpenAICompatible},
		{"groq", "", ProtocolOpenAICompatible},
		{"generic", "", ProtocolOpenAICompatible},
		// Case-insensitive / whitespace-tolerant on the name.
		{"  ANTHROPIC ", "", ProtocolAnthropic},
		{"GeMiNi", "", ProtocolGoogle},
		// Unknown name with no explicit protocol -> empty.
		{"some-unknown-provider", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		if got := NormalizedProtocol(tc.name, tc.protocol); got != tc.want {
			t.Errorf("NormalizedProtocol(%q, %q) = %q, want %q", tc.name, tc.protocol, got, tc.want)
		}
	}
}

// TestGet_BuiltinsRegistered confirms builtin.go's init() registered every
// protocol constant, and that the stored factory carries the matching
// Protocol field. A nil here means the init wiring regressed.
func TestGet_BuiltinsRegistered(t *testing.T) {
	for _, p := range []string{ProtocolAnthropic, ProtocolGoogle, ProtocolOpenAI, ProtocolOpenAICompatible} {
		f := Get(p)
		if f == nil {
			t.Fatalf("Get(%q) = nil; builtin factory not registered", p)
		}
		if f.Protocol != p {
			t.Errorf("Get(%q).Protocol = %q, want %q", p, f.Protocol, p)
		}
	}
	if f := Get("no-such-protocol"); f != nil {
		t.Errorf("Get(unknown) = %v, want nil", f)
	}
}

// TestNormalizedProtocol_OutputsAreRegistered is a consistency guard tying
// the two halves together: every non-empty protocol the normalizer can
// emit for a known provider name must have a registered factory, otherwise
// a config naming that provider would resolve to a protocol nobody can
// build.
func TestNormalizedProtocol_OutputsAreRegistered(t *testing.T) {
	names := []string{
		"anthropic", "minimax", "openai", "google", "gemini",
		"deepseek", "kimi", "zai", "alibaba", "ollama", "groq", "generic",
	}
	for _, n := range names {
		proto := NormalizedProtocol(n, "")
		if proto == "" {
			t.Errorf("known provider %q normalized to empty protocol", n)
			continue
		}
		if Get(proto) == nil {
			t.Errorf("provider %q -> protocol %q has no registered factory", n, proto)
		}
	}
}

// TestRegisterProvider_RoundTripAndDuplicatePanic covers both the happy
// path (register then Get) and the duplicate guard (a second registration
// for the same protocol panics). A unique protocol name keeps this from
// colliding with the builtin registrations in the shared global registry.
func TestRegisterProvider_RoundTripAndDuplicatePanic(t *testing.T) {
	const proto = "test-plugins-roundtrip"
	RegisterProvider(proto, Factory{Protocol: proto, DefaultBaseURL: "http://x", SupportsTools: true})

	got := Get(proto)
	if got == nil {
		t.Fatalf("Get(%q) = nil after RegisterProvider", proto)
	}
	if got.DefaultBaseURL != "http://x" || !got.SupportsTools {
		t.Errorf("round-trip mismatch: %+v", *got)
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on duplicate registration of %q", proto)
		}
	}()
	RegisterProvider(proto, Factory{Protocol: proto}) // must panic
}

// TestBuiltinFactory_BuildConfig exercises a builtin BuildConfig closure:
// the Anthropic factory must fall back to Models[0] when Model is empty and
// to the default base URL when BaseURL is empty, while passing through the
// rest of the profile.
func TestBuiltinFactory_BuildConfig(t *testing.T) {
	f := Get(ProtocolAnthropic)
	if f == nil || f.BuildConfig == nil {
		t.Fatal("anthropic factory or its BuildConfig is nil")
	}
	cfg := f.BuildConfig("primary", config.ModelConfig{
		Models:      []string{"claude-sonnet-4-6", "claude-opus-4-8"},
		APIKey:      "sk-test",
		MaxTokens:   1024,
		MaxContext:  200000,
		HTTPTimeout: 30,
	})
	if cfg.Model != "claude-sonnet-4-6" {
		t.Errorf("Model = %q, want claude-sonnet-4-6 (Models[0] fallback)", cfg.Model)
	}
	if cfg.BaseURL != "https://api.anthropic.com/v1" {
		t.Errorf("BaseURL = %q, want the anthropic default", cfg.BaseURL)
	}
	if cfg.Name != "primary" || cfg.APIKey != "sk-test" || cfg.MaxTokens != 1024 ||
		cfg.MaxContext != 200000 || cfg.HTTPTimeout != 30 || cfg.Protocol != ProtocolAnthropic {
		t.Errorf("profile passthrough mismatch: %+v", cfg)
	}
	if cfg.BestModel() != "claude-sonnet-4-6" {
		t.Errorf("BestModel() = %q, want claude-sonnet-4-6", cfg.BestModel())
	}
}

// TestBuiltinFactories_BaseURLDefaults pins the per-protocol base-URL
// behaviour across all four builtin factories. Anthropic/Google/OpenAI
// supply a default when the profile leaves BaseURL empty; the generic
// OpenAI-compatible factory deliberately does NOT (the endpoint must be
// configured), and all four must honour an explicit BaseURL override.
func TestBuiltinFactories_BaseURLDefaults(t *testing.T) {
	cases := []struct {
		protocol    string
		wantDefault string // expected BaseURL when profile.BaseURL == ""
	}{
		{ProtocolAnthropic, "https://api.anthropic.com/v1"},
		{ProtocolGoogle, "https://generativelanguage.googleapis.com/v1beta"},
		{ProtocolOpenAI, "https://api.openai.com/v1"},
		{ProtocolOpenAICompatible, ""}, // no default — must be configured
	}
	for _, tc := range cases {
		f := Get(tc.protocol)
		if f == nil || f.BuildConfig == nil {
			t.Fatalf("%s: factory/BuildConfig missing", tc.protocol)
		}
		// Empty BaseURL -> the protocol's default (or "" for compatible).
		gotDefault := f.BuildConfig("p", config.ModelConfig{Model: "m"})
		if gotDefault.BaseURL != tc.wantDefault {
			t.Errorf("%s: default BaseURL = %q, want %q", tc.protocol, gotDefault.BaseURL, tc.wantDefault)
		}
		// Explicit BaseURL is always honoured verbatim.
		const custom = "https://proxy.internal/v1"
		gotCustom := f.BuildConfig("p", config.ModelConfig{Model: "m", BaseURL: custom})
		if gotCustom.BaseURL != custom {
			t.Errorf("%s: explicit BaseURL = %q, want %q", tc.protocol, gotCustom.BaseURL, custom)
		}
	}
}
