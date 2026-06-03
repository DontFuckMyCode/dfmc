package provider

import "testing"

// TestBuildGoogleContents_ParallelToolResultsAlternate pins that parallel tool
// calls do not produce consecutive user-role contents. The agent loop flushes N
// parallel tool results as N separate user messages (one per call). Gemini
// requires multiturn contents to alternate user/model and expects parallel
// function responses to share a SINGLE user content with N functionResponse
// parts — N consecutive user contents trip a 400 ("ensure that multiturn
// requests alternate between user and model"). The Anthropic builder already
// coalesces this exact shape; the Google builder must too.
func TestBuildGoogleContents_ParallelToolResultsAlternate(t *testing.T) {
	req := CompletionRequest{
		Messages: []Message{
			{Role: "user", Content: "read both files"},
			{Role: "assistant", ToolCalls: []ToolCall{
				{ID: "c1", Name: "read_file", Input: map[string]any{"path": "a.go"}},
				{ID: "c2", Name: "read_file", Input: map[string]any{"path": "b.go"}},
			}},
			{Role: "tool", ToolCallID: "c1", ToolName: "read_file", Content: "content a"},
			{Role: "tool", ToolCallID: "c2", ToolName: "read_file", Content: "content b"},
		},
	}

	contents := buildGoogleContents(req)

	// No two adjacent contents may share a role.
	for i := 1; i < len(contents); i++ {
		if contents[i].Role == contents[i-1].Role {
			t.Fatalf("consecutive %q contents at index %d-%d; Gemini rejects non-alternating turns",
				contents[i].Role, i-1, i)
		}
	}

	// The two parallel functionResponses must be merged into one user content
	// carrying both parts (the last content here).
	last := contents[len(contents)-1]
	if last.Role != "user" {
		t.Fatalf("final content role = %q, want user", last.Role)
	}
	fnResp := 0
	for _, p := range last.Parts {
		if p.FunctionResponse != nil {
			fnResp++
		}
	}
	if fnResp != 2 {
		t.Fatalf("expected 2 functionResponse parts merged into the final user content, got %d (parts=%d)",
			fnResp, len(last.Parts))
	}
}

// TestBuildGoogleContents_SingleRoundTripStillAlternates is the negative
// control: an ordinary single-call round trip must remain three alternating
// contents (user, model, user) — the coalesce must not collapse genuinely
// alternating turns.
func TestBuildGoogleContents_SingleRoundTripStillAlternates(t *testing.T) {
	req := CompletionRequest{
		Messages: []Message{
			{Role: "user", Content: "read README"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "c1", Name: "read_file", Input: map[string]any{"path": "README.md"}}}},
			{Role: "tool", ToolCallID: "c1", ToolName: "read_file", Content: "# DFMC"},
		},
	}
	contents := buildGoogleContents(req)
	if len(contents) != 3 {
		t.Fatalf("expected 3 contents (user, model, user), got %d", len(contents))
	}
	wantRoles := []string{"user", "model", "user"}
	for i, want := range wantRoles {
		if contents[i].Role != want {
			t.Fatalf("content[%d].Role = %q, want %q", i, contents[i].Role, want)
		}
	}
}
