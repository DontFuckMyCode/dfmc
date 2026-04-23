package tui

// slash_picker.go — slash-command autocomplete and per-tool argument
// suggestion machinery.
//
// The picker's static data half (slashCommandCatalog, slashTemplate
// Overrides) lives in slash_catalog.go so "what commands exist" stays
// separate from "what to suggest next." Everything here operates on
// that catalog + user input to produce completions and arg hints.
// Every method continues to live on `Model` — no behaviour change.
// The chat key handler calls these via buildChatSuggestionState/
// handleChatKey.
//
// Two related but distinct surfaces live here:
//
//   - Slash MENU       — what the menu shows when the user types "/"
//                        (slashMenuActive, filteredSlashCommands,
//                        autocompleteSlashCommand, expandSlashSelection,
//                        slashSuggestionsForToken)
//   - Argument picker  — what's offered after the command is typed
//                        (activeSlashArgSuggestions, autocompleteSlashArg,
//                        slashAssistHints, formatSlash*, the
//                        toolParamKey* / toolValueToken* helpers, and
//                        the project-dir / run-command suggestion
//                        feeders)

import (
	"path/filepath"
	"sort"
	"strings"
)

func (m Model) slashMenuActive() bool {
	raw := strings.TrimLeft(m.chat.input, " \t\r\n")
	if !strings.HasPrefix(raw, "/") {
		return false
	}
	body := strings.TrimPrefix(raw, "/")
	if body == "" {
		return true
	}
	return !strings.ContainsAny(body, " \t\r\n")
}

func (m Model) activeSlashArgSuggestions() []string {
	raw := strings.TrimLeft(m.chat.input, " \t\r\n")
	if raw == "" || !strings.HasPrefix(raw, "/") || m.slashMenuActive() {
		return nil
	}
	cmd, args, _, err := parseChatCommandInput(raw)
	if err != nil || cmd == "" {
		return nil
	}
	trailingSpace := hasTrailingWhitespace(raw)
	switch cmd {
	case "provider":
		providers := m.availableProviders()
		if len(providers) == 0 {
			return nil
		}
		if len(args) == 0 {
			return providers
		}
		if len(args) == 1 && !trailingSpace {
			return filterSuggestionsByToken(providers, args[0])
		}
		providerName := strings.TrimSpace(args[0])
		if !containsStringFold(providers, providerName) {
			return filterSuggestionsByToken(providers, providerName)
		}
		models := m.availableModelsForProvider(providerName)
		if len(models) == 0 {
			return nil
		}
		if len(args) >= 2 && !trailingSpace {
			return filterSuggestionsByToken(models, args[len(args)-1])
		}
		return models
	case "model":
		models := m.availableModelsForProvider(m.currentProvider())
		if len(models) == 0 {
			return nil
		}
		if len(args) > 0 && !trailingSpace {
			return filterSuggestionsByToken(models, strings.Join(args, " "))
		}
		return models
	case "read":
		files := m.filesView.entries
		if len(files) == 0 {
			return nil
		}
		if len(args) == 0 {
			return firstSuggestions(files, 12)
		}
		if len(args) == 1 && !trailingSpace {
			return filterSuggestionsByToken(files, args[0])
		}
		return nil
	case "tool":
		tools := m.availableTools()
		if len(tools) == 0 {
			return nil
		}
		if len(args) == 0 {
			return tools
		}
		if len(args) == 1 && !trailingSpace {
			return filterSuggestionsByToken(tools, args[0])
		}
		toolName := strings.TrimSpace(args[0])
		if !containsStringFold(tools, toolName) {
			return filterSuggestionsByToken(tools, toolName)
		}
		paramTokens := append([]string(nil), args[1:]...)
		if len(paramTokens) == 0 || trailingSpace {
			return m.toolParamKeySuggestions(toolName, paramTokens, "")
		}
		last := strings.TrimSpace(paramTokens[len(paramTokens)-1])
		if last == "" {
			return m.toolParamKeySuggestions(toolName, paramTokens, "")
		}
		if !strings.Contains(last, "=") {
			return m.toolParamKeySuggestions(toolName, paramTokens, last)
		}
		key, value, _ := strings.Cut(last, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return m.toolParamKeySuggestions(toolName, paramTokens, "")
		}
		if suggestions := m.toolValueTokenSuggestions(toolName, key, value); len(suggestions) > 0 {
			return suggestions
		}
		return nil
	default:
		return nil
	}
}

