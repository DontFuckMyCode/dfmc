package tui

// provider_panel_actions.go — per-row mutations for the Providers
// panel: add a model to a profile, cycle a profile's preferred model,
// flip primary/fallback selection, delete the active model. All
// persist either to the user-home or project config depending on
// effectivePersistScope, then trigger a reload so the engine surface
// picks up the change without a TUI restart. Companion siblings:
//
//   - provider_panel.go                  types (providerRow /
//                                        syncModelsDevMsg) +
//                                        collectProviderRows +
//                                        filteredProviderRows +
//                                        resolveProviderOrder +
//                                        providerStatusTag/Priority/Style
//                                        + apiKeySourceBadge +
//                                        formatRelativeTime +
//                                        refreshProvidersRows +
//                                        focusProviderRow +
//                                        isProvidersInputMode +
//                                        detailProviderModels +
//                                        loadModelsDevForProvider +
//                                        pipelineNamesFromEngine
//   - provider_panel_crud.go             syncModelsDevCmd +
//                                        createProvider / deleteProvider
//                                        for whole-profile lifecycle
//   - provider_panel_actions_persist.go  pipeline lifecycle
//                                        (savePipelineDraft +
//                                        deletePipeline) + profile-edit
//                                        commit + persistProfileEdits
//                                        YAML-direct writer
//   - provider_panel_key*.go             panel keyboard router siblings

import (
	"fmt"
	"strings"
)

func (m *Model) addModelToProvider(provider, model string) {
	if m.eng == nil || m.eng.Config == nil {
		return
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	prof, ok := m.eng.Config.Providers.Profiles[provider]
	if !ok {
		return
	}
	models := prof.AllModels()
	found := false
	for _, existing := range models {
		if strings.EqualFold(strings.TrimSpace(existing), model) {
			found = true
			break
		}
	}
	if !found {
		models = append(models, model)
	}
	prof.Models = models
	if strings.TrimSpace(prof.Model) == "" {
		prof.Model = model
	}
	m.eng.Config.Providers.Profiles[provider] = prof
	m.notice = fmt.Sprintf("added model %s to %s", model, provider)
	path, err := m.persistProviderModelUserConfig(provider, prof.Model)
	if err != nil {
		m.notice += " (save failed: " + err.Error() + ")"
	} else {
		if m.eng != nil && strings.TrimSpace(m.eng.ProjectRoot) != "" {
			if reloadErr := m.reloadEngineConfig(); reloadErr != nil {
				m.notice += " (reload failed: " + reloadErr.Error() + ")"
			}
		}
		m.notice += " - saved -> " + displayConfigPath(path)
	}
	*m = m.refreshProvidersRows()
	*m = m.focusProviderRow(provider)
}

func (m Model) setProviderActiveModel(provider, model string) Model {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" || m.eng == nil || m.eng.Config == nil {
		return m
	}
	prof, ok := m.eng.Config.Providers.Profiles[provider]
	if !ok {
		m.notice = "provider not found"
		return m
	}
	models := prof.AllModels()
	found := false
	for _, item := range models {
		if strings.EqualFold(strings.TrimSpace(item), model) {
			found = true
			break
		}
	}
	if !found {
		models = append(models, model)
		prof.Models = models
	}
	prof.Model = model
	prof.FallbackModels = removeModelFromList(prof.FallbackModels, model)
	prof = applyCatalogModelLimits(prof, model)
	m.eng.Config.Providers.Profiles[provider] = prof
	m.eng.SetProviderModel(provider, model)
	path, err := m.persistProviderModelUserConfig(provider, model)
	if err != nil {
		m.notice = "model saved in memory but persist failed: " + err.Error()
	} else if m.eng != nil && strings.TrimSpace(m.eng.ProjectRoot) != "" {
		if reloadErr := m.reloadEngineConfig(); reloadErr != nil {
			m.notice = "model saved but reload failed: " + reloadErr.Error()
		} else {
			m.eng.SetProviderModel(provider, model)
			m.notice = fmt.Sprintf("model saved -> %s", displayConfigPath(path))
		}
	} else {
		m.eng.SetProviderModel(provider, model)
		m.notice = fmt.Sprintf("model saved -> %s", displayConfigPath(path))
	}
	m = m.refreshProvidersRows()
	m = m.focusProviderRow(provider)
	m.status = m.eng.Status()
	return m
}

