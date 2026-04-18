// TUI bridge for the drive package.
//
// runDriveAsync fires a Driver in a background goroutine. The driver
// publishes drive:* events through engine.EventBus, which the TUI's
// handleEngineEvent already routes (see engine_events.go). No tea.Cmd
// is needed because the engine event subscription delivers updates
// asynchronously to the model. The goroutine returns when the run
// terminates; per-TODO sub-agents respect ctx cancellation through
// the engine surface, so closing the engine cancels in-flight work
// cleanly.
//
// Errors from the driver are NOT shown via tea.Cmd here — they
// surface as drive:run:failed events the engine_events handler
// renders into a transcript line. That keeps this bridge tiny and
// the failure path identical to the CLI's.

package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/engine"
)

type tuiDriveResources struct {
	driver *drive.Driver
	store  *drive.Store
}

// runDriveAsync constructs the driver, persists the planning stub, and runs
// it in a goroutine. Returns the run ID immediately so the TUI can print a
// stable handle in the transcript instead of telling the user to go hunting
// through the activity panel.
func runDriveAsync(eng *engine.Engine, task string) (string, error) {
	resources, err := buildTUIDriver(eng)
	if err != nil {
		return "", err
	}
	run, err := drive.NewRun(task)
	if err != nil {
		return "", err
	}
	if err := resources.store.Save(run); err != nil {
		return "", fmt.Errorf("persist drive run: %w", err)
	}
	eng.StartBackgroundTask("tui.drive.run", func(ctx context.Context) {
		_, _ = resources.driver.RunPrepared(ctx, run)
	})
	return run.ID, nil
}

// runDriveResumeAsync re-enters a stopped/in-progress run. Same
// fire-and-forget pattern as runDriveAsync.
func runDriveResumeAsync(eng *engine.Engine, runID string) (string, error) {
	resources, err := buildTUIDriver(eng)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		return "", fmt.Errorf("run ID is required")
	}
	existing, err := resources.store.Load(id)
	if err != nil {
		return "", fmt.Errorf("load drive run: %w", err)
	}
	if existing == nil {
		return "", fmt.Errorf("drive run %q not found", id)
	}
	if drive.IsActive(id) {
		return "", fmt.Errorf("drive run %q is already active in this process", id)
	}
	if existing.Status == drive.RunDone || existing.Status == drive.RunFailed {
		return "", fmt.Errorf("drive run %q is already terminal (%s)", id, existing.Status)
	}
	eng.StartBackgroundTask("tui.drive.resume", func(ctx context.Context) {
		_, _ = resources.driver.Resume(ctx, id)
	})
	return id, nil
}

// buildTUIDriver collapses the runner/store/publisher wiring shared
// by runDriveAsync and runDriveResumeAsync. Returns nil + publishes a
// failure event when the engine isn't usable so the caller doesn't
// have to repeat the guard.
func buildTUIDriver(eng *engine.Engine) (*tuiDriveResources, error) {
	if eng == nil {
		return nil, fmt.Errorf("engine is not initialized")
	}
	runner := eng.NewDriveRunner()
	if runner == nil {
		err := fmt.Errorf("engine.NewDriveRunner returned nil — providers not initialized")
		eng.PublishDriveEvent(drive.EventRunFailed, map[string]any{
			"reason": err.Error(),
		})
		return nil, err
	}
	if eng.Storage == nil {
		err := fmt.Errorf("engine storage is not initialized")
		eng.PublishDriveEvent(drive.EventRunFailed, map[string]any{
			"reason": err.Error(),
		})
		return nil, err
	}
	store, err := drive.NewStore(eng.Storage.DB())
	if err != nil {
		wrapped := fmt.Errorf("drive store init failed: %w", err)
		eng.PublishDriveEvent(drive.EventRunFailed, map[string]any{
			"reason": wrapped.Error(),
		})
		return nil, wrapped
	}
	publisher := drive.Publisher(func(typ string, payload map[string]any) {
		eng.PublishDriveEvent(typ, payload)
	})
	return &tuiDriveResources{
		driver: drive.NewDriver(runner, store, publisher, drive.Config{}),
		store:  store,
	}, nil
}

// handleDriveStopSlash powers `/drive stop [id]`. Without an ID,
// stops the unique active run; with an ID, stops that one. Reports
// success/failure as a system message because the cancellation
// itself doesn't emit a transcript line directly — the driver's
// own drive:run:stopped event handles that asynchronously.
func (m Model) handleDriveStopSlash(args []string) (Model, tea.Cmd, bool) {
	id := ""
	if len(args) > 0 {
		id = strings.TrimSpace(args[0])
	}
	if id == "" {
		active := drive.ListActive()
		if len(active) == 0 {
			return m.appendSystemMessage("/drive stop: no active drive runs in this process."), nil, true
		}
		if len(active) > 1 {
			lines := []string{"/drive stop: multiple active runs — pass an explicit ID:"}
			for _, a := range active {
				lines = append(lines, "  "+a.RunID+"  "+truncateForLine(a.Task, 60))
			}
			return m.appendSystemMessage(strings.Join(lines, "\n")), nil, true
		}
		id = active[0].RunID
	}
	if drive.Cancel(id) {
		m.notice = "Drive cancellation signal sent — loop stops after the current TODO."
		return m.appendSystemMessage("▸ Drive " + id + ": cancellation signal sent. The loop stops after the current TODO finishes; watch for drive:run:stopped."), nil, true
	}
	return m.appendSystemMessage("/drive stop: " + id + " is not active in this process (already done or wrong ID)."), nil, true
}

// handleDriveActiveSlash lists currently-running drives. Useful when
// the user has lost track of what was started or wants the ID for a
// `/drive stop`.
func (m Model) handleDriveActiveSlash() (Model, tea.Cmd, bool) {
	active := drive.ListActive()
	if len(active) == 0 {
		return m.appendSystemMessage("(no active drive runs)"), nil, true
	}
	lines := []string{"Active drive runs:"}
	for _, a := range active {
		lines = append(lines, "  "+a.RunID+"  "+truncateForLine(a.Task, 80))
	}
	return m.appendSystemMessage(strings.Join(lines, "\n")), nil, true
}

// handleDriveListSlash shows every persisted run (active + historical).
// Newest first; truncates the task to fit the chat panel.
func (m Model) handleDriveListSlash() (Model, tea.Cmd, bool) {
	if m.eng == nil || m.eng.Storage == nil {
		return m.appendSystemMessage("/drive list: storage not initialized."), nil, true
	}
	store, err := drive.NewStore(m.eng.Storage.DB())
	if err != nil {
		return m.appendSystemMessage("/drive list error: " + err.Error()), nil, true
	}
	runs, err := store.List()
	if err != nil {
		return m.appendSystemMessage("/drive list error: " + err.Error()), nil, true
	}
	if len(runs) == 0 {
		return m.appendSystemMessage("(no drive runs yet)"), nil, true
	}
	lines := []string{"Drive runs (newest first):"}
	for _, r := range runs {
		done, blocked, skipped, _ := r.Counts()
		lines = append(lines, fmt.Sprintf("  %s  %s  %d done · %d blocked · %d skipped  %s",
			r.ID, r.Status, done, blocked, skipped, truncateForLine(r.Task, 60)))
	}
	return m.appendSystemMessage(strings.Join(lines, "\n")), nil, true
}
