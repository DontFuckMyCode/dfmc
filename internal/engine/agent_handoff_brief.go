package engine

// agent_handoff_brief.go — terse, LLM-free brief generator for the
// auto-handoff path. Sibling of agent_handoff.go which decides WHEN
// to rotate the conversation; this file decides WHAT to put in the
// new conversation's seed message. Output is deterministic — same
// inputs always produce the same brief — so resume behaviour is
// reproducible.
//
// The brief surfaces five things, in order: original request, recent
// follow-up turns, per-tool activity counts, open todos, and the last
// assistant answer. It also captures a "previously read" path list so
// the resumed session doesn't cold-start re-reading files the prior
// session already explored. Bounded by maxTokens (~4 chars/token);
// overflow gets a "[truncated]" tail.

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// buildHandoffBrief renders a terse, LLM-free summary of an outgoing
// conversation so the new session has just enough context to keep going
// without paying for the whole transcript. Ordering:
//  1. Original user intent (first user turn, truncated).
//  2. Subsequent user asks (one-line each, up to a few).
//  3. Tool activity summary — count per tool name, ok/fail split.
//  4. Open todos (pending + in_progress from todo_write), if any.
//  5. Last assistant answer (truncated).
//
// Bounded by maxTokens (~4 chars per token). Deterministic: identical inputs
// produce identical output.
func buildHandoffBrief(convID string, history []types.Message, openTodos []tools.TodoItem, maxTokens int) string {
	if len(history) == 0 {
		return ""
	}
	var userTurns []string
	var lastAssistant string
	toolCounts := map[string]int{}
	toolSuccess := map[string]int{}
	toolFailure := map[string]int{}
	// readPaths preserves insertion order so the brief lists files in
	// the sequence they were first touched. Map deduplicates so a file
	// read three times still appears once. The captured set is used to
	// hint the resumed session against re-reading the same files; it
	// replaces the otherwise inevitable cold-start re-discovery pass
	// that resumed loops trip into right after compaction.
	readPaths := []string{}
	seenPath := map[string]struct{}{}

	for _, msg := range history {
		switch msg.Role {
		case types.RoleUser:
			text := strings.TrimSpace(msg.Content)
			if text != "" {
				userTurns = append(userTurns, truncateRunes(text, 160))
			}
		case types.RoleAssistant:
			if text := strings.TrimSpace(msg.Content); text != "" {
				lastAssistant = text
			}
			for _, call := range msg.ToolCalls {
				name := strings.TrimSpace(call.Name)
				if name == "" {
					continue
				}
				toolCounts[name]++
				if p := readPathFromCall(call); p != "" {
					if _, dup := seenPath[p]; !dup {
						seenPath[p] = struct{}{}
						readPaths = append(readPaths, p)
					}
				}
			}
			for _, r := range msg.Results {
				name := strings.TrimSpace(r.Name)
				if name == "" {
					continue
				}
				if r.Success {
					toolSuccess[name]++
				} else {
					toolFailure[name]++
				}
			}
		}
	}

	lines := []string{fmt.Sprintf("[handoff brief · prior session %s]", convID)}
	if len(userTurns) > 0 {
		lines = append(lines, "original request: "+userTurns[0])
		if len(userTurns) > 1 {
			tail := userTurns[1:]
			if len(tail) > 3 {
				tail = tail[len(tail)-3:]
			}
			for _, t := range tail {
				lines = append(lines, "follow-up: "+t)
			}
		}
	}
	if len(toolCounts) > 0 {
		parts := make([]string, 0, len(toolCounts))
		for _, name := range sortedStringKeys(toolCounts) {
			count := toolCounts[name]
			ok := toolSuccess[name]
			fail := toolFailure[name]
			parts = append(parts, fmt.Sprintf("%s×%d ok=%d fail=%d", name, count, ok, fail))
		}
		lines = append(lines, "tool activity: "+strings.Join(parts, "; "))
	}
	if openLines := renderOpenTodos(openTodos); len(openLines) > 0 {
		lines = append(lines, openLines...)
	}
	if len(readPaths) > 0 {
		// Cap the listed paths so a runaway scan doesn't dominate the brief.
		// Eight is enough for the model to anchor on "I already explored
		// these areas"; the rest collapses into a "+N more" summary.
		const maxReadPaths = 8
		shown := readPaths
		more := 0
		if len(shown) > maxReadPaths {
			shown = shown[:maxReadPaths]
			more = len(readPaths) - maxReadPaths
		}
		hint := "previously read: " + strings.Join(shown, ", ")
		if more > 0 {
			hint += fmt.Sprintf(" (+%d more)", more)
		}
		lines = append(lines, hint)
		lines = append(lines, "(skip re-reading these unless you need a fresher view; their prior content shaped the work above)")
	}
	if lastAssistant != "" {
		lines = append(lines, "last answer: "+truncateRunes(lastAssistant, 320))
	}

	body := strings.Join(lines, "\n")
	if maxTokens > 0 {
		budgetChars := maxTokens * 4
		if budgetChars > 0 && len(body) > budgetChars {
			body = body[:budgetChars] + "\n...[truncated]"
		}
	}
	return body
}

// readPathFromCall extracts the file path a read-class tool call
// targeted, or "" when the call isn't a file read or the path can't be
// inferred from the params map. Recognises both bare backend calls
// (read_file/list_dir) and the meta-tool envelope (tool_call wrapping
// a backend name). Used by buildHandoffBrief to seed the resumed
// session's "already read" hint so it doesn't re-discover the same
// files cold.
func readPathFromCall(call types.ToolCallRecord) string {
	name := strings.TrimSpace(call.Name)
	params := call.Params
	if name == "tool_call" {
		// Unwrap a single layer: tool_call({"name":"<backend>","args":{...}})
		if inner, ok := params["name"].(string); ok {
			name = strings.TrimSpace(inner)
		}
		if argMap, ok := params["args"].(map[string]any); ok {
			params = argMap
		}
	}
	switch name {
	case "read_file", "list_dir":
		if params == nil {
			return ""
		}
		if p, ok := params["path"].(string); ok {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

// renderOpenTodos emits brief lines for todo_write items still in-flight.
// Completed items are dropped — the handoff brief is about "what's left",
// not a status report. Caps at 8 lines to keep the brief bounded; overflow
// is represented as "+N more".
func renderOpenTodos(items []tools.TodoItem) []string {
	if len(items) == 0 {
		return nil
	}
	const maxLines = 8
	var pending, active []tools.TodoItem
	for _, it := range items {
		switch strings.ToLower(strings.TrimSpace(it.Status)) {
		case "completed", "done":
			continue
		case "in_progress", "active", "doing":
			active = append(active, it)
		default:
			pending = append(pending, it)
		}
	}
	if len(pending)+len(active) == 0 {
		return nil
	}
	header := fmt.Sprintf("open todos: %d pending, %d in_progress", len(pending), len(active))
	out := []string{header}
	ordered := append(active, pending...)
	shown := 0
	for _, it := range ordered {
		if shown >= maxLines {
			out = append(out, fmt.Sprintf("  (+%d more)", len(ordered)-shown))
			break
		}
		mark := "[ ]"
		if strings.EqualFold(strings.TrimSpace(it.Status), "in_progress") {
			mark = "[~]"
		}
		out = append(out, fmt.Sprintf("  %s %s", mark, truncateRunes(strings.TrimSpace(it.Content), 140)))
		shown++
	}
	return out
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}

func sortedStringKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// tiny inlined sort — avoids pulling in sort for one caller in hot path.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
