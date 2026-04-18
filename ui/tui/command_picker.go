package tui

// command_picker.go — interactive picker for /provider, /model, /tool,
// /read, /run, /grep slash commands.
//
// Lifted out of the 10K-line tui.go god file (REPORT.md C1) so the
// "user is hunting through a list" surface lives in one obvious place.
// Every method continues to live on `Model` — no behaviour change, no
// new abstractions. Update/handleChatKey still routes keystrokes here
// via handleCommandPickerKey when m.commandPicker.active is set.
//
// Vocabulary:
//   - kind         — which slash command opened the picker
//                    (provider | model | tool | read | run | grep)
//   - query        — what the user has typed since the picker opened;
//                    drives the substring/prefix filter
//   - persist      — when true, the apply* helpers also rewrite
//                    .dfmc/config.yaml; otherwise the change is
//                    session-only (Ctrl+S toggles)
//   - all/items    — base list snapshotted at open time, then filtered
//                    on every keystroke (no live model lookup mid-typing)

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func (m Model) handleCommandPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m = m.closeCommandPicker()
		m.notice = "Command picker closed."
		return m, nil
	case tea.KeyCtrlS:
		m.commandPicker.persist = !m.commandPicker.persist
		if m.commandPicker.persist {
			m.notice = "Picker apply mode: persist to .dfmc/config.yaml"
		} else {
			m.notice = "Picker apply mode: session only"
		}
		return m, nil
	case tea.KeyUp:
		items := m.filteredCommandPickerItems()
		if len(items) == 0 {
			return m, nil
		}
		idx := clampIndex(m.commandPicker.index, len(items))
		if idx > 0 {
			idx--
		}
		m.commandPicker.index = idx
		m.notice = "Select: " + items[idx].Value
		return m, nil
	case tea.KeyDown:
		items := m.filteredCommandPickerItems()
		if len(items) == 0 {
			return m, nil
		}
		idx := clampIndex(m.commandPicker.index, len(items))
		if idx < len(items)-1 {
			idx++
		}
		m.commandPicker.index = idx
		m.notice = "Select: " + items[idx].Value
		return m, nil
	case tea.KeyTab:
		items := m.filteredCommandPickerItems()
		if len(items) == 0 {
			return m, nil
		}
		idx := clampIndex(m.commandPicker.index, len(items))
		m.commandPicker.query = items[idx].Value
		m.commandPicker.index = 0
		m.syncInputWithCommandPicker()
		return m, nil
	case tea.KeyEnter:
		items := m.filteredCommandPickerItems()
		if len(items) == 0 {
			kind := strings.ToLower(strings.TrimSpace(m.commandPicker.kind))
			if strings.EqualFold(kind, "model") && strings.TrimSpace(m.commandPicker.query) != "" {
				return m.applyCommandPickerModel(strings.TrimSpace(m.commandPicker.query))
			}
			if (strings.EqualFold(kind, "tool") || strings.EqualFold(kind, "read") || strings.EqualFold(kind, "run") || strings.EqualFold(kind, "grep")) && strings.TrimSpace(m.commandPicker.query) != "" {
				return m.applyCommandPickerPreparedInput(strings.TrimSpace(m.commandPicker.query))
			}
			m.notice = "No selectable item."
			return m, nil
		}
		idx := clampIndex(m.commandPicker.index, len(items))
		selected := strings.TrimSpace(items[idx].Value)
		switch strings.ToLower(strings.TrimSpace(m.commandPicker.kind)) {
		case "provider":
			return m.applyCommandPickerProvider(selected)
		case "model":
			return m.applyCommandPickerModel(selected)
		case "tool", "read", "run", "grep":
			return m.applyCommandPickerPreparedInput(selected)
		default:
			m.notice = "Unknown picker mode."
			return m, nil
		}
	case tea.KeyBackspace, tea.KeyCtrlH:
		if len(m.commandPicker.query) > 0 {
			runes := []rune(m.commandPicker.query)
			m.commandPicker.query = string(runes[:len(runes)-1])
			m.commandPicker.index = 0
			m.syncInputWithCommandPicker()
		}
		return m, nil
	case tea.KeySpace:
		m.commandPicker.query += " "
		m.commandPicker.index = 0
		m.syncInputWithCommandPicker()
		return m, nil
	case tea.KeyRunes:
		m.commandPicker.query += string(msg.Runes)
		m.commandPicker.index = 0
		m.syncInputWithCommandPicker()
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) startCommandPicker(kind, query string, persist bool) Model {
	kind = strings.ToLower(strings.TrimSpace(kind))
	query = strings.TrimSpace(query)
	m.commandPicker.active = true
	m.commandPicker.kind = kind
	m.commandPicker.persist = persist
	m.commandPicker.query = query
	m.commandPicker.index = 0
	m.commandPicker.all = m.commandPickerBaseItems(kind)
	m.syncInputWithCommandPicker()
	label := strings.TrimSpace(kind)
	if label != "" {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	if label == "" {
		label = "Command"
	}
	m.notice = label + " picker ready. Enter to apply, Ctrl+S toggle persist."
	return m
}

func (m Model) closeCommandPicker() Model {
	m.commandPicker.active = false
	m.commandPicker.kind = ""
	m.commandPicker.query = ""
	m.commandPicker.index = 0
	m.commandPicker.persist = false
	m.commandPicker.all = nil
	m.setChatInput("")
	return m
}

func (m *Model) syncInputWithCommandPicker() {
	if !m.commandPicker.active {
		return
	}
	query := strings.TrimSpace(m.commandPicker.query)
	switch strings.ToLower(strings.TrimSpace(m.commandPicker.kind)) {
	case "provider":
		if query == "" {
			m.setChatInput("/provider ")
		} else {
			m.setChatInput("/provider " + query)
		}
	case "model":
		if query == "" {
			m.setChatInput("/model ")
		} else {
			m.setChatInput("/model " + query)
		}
	case "tool":
		if query == "" {
			m.setChatInput("/tool ")
		} else {
			m.setChatInput("/tool " + query)
		}
	case "read":
		if query == "" {
			m.setChatInput("/read ")
		} else {
			m.setChatInput("/read " + query)
		}
	case "run":
		if query == "" {
			m.setChatInput("/run ")
		} else {
			m.setChatInput("/run " + query)
		}
	case "grep":
		if query == "" {
			m.setChatInput("/grep ")
		} else {
			m.setChatInput("/grep " + query)
		}
	}
}

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
	names := m.availableProviders()
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

func (m Model) toolPickerItems() []commandPickerItem {
	names := m.availableTools()
	if len(names) == 0 {
		return nil
	}
	items := make([]commandPickerItem, 0, len(names))
	for _, name := range names {
		desc := strings.TrimSpace(m.toolDescription(name))
		meta := strings.TrimSpace(m.toolPresetSummary(name))
		items = append(items, commandPickerItem{
			Value:       name,
			Description: desc,
			Meta:        meta,
		})
	}
	return items
}

func (m Model) readPickerItems() []commandPickerItem {
	if len(m.filesView.entries) == 0 {
		return nil
	}
	items := make([]commandPickerItem, 0, len(m.filesView.entries))
	target := strings.TrimSpace(m.toolTargetFile())
	for _, path := range m.filesView.entries {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		metaParts := []string{}
		if strings.EqualFold(path, target) {
			metaParts = append(metaParts, "current")
		}
		if strings.EqualFold(path, strings.TrimSpace(m.filesView.pinned)) {
			metaParts = append(metaParts, "pinned")
		}
		items = append(items, commandPickerItem{
			Value:       path,
			Description: "file",
			Meta:        strings.Join(metaParts, " | "),
		})
	}
	return items
}

func (m Model) runPickerItems() []commandPickerItem {
	raw := m.runCommandSuggestions()
	if len(raw) == 0 {
		return nil
	}
	items := make([]commandPickerItem, 0, len(raw))
	for _, suggestion := range raw {
		params, err := parseToolParamString(suggestion)
		if err != nil {
			continue
		}
		command := paramStr(params, "command")
		args := paramStr(params, "args")
		value := command
		if args != "" {
			value += " " + args
		}
		metaParts := []string{}
		if dir := paramStr(params, "dir"); dir != "" {
			metaParts = append(metaParts, "dir="+dir)
		}
		if timeout := paramStr(params, "timeout_ms"); timeout != "" {
			metaParts = append(metaParts, "timeout="+timeout)
		}
		items = append(items, commandPickerItem{
			Value:       value,
			Description: "guarded command preset",
			Meta:        strings.Join(metaParts, " | "),
		})
	}
	return items
}

func (m Model) grepPickerItems() []commandPickerItem {
	candidates := []string{}
	add := func(value, desc string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, item := range candidates {
			if strings.EqualFold(item, value) {
				return
			}
		}
		candidates = append(candidates, value)
	}
	if pattern := strings.TrimSpace(m.toolGrepPattern()); pattern != "" {
		add(pattern, "")
	}
	for _, item := range []string{"TODO", "FIXME", "panic\\(", "console\\.log", "fmt\\.Println"} {
		add(item, "")
	}
	items := make([]commandPickerItem, 0, len(candidates))
	for _, value := range candidates {
		desc := "search preset"
		if value == strings.TrimSpace(m.toolGrepPattern()) {
			desc = "derived from current context"
		}
		items = append(items, commandPickerItem{
			Value:       value,
			Description: desc,
			Meta:        "max_results=80",
		})
	}
	return items
}

func (m Model) applyCommandPickerProvider(selected string) (tea.Model, tea.Cmd) {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		m.notice = "Provider selection is empty."
		return m, nil
	}
	model := m.defaultModelForProvider(selected)
	m = m.applyProviderModelSelection(selected, model)
	persist := m.commandPicker.persist
	m = m.closeCommandPicker()
	if persist {
		path, err := m.persistProviderModelProjectConfig(selected, model)
		if err != nil {
			m.notice = "provider persist: " + err.Error()
			return m.appendSystemMessage(fmt.Sprintf("Provider set to %s (%s)\nPersist error: %v", selected, blankFallback(model, "-"), err)), loadStatusCmd(m.eng)
		}
		m.notice = fmt.Sprintf("Provider set to %s (%s), saved to %s", selected, blankFallback(model, "-"), filepath.ToSlash(path))
		return m.appendSystemMessage(fmt.Sprintf("Provider set to %s (%s)\nSaved project config: %s", selected, blankFallback(model, "-"), filepath.ToSlash(path))), loadStatusCmd(m.eng)
	}
	m.notice = fmt.Sprintf("Provider set to %s (%s)", selected, blankFallback(model, "-"))
	return m.appendSystemMessage(fmt.Sprintf("Provider set to %s (%s)", selected, blankFallback(model, "-"))), loadStatusCmd(m.eng)
}

