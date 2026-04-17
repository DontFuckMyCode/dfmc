package tui

// intent.go — chat-input parsers and intent-extraction heuristics.
//
// Lifted out of the 10K-line tui.go god file (REPORT.md C1) so the
// "what does the user actually want" surface is in one obvious
// place. Two related groups live here:
//
//   - parse*ChatArgs       — tiny argument parsers used by the slash
//                            command dispatcher to turn a /ls,
//                            /read, /grep, /run line into a tool
//                            params map.
//   - extract* / autoTool* — heuristic matchers that classify a
//                            free-text user prompt as a tool intent
//                            (read_file, grep_codebase, list_dir,
//                            run_command) without an explicit slash.
//                            Drives the "quick action" suggestions
//                            that surface above the composer.
//
// Bilingual on purpose: DFMC's user is Turkish, source is English,
// but the matchers accept Turkish verbs ("oku", "ara", "listele",
// "çalıştır", …) because that's how prompts actually arrive in this
// codebase.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

func parseListDirChatArgs(args []string) (map[string]any, error) {
	params := map[string]any{
		"path":        ".",
		"max_entries": 120,
	}
	pathSet := false
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		switch {
		case arg == "-r" || arg == "--recursive":
			params["recursive"] = true
		case arg == "--max":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("missing value for --max")
			}
			n, err := strconv.Atoi(strings.TrimSpace(args[i+1]))
			if err != nil {
				return nil, fmt.Errorf("invalid --max value")
			}
			params["max_entries"] = n
			i++
		case strings.HasPrefix(strings.ToLower(arg), "--max="):
			raw := strings.TrimSpace(strings.SplitN(arg, "=", 2)[1])
			n, err := strconv.Atoi(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid --max value")
			}
			params["max_entries"] = n
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("unknown flag")
		default:
			if !pathSet {
				params["path"] = arg
				pathSet = true
			}
		}
	}
	return params, nil
}

func parseReadFileChatArgs(args []string) (map[string]any, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return nil, fmt.Errorf("path required")
	}
	params := map[string]any{
		"path":       strings.TrimSpace(args[0]),
		"line_start": 1,
		"line_end":   200,
	}
	if len(args) >= 2 {
		start, err := strconv.Atoi(strings.TrimSpace(args[1]))
		if err != nil {
			return nil, fmt.Errorf("invalid line_start")
		}
		params["line_start"] = start
		if len(args) >= 3 {
			end, err := strconv.Atoi(strings.TrimSpace(args[2]))
			if err != nil {
				return nil, fmt.Errorf("invalid line_end")
			}
			params["line_end"] = end
		} else {
			params["line_end"] = start + 199
		}
	}
	return params, nil
}

func parseGrepChatArgs(args []string) (map[string]any, error) {
	pattern := strings.TrimSpace(strings.Join(args, " "))
	if pattern == "" {
		return nil, fmt.Errorf("pattern required")
	}
	return map[string]any{
		"pattern":     pattern,
		"max_results": 80,
	}, nil
}

func parseRunCommandChatArgs(args []string) (map[string]any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("command required")
	}
	command := strings.TrimSpace(args[0])
	if command == "" {
		return nil, fmt.Errorf("command required")
	}
	params := map[string]any{
		"command": command,
		"dir":     ".",
	}
	if len(args) > 1 {
		rest := strings.TrimSpace(strings.Join(args[1:], " "))
		if rest != "" {
			params["args"] = rest
		}
	}
	return params, nil
}

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

func hasReadIntentPrefix(lower string) bool {
	for _, prefix := range []string{"read ", "oku ", "incele ", "goster ", "göster ", "ac ", "aç "} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func extractRunIntentCommand(question, lower string) (string, bool) {
	for _, prefix := range []string{"run ", "calistir ", "çalıştır ", "komut calistir ", "komut çalıştır "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(question[len(prefix):]), true
		}
	}
	if strings.HasPrefix(lower, "run:") {
		return strings.TrimSpace(question[len("run:"):]), true
	}
	backtick := extractBacktickBlock(question)
	if backtick != "" && (strings.HasPrefix(lower, "run ") || strings.HasPrefix(lower, "calistir ") || strings.HasPrefix(lower, "çalıştır ")) {
		return backtick, true
	}
	return "", false
}

func extractSearchIntentPattern(question, lower string) (string, bool) {
	for _, prefix := range []string{"grep ", "ara ", "search "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(question[len(prefix):]), strings.TrimSpace(question[len(prefix):]) != ""
		}
	}
	return "", false
}

func extractListIntent(question, lower string) (string, bool, int, bool) {
	maxEntries := 120
	if strings.HasPrefix(lower, "listele") {
		tail := strings.TrimSpace(question[len("listele"):])
		tailLower := strings.ToLower(tail)
		recursive := strings.Contains(tailLower, "recursive") || strings.Contains(tailLower, "rekursif")
		path := tail
		if recursive {
			reRecursive := regexp.MustCompile(`(?i)\b(recursive|rekursif)\b`)
			path = reRecursive.ReplaceAllString(path, "")
		}
		path = strings.TrimSpace(path)
		if path == "" {
			path = "."
		}
		return path, recursive, maxEntries, true
	}
	if strings.HasPrefix(lower, "list") {
		tail := strings.TrimSpace(question[len("list"):])
		path := strings.TrimSpace(strings.TrimPrefix(tail, "files"))
		path = strings.TrimSpace(strings.TrimPrefix(path, "dir"))
		return blankFallback(path, "."), false, maxEntries, true
	}
	return "", false, 0, false
}

func extractBacktickBlock(text string) string {
	start := strings.Index(text, "`")
	if start < 0 {
		return ""
	}
	rest := text[start+1:]
	end := strings.Index(rest, "`")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func splitExecutableAndArgs(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if strings.HasPrefix(raw, "\"") {
		end := strings.Index(raw[1:], "\"")
		if end >= 0 {
			command := strings.TrimSpace(raw[1 : end+1])
			args := strings.TrimSpace(raw[end+2:])
			return command, args
		}
	}
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return "", ""
	}
	command := parts[0]
	args := ""
	if len(parts) > 1 {
		args = strings.Join(parts[1:], " ")
	}
	return command, args
}

func (m Model) detectReferencedFile(question string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return ""
	}
	markerRe := regexp.MustCompile(`\[\[file:([^\]]+)\]\]`)
	if matches := markerRe.FindStringSubmatch(question); len(matches) == 2 {
		target := filepath.ToSlash(strings.TrimSpace(matches[1]))
		if target != "" && containsStringFold(m.files, target) {
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
		if containsStringFold(m.files, token) {
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

func extractReadLineRange(question string) (int, int) {
	lower := strings.ToLower(strings.TrimSpace(question))
	if !strings.Contains(lower, "line") && !strings.Contains(lower, "satir") && !strings.Contains(lower, "satır") {
		return 1, 200
	}
	re := regexp.MustCompile(`\b(\d{1,6})\b`)
	matches := re.FindAllStringSubmatch(question, 3)
	if len(matches) == 0 {
		return 1, 200
	}
	start, err := strconv.Atoi(matches[0][1])
	if err != nil || start <= 0 {
		start = 1
	}
	end := start + 199
	if len(matches) >= 2 {
		if parsed, err := strconv.Atoi(matches[1][1]); err == nil && parsed >= start {
			end = parsed
		}
	}
	return start, end
}
