package tui

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func TestHandleEngineEvent_CoachNoteRendersSuggestedAction(t *testing.T) {
	var m Model
	m = m.handleEngineEvent(engine.Event{
		Type: "coach:note",
		Payload: map[string]any{
			"text":     "Files mutated but no validation was mentioned.",
			"severity": "warn",
			"origin":   "mutation_unvalidated",
			"action":   "`go test ./internal/auth/... -count=1`",
		},
	})

	if len(m.chat.transcript) == 0 {
		t.Fatal("expected coach transcript line")
	}
	last := m.chat.transcript[len(m.chat.transcript)-1].Content
	if !strings.Contains(last, "Suggested:") || !strings.Contains(last, "go test ./internal/auth/... -count=1") {
		t.Fatalf("expected structured action in coach transcript, got %q", last)
	}
	if !strings.Contains(m.notice, "Suggested:") {
		t.Fatalf("expected notice to include suggested action, got %q", m.notice)
	}
}
