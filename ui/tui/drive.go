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

// runDriveAsync constructs the driver and runs it in a goroutine.
// Safe to call from a bubbletea handler — never blocks. Each call is
// independent: there's no global state preventing two drive runs
// from executing concurrently (though the engine.RunSubagent path
// serializes them via the parked-state lock). Cancellation is via
// `/drive stop` which calls drive.Cancel(runID).
func runDriveAsync(eng *engine.Engine, task string) {
	driver := buildTUIDriver(eng)
	if driver == nil {
		return
	}
	go func() {
		_, _ = driver.Run(context.Background(), task)
	}()
}

// runDriveResumeAsync re-enters a stopped/in-progress run. Same
// fire-and-forget pattern as runDriveAsync.
func runDriveResumeAsync(eng *engine.Engine, runID string) {
	driver := buildTUIDriver(eng)
	if driver == nil {
		return
	}
	go func() {
		_, _ = driver.Resume(context.Background(), runID)
	}()
}

// buildTUIDriver collapses the runner/store/publisher wiring shared
// by runDriveAsync and runDriveResumeAsync. Returns nil + publishes a
// failure event when the engine isn't usable so the caller doesn't
// have to repeat the guard.
func buildTUIDriver(eng *engine.Engine) *drive.Driver {
	if eng == nil {
		return nil
	}
	runner := eng.NewDriveRunner()
	if runner == nil {
		eng.PublishDriveEvent(drive.EventRunFailed, map[string]any{
			"reason": "engine.NewDriveRunner returned nil — providers not initialized",
		})
		return nil
	}
	store, err := drive.NewStore(eng.Storage.DB())
	if err != nil {
		eng.PublishDriveEvent(drive.EventRunFailed, map[string]any{
			"reason": "drive store init failed: " + err.Error(),
		})
		return nil
	}
	publisher := drive.Publisher(func(typ string, payload map[string]any) {
		eng.PublishDriveEvent(typ, payload)
	})
	return drive.NewDriver(runner, store, publisher, drive.Config{})
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
