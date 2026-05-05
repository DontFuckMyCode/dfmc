package tui

// provider_selection.go — provider/model selection, profile reads, and project
// config persistence.
//
// Lifted out of the 10K-line tui.go god file (REPORT.md C1) so the
// "what provider/model are we on" surface lives in one obvious
// place. Two related concerns coexist here on purpose:
//
//   - read-only accessors (availableProviders, currentProvider,
//     currentModel, defaultModelForProvider, providerProfile,
//     loadDriveRoutingFromProjectConfig, persistDriveRoutingProjectConfig)
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
	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
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

// providerPanelRows builds the list of all registered provider profiles
// for the F2 providers sub-panel.
func (m Model) providerPanelRows() []theme.ProviderPanelRow {
	if m.eng == nil || m.eng.Config == nil {
		return nil
	}
	cfg := m.eng.Config.Providers
	primary := strings.TrimSpace(cfg.Primary)
	currentProvider := strings.TrimSpace(m.currentProvider())

	var rows []theme.ProviderPanelRow
	for name, profile := range cfg.Profiles {
		hasAPIKey := strings.TrimSpace(profile.APIKey) != ""
		status := "ready"
		isPlaceholder := false
		if !hasAPIKey && strings.TrimSpace(profile.BaseURL) == "" {
			status = "no-key"
		}
		rows = append(rows, theme.ProviderPanelRow{
			Name:           name,
			Active:         strings.EqualFold(name, currentProvider),
			Primary:        strings.EqualFold(name, primary),
			Models:         profile.AllModels(),
			FallbackModels: profile.FallbackModels,
			MaxContext:     profile.MaxContext,
			Protocol:       strings.TrimSpace(profile.Protocol),
			HasAPIKey:      hasAPIKey,
			Status:         status,
			IsPlaceholder:  isPlaceholder,
		})
	}
	// Also add the offline provider if not in profiles.
	hasOffline := false
	for name := range cfg.Profiles {
		if strings.EqualFold(name, "offline") {
			hasOffline = true
			break
		}
	}
	if !hasOffline {
		rows = append(rows, theme.ProviderPanelRow{
			Name:           "offline",
			Active:         strings.EqualFold("offline", currentProvider),
			Primary:        strings.EqualFold("offline", primary),
			Models:         []string{"offline-analyzer-v1"},
			FallbackModels: nil,
			MaxContext:     12000,
			Protocol:       "offline",
			HasAPIKey:      false,
			Status:         "ready",
			IsPlaceholder:  false,
		})
	}
	return rows
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

func (m Model) hydrateStatusProviderFromConfig() Model {
	if m.eng == nil || m.eng.Config == nil {
		return m
	}
	cfg := m.eng.Config.Providers
	providerName := strings.TrimSpace(m.status.Provider)
	if providerName == "" {
		providerName = strings.TrimSpace(cfg.Primary)
		m.status.Provider = providerName
	}
	if providerName == "" {
		return m
	}
	profile, ok := cfg.Profiles[providerName]
	if !ok {
		return m
	}
	if strings.TrimSpace(m.status.Model) == "" {
		m.status.Model = strings.TrimSpace(profile.Model)
	}
	if strings.TrimSpace(m.status.ProviderProfile.Name) == "" {
		m.status.ProviderProfile.Name = providerName
	}
	if strings.TrimSpace(m.status.ProviderProfile.Model) == "" {
		m.status.ProviderProfile.Model = strings.TrimSpace(profile.Model)
	}
	if strings.TrimSpace(m.status.ProviderProfile.Protocol) == "" {
		m.status.ProviderProfile.Protocol = strings.TrimSpace(profile.Protocol)
	}
	if strings.TrimSpace(m.status.ProviderProfile.BaseURL) == "" {
		m.status.ProviderProfile.BaseURL = strings.TrimSpace(profile.BaseURL)
	}
	if m.status.ProviderProfile.MaxTokens <= 0 {
		m.status.ProviderProfile.MaxTokens = profile.MaxTokens
	}
	if m.status.ProviderProfile.MaxContext <= 0 {
		m.status.ProviderProfile.MaxContext = profile.MaxContext
	}
	if m.status.ProviderProfile.CostPer1kTokens <= 0 {
		m.status.ProviderProfile.CostPer1kTokens = profile.CostPer1kTokens
	}
	if !m.status.ProviderProfile.Configured {
		m.status.ProviderProfile.Configured = providerProfileLooksConfigured(providerName, profile)
	}
	return m
}

func providerProfileLooksConfigured(name string, profile config.ModelConfig) bool {
	if strings.EqualFold(strings.TrimSpace(name), "offline") {
		return true
	}
	if strings.TrimSpace(profile.APIKey) != "" {
		return true
	}
	return strings.TrimSpace(profile.BaseURL) != ""
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

		// Persist provider + model selection to config so it survives restart.
		if m.eng.Config != nil && strings.TrimSpace(m.eng.Config.Providers.Primary) != providerName {
			m.eng.SetPrimaryProvider(providerName)
		}
		if err := m.persistProvidersPrimaryFallback(); err != nil {
			m.notice += " (save failed: " + err.Error() + ")"
		}
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
	root := ""
	if m.eng != nil {
		root = strings.TrimSpace(m.eng.ProjectRoot)
	}
	if strings.TrimSpace(root) == "" {
		root = strings.TrimSpace(m.status.ProjectRoot)
	}
	if strings.TrimSpace(root) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("project root unavailable: %w", err)
		}
		root = cwd
	}
	return filepath.Join(root, config.DefaultDirName, "config.yaml"), nil
}

