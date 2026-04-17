package tui

// provider.go — provider/model selection, profile reads, and project
// config persistence.
//
// Lifted out of the 10K-line tui.go god file (REPORT.md C1) so the
// "what provider/model are we on" surface lives in one obvious
// place. Two related concerns coexist here on purpose:
//
//   - read-only accessors (availableProviders, currentProvider,
//     currentModel, defaultModelForProvider, providerProfile,
//     snapSetupCursorToActive)
//   - mutating apply/persist (applyProviderModelSelection,
//     persistProviderModelProjectConfig, reloadEngineConfig)
//
// They share enough plumbing (config map walk, name normalization,
// provider profile lookup) that pulling them apart adds noise. The
// yaml-shaped helpers (ensureStringAnyMap, toStringAnyMap) are
// scoped to the persist path but generic enough to belong here too.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func (m Model) availableProviders() []string {
	if m.eng == nil || m.eng.Config == nil {
		return nil
	}
	names := make([]string, 0, len(m.eng.Config.Providers.Profiles))
	for name := range m.eng.Config.Providers.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m Model) currentProvider() string {
	if providerName := strings.TrimSpace(m.status.Provider); providerName != "" {
		return providerName
	}
	if m.eng == nil {
		return ""
	}
	return strings.TrimSpace(m.eng.Status().Provider)
}

// snapSetupCursorToActive lands the Setup-tab cursor on whichever
// provider is currently in use. Invoked when the user opens the Setup
// tab so the active row is highlighted instead of always starting at
// index 0 — that "active provider invisible until you scroll" feel
// confused users into thinking nothing was selected.
func (m Model) snapSetupCursorToActive() Model {
	providers := m.availableProviders()
	if len(providers) == 0 {
		return m
	}
	active := strings.TrimSpace(m.currentProvider())
	if active == "" {
		return m
	}
	for i, name := range providers {
		if strings.EqualFold(name, active) {
			m.setupIndex = i
			return m
		}
	}
	return m
}

func (m Model) currentModel() string {
	if model := strings.TrimSpace(m.status.Model); model != "" {
		return model
	}
	if m.eng == nil {
		return ""
	}
	return strings.TrimSpace(m.eng.Status().Model)
}

func (m Model) defaultModelForProvider(name string) string {
	if m.eng == nil || m.eng.Config == nil {
		return ""
	}
	profile, ok := m.eng.Config.Providers.Profiles[strings.TrimSpace(name)]
	if !ok {
		return ""
	}
	return strings.TrimSpace(profile.Model)
}

func parseModelPersistArgs(args []string) (string, bool) {
	parts := make([]string, 0, len(args))
	persist := false
	for _, raw := range args {
		arg := strings.TrimSpace(raw)
		switch strings.ToLower(arg) {
		case "--persist", "--save":
			persist = true
		default:
			if arg != "" {
				parts = append(parts, arg)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, " ")), persist
}

func parseArgsWithPersist(args []string) ([]string, bool) {
	parts := make([]string, 0, len(args))
	persist := false
	for _, raw := range args {
		arg := strings.TrimSpace(raw)
		switch strings.ToLower(arg) {
		case "--persist", "--save":
			persist = true
		default:
			if arg != "" {
				parts = append(parts, arg)
			}
		}
	}
	return parts, persist
}

func (m Model) applyProviderModelSelection(providerName, model string) Model {
	providerName = strings.TrimSpace(providerName)
	model = strings.TrimSpace(model)
	if providerName == "" {
		return m
	}
	if m.eng != nil {
		if m.eng.Config != nil {
			if m.eng.Config.Providers.Profiles == nil {
				m.eng.Config.Providers.Profiles = map[string]config.ModelConfig{}
			}
			profile := m.eng.Config.Providers.Profiles[providerName]
			if model != "" {
				profile.Model = model
			}
			m.eng.Config.Providers.Profiles[providerName] = profile
		}
		m.eng.SetProviderModel(providerName, model)
		m.status = m.eng.Status()
		m.notice = formatProviderSwitchNotice(m.status.ProviderProfile)
	}
	return m
}

// formatProviderSwitchNotice produces a one-line confirmation after a
// provider/model switch. It names the profile, whether an endpoint and
// API key are configured, and flags the likely offline-fallback case up
// front so the user doesn't discover it only when a chat turn fails.
func formatProviderSwitchNotice(p engine.ProviderProfileStatus) string {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return ""
	}
	parts := []string{"provider → " + name}
	if model := strings.TrimSpace(p.Model); model != "" {
		parts = append(parts, "model: "+model)
	}
	if !p.Configured {
		if env := config.EnvVarForProvider(name); env != "" {
			parts = append(parts, fmt.Sprintf("⚠ no API key — set %s in .env or providers.profiles.%s.api_key (falling back to offline)", env, name))
		} else {
			parts = append(parts, fmt.Sprintf("⚠ no API key — set providers.profiles.%s.api_key in config.yaml (falling back to offline)", name))
		}
		return strings.Join(parts, " · ")
	}
	if base := strings.TrimSpace(p.BaseURL); base != "" {
		parts = append(parts, "endpoint: "+base)
	}
	return strings.Join(parts, " · ")
}

