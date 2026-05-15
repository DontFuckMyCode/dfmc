package tui

// slash_picker.go — slash-command menu, autocomplete, and input-token
// plumbing.
//
// The picker's static data half (slashCommandCatalog, slashTemplate
// Overrides) lives in slash_catalog.go, and the per-tool / per-/run
// argument suggestion feeders live in slash_arg_suggestions.go — this
// file is what sits between those two. Given the user's current input
// and cursor position it figures out whether the menu is active, which
// entries to show, what the next autocomplete should be, and how to
// format a selection back into the chat buffer. Every method lives on
// `Model`; the chat key handler calls these via buildChatSuggestion
// State/handleChatKey.
//
// Two related but distinct surfaces live here:
//
//   - Slash MENU       — what the menu shows when the user types "/"
//                        (slashMenuActive, filteredSlashCommands,
//                        autocompleteSlashCommand, expandSlashSelection,
//                        slashSuggestionsForToken)
//   - Argument picker  — what's offered after the command is typed
//                        (activeSlashArgSuggestions, autocompleteSlashArg,
//                        slashAssistHints, formatSlash*). The value-side
//                        feeders and the /tool param catalog live in
//                        slash_arg_suggestions.go.

import (
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
		// Tab-completion surface lists ALL known providers, configured
		// or not. The user typing /provider <name> is asking to switch
		// targets — the API-key filter that availableProviders() uses
		// for active routing should not gate the suggestion list, or
		// they cannot tab to a profile that's missing a key.
		providers := m.allKnownProviders()
		if len(providers) == 0 {
			providers = m.availableProviders()
		}
		if len(providers) == 0 {
			return nil
		}
		providers = prioritizeSuggestion(providers, m.currentProvider())
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
		if strings.EqualFold(providerName, m.currentProvider()) {
			models = prioritizeSuggestion(models, m.currentModel())
		} else {
			models = prioritizeSuggestion(models, m.defaultModelForProvider(providerName))
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
		models = prioritizeSuggestion(models, m.currentModel())
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

// prioritizeSuggestion lives in slash_picker_format.go.

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

// formatSlashCommandInput + formatSlashArgToken + formatSlashKVToken
// live in slash_picker_format.go.

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
	// If the bare token exactly matches a catalog entry's command name,
	// the user typed a complete command but pressed Enter without args.
	// Don't expand — let executeChatCommand handle the empty-args case.
	for _, item := range items {
		cmd := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(item.Command)), "/")
		if cmd == token {
			return "", false
		}
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

// suggestionToRunCommandInput + hasTrailingWhitespace +
// filterSuggestionsByToken + firstSuggestions live in
// slash_picker_format.go.

// slashCommandCatalog assembles the list of slash commands shown in the TUI
// command menu. The canonical catalog comes from commands.DefaultRegistry()
// filtered to the TUI surface; per-command template overrides live here so
// the menu auto-fills context-aware defaults (e.g. current model, pinned
// file). TUI-only utilities that don't exist as CLI verbs — /ls, /grep, /run,
// /read, /diff, /patch, /apply, /undo, /reload, /providers, /models, /tools —
// are appended explicitly so the picker stays useful.