func removeModelFromList(models []string, model string) []string {
	model = strings.TrimSpace(model)
	out := make([]string, 0, len(models))
	for _, existing := range models {
		existing = strings.TrimSpace(existing)
		if existing == "" || strings.EqualFold(existing, model) {
			continue
		}
		out = append(out, existing)
	}
	return out
}

func (m Model) toggleProviderFallbackModel(provider, model string) Model {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" || model == "" || m.eng == nil || m.eng.Config == nil {
		return m
	}
	prof, ok := m.eng.Config.Providers.Profiles[provider]
	if !ok {
		m.notice = "provider not found"
		return m
	}
	if strings.EqualFold(strings.TrimSpace(prof.Model), model) {
		m.notice = "primary model cannot also be fallback"
		return m
	}
	next := make([]string, 0, len(prof.FallbackModels)+1)
	found := false
	for _, existing := range prof.FallbackModels {
		existing = strings.TrimSpace(existing)
		if existing == "" {
			continue
		}
		if strings.EqualFold(existing, model) {
			found = true
			continue
		}
		next = append(next, existing)
	}
	if !found {
		next = append(next, model)
	}
	prof.FallbackModels = next
	models := prof.AllModels()
	known := false
	for _, existing := range models {
		if strings.EqualFold(strings.TrimSpace(existing), model) {
			known = true
			break
		}
	}
	if !known {
		prof.Models = append(models, model)
	}
	m.eng.Config.Providers.Profiles[provider] = prof
	path, err := m.persistProviderModelUserConfig(provider, prof.Model)
	if err == nil && m.eng != nil && strings.TrimSpace(m.eng.ProjectRoot) != "" {
		if reloadErr := m.reloadEngineConfig(); reloadErr != nil {
			err = reloadErr
		}
	}
	switch {
	case err != nil:
		m.notice = "model fallback save failed: " + err.Error()
	case found:
		m.notice = fmt.Sprintf("removed fallback model %s -> %s", model, displayConfigPath(path))
	default:
		m.notice = fmt.Sprintf("added fallback model %s -> %s", model, displayConfigPath(path))
	}
	m = m.refreshProvidersRows()
	m = m.focusProviderRow(provider)
	m.status = m.eng.Status()
	return m
}

// savePipelineDraft + deletePipeline live in
// provider_panel_actions_persist.go.

func (m Model) cycleProviderModel(name string) Model {
	if m.eng == nil || m.eng.Config == nil {
		return m
	}
	prof, ok := m.eng.Config.Providers.Profiles[name]
	if !ok {
		return m
	}
	models := prof.AllModels()
	if len(models) == 0 {
		m.notice = name + " has no models to cycle"
		return m
	}
	current := strings.TrimSpace(prof.Model)
	idx := 0
	for i, model := range models {
		if strings.EqualFold(model, current) {
			idx = i
			break
		}
	}
	idx = (idx + 1) % len(models)
	prof.Model = models[idx]
	prof = applyCatalogModelLimits(prof, prof.Model)
	m.eng.Config.Providers.Profiles[name] = prof
	path, err := m.persistProviderModelUserConfig(name, prof.Model)
	switch {
	case err != nil:
		m.notice = "cycle " + name + " model → " + prof.Model + " (save failed: " + err.Error() + ")"
	case path != "":
		m.notice = fmt.Sprintf("cycle %s model → %s · saved → %s", name, prof.Model, displayConfigPath(path))
	default:
		m.notice = "cycle " + name + " model → " + prof.Model
	}
	m = m.refreshProvidersRows()
	m = m.focusProviderRow(name)
	return m
}

