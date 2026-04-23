// config_env.go — environment hydration for provider API keys. Two
// sources feed the same target (Config.Providers.Profiles[*].APIKey):
//
//  1. Process environment variables (os.LookupEnv) — always win.
//  2. Project-root .env file — fills gaps only, never overrides.
//
// The .env reader is deliberately conservative: no shell expansion,
// no variable interpolation, strips a leading `export `, honours
// quoted values via strconv.Unquote, and rejects
// `<placeholder>`-shaped values so `.env.example` copies don't leak
// fake keys into the config. BOM on the first line is tolerated
// because Windows editors occasionally inject it.
//
// providerAPIEnvVars is the single source of truth for
// env-var→profile-name mapping; EnvVarForProvider reverses the
// canonical half of it (KIMI_API_KEY / MOONSHOT_API_KEY both map to
// "kimi", but only KIMI_API_KEY is the canonical name surfaced back).
// The map is iterated in sorted order so applyEnv's two passes are
// deterministic — useful when a process has both ANTHROPIC_API_KEY
// and a project-level .env hint for the same provider.

package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func loadDotEnv(root string) (map[string]string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	path := filepath.Join(root, ".env")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	values := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if i == 0 {
			line = strings.TrimPrefix(line, "\uFEFF")
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(line[7:])
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		values[key] = parseDotEnvValue(value)
	}
	return values, nil
}

func parseDotEnvValue(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if looksLikeEnvPlaceholder(value) {
		return ""
	}
	if strings.HasPrefix(value, "\"") || strings.HasPrefix(value, "'") {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
		return strings.Trim(value, "\"'")
	}
	if idx := strings.Index(value, " #"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return value
}

// looksLikeEnvPlaceholder returns true if value looks like an unfilled
// placeholder from .env.example (e.g. "<your-key-here>", "<MY_API_KEY>").
// Such values must not be returned as literal API keys.
func looksLikeEnvPlaceholder(value string) bool {
	if len(value) < 2 || value[0] != '<' {
		return false
	}
	// "<>" or "< >" are not placeholder shapes.
	if value[len(value)-1] != '>' {
		return false
	}
	inner := value[1 : len(value)-1]
	return len(inner) > 0 && !strings.ContainsAny(inner, " \t")
}

// providerAPIEnvVars maps env var name → provider profile name. Used both
// to hydrate APIKey from the environment and to tell the user WHICH env
// var is expected when a profile falls back to offline.
var providerAPIEnvVars = map[string]string{
	"ANTHROPIC_API_KEY": "anthropic",
	"OPENAI_API_KEY":    "openai",
	"GOOGLE_AI_API_KEY": "google",
	"DEEPSEEK_API_KEY":  "deepseek",
	"KIMI_API_KEY":      "kimi",
	"MOONSHOT_API_KEY":  "kimi",
	"MINIMAX_API_KEY":   "minimax",
	"ZAI_API_KEY":       "zai",
	"ALIBABA_API_KEY":   "alibaba",
}

// EnvVarForProvider returns the canonical env var name for a provider
// profile (e.g. "anthropic" → "ANTHROPIC_API_KEY"). Returns "" for
// unknown names. When multiple env vars map to the same provider
// (e.g. KIMI / MOONSHOT), the canonical one is returned.
func EnvVarForProvider(name string) string {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return ""
	}
	canonical := map[string]string{
		"anthropic": "ANTHROPIC_API_KEY",
		"openai":    "OPENAI_API_KEY",
		"google":    "GOOGLE_AI_API_KEY",
		"deepseek":  "DEEPSEEK_API_KEY",
		"kimi":      "KIMI_API_KEY",
		"minimax":   "MINIMAX_API_KEY",
		"zai":       "ZAI_API_KEY",
		"alibaba":   "ALIBABA_API_KEY",
	}
	return canonical[key]
}

func (c *Config) applyEnv(dotEnv map[string]string) {
	if c.Providers.Profiles == nil {
		c.Providers.Profiles = map[string]ModelConfig{}
	}
	// First pass: process environment variables (they always win).
	for _, envName := range slices.Sorted(maps.Keys(providerAPIEnvVars)) {
		providerName := providerAPIEnvVars[envName]
		val, ok := os.LookupEnv(envName)
		if ok && strings.TrimSpace(val) != "" {
			prof := c.Providers.Profiles[providerName]
			prof.APIKey = strings.TrimSpace(val)
			c.Providers.Profiles[providerName] = prof
		}
	}
	// Second pass: dotenv fills gaps for providers not set by process env.
	for _, envName := range slices.Sorted(maps.Keys(providerAPIEnvVars)) {
		providerName := providerAPIEnvVars[envName]
		if p := c.Providers.Profiles[providerName]; strings.TrimSpace(p.APIKey) != "" {
			continue
		}
		val := strings.TrimSpace(dotEnv[envName])
		if val == "" {
			continue
		}
		prof := c.Providers.Profiles[providerName]
		prof.APIKey = val
		c.Providers.Profiles[providerName] = prof
	}
}
