package tui

// slash_picker_format.go — pure helpers shared between the slash
// menu and the argument picker: token formatters that re-quote
// args containing whitespace, the kv-token shape used for tool
// param=value suggestions, the suggestion-list manipulators
// (prioritizeSuggestion / filterSuggestionsByToken /
// firstSuggestions / hasTrailingWhitespace), and the
// suggestionToRunCommandInput converter that turns a /run
// preset's tool-param string back into the literal command line.
// Sibling of slash_picker.go which keeps the Model-bound dispatch
// surface (slashMenuActive, activeSlashArgSuggestions,
// autocompleteSlashArg, autocompleteSlashCommand,
// expandSlashSelection, filteredSlashCommands).

import (
	"strings"
)

func prioritizeSuggestion(values []string, preferred string) []string {
	preferred = strings.TrimSpace(preferred)
	if preferred == "" || len(values) == 0 {
		return values
	}
	idx := -1
	for i, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), preferred) {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return values
	}
	out := make([]string, 0, len(values))
	out = append(out, values[idx])
	out = append(out, values[:idx]...)
	out = append(out, values[idx+1:]...)
	return out
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
