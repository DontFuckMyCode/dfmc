package provider

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// TestCompactMessagesForRetryDoesNotOrphanToolResult pins the contract that
// compaction never cuts the history mid-tool-roundtrip. In the agent loop the
// last user message is almost always a tool_result (ToolCallID set), whose
// matching assistant tool_use sits one message earlier. The naive "keep from
// the last user message" rule would drop that assistant tool_use and ship a
// tool_result with no preceding tool_use — a shape BOTH Anthropic ("tool_use_id
// not found") and OpenAI ("must be a response to a preceding tool_calls
// message") reject with a 400. Since compaction exists to RESCUE a context
// overflow, producing an invalid request defeats the entire purpose.
//
// The fix cuts at the last *clean* user turn (ToolCallID empty), so the kept
// tail always begins with a genuine user message and every tool roundtrip
// inside it stays intact.
func TestCompactMessagesForRetryDoesNotOrphanToolResult(t *testing.T) {
	// A realistic agent-loop history: a real user task, a tool roundtrip, a
	// follow-up user turn, then another tool roundtrip whose tool_result is
	// the final (last) user message.
	msgs := []Message{
		{Role: types.RoleUser, Content: "original task"},
		{Role: types.RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "read_file"}}},
		{Role: types.RoleUser, ToolCallID: "call_1", ToolName: "read_file", Content: "file body"},
		{Role: types.RoleAssistant, Content: "intermediate answer"},
		{Role: types.RoleUser, Content: "now do the next thing"},
		{Role: types.RoleAssistant, ToolCalls: []ToolCall{{ID: "call_2", Name: "grep_codebase"}}},
		{Role: types.RoleUser, ToolCallID: "call_2", ToolName: "grep_codebase", Content: "grep hits"},
	}

	compacted, trimmed := compactMessagesForRetry(msgs)
	if trimmed == 0 {
		t.Fatalf("expected compaction to trim something, got 0 (compacted=%d)", len(compacted))
	}
	if len(compacted) < 2 {
		t.Fatalf("compacted history too short: %d", len(compacted))
	}

	// compacted[0] is the synthetic notice (user). The first real message of
	// the kept tail must be a clean user turn, NOT an orphan tool_result.
	first := compacted[1]
	if string(first.Role) != "user" {
		t.Fatalf("kept tail must start with a user message, got role %q", first.Role)
	}
	if first.ToolCallID != "" {
		t.Fatalf("kept tail starts with an ORPHAN tool_result (ToolCallID=%q); "+
			"its matching assistant tool_use was dropped — Anthropic/OpenAI reject this shape", first.ToolCallID)
	}

	// Every tool_result inside the kept tail must have its matching assistant
	// tool_use somewhere earlier in the same tail.
	seenToolUse := map[string]bool{}
	for _, m := range compacted {
		for _, c := range m.ToolCalls {
			seenToolUse[c.ID] = true
		}
		if m.ToolCallID != "" && !seenToolUse[m.ToolCallID] {
			t.Fatalf("tool_result for %q has no preceding tool_use in the compacted tail", m.ToolCallID)
		}
	}
}
