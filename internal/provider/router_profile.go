package provider

// router_profile.go — config.ModelConfig → provider.Provider
// constructor logic. Sibling of router.go which keeps the Router
// struct, the observer wiring, the public Register/Primary/Fallback/
// Get/List/ResolveOrder surface, and the top-level Complete/Stream
// entry points (with retry siblings retry_throttle.go +
// retry_chain.go + retry_context.go + race.go + stream_recovery.go).
//
// Splitting the profile→provider constructor out keeps router.go
// scoped to "what is the lookup cascade and how do I observe it"
// while this file owns "given a config block, which concrete
// Provider does the router get?" — the protocol normalize, the
// per-protocol switch (anthropic / google / openai-compat /
// placeholder), the api-key-missing→placeholder fallback, and the
// Z.AI Anthropic-endpoint→OpenAI-compatible self-heal that DFMC
// applies because the Claude-style /api/anthropic surface 404s
// against Z.AI's actual deployment.

import (
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func providerFromProfile(name string, profile config.ModelConfig) Provider {
	name = normalizeProviderName(name)
	model := profile.Model
	apiKey := strings.TrimSpace(profile.APIKey)
	baseURL := strings.TrimSpace(profile.BaseURL)
	protocol := normalizedProtocol(name, profile.Protocol)
	if name == "zai" && (protocol == "anthropic" || strings.Contains(strings.ToLower(baseURL), "/api/anthropic")) {
		// Z.AI documents an Anthropic-compatible endpoint for Claude Code style
		// clients, but DFMC's runtime behaves more reliably against Z.AI's
		// OpenAI-compatible `/api/paas/v4` surface. Users often paste the
		// Claude-style base URL into DFMC and hit 404_NOT_FOUND; remap that
		// configuration onto the stable OpenAI-compatible endpoint so the
		// profile self-heals instead of failing at runtime.
		protocol = "openai-compatible"
		baseURL = defaultOpenAIBaseURL(name)
	}

	switch protocol {
	case "anthropic":
		if apiKey == "" {
			return NewPlaceholderProvider(name, model, false, profile.MaxContext)
		}
		return NewNamedAnthropicProvider(name, model, apiKey, baseURL, profile.MaxTokens, profile.MaxContext, httpTimeout(profile.HTTPTimeout))
	case "google", "gemini":
		if apiKey == "" {
			return NewPlaceholderProvider(name, model, false, profile.MaxContext)
		}
		return NewGoogleProvider(model, apiKey, baseURL, profile.MaxTokens, profile.MaxContext, httpTimeout(profile.HTTPTimeout))
	case "openai", "openai-compatible":
		if name == "generic" && strings.TrimSpace(baseURL) == "" {
			return NewPlaceholderProvider(name, model, false, profile.MaxContext)
		}
		if name != "generic" && apiKey == "" {
			return NewPlaceholderProvider(name, model, false, profile.MaxContext)
		}
		return NewOpenAICompatibleProvider(name, model, apiKey, baseURL, profile.MaxTokens, profile.MaxContext, httpTimeout(profile.HTTPTimeout))
	default:
		configured := apiKey != "" || baseURL != ""
		return NewPlaceholderProvider(name, model, configured, profile.MaxContext)
	}
}

// httpTimeout converts an HTTPTimeout field value (seconds) to a time.Duration.
// 0 returns 0 (caller uses default via newProviderHTTPClient(0)).
func httpTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func normalizedProtocol(name, protocol string) string {
	p := strings.ToLower(strings.TrimSpace(protocol))
	if p != "" {
		return p
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic", "minimax":
		return "anthropic"
	case "openai":
		return "openai"
	case "google", "gemini":
		return "google"
	case "deepseek", "generic", "kimi", "zai", "alibaba":
		return "openai-compatible"
	default:
		return ""
	}
}
