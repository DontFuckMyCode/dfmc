// provider_panel_crud.go - write-path operations for the Providers
// tab. Provider/key/model settings are personal state, so panel writes
// go to the user-global config (~/.dfmc/config.yaml). Project config
// remains for project-only routing/pipelines/hooks:
//
//   - syncModelsDevCmd: the "Sync Models" action. Fetches the
//     models.dev catalog, merges it into user Providers.Profiles
//     (preserving API keys/endpoints), saves, and reloads engine config.
//     Reports the diff (added / model-changed / context-changed) so
//     the user sees what actually moved.
//   - createProvider: the "New Provider" action. Seeds a minimal
//     openai-compatible profile, writes the protocol key to user
//     config, and reloads engine config.
//   - deleteProvider: the "Delete Provider" action. Removes the
//     profile from the doc, writes back, reloads, and finally clears
//     the name from Primary / Fallback references in the engine's
//     in-memory copy so the router doesn't keep dangling pointers.
//
// ensureStringAnyMap / projectConfigPath / reloadEngineConfig live on
// other Model files in this package and stay accessible via same-
// package visibility.

package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func syncModelsDevCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil || eng.Config == nil {
			return syncModelsDevMsg{err: fmt.Errorf("engine not ready")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		catalog, err := config.FetchModelsDevCatalog(ctx, config.DefaultModelsDevAPIURL)
		if err != nil {
			return syncModelsDevMsg{err: fmt.Errorf("fetch: %w", err)}
		}
		providerCount, modelCount := modelsDevCatalogCounts(catalog)
		if err := config.SaveModelsDevCatalog(config.ModelsDevCachePath(), catalog); err != nil {
			return syncModelsDevMsg{err: fmt.Errorf("cache: %w", err)}
		}

		cwd := strings.TrimSpace(eng.ProjectRoot)
		if cwd == "" {
			cwd = "."
		}
		path := filepath.Join(config.UserConfigDir(), "config.yaml")

		cloned := config.DefaultConfig()
		if data, readErr := os.ReadFile(path); readErr == nil {
			if len(strings.TrimSpace(string(data))) > 0 {
				if unmarshalErr := yaml.Unmarshal(data, cloned); unmarshalErr != nil {
					return syncModelsDevMsg{err: fmt.Errorf("parse user config: %w", unmarshalErr)}
				}
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return syncModelsDevMsg{err: fmt.Errorf("read user config: %w", readErr)}
		}
		before := map[string]config.ModelConfig{}
		for n, p := range cloned.Providers.Profiles {
			before[n] = p
		}
		cloned.Providers.Profiles = config.MergeProviderProfilesFromModelsDev(cloned.Providers.Profiles, catalog, config.ModelsDevMergeOptions{})
		if err := cloned.Save(path); err != nil {
			return syncModelsDevMsg{err: fmt.Errorf("save: %w", err)}
		}
		if err := eng.ReloadConfig(cwd); err != nil {
			return syncModelsDevMsg{err: fmt.Errorf("reload: %w", err)}
		}

		var changes []string
		for n, after := range cloned.Providers.Profiles {
			beforeProf, hadBefore := before[n]
			if !hadBefore {
				changes = append(changes, fmt.Sprintf("+%s (new)", n))
				continue
			}
			if beforeProf.Model != after.Model {
				changes = append(changes, fmt.Sprintf("~%s model %s → %s", n, beforeProf.Model, after.Model))
			}
			if beforeProf.MaxContext != after.MaxContext {
				changes = append(changes, fmt.Sprintf("~%s context %d → %d", n, beforeProf.MaxContext, after.MaxContext))
			}
		}
		return syncModelsDevMsg{path: path, changes: changes, providerCount: providerCount, modelCount: modelCount}
	}
}

func modelsDevCatalogCounts(catalog config.ModelsDevCatalog) (providers, models int) {
	for _, provider := range catalog {
		providers++
		models += len(provider.Models)
	}
	return providers, models
}

func (m *Model) createProvider(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("provider name is empty")
	}
	if m.eng == nil || m.eng.Config == nil {
		return fmt.Errorf("engine not ready")
	}
	if _, exists := m.eng.Config.Providers.Profiles[name]; exists {
		return fmt.Errorf("provider %s already exists", name)
	}

	prof := config.ModelConfig{
		Protocol: "openai-compatible",
		Models:   []string{},
		Tags:     []string{"my-provider"},
	}
	m.eng.Config.Providers.Profiles[name] = prof

	// Match panel-toggle scope semantics: if project already has a
	// providers block, write there (it shadows user-home anyway);
	// otherwise create the new provider in user-home so it's
	// available across projects.
	path, err := m.configPathForScope(m.effectivePersistScope())
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
	profilesNode := ensureStringAnyMap(ensureStringAnyMap(doc, "providers"), "profiles")
	profileNode := ensureStringAnyMap(profilesNode, name)
	profileNode["protocol"] = prof.Protocol

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
	return m.reloadEngineConfig()
}

func (m *Model) deleteProvider(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("provider name is empty")
	}
	if m.eng == nil || m.eng.Config == nil {
		return fmt.Errorf("engine not ready")
	}
	delete(m.eng.Config.Providers.Profiles, name)

	// Same dynamic scope as createProvider — we delete from whichever
	// file is winning so the deletion actually takes effect on next
	// reload, not just in-memory.
	path, err := m.configPathForScope(m.effectivePersistScope())
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
	profilesNode := ensureStringAnyMap(ensureStringAnyMap(doc, "providers"), "profiles")
	delete(profilesNode, name)

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
	if err := m.reloadEngineConfig(); err != nil {
		return err
	}
	return m.persistProvidersPrimaryFallback()
}