func (m Model) autocompleteSlashArg() (string, bool) {
	raw := strings.TrimLeft(m.chat.input, " \t\r\n")
	if raw == "" || !strings.HasPrefix(raw, "/") || m.slashMenuActive() {
		return "", false
	}
	cmd, args, _, err := parseChatCommandInput(raw)
	if err != nil || cmd == "" {
		return "", false
	}
	suggestions := m.activeSlashArgSuggestions()
	if len(suggestions) == 0 {
		return "", false
	}
	selected := suggestions[clampIndex(m.slashMenu.commandArg, len(suggestions))]
	trailingSpace := hasTrailingWhitespace(raw)
	switch cmd {
	case "provider":
		updated := append([]string(nil), args...)
		if len(updated) == 0 {
			updated = []string{selected}
		} else if len(updated) == 1 && !trailingSpace {
			updated[0] = selected
		} else if trailingSpace && len(updated) == 1 {
			updated = append(updated, selected)
		} else if len(updated) >= 2 {
			updated[len(updated)-1] = selected
		}
		return formatSlashCommandInput(cmd, updated), true
	case "model":
		updated := append([]string(nil), args...)
		if len(updated) == 0 {
			updated = []string{selected}
		} else {
			updated[len(updated)-1] = selected
		}
		return formatSlashCommandInput(cmd, updated), true
	case "read":
		updated := append([]string(nil), args...)
		if len(updated) == 0 {
			updated = []string{selected}
		} else {
			updated[0] = selected
		}
		return formatSlashCommandInput(cmd, updated), true
	case "tool":
		updated := append([]string(nil), args...)
		tools := m.availableTools()
		if len(updated) == 0 {
			updated = []string{selected}
			return formatSlashCommandInput(cmd, updated), true
		}
		if len(updated) == 1 && !trailingSpace {
			updated[0] = selected
			return formatSlashCommandInput(cmd, updated), true
		}
		if !containsStringFold(tools, strings.TrimSpace(updated[0])) {
			updated[0] = selected
			return formatSlashCommandInput(cmd, updated), true
		}
		if trailingSpace {
			updated = append(updated, selected)
		} else if len(updated) >= 2 {
			updated[len(updated)-1] = selected
		} else {
			updated = append(updated, selected)
		}
		return formatSlashCommandInput(cmd, updated), true
	default:
		return "", false
	}
}

func (m Model) slashAssistHints() []string {
	raw := strings.TrimSpace(m.chat.input)
	if raw == "" || !strings.HasPrefix(raw, "/") || m.commandPicker.active {
		return nil
	}
	cmd, args, _, err := parseChatCommandInput(raw)
	if err != nil {
		return []string{"Command parse error: " + err.Error()}
	}
	if cmd == "" {
		return []string{
			"Type /help for all local commands.",
			"↑↓ + tab picks from Commands.",
		}
	}
	switch cmd {
	case "provider":
		lines := []string{
			"Usage: /provider NAME [MODEL] [--persist]",
			"Tip: /provider (without args) opens Provider Picker.",
		}
		if providers := m.availableProviders(); len(providers) > 0 {
			lines = append(lines, "Known providers: "+strings.Join(providers, ", "))
			if len(args) > 0 && !containsStringFold(providers, args[0]) {
				lines = append(lines, "Unknown provider token; Enter opens picker filtered by your input.")
			}
		}
		return lines
	case "model":
		providerName := blankFallback(m.currentProvider(), "-")
		lines := []string{
			"Usage: /model NAME [--persist]",
			"Tip: /model (without args) opens Model Picker.",
			"Active provider: " + providerName,
		}
		models := m.availableModelsForProvider(m.currentProvider())
		if len(models) > 0 {
			lines = append(lines, "Known models: "+strings.Join(models, ", "))
		}
		if len(args) > 0 && len(models) > 0 && !containsStringFold(models, strings.Join(args, " ")) {
			lines = append(lines, "Unknown model is allowed; Enter can apply typed value in model picker.")
		}
		return lines
	case "context":
		return []string{
			"Usage: /context [full|why]",
			"/context -> compact summary",
			"/context why -> retrieval reasons only",
			"/context full -> full report with per-file evidence",
		}
	case "read":
		target := blankFallback(m.toolTargetFile(), "path/to/file.go")
		return []string{
			"Usage: /read PATH [LINE_START] [LINE_END]",
			"Paths with spaces: /read \"" + target + "\" 1 120",
		}
	case "run":
		lines := []string{"Usage: /run COMMAND [ARGS...]"}
		for i, suggestion := range m.runCommandSuggestions() {
			if i >= 2 {
				break
			}
			lines = append(lines, "Example: /run "+suggestionToRunCommandInput(suggestion))
		}
		return lines
	case "tool":
		lines := []string{
			"Usage: /tool NAME key=value ...",
			"Example: /tool read_file path=\"README.md\" line_start=1 line_end=80",
		}
		if len(args) == 0 {
			if tools := m.availableTools(); len(tools) > 0 {
				lines = append(lines, "Known tools: "+strings.Join(tools, ", "))
			}
			return lines
		}
		toolName := strings.TrimSpace(args[0])
		if containsStringFold(m.availableTools(), toolName) {
			keys := m.toolParamKeySuggestions(toolName, nil, "")
			if len(keys) > 0 {
				lines = append(lines, "Param keys: "+strings.Join(keys, " "))
			}
		}
		return lines
	case "reload":
		return []string{
			"Usage: /reload",
			"Reloads .env/config into current session without restarting TUI.",
		}
	default:
		if m.slashMenuActive() {
			return nil
		}
		suggestions := m.slashSuggestionsForToken(cmd, 3)
		if len(suggestions) == 0 {
			return []string{"Unknown command. Try /help."}
		}
		lines := []string{"Unknown command. Did you mean:"}
		for _, item := range suggestions {
			lines = append(lines, item.Template+" - "+item.Description)
		}
		return lines
	}
}

