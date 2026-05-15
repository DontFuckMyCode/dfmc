package provider

// router_profile.go — config.ModelConfig → provider.Provider
// constructor logic. Now delegates to the plugin registry in
// plugins/registry.go instead of a hardcoded switch statement.

import (
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider/plugins"
)

// providerFromProfile constructs a Provider from a ModelConfig using
// the plugin registry. Falls back to PlaceholderProvider when no
// factory is registered or credentials are missing.
func providerFromProfile(name string, profile config.ModelConfig) Provider {
	// Determine protocol from name or explicit protocol field
	protocol := plugins.NormalizedProtocol(name, profile.Protocol)

	// Z.AI self-heal: if using anthropic protocol with Z.AI anthropic-style URL,
	// remap to OpenAI-compatible paas/v4 endpoint
	if name == "zai" && (protocol == "anthropic" || strings.Contains(strings.ToLower(profile.BaseURL), "/api/anthropic")) {
		protocol = plugins.ProtocolOpenAICompatible
		// Only update baseURL if it's empty
		if profile.BaseURL == "" {
			profile.BaseURL = defaultOpenAIBaseURL("zai")
		}
	}

	// Try the plugin registry first
	factory := plugins.Get(protocol)
	if factory != nil {
		cfg := factory.BuildConfig(name, profile)

		// Z.AI post-build fix: if still using anthropic-style URL, fix it
		if name == "zai" && strings.Contains(strings.ToLower(cfg.BaseURL), "/api/anthropic") {
			cfg.BaseURL = defaultOpenAIBaseURL("zai")
		}

		if cfg.APIKey == "" && cfg.BaseURL == "" {
			// Missing credentials — use placeholder
			return withProfileModelChain(NewPlaceholderProvider(name, cfg.BestModel(), false, profile.MaxContext), profile)
		}
		// Build actual provider using existing NewXXX functions
		p := buildProvider(name, protocol, cfg)
		if p != nil {
			return withProfileModelChain(p, profile)
		}
		// BuildConfig returned nil — fallback
		return withProfileModelChain(NewPlaceholderProvider(name, cfg.BestModel(), false, profile.MaxContext), profile)
	}

	// No factory registered — fall back to PlaceholderProvider
	configured := profile.APIKey != "" || profile.BaseURL != ""
	model := profile.Model
	if model == "" && len(profile.Models) > 0 {
		model = profile.Models[0]
	}
	return withProfileModelChain(NewPlaceholderProvider(name, model, configured, profile.MaxContext), profile)
}

// buildProvider constructs the actual provider based on protocol.
func buildProvider(name, protocol string, cfg plugins.Config) Provider {
	switch protocol {
	case plugins.ProtocolAnthropic:
		return newAnthropicProvider(name, cfg)
	case plugins.ProtocolGoogle:
		return newGoogleProvider(name, cfg)
	case plugins.ProtocolOpenAI, plugins.ProtocolOpenAICompatible:
		return newOpenAICompatibleProvider(name, cfg)
	default:
		return nil
	}
}

// newAnthropicProvider wraps NewNamedAnthropicProvider.
func newAnthropicProvider(name string, cfg plugins.Config) Provider {
	timeout := httpTimeout(cfg.HTTPTimeout)
	return NewNamedAnthropicProvider(name, cfg.Model, cfg.APIKey, cfg.BaseURL, cfg.MaxTokens, cfg.MaxContext, timeout)
}

// newGoogleProvider wraps NewGoogleProvider.
func newGoogleProvider(name string, cfg plugins.Config) Provider {
	timeout := httpTimeout(cfg.HTTPTimeout)
	return NewGoogleProvider(cfg.Model, cfg.APIKey, cfg.BaseURL, cfg.MaxTokens, cfg.MaxContext, timeout)
}

// newOpenAICompatibleProvider wraps NewOpenAICompatibleProvider.
func newOpenAICompatibleProvider(name string, cfg plugins.Config) Provider {
	timeout := httpTimeout(cfg.HTTPTimeout)
	return NewOpenAICompatibleProvider(name, cfg.Model, cfg.APIKey, cfg.BaseURL, cfg.MaxTokens, cfg.MaxContext, timeout)
}

func profileModelChain(profile config.ModelConfig) []string {
	chain := make([]string, 0, 1+len(profile.FallbackModels))
	if primary := strings.TrimSpace(profile.Model); primary != "" {
		chain = append(chain, primary)
	} else if len(profile.Models) > 0 {
		if primary := strings.TrimSpace(profile.Models[0]); primary != "" {
			chain = append(chain, primary)
		}
	}
	for _, model := range profile.FallbackModels {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		seen := false
		for _, existing := range chain {
			if strings.EqualFold(existing, model) {
				seen = true
				break
			}
		}
		if !seen {
			chain = append(chain, model)
		}
	}
	return chain
}

func withProfileModelChain(p Provider, profile config.ModelConfig) Provider {
	chain := profileModelChain(profile)
	if len(chain) == 0 {
		return p
	}
	if sp, ok := p.(interface{ SetModels([]string) }); ok {
		sp.SetModels(chain)
		return p
	}
	switch v := p.(type) {
	case *AnthropicProvider:
		v.model = chain[0]
		v.models = chain
	case *GoogleProvider:
		v.model = chain[0]
		v.models = chain
	case *OpenAICompatibleProvider:
		v.model = chain[0]
		v.models = chain
	case *PlaceholderProvider:
		v.model = chain[0]
		v.models = chain
	}
	return p
}

// httpTimeout converts an HTTPTimeout field value (seconds) to a time.Duration.
func httpTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
