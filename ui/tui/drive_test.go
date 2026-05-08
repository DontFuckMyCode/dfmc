package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dontfuckmycode/dfmc/internal/drive"
)

func TestRunDriveAsyncReturnsPersistedRunID(t *testing.T) {
	eng := newTUITestEngine(t)

	runID, err := runDriveAsync(eng, "add smoke test", nil)
	if err != nil {
		t.Fatalf("runDriveAsync error: %v", err)
	}
	if strings.TrimSpace(runID) == "" {
		t.Fatal("expected non-empty run ID")
	}

	store, err := drive.NewStore(eng.Storage.DB())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	run, err := store.Load(runID)
	if err != nil {
		t.Fatalf("load run: %v", err)
	}
	if run == nil {
		t.Fatalf("expected persisted run %q", runID)
	}
}

func TestRunDriveResumeAsyncRejectsMissingRun(t *testing.T) {
	eng := newTUITestEngine(t)

	_, err := runDriveResumeAsync(eng, "drv-missing")
	if err == nil {
		t.Fatal("expected missing-run error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestBuildTUIDriverRejectsNilStorage(t *testing.T) {
	eng := newTUITestEngine(t)
	orig := eng.Storage
	t.Cleanup(func() { eng.Storage = orig })
	eng.Storage = nil

	_, err := buildTUIDriver(eng, nil)
	if err == nil {
		t.Fatal("expected storage error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "storage") {
		t.Fatalf("expected storage error, got %v", err)
	}
}

func TestTUIDriveResourcesRouting(t *testing.T) {
	r := &tuiDriveResources{routing: map[string]string{"plan": "opus", "code": "sonnet"}}
	got := r.Routing()
	if got == nil {
		t.Fatal("expected non-nil routing")
	}
	if got["plan"] != "opus" || got["code"] != "sonnet" {
		t.Errorf("routing: got %v", got)
	}
}

func TestTUIDriveResourcesRouting_NilReceiver(t *testing.T) {
	var r *tuiDriveResources
	got := r.Routing()
	if got != nil {
		t.Errorf("nil receiver: got %v want nil", got)
	}
}

func TestTUIDriveResourcesSetRouting(t *testing.T) {
	r := &tuiDriveResources{}
	r.SetRouting(map[string]string{"plan": "opus"})
	if r.routing == nil {
		t.Fatal("routing not set")
	}
	if r.routing["plan"] != "opus" {
		t.Errorf("routing[plan]: got %q want opus", r.routing["plan"])
	}
}

func TestTUIDriveResourcesSetRouting_NilReceiver(t *testing.T) {
	var r *tuiDriveResources
	r.SetRouting(map[string]string{"plan": "opus"})
}

func TestTUIDriveResourcesListRuns_NilReceiver(t *testing.T) {
	var r *tuiDriveResources
	got, err := r.listRuns()
	if err != nil {
		t.Errorf("nil receiver: err=%v", err)
	}
	if got != nil {
		t.Errorf("nil receiver: got %v want nil", got)
	}
}

func TestTUIDriveResourcesListRuns_NilStore(t *testing.T) {
	r := &tuiDriveResources{store: nil}
	got, err := r.listRuns()
	if err != nil {
		t.Errorf("nil store: err=%v", err)
	}
	if got != nil {
		t.Errorf("nil store: got %v want nil", got)
	}
}

func TestHandleDriveStopSlash_NoActiveRuns(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)

	// With no args and no active runs, should report no active runs
	next, _, handled := m.handleDriveStopSlash(nil)
	if !handled {
		t.Fatal("handleDriveStopSlash should be handled")
	}
	last := next.chat.transcript[len(next.chat.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "no active drive runs") {
		t.Fatalf("expected no active runs message, got:\n%s", last)
	}
}

func TestHandleDriveStopSlash_WithArgs(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)

	// With a non-existent ID, should report not active
	next, _, handled := m.handleDriveStopSlash([]string{"drv-doesnotexist"})
	if !handled {
		t.Fatal("handleDriveStopSlash should be handled")
	}
	last := next.chat.transcript[len(next.chat.transcript)-1].Content
	// Resolver replies with "no run matches" when the prefix is bogus
	// AND there are no active runs to scan; either phrasing means the
	// stop didn't fire.
	if !strings.Contains(last, "not active") && !strings.Contains(last, "no run matches") {
		t.Fatalf("expected not-found-style message, got:\n%s", last)
	}
}

func TestHandleDriveActiveSlash_NoActiveRuns(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)

	next, _, handled := m.handleDriveActiveSlash()
	if !handled {
		t.Fatal("handleDriveActiveSlash should be handled")
	}
	last := next.chat.transcript[len(next.chat.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "no active drive runs") {
		t.Fatalf("expected no active runs message, got:\n%s", last)
	}
}

func TestHandleDriveListSlash_NoStorage(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)
	orig := m.eng.Storage
	m.eng.Storage = nil
	defer func() { m.eng.Storage = orig }()

	next, _, handled := m.handleDriveListSlash()
	if !handled {
		t.Fatal("handleDriveListSlash should be handled")
	}
	last := next.chat.transcript[len(next.chat.transcript)-1].Content
	if !strings.Contains(last, "storage not initialized") {
		t.Fatalf("expected storage error, got:\n%s", last)
	}
}

func TestDriveSlashStartShowsRunID(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)

	next, _, handled := m.executeChatCommand("/drive add smoke test")
	if !handled {
		t.Fatal("/drive should be handled")
	}
	mm := next.(Model)
	if len(mm.chat.transcript) == 0 {
		t.Fatal("expected system transcript entry")
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	if !strings.Contains(last, "run_id: drv-") {
		t.Fatalf("expected run_id in transcript, got:\n%s", last)
	}
}

// space toggles live-follow on the Workflow tab; when ON,
// snapWorkflowToLiveTarget should move the cursor onto the running TODO
// so the user sees what's currently spinning without scrolling. Esc
// releases the follow.
func TestWorkflowSpaceTogglesLiveFollowAndSnapsToRunningTodo(t *testing.T) {
	m := NewModel(context.Background(), nil)
	run := &drive.Run{
		ID:     "run-test",
		Status: drive.RunRunning,
		Todos: []drive.Todo{
			{ID: "T1", Title: "first", Status: drive.TodoDone},
			{ID: "T2", Title: "second", Status: drive.TodoRunning},
			{ID: "T3", Title: "third", Status: drive.TodoPending},
		},
	}
	m.workflow.runs = []*drive.Run{run}
	m.workflow.selectedRunID = run.ID
	m.workflow.scrollY = 0

	// Press space: follow flips ON and snaps to the running TODO (T2 at index 1).
	next, _ := m.handleWorkflowKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	mm, ok := next.(Model)
	if !ok {
		t.Fatalf("expected Model, got %T", next)
	}
	if !mm.workflow.followLive {
		t.Fatal("space should flip followLive ON")
	}
	if mm.workflow.selectedTodoID != "T2" {
		t.Fatalf("snap should select the running TODO T2, got %q", mm.workflow.selectedTodoID)
	}
	if mm.workflow.scrollY != 1 {
		t.Fatalf("scroll should land on visible index 1 (T2), got %d", mm.workflow.scrollY)
	}

	// Esc with no TODO/run-modal context simply releases follow.
	mm.workflow.selectedTodoID = "" // simulate user already deselected
	mm.workflow.selectedRunID = ""
	mm.workflow.showRoutingEditor = false
	next2, _ := mm.handleWorkflowKey(tea.KeyMsg{Type: tea.KeyEsc})
	mm2 := next2.(Model)
	if mm2.workflow.followLive {
		t.Fatal("esc should release live-follow when nothing else is dismissable")
	}
}

func TestDriveSlashResumeShowsMissingRunError(t *testing.T) {
	eng := newTUITestEngine(t)
	m := NewModel(context.Background(), eng)

	next, _, handled := m.executeChatCommand("/drive resume drv-missing")
	if !handled {
		t.Fatal("/drive resume should be handled")
	}
	mm := next.(Model)
	if len(mm.chat.transcript) == 0 {
		t.Fatal("expected transcript error entry")
	}
	last := mm.chat.transcript[len(mm.chat.transcript)-1].Content
	low := strings.ToLower(last)
	// Resolver returns "no run matches …" when the prefix doesn't
	// hit anything in the persisted store; the legacy "not found"
	// phrasing also passes for backwards compat with any callers
	// still wired to runDriveResumeAsync directly.
	if !strings.Contains(low, "not found") && !strings.Contains(low, "no run matches") {
		t.Fatalf("expected not-found error, got:\n%s", last)
	}
}
