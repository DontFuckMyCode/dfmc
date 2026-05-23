package engine

// engine_ask_history_tail.go — text-only summary of an assistant
// turn's tool work, appended when sending that turn back to the
// model in a later round so the model sees its own tool history
// instead of starting blind. Costs ~30-50 tokens per turn vs the
// kilobytes a real tool result blob would. Companion siblings:
//
//   - engine_ask_history.go         trim-window machinery
//                                   (publishHistoryTrimmedEvent +
//                                   conversationHistoryBudget /
//                                   MaxMessages + trimmedConversation
//                                   Messages + historyBudgetForRequest +
//                                   trimToTokenBudget)
//   - engine_ask_history_summary.go scaleSummaryCap +
//                                   buildHistorySummary +
//                                   latestOmittedByRole +
//                                   recentUserQuestions +
//                                   topTermsFromMessages +
//                                   tokenizeForSummary +
//                                   topFileMentions

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// renderHistoricalToolTail produces a compact, text-only summary of
// the tools an assistant message invoked. The output is appended to
// the assistant's prose when sending the message back to the model
// in a follow-up turn, so the model sees its own prior tool work
// instead of starting blind.
//
// Format example:
//
//	[prior tools (3): read_file ui/tui/tui.go (lines=240,bytes=3.2k) ·
//	 edit_file ui/tui/tui.go ok · run_command go test ./... ok (4.1s)]
//
// Caps: at most maxHistoricalToolEntries entries listed; per-entry
// hint is trimmed to ~80 chars. Failed calls show "✗" and a short
// reason. Tool params are NOT included verbatim (would blow budget);
// instead a couple of high-signal ones (path, file_path, command,
// pattern) are surfaced when present.
func renderHistoricalToolTail(msg types.Message) string {
	if len(msg.ToolCalls) == 0 && len(msg.Results) == 0 {
		return ""
	}
	const maxEntries = 8
	calls := msg.ToolCalls
	results := msg.Results
	count := len(calls)
	if len(results) > count {
		count = len(results)
	}
	if count == 0 {
		return ""
	}
	// Deduplicate by (tool_name, target_hint) key — when the model
	// calls read_file on the same file twice in one round (different
	// line ranges, or a retry), only the latest result is kept. The
	// dedup key is "name+hint" (e.g. "read_file config.go"). Saves
	// ~10-20 tokens per repeated call with no signal loss since the
	// model already knows it read that file.
	seen := make(map[string]int, count) // key → index in entries
	entries := make([]string, 0, count)
	deduped := 0
	for i := 0; i < count && len(entries)+deduped < maxEntries+deduped; i++ {
		var name, hint, status string
		if i < len(calls) {
			name = strings.TrimSpace(calls[i].Name)
			hint = compactToolParamHint(calls[i].Params)
		}
		if i < len(results) {
			if name == "" {
				name = strings.TrimSpace(results[i].Name)
			}
			cleanedOutput := compressToolResult(results[i].Output)
			if results[i].Success {
				status = compactToolResultHint(cleanedOutput)
			} else {
				status = "✗ " + compactToolResultHint(cleanedOutput)
			}
		}
		if name == "" {
			continue
		}
		entry := name
		if hint != "" {
			entry += " " + hint
		}
		if status != "" {
			entry += " → " + status
		}
		// Dedup key: tool name + target hint (the identifying param).
		dedupKey := name
		if hint != "" {
			dedupKey = name + " " + hint
		}
		if prevIdx, exists := seen[dedupKey]; exists {
			// Replace previous entry with latest result — keeps the
			// freshest status (e.g. first read failed, retry ok).
			entries[prevIdx] = entry
			deduped++
			continue
		}
		seen[dedupKey] = len(entries)
		entries = append(entries, entry)
	}
	if len(entries) == 0 {
		return ""
	}
	uniqueCount := len(entries)
	suffix := ""
	// Show how many raw calls are NOT individually listed — includes
	// both deduped (same target) and overflow (beyond display cap).
	unlisted := count - uniqueCount
	if unlisted > 0 {
		suffix = fmt.Sprintf(" (+%d more)", unlisted)
	}
	return fmt.Sprintf("[prior tools (%d): %s%s]", uniqueCount, strings.Join(entries, " · "), suffix)
}

// compactToolParamHint pulls the most-recognisable identifier from a
// tool's params blob — typically a path, command, or pattern — so the
// historical tail names what the tool actually touched without
// embedding the full params object.
func compactToolParamHint(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	for _, key := range []string{"file_path", "path", "filename", "target", "command", "pattern", "query", "url", "ref", "branch"} {
		if raw, ok := params[key]; ok {
			s := strings.TrimSpace(fmt.Sprintf("%v", raw))
			if s == "" {
				continue
			}
			if len(s) > 60 {
				s = s[:57] + "..."
			}
			return s
		}
	}
	return ""
}

// compactToolResultHint trims a tool's output to a single short line
// suitable for inclusion in the historical tool tail. Multi-line
// outputs collapse to their first non-empty line; long lines are
// truncated; an empty result becomes "ok".
func compactToolResultHint(out string) string {
	out = strings.TrimSpace(out)
	if out == "" {
		return "ok"
	}
	if idx := strings.IndexByte(out, '\n'); idx >= 0 {
		out = strings.TrimSpace(out[:idx])
	}
	if out == "" {
		return "ok"
	}
	if len(out) > 80 {
		out = out[:77] + "..."
	}
	return out
}
