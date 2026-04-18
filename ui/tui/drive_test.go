package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

func TestRunDriveAsyncReturnsPersistedRunID(t *testing.T) {
	eng := newTUITestEngine(t)

	runID, err := runDriveAsync(eng, "add smoke test")
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

	_, err := buildTUIDriver(eng)
	if err == nil {
		t.Fatal("expected storage error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "storage") {
		t.Fatalf("expected storage error, got %v", err)
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
	if !strings.Contains(strings.ToLower(last), "not found") {
		t.Fatalf("expected not-found error, got:\n%s", last)
	}
}