func (m Model) setPrimaryProvider(name string) Model {
	if m.eng == nil || m.eng.Config == nil {
		return m
	}
	m.eng.Config.Providers.Primary = name
	if m.eng.Providers != nil {
		m.eng.Providers.SetPrimary(name)
	}
	path, err := m.persistProvidersPrimaryFallbackPath()
	if err != nil {
		m.notice = "primary set failed: " + err.Error()
	} else if path != "" {
		m.notice = fmt.Sprintf("%s is now primary · saved → %s", name, displayConfigPath(path))
	} else {
		m.notice = name + " is now primary"
	}
	m = m.refreshProvidersRows()
	m = m.focusProviderRow(name)
	return m
}

func (m Model) toggleFallbackProvider(name string) Model {
	if m.eng == nil || m.eng.Config == nil {
		return m
	}
	var newFallback []string
	found := false
	for _, fb := range m.eng.Config.Providers.Fallback {
		if strings.EqualFold(fb, name) {
			found = true
			continue
		}
		newFallback = append(newFallback, fb)
	}
	if !found {
		newFallback = []string{name}
	}
	m.eng.Config.Providers.Fallback = newFallback
	if m.eng != nil && m.eng.Providers != nil {
		m.eng.Providers.SetFallback(newFallback)
	}
	path, err := m.persistProvidersPrimaryFallbackPath()
	switch {
	case err != nil:
		m.notice = "fallback toggle failed: " + err.Error()
	case found && path != "":
		m.notice = fmt.Sprintf("removed %s from fallback · saved → %s", name, displayConfigPath(path))
	case found:
		m.notice = "removed " + name + " from fallback"
	case path != "":
		m.notice = fmt.Sprintf("added %s to fallback · saved → %s", name, displayConfigPath(path))
	default:
		m.notice = "added " + name + " to fallback"
	}
	m = m.refreshProvidersRows()
	m = m.focusProviderRow(name)
	return m
}

func (m Model) deleteActiveModel() Model {
	if m.eng == nil || m.eng.Config == nil {
		m.notice = "engine not ready"
		return m
	}
	prof, ok := m.eng.Config.Providers.Profiles[m.providers.detailProvider]
	if !ok {
		m.notice = "provider not found"
		return m
	}
	models := prof.AllModels()
	if len(models) == 0 {
		m.notice = "no models to delete"
		return m
	}
	// modelEditIdx is an index into the filtered/visible model list,
	// not the full model list. Resolve to the actual model name first.
	visible := m.detailProviderVisibleModels()
	idx := m.providers.modelEditIdx
	if idx < 0 || idx >= len(visible) {
		idx = 0
	}
	if len(visible) == 0 {
		m.notice = "no visible model to delete"
		return m
	}
	target := visible[idx]
	// Find target in the full (unfiltered) list.
	fullIdx := -1
	for i, m := range models {
		if m == target {
			fullIdx = i
			break
		}
	}
	if fullIdx < 0 {
		m.notice = "model not found: " + target
		return m
	}
	deleted := models[fullIdx]
	models = append(models[:fullIdx], models[fullIdx+1:]...)
	prof.Models = models
	if len(models) > 0 {
		prof.Model = models[0]
	} else {
		prof.Model = ""
	}
	m.eng.Config.Providers.Profiles[m.providers.detailProvider] = prof
	if m.providers.modelEditIdx >= len(models) {
		m.providers.modelEditIdx = max(0, len(models)-1)
	}
	m = m.refreshProvidersRows()
	m = m.focusProviderRow(m.providers.detailProvider)
	m.notice = fmt.Sprintf("deleted model %s from %s", deleted, m.providers.detailProvider)
	return m
}

// commitProfileEditField + persistProfileEdits live in
// provider_panel_actions_persist.go.
