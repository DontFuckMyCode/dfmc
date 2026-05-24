package memory

import (
	"context"
	"testing"
)

func TestParseSuggestedEntries(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantLen int
	}{
		{
			name:    "bare json array",
			input:   `[{"key":"test","value":"result","category":"fact","confidence":0.8}]`,
			wantLen: 1,
		},
		{
			name:    "json array in markdown fences",
			input:   "```json\n[{\"key\":\"test\",\"value\":\"result\",\"category\":\"fact\",\"confidence\":0.8}]\n```",
			wantLen: 1,
		},
		{
			name:    "json array wrapped in plain text",
			input:   "Here are some suggestions:\n[{\"key\":\"postgres jsonb\",\"value\":\"Use GIN indexes\",\"category\":\"fact\",\"confidence\":0.85}]\n\nThat's it.",
			wantLen: 1,
		},
		{
			name:    "empty array",
			input:   "[]",
			wantLen: 0,
		},
		{
			name:    "plain text no json",
			input:   "Nothing worth remembering.",
			wantLen: 0,
		},
		{
			name:    "invalid json",
			input:   "{invalid json}",
			wantLen: 0,
		},
		{
			name:    "multiple entries",
			input:   `[{"key":"a","value":"b","category":"fact","confidence":0.8},{"key":"c","value":"d","category":"decision","confidence":0.9}]`,
			wantLen: 2,
		},
		{
			name:    "entries with tier field",
			input:   `[{"key":"backend","value":"use go","category":"decision","confidence":0.85,"tier":"semantic"}]`,
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSuggestedEntries(tt.input)
			if err != nil {
				t.Fatalf("parseSuggestedEntries returned error: %v", err)
			}
			if len(got) != tt.wantLen {
				t.Errorf("parseSuggestedEntries(%q) returned %d entries, want %d", tt.input, len(got), tt.wantLen)
			}
		})
	}
}

func TestCallWithPrompt(t *testing.T) {
	stubUpdater := &stubLLMUpdater{resp: `[{"key":"test key","value":"test value","category":"fact","confidence":0.8}]`}

	got, err := CallWithPrompt(context.Background(), stubUpdater, "what is go?", "Go is a programming language.", 0.6)
	if err != nil {
		t.Fatalf("CallWithPrompt returned error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("CallWithPrompt returned %d entries, want 1", len(got))
	}
	if got[0].Key != "test key" {
		t.Errorf("Key=%q, want %q", got[0].Key, "test key")
	}
	if got[0].Confidence != 0.8 {
		t.Errorf("Confidence=%f, want %f", got[0].Confidence, 0.8)
	}
}

func TestCallWithPrompt_BelowThreshold(t *testing.T) {
	stubUpdater := &stubLLMUpdater{resp: `[{"key":"test","value":"result","category":"fact","confidence":0.5}]`}

	got, err := CallWithPrompt(context.Background(), stubUpdater, "q", "a", 0.6)
	if err != nil {
		t.Fatalf("CallWithPrompt returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("CallWithPrompt returned %d entries, want 0 (below threshold)", len(got))
	}
}

func TestCallWithPrompt_EmptyResponse(t *testing.T) {
	stubUpdater := &stubLLMUpdater{resp: ""}

	got, err := CallWithPrompt(context.Background(), stubUpdater, "q", "a", 0.6)
	if err != nil {
		t.Fatalf("CallWithPrompt returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("CallWithPrompt returned %d entries, want 0", len(got))
	}
}

type stubLLMUpdater struct {
	resp string
}

func (s *stubLLMUpdater) Call(_ context.Context, _, _, _ string) (string, error) {
	return s.resp, nil
}