func (m Model) applyCommandPickerModel(selected string) (tea.Model, tea.Cmd) {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		m.notice = "Model selection is empty."
		return m, nil
	}
	providerName := m.currentProvider()
	m = m.applyProviderModelSelection(providerName, selected)
	persist := m.commandPicker.persist
	m = m.closeCommandPicker()
	if persist {
		path, err := m.persistProviderModelProjectConfig(providerName, selected)
		if err != nil {
			m.notice = "model persist: " + err.Error()
			return m.appendSystemMessage(fmt.Sprintf("Model set to %s (%s)\nPersist error: %v", selected, blankFallback(providerName, "-"), err)), loadStatusCmd(m.eng)
		}
		m.notice = fmt.Sprintf("Model set to %s (%s), saved to %s", selected, blankFallback(providerName, "-"), filepath.ToSlash(path))
		return m.appendSystemMessage(fmt.Sprintf("Model set to %s (%s)\nSaved project config: %s", selected, blankFallback(providerName, "-"), filepath.ToSlash(path))), loadStatusCmd(m.eng)
	}
	m.notice = fmt.Sprintf("Model set to %s (%s)", selected, blankFallback(providerName, "-"))
	return m.appendSystemMessage(fmt.Sprintf("Model set to %s (%s)", selected, blankFallback(providerName, "-"))), loadStatusCmd(m.eng)
}

func (m Model) applyCommandPickerPreparedInput(selected string) (tea.Model, tea.Cmd) {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		m.notice = "Selection is empty."
		return m, nil
	}
	kind := strings.ToLower(strings.TrimSpace(m.commandPicker.kind))
	switch kind {
	case "tool":
		m = m.closeCommandPicker()
		m.setChatInput("/tool " + selected + " ")
		m.notice = "Tool command prepared: " + selected
		return m, nil
	case "read":
		m = m.closeCommandPicker()
		m.setChatInput("/read " + formatSlashArgToken(selected) + " ")
		m.notice = "Read command prepared: " + selected
		return m, nil
	case "run":
		m = m.closeCommandPicker()
		m.setChatInput("/run " + selected)
		m.notice = "Run command prepared: " + selected
		return m, nil
	case "grep":
		m = m.closeCommandPicker()
		m.setChatInput("/grep " + formatSlashArgToken(selected))
		m.notice = "Grep command prepared: " + selected
		return m, nil
	default:
		m.notice = "Unknown picker mode."
		return m, nil
	}
}

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
	catalog, err := config.LoadModelsDevCatalog(config.ModelsDevCachePath())
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
