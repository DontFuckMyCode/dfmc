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
	driver  *drive.Driver
	store   drive.RunStore
	routing map[string]string // routing config set by the user in the routing editor
}

// Routing returns the current routing map.
func (r *tuiDriveResources) Routing() map[string]string {
	if r == nil {
		return nil
	}
	return r.routing
}

// SetRouting updates the routing config used for subsequent drive runs.
func (r *tuiDriveResources) SetRouting(routing map[string]string) {
	if r == nil {
		return
	}
	r.routing = routing
}

// runDriveAsync constructs the driver, persists the planning stub, and runs
// it in a goroutine. Returns the run ID immediately so the TUI can print a
// stable handle in the transcript instead of telling the user to go hunting
// through the activity panel.
func runDriveAsync(eng *engine.Engine, task string, routing map[string]string) (string, error) {
	resources, err := buildTUIDriver(eng, routing)
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
	resources, err := buildTUIDriver(eng, nil)
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
func buildTUIDriver(eng *engine.Engine, routing map[string]string) (*tuiDriveResources, error) {
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
	cfg := drive.Config{Routing: routing}
	driver := drive.NewDriver(runner, store, publisher, cfg)
	driver.SetReportDir(eng.DriveReportDir())
	return &tuiDriveResources{
		driver:  driver,
		store:   store,
		routing: routing,
	}, nil
}

// listRuns returns all persisted drive runs, newest first. Used by the
// Workflow tab to populate the run selector.
func (r *tuiDriveResources) listRuns() ([]*drive.Run, error) {
	if r == nil || r.store == nil {
		return nil, nil
	}
	return r.store.List()
}

// resolveDriveRunID accepts either a full run ID or a short prefix
// (typically the 8-char chunk we display) and returns the matching
// full ID. Returns:
//   - exact match  → (id, true, "")
//   - unique prefix match across `candidates` → (full_id, true, "")
//   - no match     → ("", false, "no run matches …")
//   - ambiguous prefix → ("", false, "multiple runs match …")
//
// Lets users copy the visible short ID and paste it into /drive stop /
// /drive resume without hunting for the full one.
func resolveDriveRunID(input string, candidates []string) (string, bool, string) {
	q := strings.TrimSpace(input)
	if q == "" {
		return "", false, "missing run id"
	}
	// Exact match wins.
	for _, c := range candidates {
		if c == q {
			return c, true, ""
		}
	}
	// Prefix match — case-insensitive so the user can be sloppy.
	qLow := strings.ToLower(q)
	var matches []string
	for _, c := range candidates {
		if strings.HasPrefix(strings.ToLower(c), qLow) {
			matches = append(matches, c)
		}
	}
	switch len(matches) {
	case 0:
		return "", false, fmt.Sprintf("no run matches %q — try /drive list to see all runs", q)
	case 1:
		return matches[0], true, ""
	default:
		return "", false, fmt.Sprintf("%q is ambiguous — matches %d runs (%s …) — paste a longer prefix", q, len(matches), strings.Join(matches[:min(3, len(matches))], ", "))
	}
}

// activeDriveRunIDs returns the IDs of every currently running drive
// in this process. Used as the candidate set for /drive stop's prefix
// resolver.
func activeDriveRunIDs() []string {
	active := drive.ListActive()
	ids := make([]string, 0, len(active))
	for _, a := range active {
		ids = append(ids, a.RunID)
	}
	return ids
}

// allDriveRunIDs returns every persisted run ID (active + historical)
// from the engine's drive store. Used as the candidate set for
// /drive resume's prefix resolver.
func (m Model) allDriveRunIDs() []string {
	if m.eng == nil || m.eng.Storage == nil {
		return nil
	}
	store, err := drive.NewStore(m.eng.Storage.DB())
	if err != nil {
		return nil
	}
	runs, err := store.List()
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(runs))
	for _, r := range runs {
		ids = append(ids, r.ID)
	}
	return ids
}

// handleDriveStopSlash powers `/drive stop [id]`. Without an ID,
// stops the unique active run; with an ID, accepts a prefix and
// resolves it to the full run ID. Reports success/failure as a
// system message because the cancellation itself doesn't emit a
// transcript line directly — the driver's own drive:run:stopped
// event handles that asynchronously.
func (m Model) handleDriveStopSlash(args []string) (Model, tea.Cmd, bool) {
	idArg := ""
	if len(args) > 0 {
		idArg = strings.TrimSpace(args[0])
	}
	active := drive.ListActive()
	if idArg == "" {
		if len(active) == 0 {
			return m.appendSystemMessage("No active drive runs in this process. /drive list shows historical runs."), nil, true
		}
		if len(active) > 1 {
			lines := []string{"Multiple active runs — pass an explicit ID (or prefix) to /drive stop:"}
			for _, a := range active {
				lines = append(lines, "  "+a.RunID+"  ·  "+truncateForLine(a.Task, 60))
			}
			return m.appendSystemMessage(strings.Join(lines, "\n")), nil, true
		}
		idArg = active[0].RunID
	}
	id, ok, errMsg := resolveDriveRunID(idArg, activeDriveRunIDs())
	if !ok {
		return m.appendSystemMessage("/drive stop: " + errMsg), nil, true
	}
	if drive.Cancel(id) {
		m.notice = "Drive [" + shortRunID(id) + "] stopping — finishes current TODO first."
		return m.appendSystemMessage("▸ Drive cancellation sent\n   run_id: " + id + "\n   The loop stops after the current TODO finishes; watch the Activity panel for drive:run:stopped."), nil, true
	}
	return m.appendSystemMessage("/drive stop: " + id + " is not active anymore. Try /drive list to see persisted runs."), nil, true
}

// handleDriveActiveSlash lists currently-running drives with their
// FULL run IDs (so the user can copy/paste into /drive stop).
func (m Model) handleDriveActiveSlash() (Model, tea.Cmd, bool) {
	active := drive.ListActive()
	if len(active) == 0 {
		return m.appendSystemMessage("No active drive runs. Start one with /drive <task>, or /drive list to see persisted runs."), nil, true
	}
	lines := []string{fmt.Sprintf("Active drive runs (%d):", len(active))}
	for _, a := range active {
		lines = append(lines, "  "+a.RunID+"  ·  "+truncateForLine(a.Task, 80))
	}
	lines = append(lines, "", "Tip: /drive stop <id-or-prefix> cancels a specific run. The 8-char prefix is enough.")
	return m.appendSystemMessage(strings.Join(lines, "\n")), nil, true
}

// handleDriveListSlash shows every persisted run (active + historical)
// with FULL run IDs so the user can copy/paste them into /drive stop /
// /drive resume. Newest first; truncates the task to fit chat width.
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
		return m.appendSystemMessage("No drive runs yet. Start one with /drive <task>."), nil, true
	}
	lines := []string{fmt.Sprintf("Drive runs (%d, newest first):", len(runs))}
	for _, r := range runs {
		done, blocked, skipped, _ := r.Counts()
		lines = append(lines, fmt.Sprintf("  %s  %-8s  %d done · %d blocked · %d skipped  %s",
			r.ID, r.Status, done, blocked, skipped, truncateForLine(r.Task, 60)))
	}
	lines = append(lines, "",
		"Tip: /drive stop <id-or-prefix> cancels active · /drive resume <id-or-prefix> restarts stopped.",
		"     The first 8 chars are usually unique enough — the resolver matches on prefix.")
	return m.appendSystemMessage(strings.Join(lines, "\n")), nil, true
}