func (m *Model) persistProvidersPrimaryFallback() error {
	if m.eng == nil || m.eng.Config == nil {
		return nil
	}
	path, err := m.projectConfigPath()
	if err != nil {
		return err
	}

	doc := map[string]any{}
	if data, readErr := os.ReadFile(path); readErr == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if unmarshalErr := yaml.Unmarshal(data, &doc); unmarshalErr != nil {
				return fmt.Errorf("parse project config: %w", unmarshalErr)
			}
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("read project config: %w", readErr)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if _, ok := doc["version"]; !ok {
		doc["version"] = 1
	}

	providersNode := ensureStringAnyMap(doc, "providers")
	if strings.TrimSpace(m.eng.Config.Providers.Primary) != "" {
		providersNode["primary"] = m.eng.Config.Providers.Primary
	}
	if len(m.eng.Config.Providers.Fallback) > 0 {
		providersNode["fallback"] = m.eng.Config.Providers.Fallback
	}
	primary := strings.TrimSpace(m.eng.Config.Providers.Primary)
	if primary != "" {
		if prof, ok := m.eng.Config.Providers.Profiles[primary]; ok {
			profilesNode := ensureStringAnyMap(providersNode, "profiles")
			profileNode := ensureStringAnyMap(profilesNode, primary)
			writeProviderProfileProjectConfig(profileNode, prof)
		}
	}

	out, marshalErr := yaml.Marshal(doc)
	if marshalErr != nil {
		return fmt.Errorf("marshal project config: %w", marshalErr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create project config dir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return fmt.Errorf("write project config: %w", err)
	}
	return nil
}

func (m *Model) reloadEngineConfig() error {
	if m.eng == nil {
		return fmt.Errorf("engine is unavailable")
	}
	projectRoot := strings.TrimSpace(m.eng.ProjectRoot)
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(m.status.ProjectRoot)
	}
	cwd := projectRoot
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
	return m.persistProviderConfigProjectConfig(providerName, model, providerName, nil)
}

func (m Model) persistProviderConfigProjectConfig(providerName, model, primary string, fallback []string) (string, error) {
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
	if primary != "" {
		providersNode["primary"] = primary
	}
	if fallback != nil {
		providersNode["fallback"] = fallback
	}
	profilesNode := ensureStringAnyMap(providersNode, "profiles")
	profileNode := ensureStringAnyMap(profilesNode, providerName)
	profileNode["model"] = model
	if m.eng != nil && m.eng.Config != nil {
		if prof, ok := m.eng.Config.Providers.Profiles[providerName]; ok {
			prof.Model = model
			writeProviderProfileProjectConfig(profileNode, prof)
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

func writeProviderProfileProjectConfig(profileNode map[string]any, prof config.ModelConfig) {
	if profileNode == nil {
		return
	}
	if model := strings.TrimSpace(prof.Model); model != "" {
		profileNode["model"] = model
	}
	if protocol := strings.TrimSpace(prof.Protocol); protocol != "" {
		profileNode["protocol"] = protocol
	}
	if baseURL := strings.TrimSpace(prof.BaseURL); baseURL != "" {
		profileNode["base_url"] = baseURL
	}
	if prof.MaxTokens > 0 {
		profileNode["max_tokens"] = prof.MaxTokens
	}
	if prof.MaxContext > 0 {
		profileNode["max_context"] = prof.MaxContext
	}
}

func (m Model) loadDriveRoutingFromProjectConfig() map[string]string {
	path, err := m.projectConfigPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	driveNode := ensureStringAnyMap(doc, "drive")
	routingNode := ensureStringAnyMap(driveNode, "routing")
	out := make(map[string]string, len(routingNode))
	for k, v := range routingNode {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func (m Model) persistDriveRoutingProjectConfig(routing map[string]string) (string, error) {
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

	driveNode := ensureStringAnyMap(doc, "drive")
	if len(routing) == 0 {
		delete(driveNode, "routing")
	} else {
		driveNode["routing"] = routing
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

func (m Model) persistPipelinesProjectConfig(name string, steps []config.PipelineStep) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("pipeline name is empty")
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

	pipelinesNode := ensureStringAnyMap(doc, "pipelines")
	stepsNode := []map[string]any{}
	for _, step := range steps {
		stepsNode = append(stepsNode, map[string]any{
			"provider": strings.TrimSpace(step.Provider),
			"model":    strings.TrimSpace(step.Model),
		})
	}
	pipelinesNode[name] = map[string]any{"steps": stepsNode}

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

	if err := m.reloadEngineConfig(); err != nil {
		return path, fmt.Errorf("reload engine: %w", err)
	}
	return path, nil
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
		Name:            strings.TrimSpace(name),
		Model:           strings.TrimSpace(profile.Model),
		Protocol:        strings.TrimSpace(profile.Protocol),
		BaseURL:         strings.TrimSpace(profile.BaseURL),
		MaxTokens:       profile.MaxTokens,
		MaxContext:      profile.MaxContext,
		CostPer1kTokens: profile.CostPer1kTokens,
		Configured:      strings.TrimSpace(profile.APIKey) != "" || strings.TrimSpace(profile.BaseURL) != "",
	}
}
