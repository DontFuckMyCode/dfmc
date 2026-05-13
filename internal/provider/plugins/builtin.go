package plugins

// builtin.go — registers the built-in provider factories.
//
// This file is imported by the provider package's init sequence so that
// existing providers (Anthropic, Google, OpenAI-compatible) are
// automatically registered in the plugin registry.
//
// Each registration pairs a protocol name with a BuildConfig function
// that extracts config from ModelConfig. Actual provider construction
// happens in router_profile.go using existing NewXXX functions.

import (
	"github.com/dontfuckmycode/dfmc/internal/config"
)

func init() {
	// Anthropic protocol — handles anthropic, minimax
	RegisterProvider(ProtocolAnthropic, Factory{
		Protocol:       ProtocolAnthropic,
		DefaultBaseURL: "https://api.anthropic.com/v1",
		SupportsTools:  true,
		BuildConfig: func(name string, profile config.ModelConfig) Config {
			model := profile.Model
			if model == "" && len(profile.Models) > 0 {
				model = profile.Models[0]
			}
			baseURL := profile.BaseURL
			if baseURL == "" {
				baseURL = "https://api.anthropic.com/v1"
			}
			return Config{
				Name:        name,
				Model:       model,
				APIKey:      profile.APIKey,
				BaseURL:     baseURL,
				MaxTokens:   profile.MaxTokens,
				MaxContext:  profile.MaxContext,
				HTTPTimeout: profile.HTTPTimeout,
				Protocol:    ProtocolAnthropic,
			}
		},
	})

	// Google protocol — handles google, gemini
	RegisterProvider(ProtocolGoogle, Factory{
		Protocol:       ProtocolGoogle,
		DefaultBaseURL: "https://generativelanguage.googleapis.com/v1beta",
		SupportsTools:  true,
		BuildConfig: func(name string, profile config.ModelConfig) Config {
			model := profile.Model
			if model == "" && len(profile.Models) > 0 {
				model = profile.Models[0]
			}
			baseURL := profile.BaseURL
			if baseURL == "" {
				baseURL = "https://generativelanguage.googleapis.com/v1beta"
			}
			return Config{
				Name:        name,
				Model:       model,
				APIKey:      profile.APIKey,
				BaseURL:     baseURL,
				MaxTokens:   profile.MaxTokens,
				MaxContext:  profile.MaxContext,
				HTTPTimeout: profile.HTTPTimeout,
				Protocol:    ProtocolGoogle,
			}
		},
	})

	// OpenAI protocol — handles openai (uses OpenAI-compatible API)
	RegisterProvider(ProtocolOpenAI, Factory{
		Protocol:       ProtocolOpenAI,
		DefaultBaseURL: "https://api.openai.com/v1",
		SupportsTools:  true,
		BuildConfig: func(name string, profile config.ModelConfig) Config {
			model := profile.Model
			if model == "" && len(profile.Models) > 0 {
				model = profile.Models[0]
			}
			baseURL := profile.BaseURL
			if baseURL == "" {
				baseURL = "https://api.openai.com/v1"
			}
			return Config{
				Name:        name,
				Model:       model,
				APIKey:      profile.APIKey,
				BaseURL:     baseURL,
				MaxTokens:   profile.MaxTokens,
				MaxContext:  profile.MaxContext,
				HTTPTimeout: profile.HTTPTimeout,
				Protocol:    ProtocolOpenAI,
			}
		},
	})

	// OpenAI-compatible protocol — handles openai, deepseek, kimi, zai,
	// alibaba, ollama, groq, and any other OpenAI-compatible API
	RegisterProvider(ProtocolOpenAICompatible, Factory{
		Protocol:       ProtocolOpenAICompatible,
		DefaultBaseURL: "", // no default — must be configured
		SupportsTools:  true,
		BuildConfig: func(name string, profile config.ModelConfig) Config {
			model := profile.Model
			if model == "" && len(profile.Models) > 0 {
				model = profile.Models[0]
			}
			return Config{
				Name:        name,
				Model:       model,
				APIKey:      profile.APIKey,
				BaseURL:     profile.BaseURL,
				MaxTokens:   profile.MaxTokens,
				MaxContext:  profile.MaxContext,
				HTTPTimeout: profile.HTTPTimeout,
				Protocol:    ProtocolOpenAICompatible,
			}
		},
	})
}