func (m Model) projectConfigPath() (string, error) {
	root := "."
	if m.eng != nil {
		root = strings.TrimSpace(m.eng.ProjectRoot)
	}
	if strings.TrimSpace(root) == "" {
		root = strings.TrimSpace(m.status.ProjectRoot)
	}
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("project root unavailable")
	}
	return filepath.Join(root, config.DefaultDirName, "config.yaml"), nil
}

func (m *Model) reloadEngineConfig() error {
	if m.eng == nil {
		return fmt.Errorf("engine is unavailable")
	}
	cwd := strings.TrimSpace(m.eng.ProjectRoot)
	if cwd == "" {
		cwd = strings.TrimSpace(m.status.ProjectRoot)
	}
	if cwd == "" {
		cwd = "."
	}
	if err := m.eng.ReloadConfig(cwd); err != nil {
		return err
	}
	m.status = m.eng.Status()
	return nil
}

func (m Model) persistProviderModelProjectConfig(providerName, model string) (string, error) {
	providerName = strings.TrimSpace(providerName)
	model = strings.TrimSpace(model)
	if providerName == "" {
		return "", fmt.Errorf("provider is empty")
	}
	if model == "" {
		return "", fmt.Errorf("model is empty")
	}
	path, err := m.projectConfigPath()
	if err != nil {
		return "", err
	}

	doc := map[string]any{}
	if data, readErr := os.ReadFile(path); readErr == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if unmarshalErr := yaml.Unmarshal(data, &doc); unmarshalErr != nil {
				return "", fmt.Errorf("parse project config: %w", unmarshalErr)
			}
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", fmt.Errorf("read project config: %w", readErr)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if _, ok := doc["version"]; !ok {
		doc["version"] = 1
	}

	providersNode := ensureStringAnyMap(doc, "providers")
	providersNode["primary"] = providerName
	profilesNode := ensureStringAnyMap(providersNode, "profiles")
	profileNode := ensureStringAnyMap(profilesNode, providerName)
	profileNode["model"] = model
	if m.eng != nil && m.eng.Config != nil {
		if prof, ok := m.eng.Config.Providers.Profiles[providerName]; ok {
			if strings.TrimSpace(prof.Protocol) != "" {
				profileNode["protocol"] = strings.TrimSpace(prof.Protocol)
			}
			if strings.TrimSpace(prof.BaseURL) != "" {
				profileNode["base_url"] = strings.TrimSpace(prof.BaseURL)
			}
			if prof.MaxTokens > 0 {
				profileNode["max_tokens"] = prof.MaxTokens
			}
			if prof.MaxContext > 0 {
				profileNode["max_context"] = prof.MaxContext
			}
		}
	}

	out, marshalErr := yaml.Marshal(doc)
	if marshalErr != nil {
		return "", fmt.Errorf("marshal project config: %w", marshalErr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create project config dir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", fmt.Errorf("write project config: %w", err)
	}
	return path, nil
}

func ensureStringAnyMap(parent map[string]any, key string) map[string]any {
	if parent == nil {
		return map[string]any{}
	}
	if existing, ok := parent[key]; ok {
		if out, ok := toStringAnyMap(existing); ok {
			parent[key] = out
			return out
		}
	}
	out := map[string]any{}
	parent[key] = out
	return out
}

func toStringAnyMap(raw any) (map[string]any, bool) {
	switch value := raw.(type) {
	case map[string]any:
		return value, true
	case map[any]any:
		out := map[string]any{}
		for key, item := range value {
			text, ok := key.(string)
			if !ok {
				continue
			}
			out[text] = item
		}
		return out, true
	default:
		return nil, false
	}
}

func (m Model) providerProfile(name string) engine.ProviderProfileStatus {
	if m.eng == nil || m.eng.Config == nil {
		return engine.ProviderProfileStatus{Name: strings.TrimSpace(name)}
	}
	profile, ok := m.eng.Config.Providers.Profiles[strings.TrimSpace(name)]
	if !ok {
		return engine.ProviderProfileStatus{Name: strings.TrimSpace(name)}
	}
	return engine.ProviderProfileStatus{
		Name:       strings.TrimSpace(name),
		Model:      strings.TrimSpace(profile.Model),
		Protocol:   strings.TrimSpace(profile.Protocol),
		BaseURL:    strings.TrimSpace(profile.BaseURL),
		MaxTokens:  profile.MaxTokens,
		MaxContext: profile.MaxContext,
		Configured: strings.TrimSpace(profile.APIKey) != "" || strings.TrimSpace(profile.BaseURL) != "",
	}
}
