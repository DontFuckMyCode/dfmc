package tui

// slash_arg_suggestions.go — per-tool and /run-command argument
// suggestion feeders.
//
// Once the picker knows the active slash command, these helpers decide
// which `key=value` pairs to offer. toolParamKey* build the list of
// parameter keys for a given tool (hard-coded for builtin tools,
// preset-driven for the rest); toolValueTokenSuggestions turns each
// known key into value candidates (paths, dirs, commands, booleans,
// numeric presets). runCommand*Suggestions + projectDirSuggestions
// mine the tool-preset history and file list for hints.
//
// Split out of slash_picker.go so the picker file is focused on menu
// / autocomplete flow rather than the lookup tables. mapSuggestions
// ToKV is the shared formatter for all the value-side helpers.

import (
	"path/filepath"
	"sort"
	"strings"
)

func (m Model) toolParamKeySuggestions(toolName string, existingTokens []string, prefix string) []string {
	keys := m.toolParamKeyCatalog(toolName)
	if len(keys) == 0 {
		return nil
	}
	used := map[string]struct{}{}
	for _, token := range existingTokens {
		key, _, ok := strings.Cut(strings.TrimSpace(token), "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		used[key] = struct{}{}
	}
	prefix = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(prefix, "=")))
	out := make([]string, 0, len(keys))
	for _, token := range keys {
		key := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(token, "=")))
		if key == "" {
			continue
		}
		if _, exists := used[key]; exists {
			continue
		}
		if prefix != "" && !strings.HasPrefix(key, prefix) && !strings.Contains(key, prefix) {
			continue
		}
		out = append(out, token)
	}
	return out
}

func (m Model) toolParamKeyCatalog(toolName string) []string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "list_dir":
		return []string{"path=", "recursive=", "max_entries="}
	case "read_file":
		return []string{"path=", "line_start=", "line_end="}
	case "grep_codebase":
		return []string{"pattern=", "max_results="}
	case "run_command":
		return []string{"command=", "args=", "dir=", "timeout_ms="}
	case "write_file":
		return []string{"path=", "content=", "overwrite=", "create_dirs="}
	case "edit_file":
		return []string{"path=", "old_string=", "new_string=", "replace_all="}
	default:
		preset := strings.TrimSpace(m.toolPresetSummary(toolName))
		if preset == "" || strings.EqualFold(preset, "no preset available") {
			return nil
		}
		params, err := parseToolParamString(preset)
		if err != nil || len(params) == 0 {
			return nil
		}
		keys := make([]string, 0, len(params))
		for key := range params {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			keys = append(keys, key+"=")
		}
		sort.Strings(keys)
		return keys
	}
}

func (m Model) toolValueTokenSuggestions(toolName, key, valuePrefix string) []string {
	key = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(key, "=")))
	valuePrefix = strings.TrimSpace(valuePrefix)
	switch key {
	case "path":
		candidates := m.filesView.entries
		if strings.EqualFold(strings.TrimSpace(toolName), "list_dir") {
			candidates = m.projectDirSuggestions()
		}
		candidates = filterSuggestionsByToken(candidates, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 12)
	case "dir":
		candidates := filterSuggestionsByToken(m.projectDirSuggestions(), valuePrefix)
		return mapSuggestionsToKV(key, candidates, 10)
	case "command":
		candidates := filterSuggestionsByToken(m.runCommandNameSuggestions(), valuePrefix)
		return mapSuggestionsToKV(key, candidates, 8)
	case "args":
		candidates := filterSuggestionsByToken(m.runCommandArgSuggestions(), valuePrefix)
		return mapSuggestionsToKV(key, candidates, 8)
	case "pattern":
		candidates := []string{}
		if pattern := strings.TrimSpace(m.toolGrepPattern()); pattern != "" {
			candidates = append(candidates, pattern)
		}
		candidates = append(candidates, "TODO", "FIXME")
		candidates = filterSuggestionsByToken(candidates, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 6)
	case "recursive", "overwrite", "create_dirs", "replace_all":
		candidates := filterSuggestionsByToken([]string{"true", "false"}, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 2)
	case "line_start", "line_end":
		candidates := filterSuggestionsByToken([]string{"1", "80", "120", "200"}, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 4)
	case "max_entries":
		candidates := filterSuggestionsByToken([]string{"40", "80", "120", "200"}, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 4)
	case "max_results":
		candidates := filterSuggestionsByToken([]string{"20", "40", "80", "120"}, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 4)
	case "timeout_ms":
		candidates := filterSuggestionsByToken([]string{"5000", "10000", "30000", "60000"}, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 4)
	default:
		return nil
	}
}

func mapSuggestionsToKV(key string, values []string, limit int) []string {
	if len(values) == 0 {
		return nil
	}
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, key+"="+value)
	}
	return out
}

func (m Model) runCommandNameSuggestions() []string {
	raw := m.runCommandSuggestions()
	if len(raw) == 0 {
		return nil
	}
	set := map[string]string{}
	for _, suggestion := range raw {
		params, err := parseToolParamString(suggestion)
		if err != nil {
			continue
		}
		command := paramStr(params, "command")
		if command == "" {
			continue
		}
		lower := strings.ToLower(command)
		if _, exists := set[lower]; exists {
			continue
		}
		set[lower] = command
	}
	out := make([]string, 0, len(set))
	for _, value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (m Model) runCommandArgSuggestions() []string {
	raw := m.runCommandSuggestions()
	if len(raw) == 0 {
		return nil
	}
	set := map[string]string{}
	for _, suggestion := range raw {
		params, err := parseToolParamString(suggestion)
		if err != nil {
			continue
		}
		args := paramStr(params, "args")
		if args == "" {
			continue
		}
		lower := strings.ToLower(args)
		if _, exists := set[lower]; exists {
			continue
		}
		set[lower] = args
	}
	out := make([]string, 0, len(set))
	for _, value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (m Model) projectDirSuggestions() []string {
	set := map[string]string{
		".": ".",
	}
	if dir := strings.TrimSpace(m.toolTargetDir()); dir != "" {
		set[strings.ToLower(dir)] = dir
	}
	for _, file := range m.filesView.entries {
		file = filepath.ToSlash(strings.TrimSpace(file))
		if file == "" {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(file))
		if dir == "" {
			dir = "."
		}
		lower := strings.ToLower(dir)
		if _, exists := set[lower]; exists {
			continue
		}
		set[lower] = dir
	}
	out := make([]string, 0, len(set))
	for _, value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
