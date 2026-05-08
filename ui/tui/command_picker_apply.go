package tui

// command_picker_apply.go — selection appliers for the command
// picker. Companion siblings:
//
//   - command_picker.go       lifecycle + keyboard handler + filter
//   - command_picker_items.go list builders for each picker kind
//
// applyCommandPickerProvider/Model wire the choice into the engine's
// active provider/model and (when persist is on) write the change to
// the project config; both paths emit a system message into the chat
// transcript so the user sees what just happened. apply
// CommandPickerPreparedInput is the lighter path for /tool, /read,
// /run, /grep — it just stuffs the slash command into the composer
// for the user to send.

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

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
