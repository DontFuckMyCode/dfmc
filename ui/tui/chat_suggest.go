// Chat composer suggestion engine: builds the per-keystroke
// chatSuggestionState (slash menu, slash-arg suggestions, @mention
// completions, quick-action hints) and translates detected intents
// (read, list, grep, run) into concrete pre-filled slash commands.
// Extracted from tui.go.

package tui

import (
	"path/filepath"
	"regexp"
	"strings"
)

func (m Model) buildChatSuggestionState() chatSuggestionState {
	state := chatSuggestionState{
		slashMenuActive: m.slashMenuActive(),
	}
	if state.slashMenuActive {
		state.slashCommands = m.filteredSlashCommands()
	} else {
		state.slashArgSuggestions = m.activeSlashArgSuggestions()
	}
	if query, rangeSuffix, ok := activeMentionQuery(m.chat.input); ok {
		state.mentionActive = true
		state.mentionQuery = query
		state.mentionRange = rangeSuffix
		state.mentionSuggestions = m.mentionSuggestions(query, 8)
	}
	if !state.slashMenuActive && !state.mentionActive && !m.commandPicker.active && !m.chat.sending {
		state.quickActions = m.quickActionsForCurrentInput()
	}
	return state
}

func autocompleteMentionSelectionFromSuggestions(input string, mentionIndex int, suggestions []mentionRow) (string, bool) {
	if len(suggestions) == 0 {
		return "", false
	}
	idx := clampIndex(mentionIndex, len(suggestions))
	_, rangeSuffix, _ := activeMentionQuery(input)
	return replaceActiveMention(input, suggestions[idx].Path, rangeSuffix), true
}

func (m Model) quickActionsForCurrentInput() []quickActionSuggestion {
	raw := strings.TrimSpace(m.chat.input)
	if raw == "" || strings.HasPrefix(raw, "/") {
		return nil
	}
	question := m.chatPrompt()
	if strings.TrimSpace(question) == "" {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(question))
	out := make([]quickActionSuggestion, 0, 4)
	seen := map[string]struct{}{}
	add := func(name string, params map[string]any, reason string) {
		prepared := quickActionPreparedInput(name, params)
		if prepared == "" {
			return
		}
		key := strings.ToLower(strings.TrimSpace(prepared))
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, quickActionSuggestion{
			Tool:          name,
			Params:        params,
			Reason:        reason,
			PreparedInput: prepared,
		})
	}
	if name, params, reason, ok := m.autoToolIntentFromQuestion(question); ok {
		add(name, params, reason)
	}
	if target := strings.TrimSpace(m.detectReferencedFile(question)); target != "" {
		start, end := extractReadLineRange(question)
		add("read_file", map[string]any{
			"path":       target,
			"line_start": start,
			"line_end":   end,
		}, "read referenced file")
		base := strings.TrimSpace(strings.TrimSuffix(filepath.Base(target), filepath.Ext(target)))
		if base != "" {
			add("grep_codebase", map[string]any{
				"pattern":     regexp.QuoteMeta(base),
				"max_results": 80,
			}, "search symbols related to referenced file")
		}
	}
	if pattern, ok := extractSearchIntentPattern(question, lower); ok {
		add("grep_codebase", map[string]any{
			"pattern":     strings.TrimSpace(pattern),
			"max_results": 80,
		}, "search codebase")
	}
	if path, recursive, maxEntries, ok := extractListIntent(question, lower); ok {
		params := map[string]any{
			"path":        blankFallback(strings.TrimSpace(path), "."),
			"max_entries": maxEntries,
		}
		if recursive {
			params["recursive"] = true
		}
		add("list_dir", params, "list matching directory scope")
	}
	if cmd, ok := extractRunIntentCommand(question, lower); ok {
		command, args := splitExecutableAndArgs(cmd)
		if command != "" {
			params := map[string]any{
				"command": command,
				"dir":     ".",
			}
			if strings.TrimSpace(args) != "" {
				params["args"] = strings.TrimSpace(args)
			}
			add("run_command", params, "run detected command")
		}
	}
	return out
}

func quickActionPreparedInput(name string, params map[string]any) string {
	name = strings.TrimSpace(strings.ToLower(name))
	switch name {
	case "read_file":
		path := paramStr(params, "path")
		if path == "" {
			return ""
		}
		parts := []string{"/read", formatSlashArgToken(path)}
		if start := paramStr(params, "line_start"); start != "" {
			parts = append(parts, start)
		}
		if end := paramStr(params, "line_end"); end != "" {
			parts = append(parts, end)
		}
		return strings.Join(parts, " ")
	case "list_dir":
		path := paramStr(params, "path")
		if path == "" {
			path = "."
		}
		parts := []string{"/ls", formatSlashArgToken(path)}
		if recursive, ok := params["recursive"].(bool); ok && recursive {
			parts = append(parts, "--recursive")
		}
		if maxEntries := paramStr(params, "max_entries"); maxEntries != "" {
			parts = append(parts, "--max", maxEntries)
		}
		return strings.Join(parts, " ")
	case "grep_codebase":
		pattern := paramStr(params, "pattern")
		if pattern == "" {
			return ""
		}
		return "/grep " + formatSlashArgToken(pattern)
	case "run_command":
		command := paramStr(params, "command")
		if command == "" {
			return ""
		}
		args := paramStr(params, "args")
		if args == "" {
			return "/run " + command
		}
		// H3: tokens with whitespace must be quoted so `/run cmd "arg with spaces"`
		// survives the slash-handler's whitespace tokenizer. Without this,
		// `git commit -m "fix bug"` gets split on every space and the model's
		// suggested command becomes nonsense.
		return "/run " + command + " " + formatRunArgList(args)
	default:
		return ""
	}
}
