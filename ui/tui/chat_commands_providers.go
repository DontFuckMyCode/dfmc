package tui

// Provider / model slash commands: /providers, /provider,
// /models, /model. Extracted from chat_commands.go — each handler
// here applies a provider/model selection against the engine and
// optionally persists the choice to the project config.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) runProviderCommand(cmd string, args []string) (tea.Model, tea.Cmd, bool) {
	switch cmd {
	case "providers":
		names := m.availableProviders()
		if len(names) == 0 {
			m.notice = "No providers configured."
			return m.appendSystemMessage("No providers configured."), nil, true
		}
		m.chat.input = ""
		return m.appendSystemMessage("Providers: " + strings.Join(names, ", ")), loadStatusCmd(m.eng), true
	case "provider":
		parts, persist := parseArgsWithPersist(args)
		if len(parts) == 0 {
			m = m.startCommandPicker("provider", "", persist)
			return m, nil, true
		}
		name := strings.TrimSpace(parts[0])
		model := strings.TrimSpace(strings.Join(parts[1:], " "))
		// Accept any provider that exists in the profile map, not just
		// the availableProviders() subset (which filters by API-key
		// presence). The user spelling the name out is a clear intent
		// signal; if the provider needs a key, the engine surface
		// (PlaceholderProvider) handles the degraded path and /key
		// can wire one up. Hiding known profiles behind the picker
		// here forced an extra round trip for every fresh install.
		if !m.providerProfileExists(name) {
			m = m.startCommandPicker("provider", name, persist)
			return m, nil, true
		}
		if model == "" {
			model = m.defaultModelForProvider(name)
		}
		m = m.applyProviderModelSelection(name, model)
		m.chat.input = ""
		if persist {
			path, err := m.persistProviderModelProjectConfig(name, model)
			if err != nil {
				m.notice = "provider persist: " + err.Error()
				return m.appendSystemMessage(fmt.Sprintf("Provider set to %s (%s)\nPersist error: %v", name, blankFallback(model, "-"), err)), loadStatusCmd(m.eng), true
			}
			m.notice = fmt.Sprintf("Provider set to %s (%s) · saved → %s", name, blankFallback(model, "-"), displayConfigPath(path))
			return m.appendSystemMessage(fmt.Sprintf("Provider set to %s (%s)\nSaved → %s", name, blankFallback(model, "-"), displayConfigPath(path))), loadStatusCmd(m.eng), true
		}
		return m.appendSystemMessage(fmt.Sprintf("Provider set to %s (%s)", name, blankFallback(model, "-"))), loadStatusCmd(m.eng), true
	case "models":
		current := m.currentProvider()
		if current == "" {
			return m.appendSystemMessage("No active provider."), nil, true
		}
		model := m.defaultModelForProvider(current)
		choices := m.availableModelsForProvider(current)
		message := fmt.Sprintf("Configured model for %s: %s", current, blankFallback(model, "-"))
		if len(choices) > 0 {
			message += "\nKnown models: " + strings.Join(choices, ", ")
		}
		m.chat.input = ""
		return m.appendSystemMessage(message), nil, true
	case "model":
		providerName := m.currentProvider()
		model, persist := parseModelPersistArgs(args)
		if model == "" {
			m = m.startCommandPicker("model", "", persist)
			return m, nil, true
		}
		if choices := m.availableModelsForProvider(providerName); len(choices) > 0 && !containsStringFold(choices, model) {
			m = m.startCommandPicker("model", model, persist)
			return m, nil, true
		}
		m = m.applyProviderModelSelection(providerName, model)
		m.chat.input = ""
		if persist {
			path, err := m.persistProviderModelProjectConfig(providerName, model)
			if err != nil {
				m.notice = "model persist: " + err.Error()
				return m.appendSystemMessage(fmt.Sprintf("Model set to %s (%s)\nPersist error: %v", model, blankFallback(providerName, "-"), err)), loadStatusCmd(m.eng), true
			}
			m.notice = fmt.Sprintf("Model set to %s (%s) · saved → %s", model, blankFallback(providerName, "-"), displayConfigPath(path))
			return m.appendSystemMessage(fmt.Sprintf("Model set to %s (%s)\nSaved → %s", model, blankFallback(providerName, "-"), displayConfigPath(path))), loadStatusCmd(m.eng), true
		}
		return m.appendSystemMessage(fmt.Sprintf("Model set to %s (%s)", model, blankFallback(providerName, "-"))), loadStatusCmd(m.eng), true
	}
	return m, nil, false
}
