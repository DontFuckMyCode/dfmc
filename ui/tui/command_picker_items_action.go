// command_picker_items_action.go — list builders for the action-tool
// pickers (/tool, /read, /run, /grep). Sibling of
// command_picker_items.go which keeps the dispatcher
// (commandPickerBaseItems), the prefix-then-contains filter
// (filteredCommandPickerItems), the provider/model pickers, and the
// models.dev catalog helpers shared between provider and model.
//
// Splitting these out keeps command_picker_items.go scoped to
// "what does the model picker know about the provider catalog" while
// this file owns "what work does each tool/read/run/grep preset
// suggest". They evolve independently — the model picker grows when
// models.dev sync changes the catalog shape; the action pickers grow
// when new tools or convenience presets are added.

package tui

import (
	"path/filepath"
	"strings"
)

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
