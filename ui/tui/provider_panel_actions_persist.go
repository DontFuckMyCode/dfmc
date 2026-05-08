// provider_panel_actions_persist.go — heavier persistence surfaces
// for the Providers panel: profile-field commit + project-config
// YAML writer (commitProfileEditField + persistProfileEdits) and the
// pipeline lifecycle (savePipelineDraft + deletePipeline). Sibling
// of provider_panel_actions.go which keeps the per-row mutations
// (addModelToProvider / cycleProviderModel / setPrimaryProvider /
// toggleFallbackProvider / deleteActiveModel) that go through their
// own one-line persisters.
//
// Splitting these out keeps provider_panel_actions.go scannable when
// adjusting per-row toggles, and groups the YAML-direct surfaces
// (which need the project-config doc walk + ensureStringAnyMap +
// MkdirAll/WriteFile) into one file.

package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func (m *Model) savePipelineDraft() error {
	name := strings.TrimSpace(m.providers.pipelineDraftName)
	if name == "" {
		return fmt.Errorf("pipeline name is required")
	}
	if len(m.providers.pipelineDraftSteps) == 0 {
		return fmt.Errorf("pipeline needs at least one step")
	}
	for i, step := range m.providers.pipelineDraftSteps {
		if strings.TrimSpace(step.Provider) == "" {
			return fmt.Errorf("step %d provider is required", i+1)
		}
		if strings.TrimSpace(step.Model) == "" {
			return fmt.Errorf("step %d model is required", i+1)
		}
	}
	path, err := m.persistPipelinesProjectConfig(name, m.providers.pipelineDraftSteps)
	if err != nil {
		return err
	}
	m.providers.pipelineEditMode = false
	m.providers.pipelineDraftName = ""
	m.providers.pipelineDraftSteps = nil
	m.providers.pipelineDraftBuf = ""
	m.providers.pipelineNames = m.pipelineNamesFromEngine()
	m.notice = "saved pipeline to " + path
	return nil
}

func (m *Model) deletePipeline(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("pipeline name is empty")
	}
	if m.eng == nil || m.eng.Config == nil {
		return fmt.Errorf("engine not ready")
	}
	path, err := m.projectConfigPath()
	if err != nil {
		return err
	}
	cfg := m.eng.Config
	if cfg.Pipelines == nil {
		return fmt.Errorf("pipeline not found")
	}
	delete(cfg.Pipelines, name)
	if err := cfg.Save(path); err != nil {
		return err
	}
	if err := m.reloadEngineConfig(); err != nil {
		return err
	}
	if m.providers.activePipeline == name {
		m.providers.activePipeline = ""
	}
	return nil
}

func (m *Model) commitProfileEditField() {
	if m.eng == nil || m.eng.Config == nil {
		return
	}
	prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
	if !ok {
		return
	}
	draft := strings.TrimSpace(m.providers.profileEditDraft)
	if draft == "" {
		return
	}
	switch m.providers.profileEditField {
	case 0:
		prof.Protocol = draft
	case 1:
		prof.BaseURL = draft
	case 2:
		if v, err := strconv.Atoi(draft); err == nil {
			prof.MaxContext = v
		}
	case 3:
		if v, err := strconv.Atoi(draft); err == nil {
			prof.MaxTokens = v
		}
	}
	m.eng.Config.Providers.Profiles[m.providers.detailProvider] = prof
	m.providers.profileEditDraft = ""
}

func (m *Model) persistProfileEdits() error {
	if m.eng == nil || m.eng.Config == nil {
		return fmt.Errorf("engine not ready")
	}
	prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
	if !ok {
		return fmt.Errorf("provider not found")
	}
	// Pick the scope dynamically — same logic as panel toggles. If
	// the project already has a providers block, edits land there
	// (otherwise the next reload would silently revert because
	// project shadows user-home). Otherwise edits go to user-home so
	// the profile change survives across project switches.
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
	profileNode := ensureStringAnyMap(profilesNode, m.providers.detailProvider)
	if strings.TrimSpace(prof.Protocol) != "" {
		profileNode["protocol"] = prof.Protocol
	}
	if strings.TrimSpace(prof.BaseURL) != "" {
		profileNode["base_url"] = prof.BaseURL
	}
	if prof.MaxTokens > 0 {
		profileNode["max_tokens"] = prof.MaxTokens
	}
	if prof.MaxContext > 0 {
		profileNode["max_context"] = prof.MaxContext
	}
	if strings.TrimSpace(prof.Model) != "" {
		profileNode["model"] = prof.Model
	}
	if len(prof.Models) > 0 {
		profileNode["models"] = prof.Models
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
	return m.reloadEngineConfig()
}
