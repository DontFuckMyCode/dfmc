package engine

// agent_compact_rounds.go — tool-round splitting + the orphan-tool-use
// patcher used by the offline auto-compactor. Companion siblings:
//
//   - agent_compact.go         lifecycle config + maybe/proactive/force
//                              entry points + the shared collapse
//                              routine + estimateRequestTokens
//   - agent_compact_summary.go terse-summary text builders for the
//                              rounds being dropped (per-round line +
//                              per-tool-call line + result excerpt)
//
// A "round" is one assistant turn plus the consecutive user
// tool_result messages that follow it. patchUnresolvedToolUses
// injects synthetic ToolError results for any kept assistant
// tool_call that has no matching tool_result — without it,
// Anthropic rejects the next request with "tool_use ID matched no
// tool_result" mid-resume and the model never gets to recover.

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// toolRound groups one assistant turn (optionally carrying tool_calls) with
// the user tool_result messages that immediately follow it.
type toolRound struct {
	Messages []provider.Message
}

// patchUnresolvedToolUses scans each kept round and, for any assistant
// ToolCalls that lack a matching tool_result in that round, appends a
// synthetic tool_result message with ToolError=true so the provider
// receives a well-formed conversation. Without this, Anthropic in
// particular rejects the next request with "tool_use ID matched no
// tool_result" — the symptom the user-facing error log shows is a
// confusing 400 mid-resume that the model never gets to recover from.
//
// The synthetic result deliberately states the lost-result reality
// instead of inventing an answer; the model can read it and decide
// whether to re-issue the call.
func patchUnresolvedToolUses(rounds []toolRound) []toolRound {
	if len(rounds) == 0 {
		return rounds
	}
	out := make([]toolRound, len(rounds))
	for i, r := range rounds {
		out[i] = patchRoundUnresolvedToolUses(r)
	}
	return out
}

func patchRoundUnresolvedToolUses(r toolRound) toolRound {
	if len(r.Messages) == 0 {
		return r
	}
	head := r.Messages[0]
	if head.Role != types.RoleAssistant || len(head.ToolCalls) == 0 {
		return r
	}
	// Index the tool_result IDs already present in this round.
	have := make(map[string]struct{}, len(r.Messages))
	for i := 1; i < len(r.Messages); i++ {
		if id := strings.TrimSpace(r.Messages[i].ToolCallID); id != "" {
			have[id] = struct{}{}
		}
	}
	// Synthesize results for any unresolved IDs.
	missing := make([]provider.ToolCall, 0)
	for _, c := range head.ToolCalls {
		if strings.TrimSpace(c.ID) == "" {
			continue
		}
		if _, ok := have[c.ID]; !ok {
			missing = append(missing, c)
		}
	}
	if len(missing) == 0 {
		return r
	}
	patched := toolRound{Messages: append([]provider.Message(nil), r.Messages...)}
	for _, c := range missing {
		patched.Messages = append(patched.Messages, provider.Message{
			Role:       types.RoleUser,
			ToolCallID: c.ID,
			ToolName:   c.Name,
			ToolError:  true,
			Content:    "[result lost during compaction; this tool call did not produce a recorded result. Re-issue the call if the answer is still needed.]",
		})
	}
	return patched
}

// findNativeLoopPrefixEnd returns the index where the provider-injected
// prefix (history + original user question) ends and tool rounds begin. The
// prefix ends after the last user message that carries no ToolCallID — i.e.
// the organic user turn, not a tool_result turn.
func findNativeLoopPrefixEnd(msgs []provider.Message) int {
	end := 0
	for i, m := range msgs {
		if m.Role == types.RoleUser && strings.TrimSpace(m.ToolCallID) == "" {
			end = i + 1
		}
	}
	return end
}

// splitNativeLoopRounds walks the post-prefix slice and groups each assistant
// message with any consecutive user tool_result messages that follow. An
// assistant message with no trailing tool_results still forms a lone round
// (e.g. an interim reasoning turn).
func splitNativeLoopRounds(msgs []provider.Message) []toolRound {
	out := make([]toolRound, 0, len(msgs)/2+1)
	i := 0
	for i < len(msgs) {
		if msgs[i].Role != types.RoleAssistant {
			// Stray non-assistant start — attach to previous round if present,
			// otherwise start a new degenerate round. Either way, keep things
			// ordered so we don't lose messages.
			if len(out) > 0 {
				out[len(out)-1].Messages = append(out[len(out)-1].Messages, msgs[i])
				i++
				continue
			}
			out = append(out, toolRound{Messages: []provider.Message{msgs[i]}})
			i++
			continue
		}
		round := toolRound{Messages: []provider.Message{msgs[i]}}
		i++
		for i < len(msgs) && msgs[i].Role == types.RoleUser && strings.TrimSpace(msgs[i].ToolCallID) != "" {
			round.Messages = append(round.Messages, msgs[i])
			i++
		}
		out = append(out, round)
	}
	return out
}

func totalRoundMessages(rounds []toolRound) int {
	n := 0
	for _, r := range rounds {
		n += len(r.Messages)
	}
	return n
}
