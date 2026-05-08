package engine

// assistant_hints.go — parser for the structured tail an assistant
// turn now must end with. Two markers:
//
//   [next: ...]    — at least 2-3 next-action suggestions, one per
//                    line, that the TUI surfaces below the answer.
//   [cleanup: ...] — comma-separated list of message IDs the model
//                    judges no longer needed for ongoing work; the
//                    Conversation manager drops them so the rolling
//                    window stays bounded without us guessing.
//
// User-stated invariant (the load-bearing prompt):
//   "context icindeki her bir assistant mesajına ve user mesajına
//    unique id ver, llm de id atasın ve son dönüşte mutlaka bana en
//    az 2-3 tane sonraki işlem önerisi ile beraber context den
//    cıkarılabilir mesajların id lerini versin ve biz context den
//    temizleyelim o user ve assistant mesajlarını"
//
// Both markers are optional — a partial response (no markers, only
// markers, only one of them) parses cleanly. Unknown text outside
// the markers is preserved as the stripped answer.

import (
	"regexp"
	"strings"
)

// hintBlockPattern matches a `[name: …]` block that may span multiple
// lines (the next-actions list typically does). The match is lazy on
// content so two adjacent blocks do not collapse into one.
var hintBlockPattern = regexp.MustCompile(`(?ms)\[(cleanup|next|done)\s*:\s*(.*?)\]`)

// idTokenPattern matches the short opaque IDs assigned by
// conversation.AssignMessageID — `<role>-<6 hex>` with role one of
// u/a/s/t. Anchored loose so the parser tolerates whitespace and
// stray punctuation between IDs.
var idTokenPattern = regexp.MustCompile(`\b[uast]-[0-9a-f]{4,8}\b`)

// AssistantHints is the structured payload parsed from an assistant
// turn's tail markers. Keeps the parser callable from places that
// want all three signals (next-actions, cleanup IDs, done flag) at
// once without reaching into a 3-return signature.
type AssistantHints struct {
	CleanupIDs  []string
	NextActions []string
	// Done reports whether the assistant explicitly marked the task
	// complete via `[done: true]`. The auto-continue loop treats
	// absence of this marker (or `[done: false]`) as "more work to
	// do" and self-resumes with the first next-action as the next
	// user prompt. The user explicitly asked for this:
	// "kendine tekrar promptu kendi atmalı devam etmeli ne bileyim".
	Done    bool
	DoneSet bool // true when the model emitted `[done: ...]` at all
}

// parseAssistantHints scans an assistant answer for the [cleanup:],
// [next:], and [done:] tail markers, returns the parsed payloads,
// and yields the answer with all markers stripped. The order of
// markers in the answer is irrelevant; each is parsed independently.
// Unknown marker names are left intact (so a future `[plan: …]`
// doesn't silently vanish if added later).
func parseAssistantHints(answer string) (cleanupIDs, nextActions []string, stripped string) {
	hints := parseAssistantHintsFull(answer)
	return hints.CleanupIDs, hints.NextActions, stripped_(answer, hints)
}

// parseAssistantHintsFull is the structured variant — returns the
// full AssistantHints struct. Internal callers that need the Done
// flag use this; the legacy 3-return wrapper above stays for callers
// that only care about cleanup + next.
func parseAssistantHintsFull(answer string) AssistantHints {
	hints := AssistantHints{}
	if strings.TrimSpace(answer) == "" {
		return hints
	}
	idSet := map[string]struct{}{}
	actionSet := map[string]struct{}{}
	hintBlockPattern.ReplaceAllStringFunc(answer, func(block string) string {
		match := hintBlockPattern.FindStringSubmatch(block)
		if len(match) < 3 {
			return block
		}
		kind := strings.ToLower(strings.TrimSpace(match[1]))
		body := strings.TrimSpace(match[2])
		switch kind {
		case "cleanup":
			for _, id := range idTokenPattern.FindAllString(body, -1) {
				if _, ok := idSet[id]; ok {
					continue
				}
				idSet[id] = struct{}{}
				hints.CleanupIDs = append(hints.CleanupIDs, id)
			}
		case "next":
			for _, line := range strings.Split(body, "\n") {
				action := normalizeNextActionLine(line)
				if action == "" {
					continue
				}
				if _, ok := actionSet[action]; ok {
					continue
				}
				actionSet[action] = struct{}{}
				hints.NextActions = append(hints.NextActions, action)
			}
		case "done":
			hints.DoneSet = true
			low := strings.ToLower(strings.TrimSpace(body))
			hints.Done = low == "true" || low == "yes" || low == "1" || low == "complete" || low == "completed"
		}
		return ""
	})
	return hints
}

// stripped_ runs the regex strip step a second time so the legacy
// 3-return parseAssistantHints still yields the cleaned text. We
// could plumb the cleaned string through parseAssistantHintsFull but
// keeping the separate strip path lets the two callers stay simple.
func stripped_(answer string, _ AssistantHints) string {
	out := hintBlockPattern.ReplaceAllStringFunc(answer, func(block string) string {
		match := hintBlockPattern.FindStringSubmatch(block)
		if len(match) < 3 {
			return block
		}
		kind := strings.ToLower(strings.TrimSpace(match[1]))
		switch kind {
		case "cleanup", "next", "done":
			return ""
		default:
			return block
		}
	})
	return strings.TrimRight(out, " \t\n")
}

// normalizeNextActionLine peels list bullets, numeric prefixes, and
// surrounding whitespace from a single next-action entry. Empty or
// purely-decorative lines yield "" (caller filters those out).
func normalizeNextActionLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	// Common bullet prefixes: "- foo", "* foo", "• foo", "1. foo",
	// "1) foo". We strip them all so the action text reads cleanly
	// in the TUI without copy/paste artefacts.
	for _, prefix := range []string{"- ", "* ", "• ", "→ ", "› "} {
		if strings.HasPrefix(line, prefix) {
			line = strings.TrimSpace(line[len(prefix):])
			break
		}
	}
	if len(line) > 2 {
		// Numeric "1." or "1)" prefix.
		if isDigitRune(line[0]) {
			j := 1
			for j < len(line) && isDigitRune(line[j]) {
				j++
			}
			if j < len(line) && (line[j] == '.' || line[j] == ')') {
				line = strings.TrimSpace(line[j+1:])
			}
		}
	}
	return line
}

func isDigitRune(b byte) bool { return b >= '0' && b <= '9' }
