// config_model.go — per-provider model profile and its accessor methods.
// Extracted from config_types.go to keep the top-level Config struct readable.
// All methods are pure field readers; behaviour lives in the loader/saver.

package config

import "strings"

// ModelConfig describes one named provider profile: credentials, model list,
// cost hint, and capability tags. Profiles are stored in ProvidersConfig.Profiles
// and referenced by name from RoutingConfig rules.
type ModelConfig struct {
	APIKey          string   `yaml:"api_key,omitempty"`
	APIKeyEncrypted string   `yaml:"api_key_enc,omitempty"`
	BaseURL         string   `yaml:"base_url,omitempty"`
	CatalogID       string   `yaml:"catalog_id,omitempty"`
	Models          []string `yaml:"models,omitempty"`
	FallbackModels  []string `yaml:"fallback_models,omitempty"`
	Model           string   `yaml:"model,omitempty"`
	MaxTokens       int      `yaml:"max_tokens,omitempty"`
	MaxContext      int      `yaml:"max_context,omitempty"`
	Protocol        string   `yaml:"protocol,omitempty"`
	Region          string   `yaml:"region,omitempty"`
	HTTPTimeout     int      `yaml:"http_timeout,omitempty"`
	Tags            []string `yaml:"tags,omitempty"`
	CostPer1kTokens float64  `yaml:"cost_per_1k_tokens,omitempty"`
}

// BestModel returns the preferred model: Models[0] if set, otherwise Model.
func (c ModelConfig) BestModel() string {
	if len(c.Models) > 0 {
		return c.Models[0]
	}
	return c.Model
}

// AllModels returns the full ordered model list.
func (c ModelConfig) AllModels() []string {
	if len(c.Models) > 0 {
		return c.Models
	}
	if c.Model != "" {
		return []string{c.Model}
	}
	return nil
}

// TagMatches reports whether the profile has a tag matching name (case-insensitive).
func (c ModelConfig) TagMatches(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, t := range c.Tags {
		if strings.EqualFold(strings.TrimSpace(t), name) {
			return true
		}
	}
	return false
}
