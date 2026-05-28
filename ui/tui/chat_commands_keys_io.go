package tui

// chat_commands_keys_io.go — persistence side of the /key command:
// writes/clears api_key in ~/.dfmc/config.yaml, walks the project
// .env to migrate recognised keys to user-home config, and reads
// back what's actually written on disk (separate from the merged
// in-memory state). The slash dispatcher, render/collect, and small
// helpers live in chat_commands_keys.go.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func (m Model) writeProviderAPIKeyToUserConfig(providerName, apiKey string) (string, error) {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	apiKey = strings.TrimSpace(apiKey)
	if providerName == "" {
		return "", errors.New("provider name is empty")
	}
	if apiKey == "" {
		return "", errors.New("api key is empty")
	}
	path, err := m.userConfigPath()
	if err != nil {
		return "", err
	}
	doc, err := readYAMLDocOrEmpty(path)
	if err != nil {
		return "", err
	}
	if _, ok := doc["version"]; !ok {
		doc["version"] = 1
	}
	providersNode := ensureStringAnyMap(doc, "providers")
	profilesNode := ensureStringAnyMap(providersNode, "profiles")
	profileNode := ensureStringAnyMap(profilesNode, providerName)
	profileNode["api_key"] = apiKey
	if err := writeYAMLDocAtomically(path, doc); err != nil {
		return "", err
	}
	return path, nil
}

// clearProviderAPIKeyFromUserConfig removes the api_key field for one
// provider profile from user-home config. Other fields stay. Returns
// (path, removed, err).
func (m Model) clearProviderAPIKeyFromUserConfig(providerName string) (string, bool, error) {
	providerName = strings.ToLower(strings.TrimSpace(providerName))
	path, err := m.userConfigPath()
	if err != nil {
		return "", false, err
	}
	doc, err := readYAMLDocOrEmpty(path)
	if err != nil {
		return path, false, err
	}
	providersRaw, ok := doc["providers"]
	if !ok {
		return path, false, nil
	}
	providers, ok := toStringAnyMap(providersRaw)
	if !ok {
		return path, false, nil
	}
	profilesRaw, ok := providers["profiles"]
	if !ok {
		return path, false, nil
	}
	profiles, ok := toStringAnyMap(profilesRaw)
	if !ok {
		return path, false, nil
	}
	profileRaw, ok := profiles[providerName]
	if !ok {
		return path, false, nil
	}
	profile, ok := toStringAnyMap(profileRaw)
	if !ok {
		return path, false, nil
	}
	if _, hasKey := profile["api_key"]; !hasKey {
		return path, false, nil
	}
	delete(profile, "api_key")
	profiles[providerName] = profile
	providers["profiles"] = profiles
	doc["providers"] = providers
	if err := writeYAMLDocAtomically(path, doc); err != nil {
		return path, false, err
	}
	return path, true, nil
}

// migrateDotEnvKeysToUserConfig walks the project root's .env file,
// pulls every recognised API_KEY entry, and writes them to user-home
// config in one batch. Existing user-home keys are NOT overwritten —
// migration is additive so an incomplete .env doesn't blow away a
// good key the user already saved.
func (m Model) migrateDotEnvKeysToUserConfig() string {
	dotEnv := m.readProjectDotEnvKeys()
	if len(dotEnv) == 0 {
		return "/key migrate: no project .env file found (or no keys in it). Nothing to migrate."
	}
	userKeys := m.readUserConfigAPIKeys()
	path, err := m.userConfigPath()
	if err != nil {
		return "/key migrate: " + err.Error()
	}
	doc, err := readYAMLDocOrEmpty(path)
	if err != nil {
		return "/key migrate: " + err.Error()
	}
	if _, ok := doc["version"]; !ok {
		doc["version"] = 1
	}
	providersNode := ensureStringAnyMap(doc, "providers")
	profilesNode := ensureStringAnyMap(providersNode, "profiles")

	migrated := []string{}
	skipped := []string{}
	for _, p := range knownKeyProviders() {
		envVar := config.EnvVarForProvider(p)
		val := strings.TrimSpace(dotEnv[envVar])
		if val == "" {
			continue
		}
		if strings.TrimSpace(userKeys[p]) != "" {
			skipped = append(skipped, p)
			continue
		}
		profileNode := ensureStringAnyMap(profilesNode, p)
		profileNode["api_key"] = val
		m.applyProviderAPIKeyInMemory(p, val)
		migrated = append(migrated, p)
	}
	if len(migrated) == 0 {
		report := "/key migrate: nothing to migrate — every .env key is already present in ~/.dfmc/config.yaml."
		if len(skipped) > 0 {
			report += "\nAlready in user-home (skipped): " + strings.Join(skipped, ", ")
		}
		return report
	}
	if err := writeYAMLDocAtomically(path, doc); err != nil {
		return "/key migrate: write failed: " + err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "/key migrate: copied %d key(s) from .env → ", len(migrated))
	b.WriteString(displayConfigPath(path))
	b.WriteString("\n  Migrated: ")
	b.WriteString(strings.Join(migrated, ", "))
	if len(skipped) > 0 {
		b.WriteString("\n  Skipped (already in user-home): ")
		b.WriteString(strings.Join(skipped, ", "))
	}
	b.WriteString("\n\nThe project .env is no longer needed for these providers. Remove it manually when you're sure (`rm .env`) — DFMC won't delete it for you.")
	return b.String()
}

// readProjectDotEnvKeys returns the env-var → value map parsed from
// the project root's .env, filtered to recognised provider keys.
// Empty when no .env exists or no recognised keys are present.
func (m Model) readProjectDotEnvKeys() map[string]string {
	root := m.projectRootForKeyOps()
	if root == "" {
		return nil
	}
	path := filepath.Join(root, ".env")
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	for _, raw := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		if key == "" || val == "" {
			continue
		}
		// Only return values for env vars we recognise as API keys.
		if config.EnvVarForProvider(providerForEnvVar(key)) == key {
			out[key] = val
		}
	}
	return out
}

// readUserConfigAPIKeys returns provider-name → api_key from
// ~/.dfmc/config.yaml without merging defaults — we want to show
// what's literally written in the user file, not the resolved
// in-memory state (which already includes process env overrides).
func (m Model) readUserConfigAPIKeys() map[string]string {
	out := map[string]string{}
	path, err := m.userConfigPath()
	if err != nil {
		return out
	}
	doc, err := readYAMLDocOrEmpty(path)
	if err != nil {
		return out
	}
	providersRaw, ok := doc["providers"]
	if !ok {
		return out
	}
	providers, ok := toStringAnyMap(providersRaw)
	if !ok {
		return out
	}
	profilesRaw, ok := providers["profiles"]
	if !ok {
		return out
	}
	profiles, ok := toStringAnyMap(profilesRaw)
	if !ok {
		return out
	}
	for name, raw := range profiles {
		profile, ok := toStringAnyMap(raw)
		if !ok {
			continue
		}
		if k, ok := profile["api_key"].(string); ok && strings.TrimSpace(k) != "" {
			out[strings.ToLower(name)] = strings.TrimSpace(k)
		}
	}
	return out
}

// readYAMLDocOrEmpty returns the parsed YAML doc at path, or an empty
// map if the file is missing/empty. Errors propagate for malformed
// YAML so we don't silently overwrite a broken file.
func readYAMLDocOrEmpty(path string) (map[string]any, error) {
	doc := map[string]any{}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doc, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return doc, nil
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

func writeYAMLDocAtomically(path string, doc map[string]any) error {
	out, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
