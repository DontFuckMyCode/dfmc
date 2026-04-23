// provider_panel_crud.go — write-path operations for the Providers
// tab. Three surfaces, all of which mutate the project-level config
// file (.dfmc/config.yaml) via a read-modify-write cycle on the raw
// YAML document so comments and unrelated keys survive:
//
//   - syncModelsDevCmd: the "Sync Models" action. Fetches the
//     models.dev catalog, merges it into the Providers.Profiles
//     block (preserving API keys), saves, and reloads engine config.
//     Reports the diff (added / model-changed / context-changed) so
//     the user sees what actually moved.
//   - createProvider: the "New Provider" action. Seeds a minimal
//     openai-compatible profile, writes the protocol key to
//     .dfmc/config.yaml, and reloads engine config.
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
		if err := config.SaveModelsDevCatalog(config.ModelsDevCachePath(), catalog); err != nil {
			return syncModelsDevMsg{err: fmt.Errorf("cache: %w", err)}
		}

		cwd := eng.ProjectRoot
		if cwd == "" {
			cwd = "."
		}
		path := filepath.Join(cwd, config.DefaultDirName, "config.yaml")

		cloned, err := config.LoadWithOptions(config.LoadOptions{CWD: cwd})
		if err != nil {
			cloned = config.DefaultConfig()
		}
		before := map[string]config.ModelConfig{}
		for n, p := range cloned.Providers.Profiles {
			before[n] = p
		}
		cloned.Providers.Profiles = config.MergeProviderProfilesFromModelsDev(cloned.Providers.Profiles, catalog, config.ModelsDevMergeOptions{RewriteBaseURL: true})
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
		return syncModelsDevMsg{path: path, changes: changes}
	}
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
	}
	m.eng.Config.Providers.Profiles[name] = prof

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
	if m.eng != nil {
		if strings.EqualFold(m.eng.Config.Providers.Primary, name) {
			m.eng.Config.Providers.Primary = ""
		}
		var newFallback []string
		for _, fb := range m.eng.Config.Providers.Fallback {
			if !strings.EqualFold(fb, name) {
				newFallback = append(newFallback, fb)
			}
		}
		m.eng.Config.Providers.Fallback = newFallback
	}
	return nil
}
