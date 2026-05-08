package tui

// intent_extract.go — pure heuristic extractors for the chat-input
// intent classifier. Sibling of intent.go which keeps the Model-bound
// surface (looksLikeActionRequest, enforceToolUseForActionRequests,
// hasToolCapableProvider, autoToolIntentFromQuestion,
// detectReferencedFile).
//
// Splitting the per-intent matchers out keeps intent.go scoped to
// "what does the user want at the Model level" while this file owns
// the bilingual prefix walks (English + Turkish), the read-range
// regex parser, the backtick-block extractor for `…`-quoted commands,
// and the "executable vs args" splitter that lets the run-command
// path build a clean tool-param map without each caller re-deriving
// the boundary.

import (
	"regexp"
	"strconv"
	"strings"
)

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
