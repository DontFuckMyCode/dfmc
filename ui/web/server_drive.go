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
	"strconv"
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
	AutoSurvey     bool              `json:"auto_survey,omitempty"`
	AutoVerify     bool              `json:"auto_verify,omitempty"`
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
	// VULN-034: cap max_parallel and auto_approve to prevent memory exhaustion
	// from maliciously large values.
	if req.MaxParallel > 1000 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":       "max_parallel exceeds maximum allowed (1000)",
			"max_allowed": 1000,
		})
		return
	}
	if len(req.AutoApprove) > 5000 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":       "auto_approve length exceeds maximum allowed (5000)",
			"max_allowed": 5000,
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
		AutoSurvey:     req.AutoSurvey,
		AutoVerify:     req.AutoVerify,
		PlannerModel:   req.PlannerModel,
		Routing:        req.Routing,
		AutoApprove:    req.AutoApprove,
	}
	publisher := drive.Publisher(func(typ string, payload map[string]any) {
		s.engine.PublishDriveEvent(typ, payload)
	})
	driver := drive.NewDriver(runner, store, publisher, cfg)
	driver.SetReportDir(s.engine.DriveReportDir())
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
// CLI's `--json` output (slice of *drive.Run). Unbounded — callers
// can pass ?limit=N but the store returns all rows if limit is 0,
// so we cap here to prevent memory exhaustion (VULN-042).
func (s *Server) handleDriveList(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not initialized"})
		return
	}
	store, err := drive.NewStore(s.engine.Storage.DB())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	limit := 200
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = min(n, 1000)
		}
	}
	runs, err := store.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if runs == nil {
		runs = []*drive.Run{} // emit [] not null so clients don't have to handle both
	}
	if len(runs) > limit {
		runs = runs[:limit]
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleDriveShow / handleDriveResume / handleDriveStop /
// handleDriveActive / handleDriveDelete live in server_drive_runs.go.
