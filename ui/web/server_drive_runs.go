// server_drive_runs.go — read/control handlers for the Drive HTTP
// surface (show, resume, stop, active, delete). Sibling of
// server_drive.go which keeps the package doc-comment, the
// DriveStartRequest shape, the heavier handleDriveStart fire-and-
// forget runner spinup, and the handleDriveList history endpoint.
//
// Splitting the per-id surfaces out keeps server_drive.go scoped to
// "what does it take to start a run" while this file owns the
// inspection + lifecycle endpoints. They evolve more slowly than the
// start path (which carries the configurable knob surface that grows
// as new Drive runner config fields land).

package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// handleDriveShow fetches one run by ID. Returns 404 on miss with a
// clear message so the workbench can distinguish "not found" from
// "engine error".
func (s *Server) handleDriveShow(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id is required"})
		return
	}
	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not initialized"})
		return
	}
	store, err := drive.NewStore(s.engine.Storage.DB())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	run, err := store.Load(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if run == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "run " + id + " not found"})
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// handleDriveResume re-enters a stopped/in-progress run. Same fire-
// and-forget pattern as start: returns 202 Accepted immediately and
// runs the loop in a goroutine. Already-terminal runs (Done/Failed)
// return 409 Conflict instead of silently re-running them.
func (s *Server) handleDriveResume(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id is required"})
		return
	}
	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not initialized"})
		return
	}
	runner := s.engine.NewDriveRunner()
	if runner == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "drive runner unavailable"})
		return
	}
	store, err := drive.NewStore(s.engine.Storage.DB())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	// Quick existence/status check before kicking off the goroutine
	// — that way the caller gets a useful HTTP code (404 / 409)
	// instead of a silent no-op.
	existing, err := store.Load(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if existing == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "run " + id + " not found"})
		return
	}
	if drive.IsActive(id) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  "run already active",
			"status": string(existing.Status),
		})
		return
	}
	if existing.Status == drive.RunDone || existing.Status == drive.RunFailed {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":  "run already terminal",
			"status": string(existing.Status),
		})
		return
	}
	publisher := drive.Publisher(func(typ string, payload map[string]any) {
		s.engine.PublishDriveEvent(typ, payload)
	})
	driver := drive.NewDriver(runner, store, publisher, drive.Config{})
	driver.SetReportDir(s.engine.DriveReportDir())
	s.engine.StartBackgroundTask("web.drive.resume", func(ctx context.Context) {
		_, _ = driver.Resume(ctx, id)
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"resumed": id})
}

// handleDriveStop signals an active drive run to cancel. Returns
// 200 with {"cancelled": true} when the registry had a live cancel
// func for the ID, 404 when the ID isn't active in this process
// (already done, wrong ID, or running in a different dfmc
// process — though the SQLite lock makes that last case
// architecturally impossible).
//
// The driver doesn't return synchronously from cancellation — its
// loop finalizes via drainAndFinalize before publishing
// drive:run:stopped. Callers should subscribe to /ws if they need
// the post-cancel run record; polling GET /api/v1/drive/{id} works
// too, just with worse latency.
func (s *Server) handleDriveStop(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id is required"})
		return
	}
	if !drive.IsActive(id) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": "no active drive run with id " + id + " in this process",
			"hint":  "the run may have already finished; GET /api/v1/drive/" + id + " for the persisted state",
		})
		return
	}
	cancelled := drive.Cancel(id)
	writeJSON(w, http.StatusOK, map[string]any{
		"run_id":    id,
		"cancelled": cancelled,
	})
}

// handleDriveActive returns the list of currently-running drive
// runs in this process. Useful for a workbench dashboard that
// needs to render an "in flight" panel without polling every run.
func (s *Server) handleDriveActive(w http.ResponseWriter, _ *http.Request) {
	active := drive.ListActive()
	if active == nil {
		active = []drive.ActiveRun{}
	}
	writeJSON(w, http.StatusOK, active)
}

// handleDriveDelete is idempotent — deleting a missing run still
// returns 200. Useful for cleanup automation that doesn't want to
// special-case the "already gone" outcome.
func (s *Server) handleDriveDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id is required"})
		return
	}
	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not initialized"})
		return
	}
	store, err := drive.NewStore(s.engine.Storage.DB())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if err := store.Delete(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}
