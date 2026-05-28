package engine

// agent_compact_summary.go — terse offline summary text builders for
// the rounds the offline auto-compactor drops. Companion siblings:
//
//   - agent_compact.go        lifecycle config + maybe/proactive/force
//                             entry points + the shared collapse
//                             routine + estimateRequestTokens
//   - agent_compact_rounds.go toolRound type + splitNativeLoopRounds +
//                             findNativeLoopPrefixEnd +
//                             patchUnresolvedToolUses
//
// Each round emits its own multi-line block that PRESERVES enough
// signal for the model to keep reasoning about earlier work after
// compaction:
//
//   - the model's own narration line (head.Content) when present
//   - one indented line per tool_call: "↳ <tool> <target> → <result-head>"
//   - failures get the error tail trimmed for the same reason
//
// Result excerpts cap at ~120 chars per call so a 12-call round still
// fits in a few hundred tokens.

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// summariseCollapsedRounds builds a terse textual summary of the rounds being
// dropped. Offline only: no LLM call. Each round contributes one line listing
// the tool names invoked and a short result-success/error tag.
func summariseCollapsedRounds(rounds []toolRound, maxTokens int) string {
	if len(rounds) == 0 {
		return ""
	}
	lines := make([]string, 0, len(rounds))
	for i, r := range rounds {
		if line := summariseSingleRound(i+1, r); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	body := strings.Join(lines, "\n")
	budget := maxTokens * 4 // ~4 chars per token rough guide
	if budget > 0 && len(body) > budget {
		body = body[:budget] + "\n...[truncated]"
	}
	return body
}

// summariseSingleRound collapses one round (assistant turn + its
// tool_results) into a multi-line block. Pre-fix the summary was the
// bare shape "round 5 · tools=read_file · ok=2 fail=0" — the actual
// file contents the model had read, the commands it ran, the errors
// it saw were all destroyed. After 5+ rounds the model knew it "did
// something" but not what, so it re-discovered the same files /
// re-ran the same commands and burned budget rediscovering its own
// past. The user noticed: budget caps don't matter if tool results
// don't keep doing work in the model's reasoning context.
func summariseSingleRound(index int, round toolRound) string {
	if len(round.Messages) == 0 {
		return ""
	}
	head := round.Messages[0]
	if head.Role != types.RoleAssistant {
		return ""
	}

	// Pair each tool_call with its tool_result by ToolCallID so we can
	// emit the target+excerpt together. Map keyed by ID; calls without
	// a matching result (race, missing pair) still surface as "(no
	// result)" so the round count stays accurate.
	resultByID := make(map[string]provider.Message, len(round.Messages)-1)
	for _, m := range round.Messages[1:] {
		if m.ToolCallID != "" {
			resultByID[m.ToolCallID] = m
		}
	}

	successes := 0
	failures := 0
	for _, m := range round.Messages[1:] {
		if m.ToolError {
			failures++
		} else {
			successes++
		}
	}

	header := fmt.Sprintf("round %d", index)
	if len(head.ToolCalls) > 0 {
		header += fmt.Sprintf(" · %d call(s) · ok=%d fail=%d", len(head.ToolCalls), successes, failures)
	}
	text := strings.TrimSpace(head.Content)
	if runes := []rune(text); len(runes) > 120 {
		text = string(runes[:120]) + "..."
	}
	if text != "" {
		header += " · note=" + text
	}

	out := []string{"- " + header}
	for _, call := range head.ToolCalls {
		line := summariseSingleToolCall(call, resultByID[call.ID])
		if line != "" {
			out = append(out, "  "+line)
		}
	}
	return strings.Join(out, "\n")
}

// summariseSingleToolCall renders one indented line per tool_call
// inside a collapsed round. Format:
//
//	↳ read_file path=foo.go (lines 1-80) → "package config\n..."
//	↳ run_command go build ./... → exit 1: ./foo.go:12: undefined
//	↳ grep_codebase pattern=loadDotEnv → 3 hits across 2 files
//
// Keeps enough signal that the model can recall "I read foo.go and
// saw a Config struct" even after the round is collapsed. Result
// excerpts trimmed to ~120 chars; errors get the first error line.
func summariseSingleToolCall(call provider.ToolCall, result provider.Message) string {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return ""
	}
	target := summariseToolCallTarget(call)
	body := "↳ " + name
	if target != "" {
		body += " " + target
	}
	excerpt := summariseToolResultExcerpt(result)
	if excerpt != "" {
		body += " → " + excerpt
	}
	return body
}

// summariseToolCallTarget pulls the most identifying input args out
// of a ToolCall — path / pattern / command / dir — so the collapsed
// round line names the file, command, or query the model worked with.
// Mirrors the same priority order as the live TUI batch-inner preview
// (tools.previewBatchTarget) so the user sees consistent identifiers
// across "live chip" and "collapsed history" surfaces.
func summariseToolCallTarget(call provider.ToolCall) string {
	input := call.Input
	// Meta-tool wrappers (tool_call, tool_batch_call) carry the real
	// target one level down inside `args`. Unwrap so the summary still
	// names the underlying file/command rather than a meaningless "tool_call".
	if name, ok := input["name"].(string); ok && strings.TrimSpace(name) != "" {
		if inner, ok := input["args"].(map[string]any); ok {
			input = inner
			// Rename the line to point at the real backend tool, not
			// the wrapper.
			_ = name // keep go vet happy; we intentionally don't override `name` here
		}
	}
	for _, key := range []string{"path", "pattern", "query", "command", "dir", "url"} {
		if raw, ok := input[key]; ok {
			value := strings.TrimSpace(fmt.Sprint(raw))
			if value == "" {
				continue
			}
			if key == "command" {
				if rest := summariseAnyArgsList(input["args"]); rest != "" {
					value += " " + rest
				}
			}
			if key == "path" {
				if start, ok := pickInt(input["line_start"]); ok {
					if end, ok := pickInt(input["line_end"]); ok && end > 0 {
						value += fmt.Sprintf(" (lines %d-%d)", start, end)
					}
				}
			}
			if len(value) > 80 {
				value = value[:77] + "..."
			}
			return key + "=" + value
		}
	}
	return ""
}

// summariseToolResultExcerpt returns a one-line snippet of the
// tool_result content so the model can glance at "what came back"
// after the round was collapsed. Failures get the first error line
// (or the wrapped exit message) which is usually the actionable bit;
// successes get the first non-empty line of the output, capped to
// ~120 chars. Empty when the result is missing — caller skips the
// "→" so the line stays clean.
func summariseToolResultExcerpt(result provider.Message) string {
	body := strings.TrimSpace(result.Content)
	if body == "" {
		return ""
	}
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if result.ToolError && strings.HasPrefix(line, "ERROR:") {
			// "ERROR: command exited with code 1" → strip the prefix
			// so the excerpt reads naturally with the failure marker
			// downstream.
			line = strings.TrimSpace(strings.TrimPrefix(line, "ERROR:"))
		}
		if len(line) > 120 {
			line = line[:117] + "..."
		}
		if result.ToolError {
			line = "FAIL " + line
		}
		return line
	}
	return ""
}

// summariseAnyArgsList renders a short whitespace-joined preview of
// run_command args. Tolerates the JSON shapes commandArgs() accepts.
func summariseAnyArgsList(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case []string:
		return strings.Join(v, " ")
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// pickInt extracts an int from the loose JSON-derived shapes the
// agent's input map can hold (json.Number, float64, int, int64).
func pickInt(raw any) (int, bool) {
	switch v := raw.(type) {
	case nil:
		return 0, false
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
}
