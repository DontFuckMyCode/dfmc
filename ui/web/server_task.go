// Task HTTP endpoints — CRUD for the bbolt-backed task store.
// These expose the task store (independent of drive runs) so callers can
// inspect, create, and update tasks without starting a full drive run.
//
// GET    /api/v1/tasks            list tasks (?parent_id=&state=&run_id=&limit=&offset=)
// POST   /api/v1/tasks            create a task
// GET    /api/v1/tasks/{id}       show one task
// PATCH  /api/v1/tasks/{id}       partial update
// DELETE /api/v1/tasks/{id}       delete a task

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

// TaskCreateRequest is the POST body for creating a task.
type TaskCreateRequest struct {
	ID            string   `json:"id,omitempty"`
	ParentID      string   `json:"parent_id,omitempty"`
	Origin        string   `json:"origin,omitempty"`
	Title         string   `json:"title"`
	Detail        string   `json:"detail,omitempty"`
	State         string   `json:"state,omitempty"`
	DependsOn     []string `json:"depends_on,omitempty"`
	FileScope     []string `json:"file_scope,omitempty"`
	ReadOnly      bool     `json:"read_only,omitempty"`
	ProviderTag   string   `json:"provider_tag,omitempty"`
	WorkerClass   string   `json:"worker_class,omitempty"`
	Skills        []string `json:"skills,omitempty"`
	AllowedTools  []string `json:"allowed_tools,omitempty"`
	Labels        []string `json:"labels,omitempty"`
	Verification  string   `json:"verification,omitempty"`
	Confidence    float64  `json:"confidence,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	BlockedReason string   `json:"blocked_reason,omitempty"`
}

// handleTaskList returns tasks from the task store, filtered by query params.
func (s *Server) handleTaskList(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not initialized"})
		return
	}
	store := s.engine.Tools.TaskStore()
	if store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "task store not initialized"})
		return
	}
	opts := taskstore.ListOptions{
		ParentID: strings.TrimSpace(r.URL.Query().Get("parent_id")),
		RunID:    strings.TrimSpace(r.URL.Query().Get("run_id")),
		State:    strings.TrimSpace(r.URL.Query().Get("state")),
		Label:    strings.TrimSpace(r.URL.Query().Get("label")),
	}
	if lim := r.URL.Query().Get("limit"); lim != "" {
		if n, err := strconv.Atoi(lim); err == nil && n > 0 {
			opts.Limit = n
		}
	}
	// VULN-042: cap limit to taskListLimitMax so a caller cannot
	// request unlimited rows and exhaust memory.
	if opts.Limit > taskListLimitMax {
		opts.Limit = taskListLimitMax
	}
	if off := r.URL.Query().Get("offset"); off != "" {
		if n, err := strconv.Atoi(off); err == nil && n >= 0 {
			opts.Offset = n
		}
	}
	tasks, err := store.ListTasks(opts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if tasks == nil {
		tasks = []*supervisor.Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

// handleTaskCreate persists a new task to the store.
func (s *Server) handleTaskCreate(w http.ResponseWriter, r *http.Request) {
	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not initialized"})
		return
	}
	store := s.engine.Tools.TaskStore()
	if store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "task store not initialized"})
		return
	}
	var req TaskCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "title is required"})
		return
	}
	// VULN-033: id is server-generated; reject client-supplied values.
	if id := strings.TrimSpace(req.ID); id != "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id is server-generated; do not supply it in the request body"})
		return
	}
	id := taskstore.NewTaskID()
	task := supervisor.Task{
		ID:            id,
		ParentID:      strings.TrimSpace(req.ParentID),
		Origin:        strings.TrimSpace(req.Origin),
		Title:         strings.TrimSpace(req.Title),
		Detail:        strings.TrimSpace(req.Detail),
		State:         supervisor.TaskState(strings.TrimSpace(req.State)),
		DependsOn:     append([]string(nil), req.DependsOn...),
		FileScope:     append([]string(nil), req.FileScope...),
		ReadOnly:      req.ReadOnly,
		ProviderTag:   strings.TrimSpace(req.ProviderTag),
		WorkerClass:   supervisor.WorkerClass(strings.TrimSpace(req.WorkerClass)),
		Skills:        append([]string(nil), req.Skills...),
		AllowedTools:  append([]string(nil), req.AllowedTools...),
		Labels:        append([]string(nil), req.Labels...),
		Verification:  supervisor.VerificationStatus(strings.TrimSpace(req.Verification)),
		Confidence:    req.Confidence,
		Summary:       strings.TrimSpace(req.Summary),
		BlockedReason: strings.TrimSpace(req.BlockedReason),
	}
	if err := store.SaveTask(&task); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "save failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

// handleTaskShow returns one task by ID.
func (s *Server) handleTaskShow(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not initialized"})
		return
	}
	store := s.engine.Tools.TaskStore()
	if store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "task store not initialized"})
		return
	}
	task, err := store.LoadTask(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if task == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "task " + id + " not found"})
		return
	}
	// Emit ETag so clients can pass the version back via If-Match on a
	// later PATCH without inferring it from the JSON body.
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, task.Version))
	writeJSON(w, http.StatusOK, task)
}

// handleTaskUpdate (PATCH) + findTaskAncestor + stringField/cleanStringSlice
// helpers live in server_task_update.go.

// handleTaskDelete removes a task from the store.
func (s *Server) handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if s.engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "engine not initialized"})
		return
	}
	store := s.engine.Tools.TaskStore()
	if store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "task store not initialized"})
		return
	}
	if err := store.DeleteTask(id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

