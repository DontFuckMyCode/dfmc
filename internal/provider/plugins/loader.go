package plugins

// loader.go — config-driven provider factory loader.
//
// Reads a config map and delegates to the global registry
// to construct Provider instances. Falls back to PlaceholderProvider
// when credentials are missing or no factory is registered.

import (
	"fmt"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// LoadProviders reads the provider profiles and returns a map of
// name -> Config (for router_profile.go to construct actual providers).
func LoadProviders(profiles map[string]config.ModelConfig) (map[string]Config, error) {
	result := make(map[string]Config)

	for name, profile := range profiles {
		cfg := loadProvider(name, profile)
		if cfg.Model != "" || cfg.BaseURL != "" || cfg.APIKey != "" {
			result[name] = cfg
		}
	}

	return result, nil
}

// loadProvider extracts a Config from a profile.
func loadProvider(name string, profile config.ModelConfig) Config {
	protocol := NormalizedProtocol(name, profile.Protocol)

	// Try registry to get default base URL
	factory := Get(protocol)
	defaultBaseURL := ""
	if factory != nil {
		defaultBaseURL = factory.DefaultBaseURL
	}

	baseURL := profile.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	return Config{
		Name:        name,
		Model:       profile.Model,
		Models:      profile.Models,
		APIKey:      profile.APIKey,
		BaseURL:     baseURL,
		MaxTokens:   profile.MaxTokens,
		MaxContext:  profile.MaxContext,
		HTTPTimeout: profile.HTTPTimeout,
		Protocol:    protocol,
	}
}

// ErrUnknownProtocol is returned when a protocol has no registered factory.
var ErrUnknownProtocol = fmt.Errorf("unknown provider protocol")