func formatSlashCommandInput(cmd string, args []string) string {
	cmd = strings.TrimSpace(strings.TrimPrefix(cmd, "/"))
	if cmd == "" {
		return "/"
	}
	if len(args) == 0 {
		return "/" + cmd
	}
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		if token := formatSlashArgToken(arg); token == "" {
			continue
		} else {
			parts = append(parts, token)
		}
	}
	if len(parts) == 0 {
		return "/" + cmd
	}
	return "/" + cmd + " " + strings.Join(parts, " ")
}

func formatSlashArgToken(arg string) string {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return ""
	}
	if strings.Contains(arg, "=") {
		key, value, _ := strings.Cut(arg, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" {
			return formatSlashKVToken(key, value)
		}
	}
	if strings.ContainsAny(arg, " \t\r\n\"") {
		return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
	}
	return arg
}

func formatSlashKVToken(key, value string) string {
	key = strings.TrimSpace(strings.TrimSuffix(key, "="))
	if key == "" {
		return ""
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return key + "="
	}
	if strings.ContainsAny(value, " \t\r\n\"") {
		return key + `="` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return key + "=" + value
}

func (m Model) autocompleteSlashCommand() (string, bool) {
	if !m.slashMenuActive() {
		return "", false
	}
	items := m.filteredSlashCommands()
	if len(items) == 0 {
		return "", false
	}
	idx := clampIndex(m.slashMenu.command, len(items))
	return items[idx].Template, true
}

func (m Model) expandSlashSelection(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "/") {
		return "", false
	}
	fields := strings.Fields(raw)
	if len(fields) > 1 {
		return "", false
	}
	token := strings.TrimPrefix(strings.ToLower(raw), "/")
	if isKnownChatCommandToken(token) {
		return "", false
	}
	items := m.filteredSlashCommands()
	if len(items) == 0 {
		return "", false
	}
	idx := clampIndex(m.slashMenu.command, len(items))
	return items[idx].Template, true
}

func (m Model) filteredSlashCommands() []slashCommandItem {
	query := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(m.chat.input)), "/")
	catalog := m.slashCommandCatalog()
	if query == "" {
		return catalog
	}
	out := make([]slashCommandItem, 0, len(catalog))
	for _, item := range catalog {
		cmd := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(item.Command)), "/")
		if strings.HasPrefix(cmd, query) {
			out = append(out, item)
		}
	}
	return out
}

func (m Model) slashSuggestionsForToken(token string, limit int) []slashCommandItem {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return nil
	}
	catalog := m.slashCommandCatalog()
	prefix := make([]slashCommandItem, 0, len(catalog))
	contains := make([]slashCommandItem, 0, len(catalog))
	for _, item := range catalog {
		name := strings.ToLower(strings.TrimSpace(item.Command))
		switch {
		case strings.HasPrefix(name, token):
			prefix = append(prefix, item)
		case strings.Contains(name, token):
			contains = append(contains, item)
		}
	}
	out := append(prefix, contains...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func suggestionToRunCommandInput(suggestion string) string {
	params, err := parseToolParamString(suggestion)
	if err != nil {
		return "go test ./..."
	}
	command := paramStr(params, "command")
	if command == "" {
		command = "go"
	}
	args := paramStr(params, "args")
	if args == "" {
		return command
	}
	return command + " " + args
}

func hasTrailingWhitespace(text string) bool {
	if text == "" {
		return false
	}
	last := text[len(text)-1]
	return last == ' ' || last == '\t' || last == '\n' || last == '\r'
}

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

func filterSuggestionsByToken(items []string, token string) []string {
	items = append([]string(nil), items...)
	if len(items) == 0 {
		return nil
	}
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return items
	}
	prefix := make([]string, 0, len(items))
	contains := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item)
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, token) {
			prefix = append(prefix, name)
			continue
		}
		if strings.Contains(lower, token) {
			contains = append(contains, name)
		}
	}
	return append(prefix, contains...)
}

func firstSuggestions(items []string, limit int) []string {
	if len(items) == 0 || limit <= 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]string(nil), items...)
	}
	return append([]string(nil), items[:limit]...)
}

// slashCommandCatalog assembles the list of slash commands shown in the TUI
// command menu. The canonical catalog comes from commands.DefaultRegistry()
// filtered to the TUI surface; per-command template overrides live here so
// the menu auto-fills context-aware defaults (e.g. current model, pinned
// file). TUI-only utilities that don't exist as CLI verbs — /ls, /grep, /run,
// /read, /diff, /patch, /apply, /undo, /reload, /providers, /models, /tools —
// are appended explicitly so the picker stays useful.
