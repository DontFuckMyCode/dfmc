package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/planning"
)

func newActivityTestModel() Model {
	return Model{
		tabs:                  []string{"Chat", "Status", "Files", "Patch", "Workflow", "Tools", "Activity", "Memory", "CodeMap", "Conversations", "Prompts", "Security", "Plans", "Context", "Providers"},
		activeTab:             6,
		activity:              activityPanelState{follow: true},
		diagnosticPanelsState: newDiagnosticPanelsState(),
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
	if len(m.activity.entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(m.activity.entries))
	}
	e := m.activity.entries[0]
	if e.Kind != activityKindTool {
		t.Fatalf("kind=%s want tool", e.Kind)
	}
	if !strings.Contains(e.Text, "read_file") {
		t.Fatalf("text=%q missing tool name", e.Text)
	}
	if !strings.Contains(e.Text, "step 3") {
		t.Fatalf("text=%q missing step", e.Text)
	}
	if len(e.Details) == 0 {
		t.Fatalf("expected detail lines for activity entry")
	}
}

func TestRecordActivityDedupesConsecutiveIdentical(t *testing.T) {
	m := newActivityTestModel()
	for i := 0; i < 5; i++ {
		m.recordActivityEvent(engine.Event{Type: "stream:delta"})
	}
	if len(m.activity.entries) != 1 {
		t.Fatalf("want dedupe, got %d entries", len(m.activity.entries))
	}
	if m.activity.entries[0].Count != 5 {
		t.Fatalf("expected dedupe counter=5, got %d", m.activity.entries[0].Count)
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
	if len(m.activity.entries) != maxActivityEntries {
		t.Fatalf("want cap=%d, got %d", maxActivityEntries, len(m.activity.entries))
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
	if len(m.activity.entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(m.activity.entries))
	}
	for _, e := range m.activity.entries {
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
	if m.activity.scroll != 0 {
		t.Fatalf("follow-on should pin to tail, scroll=%d", m.activity.scroll)
	}
	// k moves up (older), unsets follow.
	m2, _ := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = m2.(Model)
	if m.activity.scroll != 1 || m.activity.follow {
		t.Fatalf("k should scroll up and pause follow: scroll=%d follow=%v", m.activity.scroll, m.activity.follow)
	}
	// Adding a new event while paused must NOT auto-jump to tail.
	m.recordActivityEvent(engine.Event{
		Type:    "tool:call",
		Payload: map[string]any{"tool": "t", "step": 999},
	})
	if m.activity.scroll == 0 {
		t.Fatalf("paused view jumped to tail after new event")
	}
	// G jumps to tail and resumes follow.
	m2, _ = m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = m2.(Model)
	if m.activity.scroll != 0 || !m.activity.follow {
		t.Fatalf("G should resume follow: scroll=%d follow=%v", m.activity.scroll, m.activity.follow)
	}
}

func TestActivityPausedScrollIgnoresFilteredTailEvents(t *testing.T) {
	m := newActivityTestModel()
	m.recordActivityEvent(engine.Event{Type: "tool:call", Payload: map[string]any{"tool": "read_file"}})
	m.recordActivityEvent(engine.Event{Type: "tool:call", Payload: map[string]any{"tool": "write_file"}})

	m.activity.follow = false
	m.activity.query = "read_file"
	m.activity.scroll = 0

	m.recordActivityEvent(engine.Event{Type: "tool:call", Payload: map[string]any{"tool": "write_file"}})
	if m.activity.scroll != 0 {
		t.Fatalf("filtered-out tail event should not move paused scroll, got %d", m.activity.scroll)
	}

	m.recordActivityEvent(engine.Event{Type: "tool:call", Payload: map[string]any{"tool": "read_file"}})
	if m.activity.scroll != 1 {
		t.Fatalf("matching tail event should preserve paused selection by incrementing scroll, got %d", m.activity.scroll)
	}
}

func TestActivityClearResets(t *testing.T) {
	m := newActivityTestModel()
	for i := 0; i < 3; i++ {
		m.recordActivityEvent(engine.Event{
			Type:    "agent:loop:thinking",
			Payload: map[string]any{"step": i + 1, "max_tool_steps": 10},
		})
	}
	m2, _ := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	m = m2.(Model)
	if len(m.activity.entries) != 0 {
		t.Fatalf("c should clear entries, got %d", len(m.activity.entries))
	}
	if !m.activity.follow {
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
	m.activity.follow = false
	m.activity.scroll = 1
	out := m.renderActivityView(80)
	if !strings.Contains(out, "paused") {
		t.Fatalf("paused banner missing when follow=false:\n%s", out)
	}
}

func TestHandleActivitySearchKeyFiltersEntries(t *testing.T) {
	m := newActivityTestModel()
	m.recordActivityEvent(engine.Event{Type: "tool:call", Payload: map[string]any{"tool": "read_file"}})
	m.recordActivityEvent(engine.Event{Type: "provider:throttle:retry", Payload: map[string]any{"provider": "zai", "attempt": 2}})

	m2, _ := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = m2.(Model)
	if !m.activity.searchActive {
		t.Fatal("expected activity search mode to activate")
	}
	m2, _ = m.handleActivitySearchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	m = m2.(Model)
	m2, _ = m.handleActivitySearchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = m2.(Model)
	m2, _ = m.handleActivitySearchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	m = m2.(Model)
	m2, _ = m.handleActivitySearchKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = m2.(Model)

	filtered := m.filteredActivityEntries()
	if len(filtered) != 1 {
		t.Fatalf("expected one filtered activity entry, got %d", len(filtered))
	}
	if !strings.Contains(filtered[0].Text, "zai") {
		t.Fatalf("expected zai match in filtered entry, got %#v", filtered[0])
	}
}

func TestHandleActivityModeSwitchFiltersEntries(t *testing.T) {
	m := newActivityTestModel()
	m.recordActivityEvent(engine.Event{Type: "tool:call", Payload: map[string]any{"tool": "read_file"}})
	m.recordActivityEvent(engine.Event{Type: "agent:loop:thinking", Payload: map[string]any{"step": 1, "max_tool_steps": 4}})
	m.recordActivityEvent(engine.Event{Type: "tool:error", Payload: map[string]any{"tool": "write_file", "error": "boom"}})

	m2, _ := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("4")})
	m = m2.(Model)
	filtered := m.filteredActivityEntries()
	if len(filtered) != 1 {
		t.Fatalf("expected error-only filter to keep one entry, got %d", len(filtered))
	}
	if filtered[0].Kind != activityKindError {
		t.Fatalf("expected error entry after mode switch, got kind=%s", filtered[0].Kind)
	}
}

func TestRenderActivityViewShowsInspectorAndQuery(t *testing.T) {
	m := newActivityTestModel()
	m.recordActivityEvent(engine.Event{
		Type:   "drive:todo:blocked",
		Source: "drive",
		Payload: map[string]any{
			"todo_id": "T3",
			"error":   "missing provider key",
		},
	})
	m.activity.query = "provider"
	out := m.renderActivityViewSized(140, 24)
	if !strings.Contains(out, "INSPECTOR") {
		t.Fatalf("expected inspector pane in activity view:\n%s", out)
	}
	if !strings.Contains(out, "query:") || !strings.Contains(out, "provider") {
		t.Fatalf("expected query line in activity view:\n%s", out)
	}
	if !strings.Contains(out, "TIMELINE") {
		t.Fatalf("expected timeline pane in activity view:\n%s", out)
	}
	if !strings.Contains(out, "open: enter/o") {
		t.Fatalf("expected action rail hints in inspector:\n%s", out)
	}
}

func TestActivityOpenSelectionRoutesProviderEventsToProvidersTab(t *testing.T) {
	m := newActivityTestModel()
	m.providers.rows = []providerRow{
		{Name: "openai"},
		{Name: "zai"},
	}
	m.recordActivityEvent(engine.Event{
		Type:    "provider:throttle:retry",
		Payload: map[string]any{"provider": "zai", "attempt": 2},
	})
	nextModel, _ := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	next := nextModel.(Model)
	if next.activeTab != 14 {
		t.Fatalf("expected provider activity to jump to Providers tab, got %d", next.activeTab)
	}
	if next.providers.scroll != 1 {
		t.Fatalf("expected provider activity to focus zai row, got scroll=%d", next.providers.scroll)
	}
}

func TestActivityOpenSelectionRoutesWorkflowEventsToPlansTab(t *testing.T) {
	m := newActivityTestModel()
	m.recordActivityEvent(engine.Event{
		Type:    "drive:todo:blocked",
		Payload: map[string]any{"todo_id": "T-7", "title": "investigate blocked provider flow"},
	})
	nextModel, _ := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := nextModel.(Model)
	if next.activeTab != 12 {
		t.Fatalf("expected drive activity to jump to Plans tab, got %d", next.activeTab)
	}
	if !strings.Contains(next.plans.query, "investigate blocked provider flow") {
		t.Fatalf("expected plan query to inherit activity context, got %q", next.plans.query)
	}
	if next.plans.plan == nil {
		t.Fatal("expected activity-opened plans panel to compute a plan")
	}
}

func TestActivityOpenSelectionFocusesFileWhenPathExists(t *testing.T) {
	m := newActivityTestModel()
	m.filesView.entries = []string{"README.md", "main.go"}
	m.recordActivityEvent(engine.Event{
		Type: "tool:call",
		Payload: map[string]any{
			"tool": "read_file",
			"path": "README.md",
		},
	})
	nextModel, cmd := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := nextModel.(Model)
	if next.activeTab != 2 {
		t.Fatalf("expected file-backed activity to jump to Files tab, got %d", next.activeTab)
	}
	if next.filesView.index != 0 {
		t.Fatalf("expected file index to focus README.md, got %d", next.filesView.index)
	}
	if cmd == nil {
		t.Fatal("expected file focus to schedule a preview load")
	}
}

func TestActivityFocusSelectionFileUsesFShortcut(t *testing.T) {
	m := newActivityTestModel()
	m.recordActivityEvent(engine.Event{
		Type: "config:reload:auto",
		Payload: map[string]any{
			"path": ".dfmc/config.yaml",
		},
	})
	nextModel, cmd := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	next := nextModel.(Model)
	if next.activeTab != 2 {
		t.Fatalf("expected f shortcut to jump to Files tab, got %d", next.activeTab)
	}
	if next.filesView.path != ".dfmc/config.yaml" {
		t.Fatalf("expected focused file path to be carried over, got %q", next.filesView.path)
	}
	if cmd == nil {
		t.Fatal("expected file shortcut to schedule preview load")
	}
}

func TestActivityOpenSelectionSeedsContextPreview(t *testing.T) {
	m := newActivityTestModel()
	m.recordActivityEvent(engine.Event{
		Type: "context:lifecycle:handoff",
		Payload: map[string]any{
			"query": "explain token budget around provider retries",
		},
	})
	nextModel, _ := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := nextModel.(Model)
	if next.activeTab != 13 {
		t.Fatalf("expected context activity to jump to Context tab, got %d", next.activeTab)
	}
	if next.contextPanel.query != "explain token budget around provider retries" {
		t.Fatalf("expected context query seed, got %q", next.contextPanel.query)
	}
	if next.contextPanel.preview == nil && next.eng != nil {
		t.Fatal("expected context preview to be recomputed")
	}
}

func TestActivityCopySelectionReturnsClipboardCommand(t *testing.T) {
	m := newActivityTestModel()
	m.recordActivityEvent(engine.Event{
		Type:   "tool:error",
		Source: "engine",
		Payload: map[string]any{
			"tool":  "write_file",
			"error": "boom",
			"path":  "main.go",
		},
	})
	nextModel, cmd := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	next := nextModel.(Model)
	if cmd == nil {
		t.Fatal("expected y to return a clipboard command")
	}
	if !strings.Contains(strings.ToLower(next.notice), "activity event") {
		t.Fatalf("expected copy notice for activity event, got %q", next.notice)
	}
}

func TestActivityOpenSelectionUsesExistingPlanQueryWithoutOverwritingBlank(t *testing.T) {
	m := newActivityTestModel()
	m.plans.query = "existing task"
	p := planning.SplitTask(m.plans.query)
	m.plans.plan = &p
	m.recordActivityEvent(engine.Event{
		Type: "drive:todo:blocked",
		Payload: map[string]any{
			"todo_id": "T-9",
		},
	})
	nextModel, _ := m.handleActivityKey(tea.KeyMsg{Type: tea.KeyEnter})
	next := nextModel.(Model)
	if next.plans.query != "existing task" {
		t.Fatalf("expected existing plan query to survive blank activity query, got %q", next.plans.query)
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

func TestClassifyActivityContextCompactedAcceptsCanonicalPayloadKeys(t *testing.T) {
	_, text := classifyActivity(engine.Event{
		Type: "context:lifecycle:compacted",
		Payload: map[string]any{
			"before_tokens": 1200,
			"after_tokens":  450,
		},
	})
	if !strings.Contains(text, "1200") || !strings.Contains(text, "450") {
		t.Fatalf("expected canonical token keys in text, got %q", text)
	}
}

func TestClassifyActivityToolResultAcceptsCamelCaseDuration(t *testing.T) {
	_, text := classifyActivity(engine.Event{
		Type: "tool:result",
		Payload: map[string]any{
			"tool":       "read_file",
			"durationMs": 77,
		},
	})
	if !strings.Contains(text, "77ms") {
		t.Fatalf("expected camelCase duration in text, got %q", text)
	}
}
