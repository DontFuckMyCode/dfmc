package tui

import (
	"context"
	"strings"
	"testing"
)

func TestRenderContextStrip_SplitsConversationAndEvidence(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.chat.input = "Review [[file:a.go]] and explain the failure"
	got := m.renderContextStrip(180)
	for _, want := range []string{"CTX conversation", "CTX evidence", "mode:", "explicit/tool", "markers:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected context strip to contain %q, got %q", want, got)
		}
	}
}
