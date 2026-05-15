package web

// server_task_update.go — the heavy PATCH /api/v1/tasks/{id} handler:
// optimistic-concurrency If-Match routing, the per-field patch
// mutator, parent-cycle detection, and the small JSON field
// coercers it depends on.
//
// Sibling of server_task.go which keeps TaskCreateRequest plus the
// list/show/create/delete handlers.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

// errTaskValidation is wrapped around mutator-side validation errors
// (unknown state, self-reparent, descendant cycle) so the outer
// handler can route them to 400 via errors.Is instead of grepping
// the error message it just raised.
var errTaskValidation = errors.New("task validation")

// handleTaskUpdate applies a partial update to an existing task.
func (s *Server) handleTaskUpdate(w http.ResponseWriter, r *http.Request) {
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
	var patch map[string]any
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	// Optimistic concurrency: when the client passes If-Match: <version>,
	// route the write through UpdateTaskCAS so a stale version returns
	// 412 Precondition Failed instead of silently overwriting concurrent
	// edits. Bbolt's per-key transaction already serializes the closure,
	// but a client that read at version N and submits at version N+M
	// without realising deserves a clear refusal, not last-writer-wins.
	// Without the header, behaviour is unchanged.
	ifMatch := strings.TrimSpace(r.Header.Get("If-Match"))
	expectedVersion := -1
	if ifMatch != "" {
		ifMatch = strings.Trim(ifMatch, `"`) // permit ETag-style quoted form
		v, parseErr := strconv.Atoi(ifMatch)
		if parseErr != nil || v < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "If-Match must be a non-negative integer version"})
			return
		}
		expectedVersion = v
	}
	mutator := func(t *supervisor.Task) error {
		if v, ok := patch["title"]; ok {
			t.Title = strings.TrimSpace(stringField(v))
		}
		if v, ok := patch["detail"]; ok {
			t.Detail = strings.TrimSpace(stringField(v))
		}
		if v, ok := patch["state"]; ok {
			s := strings.TrimSpace(stringField(v))
			// VULN-032: reject unknown state strings before writing.
			switch supervisor.TaskState(s) {
			case supervisor.TaskPending, supervisor.TaskRunning, supervisor.TaskDone,
				supervisor.TaskBlocked, supervisor.TaskSkipped,
				supervisor.TaskVerifying, supervisor.TaskWaiting,
				supervisor.TaskExternalReview:
				t.State = supervisor.TaskState(s)
			default:
				return fmt.Errorf("%w: unknown state %q; valid values: pending, running, done, blocked, skipped, verifying, waiting, external_review", errTaskValidation, s)
			}
		}
		if v, ok := patch["summary"]; ok {
			t.Summary = strings.TrimSpace(stringField(v))
		}
		if v, ok := patch["error"]; ok {
			t.Error = strings.TrimSpace(stringField(v))
		}
		if v, ok := patch["confidence"]; ok {
			if f, ok := v.(float64); ok {
				t.Confidence = f
			}
		}
		if v, ok := patch["blocked_reason"]; ok {
			t.BlockedReason = strings.TrimSpace(stringField(v))
		}
		if v, ok := patch["labels"]; ok {
			t.Labels = cleanStringSlice(v)
		}
		if v, ok := patch["parent_id"]; ok {
			t.ParentID = strings.TrimSpace(stringField(v))
		}
		// VULN-032: detect self-reparent and descendant cycles.
		if t.ParentID != "" {
			if t.ParentID == t.ID {
				return fmt.Errorf("%w: cannot set parent_id to own task id (self-reparent)", errTaskValidation)
			}
			if ancestor, found := findTaskAncestor(store, t.ID, t.ParentID); found {
				return fmt.Errorf("%w: cannot set parent_id=%q: would create a cycle (found ancestor %q)", errTaskValidation, t.ParentID, ancestor)
			}
		}
		return nil
	}
	var err error
	if expectedVersion >= 0 {
		err = store.UpdateTaskCAS(id, expectedVersion, mutator)
		if errors.Is(err, taskstore.ErrTaskVersionConflict) {
			writeJSON(w, http.StatusPreconditionFailed, map[string]any{
				"error":            "If-Match version is stale; reload the task and retry",
				"expected_version": expectedVersion,
			})
			return
		}
	} else {
		err = store.UpdateTask(id, mutator)
	}
	if err != nil {
		if errors.Is(err, taskstore.ErrTaskNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "task " + id + " not found"})
			return
		}
		// VULN-032: validation errors (unknown state, self-reparent, cycles)
		// are client mistakes → 400, not 500. Detected via the
		// errTaskValidation sentinel the mutator wraps onto its return.
		if errors.Is(err, errTaskValidation) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	updated, _ := store.LoadTask(id)
	if updated != nil {
		// Emit the post-write version as an ETag so a follow-up PATCH
		// can pass it back via If-Match without a separate GET round-trip.
		w.Header().Set("ETag", fmt.Sprintf(`"%d"`, updated.Version))
	}
	writeJSON(w, http.StatusOK, updated)
}

// findTaskAncestor walks the ancestor chain from candidateParent upward.
// Returns (ancestorID, true) if targetID is found as an ancestor, which
// means setting candidateParent as the parent of targetID would create a cycle.
func findTaskAncestor(store *taskstore.Store, targetID, candidateParent string) (string, bool) {
	visited := make(map[string]bool)
	current := candidateParent
	for current != "" && !visited[current] {
		visited[current] = true
		task, err := store.LoadTask(current)
		if err != nil || task == nil {
			return "", false
		}
		if task.ParentID == "" {
			return "", false
		}
		if task.ParentID == targetID {
			return current, true
		}
		current = task.ParentID
	}
	return "", false
}

func stringField(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func cleanStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch arr := v.(type) {
	case []string:
		out := make([]string, 0, len(arr))
		for _, s := range arr {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(arr))
		for _, x := range arr {
			if s := strings.TrimSpace(fmt.Sprint(x)); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
