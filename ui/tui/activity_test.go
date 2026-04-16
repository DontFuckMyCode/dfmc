package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func newActivityTestModel() Model {
	return Model{
		tabs:           []string{"Chat", "Status", "Files", "Patch", "Setup", "Tools", "Activity"},
		activeTab:      6,
		activityFollow: true,
	}
}

func TestRecordActivityCapturesToolCall(t *testing.T) {
	m := newActivityTestModel()
	m.recordActivityEvent(engine.Event{
		Type: "tool:call",
		Payload: map[string]any{
			"tool": "read_file",
			"step": 3,
		},
	})
	if len(m.activityEntries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(m.activityEntries))
	}
	e := m.activityEntries[0]
	if e.Kind != activityKindTool {
		t.Fatalf("kind=%s want tool", e.Kind)
	}
	if !strings.Contains(e.Text, "read_file") {
		t.Fatalf("text=%q missing tool name", e.Text)
	}
	if !strings.Contains(e.Text, "step 3") {
		t.Fatalf("text=%q missing step", e.Text)
	}
}

func TestRecordActivityDedupesConsecutiveIdentical(t *testing.T) {
	m := newActivityTestModel()
	for i := 0; i < 5; i++ {
		m.recordActivityEvent(engine.Event{Type: "stream:delta"})
	}
	if len(m.activityEntries) != 1 {
		t.Fatalf("want dedupe, got %d entries", len(m.activityEntries))
	}
}

func TestRecordActivityRingBufferCap(t *testing.T) {
	m := newActivityTestModel()
	for i := 0; i < maxActivityEntries+50; i++ {
		// Vary tool name so dedupe doesn't collapse them.
		m.recordActivityEvent(engine.Event{
			Type:    "tool:call",
			Payload: map[string]any{"tool": "t", "step": i + 1},
		})
	}
	if len(m.activityEntries) != maxActivityEntries {
		t.Fatalf("want cap=%d, got %d", maxActivityEntries, len(m.activityEntries))
	}
}

func TestRecordActivityClassifiesErrorEvents(t *testing.T) {
	m := newActivityTestModel()
	m.recordActivityEvent(engine.Event{
		Type:    "tool:error",
		Payload: map[string]any{"tool": "write_file", "error": "boom"},
	})
	m.recordActivityEvent(engine.Event{
		Type:    "index:error",
		Payload: map[string]any{"error": "parse failed"},
	})
	if len(m.activityEntries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(m.activityEntries))
	}
	for _, e := range m.activityEntries {
		if e.Kind != activityKindError {
			t.Errorf("event %q not classified as error: kind=%s", e.EventID, e.Kind)
		}
	}
}

func TestActivityFollowScrollBehavior(t *testing.T) {
	m := newActivityTestModel()
	for i := 0; i < 10; i++ {
		m.recordActivityEvent(engine.Event{
			Type:    "tool:call",
			Payload: map[string]any{"tool": "t", "step": i + 1},
		})
	}
	if m.activityScroll != 0 {
		t.Fatalf("follow-on should pin to tail, scroll=%d", m.activityScroll)
	}
	// k moves up (older), unsets follow.
	m2, _ := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = m2.(Model)
	if m.activityScroll != 1 || m.activityFollow {
		t.Fatalf("k should scroll up and pause follow: scroll=%d follow=%v", m.activityScroll, m.activityFollow)
	}
	// Adding a new event while paused must NOT auto-jump to tail.
	m.recordActivityEvent(engine.Event{
		Type:    "tool:call",
		Payload: map[string]any{"tool": "t", "step": 999},
	})
	if m.activityScroll == 0 {
		t.Fatalf("paused view jumped to tail after new event")
	}
	// G jumps to tail and resumes follow.
	m2, _ = m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = m2.(Model)
	if m.activityScroll != 0 || !m.activityFollow {
		t.Fatalf("G should resume follow: scroll=%d follow=%v", m.activityScroll, m.activityFollow)
	}
}

func TestActivityClearResets(t *testing.T) {
	m := newActivityTestModel()
	for i := 0; i < 3; i++ {
		m.recordActivityEvent(engine.Event{
			Type: "agent:loop:thinking",
			Payload: map[string]any{"step": i + 1, "max_tool_steps": 10},
		})
	}
	m2, _ := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = m2.(Model)
	if len(m.activityEntries) != 0 {
		t.Fatalf("c should clear entries, got %d", len(m.activityEntries))
	}
	if !m.activityFollow {
		t.Fatalf("c should restore follow")
	}
}

func TestRenderActivityViewEmptyState(t *testing.T) {
	m := newActivityTestModel()
	out := m.renderActivityView(80)
	if !strings.Contains(out, "No events yet") {
		t.Fatalf("empty state missing copy:\n%s", out)
	}
	if !strings.Contains(out, "Activity") {
		t.Fatalf("empty state missing header:\n%s", out)
	}
}

func TestRenderActivityViewPausedBanner(t *testing.T) {
	m := newActivityTestModel()
	for i := 0; i < 3; i++ {
		m.recordActivityEvent(engine.Event{
			Type:    "tool:call",
			Payload: map[string]any{"tool": "t", "step": i + 1},
		})
	}
	m.activityFollow = false
	m.activityScroll = 1
	out := m.renderActivityView(80)
	if !strings.Contains(out, "paused") {
		t.Fatalf("paused banner missing when follow=false:\n%s", out)
	}
}

func TestClassifyActivityFallbackUsesEventType(t *testing.T) {
	kind, text := classifyActivity(engine.Event{Type: "engine:ready"})
	if kind != activityKindInfo {
		t.Fatalf("kind=%s want info", kind)
	}
	if !strings.Contains(text, "ready") {
		t.Fatalf("text=%q missing 'ready'", text)
	}
}

func TestClassifyActivityStringPayloadAppended(t *testing.T) {
	_, text := classifyActivity(engine.Event{Type: "coach:note", Payload: "watch the context budget"})
	if !strings.Contains(text, "coach:note") || !strings.Contains(text, "watch") {
		t.Fatalf("text=%q should include type + string payload", text)
	}
}
