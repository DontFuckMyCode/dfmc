package provider

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// TestBuildAnthropicMessages_CoalescesParallelToolResults pins the fix
// for a silent-400 bug against Anthropic and every Anthropic-compat
// backend (kimi/zai/minimax/alibaba). When one assistant turn emits N
// parallel tool_use blocks, the engine flushes back N separate user
// messages - one tool_result per call - via the per-call append loop
// in agent_loop_phases.go. Pre-fix, buildAnthropicMessages emitted
// these as N consecutive user anthropicMessages, which Anthropic
// rejects with a generic "messages: alternation" 400 that surfaces
// three frames up as an unexplained failure.
//
// The fix coalesces consecutive same-role messages by appending their
// content blocks, preserving every tool_use_id and text segment.
func TestBuildAnthropicMessages_CoalescesParallelToolResults(t *testing.T) {
	req := CompletionRequest{
		Messages: []Message{
			{Role: types.RoleUser, Content: "find Engine"},
			{
				Role: types.RoleAssistant,
				ToolCalls: []ToolCall{
					{ID: "toolu_a", Name: "read_file", Input: map[string]any{"path": "a.go"}},
					{ID: "toolu_b", Name: "read_file", Input: map[string]any{"path": "b.go"}},
				},
			},
			{Role: types.RoleUser, Content: "bytes of a", ToolCallID: "toolu_a", ToolName: "read_file"},
			{Role: types.RoleUser, Content: "bytes of b", ToolCallID: "toolu_b", ToolName: "read_file"},
		},
	}

	out := buildAnthropicMessages(req)

	// Expected shape: user, assistant, user (merged).
	if len(out) != 3 {
		t.Fatalf("expected 3 coalesced messages, got %d: %+v", len(out), out)
	}
	for i := 1; i < len(out); i++ {
		if out[i].Role == out[i-1].Role {
			t.Fatalf("consecutive %q at index %d-%d; Anthropic rejects this shape", out[i].Role, i-1, i)
		}
	}
	// The merged user message must carry BOTH tool_results - no payload loss.
	tail := out[len(out)-1]
	if tail.Role != "user" {
		t.Fatalf("merged tail must be user, got %q", tail.Role)
	}
	if len(tail.Content) != 2 {
		t.Fatalf("expected 2 tool_result blocks in merged user message, got %d: %+v", len(tail.Content), tail.Content)
	}
	seen := map[string]bool{}
	for _, block := range tail.Content {
		m, ok := block.(map[string]any)
		if !ok {
			t.Fatalf("expected map content block, got %T", block)
		}
		if m["type"] != "tool_result" {
			t.Fatalf("expected tool_result block, got type=%v", m["type"])
		}
		id, _ := m["tool_use_id"].(string)
		seen[id] = true
	}
	if !seen["toolu_a"] || !seen["toolu_b"] {
		t.Fatalf("both tool_use_ids must survive the merge, got %v", seen)
	}
}

// TestBuildAnthropicMessages_CoalescesResumeNoteAfterToolResult pins
// the ResumeAgent case: /continue <note> appends a plain user message
// after a parked state whose tail is a user tool_result. Pre-fix, this
// emitted two consecutive user messages and tripped the same 400.
func TestBuildAnthropicMessages_CoalescesResumeNoteAfterToolResult(t *testing.T) {
	req := CompletionRequest{
		Messages: []Message{
			{Role: types.RoleUser, Content: "start"},
			{
				Role: types.RoleAssistant,
				ToolCalls: []ToolCall{
					{ID: "toolu_x", Name: "read_file", Input: map[string]any{"path": "x.go"}},
				},
			},
			{Role: types.RoleUser, Content: "file bytes", ToolCallID: "toolu_x", ToolName: "read_file"},
			{Role: types.RoleUser, Content: "focus on auth tests"}, // resume note
		},
	}

	out := buildAnthropicMessages(req)

	if len(out) != 3 {
		t.Fatalf("expected 3 coalesced messages, got %d", len(out))
	}
	for i := 1; i < len(out); i++ {
		if out[i].Role == out[i-1].Role {
			t.Fatalf("consecutive %q at index %d-%d", out[i].Role, i-1, i)
		}
	}
	// The merged tail must carry BOTH the tool_result and the text note.
	tail := out[len(out)-1]
	if len(tail.Content) != 2 {
		t.Fatalf("expected tool_result + text in merged user, got %d blocks: %+v", len(tail.Content), tail.Content)
	}
	types := []string{}
	for _, b := range tail.Content {
		if m, ok := b.(map[string]any); ok {
			types = append(types, asString(m["type"]))
		}
	}
	want := map[string]bool{"tool_result": false, "text": false}
	for _, t0 := range types {
		if _, ok := want[t0]; ok {
			want[t0] = true
		}
	}
	for k, seen := range want {
		if !seen {
			t.Fatalf("merged user missing expected block type %q; got types=%v", k, types)
		}
	}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
