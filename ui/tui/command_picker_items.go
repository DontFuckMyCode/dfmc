package tui

// command_picker_items.go — list builders for the /provider and
// /model pickers + the dispatcher (commandPickerBaseItems) + the
// prefix-then-contains filter (filteredCommandPickerItems) + the
// models.dev catalog helpers shared between provider and model.
// Action-tool pickers (/tool, /read, /run, /grep) live in
// command_picker_items_action.go. Companion siblings:
//
//   - command_picker.go             lifecycle + keyboard handler +
//                                   filter (handleCommandPickerKey,
//                                   startCommandPicker,
//                                   closeCommandPicker,
//                                   syncInputWithCommandPicker)
//   - command_picker_items_action.go tool/read/run/grep picker items
//   - command_picker_apply.go        selection appliers
//                                    (applyCommandPickerProvider/Model/PreparedInput)
//
// Each *PickerItems function returns a base list snapshotted at picker
// open time; filteredCommandPickerItems then applies the user's
// running query as a (prefix-first, then contains) filter on every
// keystroke. modelsFromModelsDevCache + modelsDevModelKnown read the
// provider catalog the engine sync'd from models.dev.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func (m Model) commandPickerBaseItems(kind string) []commandPickerItem {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "provider":
		return m.providerPickerItems()
	case "model":
		return m.modelPickerItems(m.currentProvider())
	case "tool":
		return m.toolPickerItems()
	case "read":
		return m.readPickerItems()
	case "run":
		return m.runPickerItems()
	case "grep":
		return m.grepPickerItems()
	default:
		return nil
	}
}

func (m Model) filteredCommandPickerItems() []commandPickerItem {
	items := append([]commandPickerItem(nil), m.commandPicker.all...)
	if len(items) == 0 {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(m.commandPicker.query))
	if query == "" {
		return items
	}
	prefix := make([]commandPickerItem, 0, len(items))
	contains := make([]commandPickerItem, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Value)
		if name == "" {
			continue
		}
		searchBlob := strings.ToLower(strings.Join([]string{name, item.Description, item.Meta}, " "))
		if strings.HasPrefix(strings.ToLower(name), query) {
			prefix = append(prefix, item)
			continue
		}
		if strings.Contains(searchBlob, query) {
			contains = append(contains, item)
		}
	}
	return append(prefix, contains...)
}

func (m Model) providerPickerItems() []commandPickerItem {
	// Enumerate ALL known provider profiles, not just the
	// availableProviders() subset that filters by API-key presence.
	// The picker already surfaces an "unconfigured" meta tag so the
	// user can pick a provider, learn it needs a key, and run /key.
	// Hiding unconfigured ones makes the surface feel broken on a
	// freshly installed binary where only "generic" / "offline" have
	// keys (or in test environments where keys aren't set).
	names := []string{}
	if m.eng != nil && m.eng.Config != nil {
		for name := range m.eng.Config.Providers.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	if len(names) == 0 {
		names = m.availableProviders()
	}
	if len(names) == 0 {
		return nil
	}
	items := make([]commandPickerItem, 0, len(names))
	active := strings.TrimSpace(m.currentProvider())
	for _, name := range names {
		profile := m.providerProfile(name)
		desc := blankFallback(profile.Protocol, "provider")
		if model := strings.TrimSpace(profile.Model); model != "" {
			desc += " | " + model
		}
		metaParts := []string{}
		if strings.EqualFold(name, active) {
			metaParts = append(metaParts, "active")
		}
		if profile.Configured {
			metaParts = append(metaParts, "configured")
		} else {
			metaParts = append(metaParts, "unconfigured")
		}
		if profile.MaxContext > 0 {
			metaParts = append(metaParts, fmt.Sprintf("ctx=%d", profile.MaxContext))
		}
		items = append(items, commandPickerItem{
			Value:       name,
			Description: desc,
			Meta:        strings.Join(metaParts, " | "),
		})
	}
	return items
}

func (m Model) modelPickerItems(providerName string) []commandPickerItem {
	models := m.availableModelsForProvider(providerName)
	if len(models) == 0 {
		return nil
	}
	currentProvider := strings.TrimSpace(m.currentProvider())
	currentModel := strings.TrimSpace(m.currentModel())
	defaultModel := strings.TrimSpace(m.defaultModelForProvider(providerName))
	items := make([]commandPickerItem, 0, len(models))
	for _, model := range models {
		metaParts := []string{}
		if strings.EqualFold(providerName, currentProvider) && strings.EqualFold(model, currentModel) {
			metaParts = append(metaParts, "active")
		}
		if strings.EqualFold(model, defaultModel) {
			metaParts = append(metaParts, "default")
		}
		if modelsDevModelKnown(providerName, model) {
			metaParts = append(metaParts, "catalog")
		}
		items = append(items, commandPickerItem{
			Value:       model,
			Description: "provider " + blankFallback(providerName, "-"),
			Meta:        strings.Join(metaParts, " | "),
		})
	}
	return items
}

// toolPickerItems / readPickerItems / runPickerItems /
// grepPickerItems live in command_picker_items_action.go.

func (m Model) availableModelsForProvider(providerName string) []string {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		providerName = strings.TrimSpace(m.currentProvider())
	}
	set := map[string]string{}
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		key := strings.ToLower(model)
		if _, exists := set[key]; exists {
			return
		}
		set[key] = model
	}
	add(m.currentModel())
	add(m.defaultModelForProvider(providerName))
	if m.eng != nil && m.eng.Config != nil {
		if profile, ok := m.eng.Config.Providers.Profiles[providerName]; ok {
			add(profile.Model)
		}
	}
	for _, model := range modelsFromModelsDevCache(providerName) {
		add(model)
	}
	out := make([]string, 0, len(set))
	for _, model := range set {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

func modelsFromModelsDevCache(providerName string) []string {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return nil
	}
	catalog, err := config.LoadModelsDevCatalogCached(config.ModelsDevCachePath())
	if err != nil || len(catalog) == 0 {
		return nil
	}
	candidates := []config.ModelsDevProvider{}
	for key, item := range catalog {
		if strings.EqualFold(strings.TrimSpace(key), providerName) || strings.EqualFold(strings.TrimSpace(item.ID), providerName) {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	set := map[string]string{}
	for _, item := range candidates {
		for key, model := range item.Models {
			id := strings.TrimSpace(model.ID)
			if id == "" {
				id = strings.TrimSpace(key)
			}
			if id == "" {
				continue
			}
			lower := strings.ToLower(id)
			if _, ok := set[lower]; ok {
				continue
			}
			set[lower] = id
		}
	}
	out := make([]string, 0, len(set))
	for _, model := range set {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

func modelsDevModelKnown(providerName, modelName string) bool {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return false
	}
	for _, item := range modelsFromModelsDevCache(providerName) {
		if strings.EqualFold(strings.TrimSpace(item), modelName) {
			return true
		}
	}
	return false
}
