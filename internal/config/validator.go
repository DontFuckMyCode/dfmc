package config

import (
	"fmt"
	"net/url"
	"strings"
)

func (c *Config) Validate() error {
	if c.Version <= 0 {
		return fmt.Errorf("invalid version: %d", c.Version)
	}

	if strings.TrimSpace(c.Providers.Primary) == "" {
		return fmt.Errorf("providers.primary is required")
	}
	if c.Providers.Profiles == nil {
		return fmt.Errorf("providers.profiles must be defined")
	}
	if _, ok := c.Providers.Profiles[c.Providers.Primary]; !ok {
		return fmt.Errorf("providers.primary %q not found in providers.profiles", c.Providers.Primary)
	}
	for _, fb := range c.Providers.Fallback {
		if _, ok := c.Providers.Profiles[fb]; !ok {
			return fmt.Errorf("providers.fallback %q not found in providers.profiles", fb)
		}
	}
	for name, profile := range c.Providers.Profiles {
		if name == "offline" {
			continue
		}
		if strings.TrimSpace(profile.Model) == "" {
			return fmt.Errorf("providers.profiles.%q: model is required for non-offline providers", name)
		}
		if profile.BaseURL != "" {
			u, err := url.Parse(profile.BaseURL)
			if err != nil {
				return fmt.Errorf("providers.profiles.%q: base_url %q is not a valid URL: %v", name, profile.BaseURL, err)
			}
			if u.Scheme == "" || u.Host == "" {
				return fmt.Errorf("providers.profiles.%q: base_url %q must include scheme (https) and host", name, profile.BaseURL)
			}
			if u.Scheme != "http" && u.Scheme != "https" {
				return fmt.Errorf("providers.profiles.%q: base_url %q scheme must be http or https", name, profile.BaseURL)
			}
		}
		if profile.Protocol != "" {
			switch strings.ToLower(profile.Protocol) {
			case "anthropic", "google", "gemini", "openai", "openai-compatible":
			default:
				return fmt.Errorf("providers.profiles.%q: protocol %q must be one of anthropic|google|gemini|openai|openai-compatible", name, profile.Protocol)
			}
		}
		if profile.MaxTokens < 0 {
			return fmt.Errorf("providers.profiles.%q: max_tokens must be >= 0", name)
		}
		if profile.MaxContext < 0 {
			return fmt.Errorf("providers.profiles.%q: max_context must be >= 0", name)
		}
		if isLikelyPlaceholder(profile.APIKey) {
			return fmt.Errorf("providers.profiles.%q: api_key %q looks like an unfilled placeholder — replace it with your actual key or remove the line", name, profile.APIKey)
		}
	}

	if c.Context.MaxFiles <= 0 {
		return fmt.Errorf("context.max_files must be > 0")
	}
	if c.Context.MaxTokensTotal <= 0 {
		return fmt.Errorf("context.max_tokens_total must be > 0")
	}
	if c.Context.MaxTokensPerFile <= 0 {
		return fmt.Errorf("context.max_tokens_per_file must be > 0")
	}
	if c.Context.MaxHistoryTokens <= 0 {
		return fmt.Errorf("context.max_history_tokens must be > 0")
	}
	if c.AST.CacheSize < 0 {
		return fmt.Errorf("ast.cache_size must be >= 0")
	}
	switch c.Context.Compression {
	case "none", "standard", "aggressive":
	default:
		return fmt.Errorf("context.compression must be one of none|standard|aggressive")
	}

	if c.Web.Port <= 0 || c.Web.Port > 65535 {
		return fmt.Errorf("web.port out of range: %d", c.Web.Port)
	}
	if c.Remote.GRPCPort <= 0 || c.Remote.GRPCPort > 65535 {
		return fmt.Errorf("remote.grpc_port out of range: %d", c.Remote.GRPCPort)
	}
	if c.Remote.WSPort <= 0 || c.Remote.WSPort > 65535 {
		return fmt.Errorf("remote.ws_port out of range: %d", c.Remote.WSPort)
	}

	switch c.Web.Auth {
	case "none", "token":
	default:
		return fmt.Errorf("web.auth must be none|token")
	}

	switch c.Remote.Auth {
	case "none", "token", "mtls":
	default:
		return fmt.Errorf("remote.auth must be none|token|mtls")
	}

	return nil
}

// isLikelyPlaceholder returns true if key looks like an unfilled .env.example
// placeholder (e.g. "<your-key-here>", "<MY_API_KEY>"). Such literals
// must not be accepted as real API keys — they cause confusing provider
// authentication failures that are hard to trace back to a misconfigured
// config file.
func isLikelyPlaceholder(key string) bool {
	if len(key) < 2 || key[0] != '<' {
		return false
	}
	if key[len(key)-1] != '>' {
		return false
	}
	inner := key[1 : len(key)-1]
	return len(inner) > 0 && !strings.ContainsAny(inner, " \t")
}
