// Drive HTTP endpoints — fire/list/show/resume/delete autonomous
// runs. Mirrors the CLI surface (`dfmc drive ...`) so a remote
// client can drive an unattended dfmc instance over HTTP. Live
// progress arrives via the existing `/ws` event stream — drive:*
// events are published through engine.EventBus by the driver and
// flow out the same WebSocket subscribers already use.
//
// POST /api/v1/drive            start a new run
// GET  /api/v1/drive            list past runs
// GET  /api/v1/drive/{id}       show one run's record
// POST /api/v1/drive/{id}/resume  re-enter a stopped/in-progress run
// DELETE /api/v1/drive/{id}     delete a run record
//
// Concurrency: POST /drive returns immediately with the run record
// (status=planning) and runs the driver in a goroutine. The caller
// polls GET /drive/{id} or subscribes to /ws for live updates.
//
// Auth: the package-level Handler can wrap these routes in bearer-token
// auth when web.auth=token is configured by the host surface.

package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// DriveStartRequest mirrors `dfmc drive` flags so an HTTP client can
// configure a run without round-tripping through the CLI. Empty
// fields fall back to drive.DefaultConfig — same as the CLI.
type DriveStartRequest struct {
	Task           string            `json:"task"`
	MaxTodos       int               `json:"max_todos,omitempty"`
	MaxFailedTodos int               `json:"max_failed_todos,omitempty"`
	MaxWallTimeMs  int64             `json:"max_wall_time_ms,omitempty"`
	Retries        int               `json:"retries,omitempty"`
	MaxParallel    int               `json:"max_parallel,omitempty"`
	PlannerModel   string            `json:"planner_model,omitempty"`
	Routing        map[string]string `json:"routing,omitempty"`
	AutoApprove    []string          `json:"auto_approve,omitempty"`
}

// handleDriveStart accepts a task, builds a Driver against the
// engine, and runs it in a background goroutine. Returns the run ID
// immediately after persisting the planning stub so the caller can poll
// or subscribe before the planner finishes. Validation errors return
// 400 with a specific message; engine wiring failures return 500.
func (s *Server) handleDriveStart(w http.ResponseWriter, r *http.Request) {
	var req DriveStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Task) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "task is required",
			"hint":  `POST {"task":"add input validation to /api/users"}`,
		})
		return
	}
	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not initialized"})
		return
	}
	runner := s.engine.NewDriveRunner()
	if runner == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "drive runner unavailable (provider router missing)"})
		return
	}
	store, err := drive.NewStore(s.engine.Storage.DB())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "drive store init: " + err.Error()})
		return
	}
	cfg := drive.Config{
		MaxTodos:       req.MaxTodos,
		MaxFailedTodos: req.MaxFailedTodos,
		MaxWallTime:    time.Duration(req.MaxWallTimeMs) * time.Millisecond,
		Retries:        req.Retries,
		MaxParallel:    req.MaxParallel,
		PlannerModel:   req.PlannerModel,
		Routing:        req.Routing,
		AutoApprove:    req.AutoApprove,
	}
	publisher := drive.Publisher(func(typ string, payload map[string]any) {
		s.engine.PublishDriveEvent(typ, payload)
	})
	driver := drive.NewDriver(runner, store, publisher, cfg)
	run, err := drive.NewRun(req.Task)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := store.Save(run); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "persist run: " + err.Error()})
		return
	}

	// Fire-and-forget off the request path, but keep the run tied to the
	// engine lifecycle so Shutdown cancels it before Storage is closed.
	s.engine.StartBackgroundTask("web.drive.run", func(ctx context.Context) {
		_, _ = driver.RunPrepared(ctx, run)
	})

	// Return a persisted planning stub immediately. We do NOT wait for the
	// planner to finish — that can take seconds under slow providers.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"started": true,
		"run_id":  run.ID,
		"hint":    "subscribe to /ws for drive:* events, or poll GET /api/v1/drive for the run record once the planner publishes drive:plan:done",
	})
}

// handleDriveList returns every persisted run, newest first. Used
// by the workbench's drive history panel. JSON shape matches the
// CLI's `--json` output (slice of *drive.Run).
func (s *Server) handleDriveList(w http.ResponseWriter, _ *http.Request) {
	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not initialized"})
		return
	}
	store, err := drive.NewStore(s.engine.Storage.DB())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	runs, err := store.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if runs == nil {
		runs = []*drive.Run{} // emit [] not null so clients don't have to handle both
	}
	writeJSON(w, http.StatusOK, runs)
}

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
	s.engine.StartBackgroundTask("web.drive.resume", func(ctx context.Context) {
		_, _ = driver.Resume(ctx, id)
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"resumed": id})
}

// handleDriveStop signals an active drive run to cancel. Returns
// 200 with {"cancelled": true} when the registry had a live cancel
// func for the ID, 404 when the ID isn't active in this process
// (already done, wrong ID, or running in a different dfmc
// process — though the bbolt lock makes that last case
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
