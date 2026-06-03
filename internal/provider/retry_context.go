package provider

// retry_context.go — context-overflow detection and message
// compaction. Used by modelChainRetry (in retry_chain.go) on the
// SAME provider/model after an ErrContextOverflow rather than
// switching providers — moving to a different provider doesn't
// help because every provider would see the same conversation.

import (
	"errors"
	"fmt"
	"strings"
)

// isContextOverflow matches either the explicit ErrContextOverflow sentinel or
// the well-known upstream phrasing used by Anthropic and OpenAI. New upstreams
// can just wrap ErrContextOverflow — the string-match branch is a best-effort
// catch for providers that haven't been taught to.
func isContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrContextOverflow) {
		return true
	}
	msg := strings.ToLower(err.Error())
	phrases := []string{
		"context_length_exceeded",
		"maximum context length",
		"prompt is too long",
		"context length",
		"too many tokens",
		"context window",
		"input is too long",
		"request too large",
	}
	for _, p := range phrases {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// compactMessagesForRetry drops the oldest non-tail messages and preserves:
//   - the final *clean* user turn (required for every provider),
//   - any trailing assistant/tool-result chain that follows that user turn,
//   - a synthetic [context compacted] note so the model sees *why* older
//     turns are missing instead of treating them as never having happened.
//
// "Clean" means the kept tail starts at a user message whose ToolCallID is
// empty — a genuine user prompt, not a tool_result. The naive "last user
// message" rule cut the history mid-tool-roundtrip: in the agent loop the
// last user message is almost always a tool_result whose matching assistant
// tool_use sits one message earlier, so keeping from there orphans the
// tool_result. Both Anthropic ("tool_use_id not found") and OpenAI ("must be
// a response to a preceding tool_calls message") reject that shape with a
// 400 — which would make the compaction retry fail the very overflow it
// exists to rescue. Cutting at the last clean user turn keeps every tool
// roundtrip in the tail intact.
//
// Returns the compacted slice and the count of messages that were actually
// dropped. When trimming would leave fewer than 2 messages, returns the
// original slice and 0 — giving up is better than shipping a stub.
func compactMessagesForRetry(msgs []Message) ([]Message, int) {
	if len(msgs) <= 2 {
		return msgs, 0
	}
	// Find the last *clean* user index (a real prompt, ToolCallID empty) —
	// that's the start of the tail we must keep without orphaning a
	// tool_result whose tool_use lives in the dropped prefix.
	lastUser := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if string(msgs[i].Role) == "user" && strings.TrimSpace(msgs[i].ToolCallID) == "" {
			lastUser = i
			break
		}
	}
	if lastUser <= 0 {
		return msgs, 0
	}
	// If only one message would be dropped, don't bother — the retry is
	// unlikely to fit otherwise.
	if lastUser < 2 {
		return msgs, 0
	}
	tail := msgs[lastUser:]
	notice := Message{
		Role:    "user",
		Content: "[prior conversation compacted to fit context window; " + fmt.Sprintf("%d", lastUser) + " older messages omitted]",
	}
	compacted := make([]Message, 0, len(tail)+1)
	compacted = append(compacted, notice)
	compacted = append(compacted, tail...)
	return compacted, lastUser
}
