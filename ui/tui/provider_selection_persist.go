package tui

// provider_selection_persist.go — write-side YAML editing for the
// provider selection surface. Every persist function rebuilds the
// target config doc by reading-merge-writing so the user's hand-edited
// fields aren't clobbered. Path resolution + scope rules live in
// provider_selection_paths.go; reads / hydration / display formatting
// live in provider_selection.go.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// persistProvidersPrimaryFallback writes the in-memory primary +
// fallback to disk. Defaults to user-home (~/.dfmc/config.yaml) so
// the user's choice survives across projects — that was the user's
// repeated complaint, "save oluyorum kayıt oluyor mu, sürekli
// kasıyoruz". Pass persistScopeProject to override per-project.
func (m *Model) persistProvidersPrimaryFallback() error {
	_, err := m.persistProvidersPrimaryFallbackPath()
	return err
}

// persistProvidersPrimaryFallbackPath is the path-returning variant —
// callers that want to surface the save target in a user notice use
// this; the bare-error wrapper above keeps older callsites unchanged.
// Picks the scope dynamically so the save lands where it'll actually
// win on next load (project config overrides user-home).
func (m *Model) persistProvidersPrimaryFallbackPath() (string, error) {
	return m.persistProvidersPrimaryFallbackTo(m.effectivePersistScope())
}

func (m *Model) persistProvidersPrimaryFallbackTo(scope persistScope) (string, error) {
	if m.eng == nil || m.eng.Config == nil {
		return "", nil
	}
	path, err := m.configPathForScope(scope)
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

// persistProviderModelProjectConfig writes a model choice to the
// project-local config (<project>/.dfmc/config.yaml). This is the
// explicit per-project save path used by `/provider X Y --persist`
// slash-commands, where the user is deliberately overriding for the
// current project.
func (m Model) persistProviderModelProjectConfig(providerName, model string) (string, error) {
	return m.persistProviderModelTo(providerName, model, persistScopeProject)
}

// persistProviderModelUserConfig is the panel-driven persist path —
// despite the historical "user" suffix, it now picks the scope
// dynamically so the save lands in whichever file actually wins on
// next load. When the project config has its own `providers:` block,
// project wins over user-home, so saving to user-home would be a
// silent no-op — we write to project instead. When the project has
// no providers section, the save lands in ~/.dfmc/config.yaml so the
// choice survives across projects.
func (m Model) persistProviderModelUserConfig(providerName, model string) (string, error) {
	return m.persistProviderModelTo(providerName, model, m.effectivePersistScope())
}

func (m Model) persistProviderModelTo(providerName, model string, scope persistScope) (string, error) {
	return m.persistProviderConfigTo(providerName, model, providerName, nil, scope)
}

func (m Model) persistProviderConfigTo(providerName, model, primary string, fallback []string, scope persistScope) (string, error) {
	providerName = strings.TrimSpace(providerName)
	model = strings.TrimSpace(model)
	if providerName == "" {
		return "", fmt.Errorf("provider is empty")
	}
	if model == "" {
		return "", fmt.Errorf("model is empty")
	}
	path, err := m.configPathForScope(scope)
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

// loadDriveRoutingFromProjectConfig + persistDriveRoutingProjectConfig +
// persistPipelinesProjectConfig live in
// provider_selection_persist_drive.go.
