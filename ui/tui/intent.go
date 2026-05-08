package tui

// intent.go — Model-bound chat-input intent classifier: action-
// request detector, tool-use directive enforcer, tool-capable
// provider check, free-text→tool dispatcher (autoToolIntentFromQuestion),
// and detectReferencedFile heuristic.
//
// Lifted out of the 10K-line tui.go god file (REPORT.md C1) so the
// "what does the user actually want" surface is in one obvious
// place. Sibling: intent_extract.go keeps the pure heuristic
// extractors (extractRunIntentCommand, extractSearchIntentPattern,
// extractListIntent, extractBacktickBlock, splitExecutableAndArgs,
// hasReadIntentPrefix, extractReadLineRange).
//
// Bilingual on purpose: DFMC's user is Turkish, source is English,
// but the matchers accept Turkish verbs ("oku", "ara", "listele",
// "çalıştır", …) because that's how prompts actually arrive in this
// codebase.

import (
	"path/filepath"
	"regexp"
	"strings"
)

// parse*ChatArgs slash-command parsers live in intent_chat_args.go.

// looksLikeActionRequest returns true when the user's question contains a
// clear write/edit verb. Used as the gate for the offline-mode guardrail —
// we only warn when the user seems to expect file mutation, not on plain
// read/explain questions where offline heuristics are still useful.
func (m Model) looksLikeActionRequest(question string) bool {
	lower := strings.ToLower(strings.TrimSpace(question))
	if lower == "" {
		return false
	}
	// Presence of a [[file:...]] marker alongside a verb is the strongest
	// signal; bare verbs ("güncelle") without a file target are ambiguous
	// and better left to the LLM than pre-empted.
	hasFileMarker := strings.Contains(lower, "[[file:") || strings.Contains(lower, "@")
	verbs := []string{
		"güncelle", "guncelle", "yaz", "düzelt", "duzelt", "değiştir", "degistir",
		"ekle", "kaldır", "kaldir", "sil", "refactor", "modify",
		"update", "write", "edit", "fix", "change", "rename", "delete",
		"add ", "remove ", "replace",
	}
	for _, v := range verbs {
		if strings.Contains(lower, v) {
			return hasFileMarker || strings.Contains(lower, ".go") ||
				strings.Contains(lower, ".py") || strings.Contains(lower, ".md") ||
				strings.Contains(lower, ".ts") || strings.Contains(lower, ".js")
		}
	}
	return false
}

// enforceToolUseForActionRequests appends a terse, forceful directive to
// the question when the user clearly wants a mutation on a tool-capable
// provider. Returns the question untouched otherwise. The directive is
// appended (not prepended) so file markers + user context stay adjacent,
// and the tool-use sentence lands right before the assistant turn where
// attention is highest. Skipped when the question already mentions a
// tool or meta-tool name — we don't want to double up.
func (m Model) enforceToolUseForActionRequests(question string) string {
	if !m.looksLikeActionRequest(question) || !m.hasToolCapableProvider() {
		return question
	}
	lower := strings.ToLower(question)
	for _, existing := range []string{
		"tool_call", "tool_batch_call", "apply_patch", "edit_file", "write_file",
	} {
		if strings.Contains(lower, existing) {
			return question
		}
	}
	directive := "\n\n[DFMC directive] You MUST use tool calls to make the requested changes. " +
		"Route through tool_call with apply_patch (preferred), edit_file, or write_file as appropriate — " +
		"read the target first if you haven't, then emit the tool call. " +
		"Do NOT just describe the changes in prose; the user wants the files actually modified."
	return strings.TrimRight(question, "\n") + directive
}

// hasToolCapableProvider reports whether the active provider is a real
// LLM capable of issuing tool calls (so the agent loop can actually
// modify files). The offline analyzer and the placeholder are both
// read-only — everything else can route through the tool registry.
func (m Model) hasToolCapableProvider() bool {
	if m.eng == nil || m.eng.Tools == nil {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(m.status.Provider))
	if provider == "" || provider == "offline" || provider == "placeholder" {
		return false
	}
	return m.status.ProviderProfile.Configured
}

func (m Model) autoToolIntentFromQuestion(question string) (string, map[string]any, string, bool) {
	question = strings.TrimSpace(question)
	if question == "" || strings.HasPrefix(question, "/") {
		return "", nil, "", false
	}
	lower := strings.ToLower(question)

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
			return "run_command", params, "detected command execution intent", true
		}
	}

	if pattern, ok := extractSearchIntentPattern(question, lower); ok {
		params := map[string]any{
			"pattern":     strings.TrimSpace(pattern),
			"max_results": 80,
		}
		return "grep_codebase", params, "detected search intent", true
	}

	if path, recursive, maxEntries, ok := extractListIntent(question, lower); ok {
		params := map[string]any{
			"path":        blankFallback(strings.TrimSpace(path), "."),
			"max_entries": maxEntries,
		}
		if recursive {
			params["recursive"] = true
		}
		return "list_dir", params, "detected listing intent", true
	}

	if hasReadIntentPrefix(lower) {
		target := m.detectReferencedFile(question)
		if target == "" {
			target = strings.TrimSpace(m.toolTargetFile())
		}
		if target != "" {
			start, end := extractReadLineRange(question)
			params := map[string]any{
				"path":       target,
				"line_start": start,
				"line_end":   end,
			}
			return "read_file", params, "detected file read intent", true
		}
	}

	return "", nil, "", false
}

// hasReadIntentPrefix + extractRunIntentCommand +
// extractSearchIntentPattern + extractListIntent +
// extractBacktickBlock + splitExecutableAndArgs +
// extractReadLineRange live in intent_extract.go.

func (m Model) detectReferencedFile(question string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return ""
	}
	markerRe := regexp.MustCompile(`\[\[file:([^\]]+)\]\]`)
	if matches := markerRe.FindStringSubmatch(question); len(matches) == 2 {
		target := filepath.ToSlash(strings.TrimSpace(matches[1]))
		if target != "" && containsStringFold(m.filesView.entries, target) {
			return target
		}
	}
	candidates := strings.FieldsFunc(question, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ',' || r == ';' || r == '(' || r == ')' || r == '[' || r == ']'
	})
	for _, raw := range candidates {
		token := strings.TrimSpace(strings.Trim(raw, "\"'`"))
		token = strings.TrimPrefix(token, "@")
		token = strings.TrimSuffix(token, ".")
		token = strings.TrimSuffix(token, ":")
		token = filepath.ToSlash(token)
		if token == "" {
			continue
		}
		if containsStringFold(m.filesView.entries, token) {
			return token
		}
		if strings.Contains(token, "/") || strings.Contains(token, ".") {
			if m.projectHasFile(token) {
				return token
			}
		}
	}
	return ""
}

// extractReadLineRange lives in intent_extract.go.
