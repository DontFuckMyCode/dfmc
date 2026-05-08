package cli

// cli_doctor_fix.go — auto-fix surface for the dfmc doctor command.
// applyDoctorFixes is conservative: it only touches missing/invalid
// version, missing providers.profiles, an unset/dangling
// providers.primary, normalizes web/remote auth modes when set to
// nonsense values, and rewrites a known-broken zai profile shape. Anything
// risky (rotating keys, deleting profiles, changing endpoints the user
// supplied) stays out of scope and falls to manual edits.
//
// The picker helpers below (choosePreferredProvider/profileByName/
// modelConfigFromAny/profilesHasKey) live here because they only run
// during fix; runDoctor never calls them. Splitting them out of the
// runner keeps the read-side check loop in cli_doctor.go free of
// "what would we set if we had to" logic.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func applyDoctorFixes(eng *engine.Engine, global bool) (string, error) {
	if eng == nil {
		return "", fmt.Errorf("engine is nil")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	targetPath := projectConfigPath(cwd)
	if global {
		targetPath = filepath.Join(config.UserConfigDir(), "config.yaml")
	}

	currentMap, err := loadConfigFileMap(targetPath)
	if err != nil {
		return "", err
	}
	if len(currentMap) == 0 {
		defMap, err := configToMap(config.DefaultConfig())
		if err != nil {
			return "", err
		}
		currentMap = defMap
	}

	if _, ok := getConfigPath(currentMap, "version"); !ok {
		if err := setConfigPath(currentMap, "version", config.DefaultVersion); err != nil {
			return "", err
		}
	}
	if _, ok := getConfigPath(currentMap, "providers.profiles"); !ok {
		if err := setConfigPath(currentMap, "providers.profiles", config.DefaultConfig().Providers.Profiles); err != nil {
			return "", err
		}
	}

	profiles := map[string]any{}
	if raw, ok := getConfigPath(currentMap, "providers.profiles"); ok {
		switch v := raw.(type) {
		case map[string]any:
			profiles = v
		case map[any]any:
			for k, val := range v {
				key := strings.TrimSpace(fmt.Sprint(k))
				if key != "" {
					profiles[key] = val
				}
			}
		}
	}
	if len(profiles) == 0 {
		defMap, err := configToMap(config.DefaultConfig())
		if err != nil {
			return "", err
		}
		if err := setConfigPath(currentMap, "providers.profiles", defMap["providers"].(map[string]any)["profiles"]); err != nil {
			return "", err
		}
		if raw, ok := getConfigPath(currentMap, "providers.profiles"); ok {
			if v, ok := raw.(map[string]any); ok {
				profiles = v
			}
		}
	}

	rawPrimary, _ := getConfigPath(currentMap, "providers.primary")
	primary := strings.TrimSpace(fmt.Sprint(rawPrimary))
	if primary == "" || !profilesHasKey(profiles, primary) {
		primary = choosePreferredProvider(profiles, config.DefaultConfig().Providers.Primary)
		if primary == "" {
			primary = config.DefaultConfig().Providers.Primary
		}
		if err := setConfigPath(currentMap, "providers.primary", primary); err != nil {
			return "", err
		}
	}

	if raw, ok := getConfigPath(currentMap, "web.auth"); ok {
		auth := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
		if auth != "none" && auth != "token" {
			if err := setConfigPath(currentMap, "web.auth", "none"); err != nil {
				return "", err
			}
		}
	}
	if raw, ok := getConfigPath(currentMap, "remote.auth"); ok {
		auth := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw)))
		if auth != "none" && auth != "token" && auth != "mtls" {
			if err := setConfigPath(currentMap, "remote.auth", "token"); err != nil {
				return "", err
			}
		}
	}
	if raw, ok := getConfigPath(currentMap, "providers.profiles.zai"); ok {
		if profileMap, ok := raw.(map[string]any); ok {
			modelCfg := modelConfigFromAny(profileMap)
			if advisories := config.ProviderProfileAdvisories("zai", modelCfg); len(advisories) > 0 {
				profileMap["protocol"] = "openai-compatible"
				profileMap["base_url"] = "https://api.z.ai/api/paas/v4"
				if err := setConfigPath(currentMap, "providers.profiles.zai", profileMap); err != nil {
					return "", err
				}
			}
		}
	}

	var oldData []byte
	oldData, _ = os.ReadFile(targetPath)
	if err := saveConfigFileMap(targetPath, currentMap); err != nil {
		return "", err
	}
	if err := eng.ReloadConfig(cwd); err != nil {
		if len(oldData) == 0 {
			_ = os.Remove(targetPath)
		} else {
			_ = os.WriteFile(targetPath, oldData, 0o644)
		}
		return "", fmt.Errorf("fix applied but reload failed (reverted): %w", err)
	}

	return "updated " + targetPath, nil
}

func profilesHasKey(profiles map[string]any, name string) bool {
	for k := range profiles {
		if strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func choosePreferredProvider(profiles map[string]any, fallback string) string {
	preferredOrder := []string{
		"anthropic",
		"openai",
		"deepseek",
		"google",
		"zai",
		"generic",
		"alibaba",
		"kimi",
		"minimax",
	}
	for _, name := range preferredOrder {
		prof, ok := profileByName(profiles, name)
		if !ok {
			continue
		}
		modelCfg := modelConfigFromAny(prof)
		if providerConfigured(name, modelCfg) {
			return name
		}
	}
	for _, name := range preferredOrder {
		if profilesHasKey(profiles, name) {
			return name
		}
	}
	if profilesHasKey(profiles, fallback) {
		return fallback
	}
	keys := make([]string, 0, len(profiles))
	for k := range profiles {
		keys = append(keys, strings.TrimSpace(k))
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return keys[0]
	}
	return ""
}

func profileByName(profiles map[string]any, name string) (any, bool) {
	for k, v := range profiles {
		if strings.EqualFold(strings.TrimSpace(k), strings.TrimSpace(name)) {
			return v, true
		}
	}
	return nil, false
}

func modelConfigFromAny(v any) config.ModelConfig {
	out := config.ModelConfig{}
	switch m := v.(type) {
	case map[string]any:
		if raw, ok := m["api_key"]; ok {
			out.APIKey = strings.TrimSpace(fmt.Sprint(raw))
		}
		if raw, ok := m["base_url"]; ok {
			out.BaseURL = strings.TrimSpace(fmt.Sprint(raw))
		}
		if raw, ok := m["model"]; ok {
			out.Model = strings.TrimSpace(fmt.Sprint(raw))
		}
	case config.ModelConfig:
		out = m
	}
	return out
}
