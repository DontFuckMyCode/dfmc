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
//   - persist      — when true, the apply* helpers also print the
//                    saved config path; provider/model selections are
//                    auto-saved either way (Ctrl+S toggles)
//   - all/items    — base list snapshotted at open time, then filtered
//                    on every keystroke (no live model lookup mid-typing)
//
// Companion siblings (extracted to keep this file scannable):
//
//   - command_picker_items.go list builders for each picker kind
//                             (commandPickerBaseItems / filtered + the
//                             six per-kind picker functions + models.dev
//                             catalog readers)
//   - command_picker_apply.go selection appliers
//                             (applyCommandPickerProvider/Model
//                             persist + applyCommandPickerPreparedInput
//                             prep-input variant)

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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
			m.notice = "Picker apply mode: show saved config path"
		} else {
			m.notice = "Picker apply mode: auto-save quietly"
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
	m.commandPicker.index = m.defaultCommandPickerIndex(kind, m.commandPicker.all)
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

func (m Model) defaultCommandPickerIndex(kind string, items []commandPickerItem) int {
	if len(items) == 0 {
		return 0
	}
	var preferred string
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "provider":
		preferred = m.currentProvider()
	case "model":
		preferred = m.currentModel()
	default:
		return 0
	}
	for i, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.Value), strings.TrimSpace(preferred)) {
			return i
		}
	}
	return 0
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
