package provider

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// TestBuildAnthropicMessages_ContextDoesNotViolateAlternation pins the
// fix for a companion to the parallel-tool-results bug (see
// anthropic_coalesce_test.go). buildAnthropicMessages appends a
// user-role "Relevant code context" message whenever req.Context is
// non-empty - but the agent loop re-submits CompletionRequest.Context
// on every round-trip, and after round one Messages ends with a user
// tool_result (the engine flushes each tool call's result as a
// separate user message, then the buildAnthropicMessages per-message
// coalesce merges those into one user turn). Appending a fresh
// context user after that tail produced exactly the
// consecutive-user-messages shape Anthropic's /messages API rejects
// with a generic 400.
//
// The fix: fold the context text block into the tail user message's
// content array when the tail is already user, otherwise emit it as
// its own message like before. The payload the model sees is
// identical - only the wire-level message layout changes.
func TestBuildAnthropicMessages_ContextDoesNotViolateAlternation(t *testing.T) {
	cases := []struct {
		name string
		msgs []Message
	}{
		{
			name: "tail is user question (round 0)",
			msgs: []Message{
				{Role: types.RoleUser, Content: "what does Engine.Init do?"},
			},
		},
		{
			name: "tail is coalesced user tool_results (round N+1)",
			msgs: []Message{
				{Role: types.RoleUser, Content: "find Engine.Init"},
				{
					Role: types.RoleAssistant,
					ToolCalls: []ToolCall{
						{ID: "toolu_a", Name: "read_file", Input: map[string]any{"path": "engine.go"}},
						{ID: "toolu_b", Name: "grep_codebase", Input: map[string]any{"pattern": "Init"}},
					},
				},
				{Role: types.RoleUser, Content: "a bytes", ToolCallID: "toolu_a", ToolName: "read_file"},
				{Role: types.RoleUser, Content: "b bytes", ToolCallID: "toolu_b", ToolName: "grep_codebase"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := CompletionRequest{
				Messages: tc.msgs,
				Context: []types.ContextChunk{
					{Path: "internal/engine/engine.go", Score: 0.9, Content: "func Init() {}"},
				},
			}
			out := buildAnthropicMessages(req)
			if len(out) == 0 {
				t.Fatalf("expected at least 1 message, got 0")
			}
			for i := 1; i < len(out); i++ {
				if out[i].Role == out[i-1].Role {
					t.Fatalf("consecutive %q at index %d-%d; Anthropic rejects this shape. Sequence: %s",
						out[i].Role, i-1, i, roleSequence(out))
				}
			}
			// The tail user must carry the context text block so the
			// payload reaches the model even after folding.
			tail := out[len(out)-1]
			if tail.Role != "user" {
				t.Fatalf("tail must be user, got %q (full sequence: %s)", tail.Role, roleSequence(out))
			}
			found := false
			for _, block := range tail.Content {
				m, ok := block.(map[string]any)
				if !ok {
					continue
				}
				if txt, _ := m["text"].(string); txt != "" && contains(txt, "Relevant code context") {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("context text block must land in the tail user message; blocks=%+v", tail.Content)
			}
		})
	}
}

func roleSequence(out []anthropicMessage) string {
	if len(out) == 0 {
		return "[]"
	}
	s := ""
	for i, m := range out {
		if i > 0 {
			s += " -> "
		}
		s += m.Role
	}
	return s
}

func contains(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
