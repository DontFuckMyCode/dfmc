// Workspace patch/diff handlers for the web API. Extracted from server.go
// to keep the construction/wiring lean. Diff/patch/apply endpoints share the
// same unified-diff parsing and `git` shell helpers, which live in
// server_workspace_diff.go (gitWorkingDiffWeb / gitChangedFilesWeb /
// latestAssistantUnifiedDiffWeb / extractUnifiedDiffWeb /
// looksLikeUnifiedDiffWeb / applyUnifiedDiffWeb / pathsFromUnifiedDiff /
// assertPathWithinRoot / absPathNoClean). This file holds only the three
// HTTP handler entry points.

package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func (s *Server) handleWorkspaceDiff(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(s.engine.Status().ProjectRoot)
	if root == "" {
		root = "."
	}
	diff, err := gitWorkingDiffWeb(root, 200_000)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	changed, err := gitChangedFilesWeb(r.Context(), root, 24)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"diff":          diff,
		"clean":         strings.TrimSpace(diff) == "",
		"changed_files": changed,
	})
}

func (s *Server) handleWorkspacePatch(w http.ResponseWriter, _ *http.Request) {
	patch := latestAssistantUnifiedDiffWeb(s.engine.ConversationActive())
	writeJSON(w, http.StatusOK, map[string]any{
		"patch":     patch,
		"available": strings.TrimSpace(patch) != "",
	})
}

func (s *Server) handleWorkspaceApply(w http.ResponseWriter, r *http.Request) {
	req := WorkspaceApplyRequest{}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	patch := strings.TrimSpace(req.Patch)
	if patch == "" && strings.EqualFold(strings.TrimSpace(req.Source), "latest") {
		patch = latestAssistantUnifiedDiffWeb(s.engine.ConversationActive())
	}
	if patch == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "patch is required or source=latest must resolve to an assistant diff"})
		return
	}
	root := strings.TrimSpace(s.engine.Status().ProjectRoot)
	if root == "" {
		root = "."
	}
	// VULN-009: check_only dry-run is read-only — bypass the approval gate
	// by using the local apply path directly (same containment checks apply).
	// Mutation calls go through CallToolFromSource so the gate fires for
	// non-user sources (web/ws/mcp) as required.
	if req.CheckOnly {
		if err := applyUnifiedDiffWeb(root, patch, true); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"status":     "error",
				"check_only": true,
				"error":      err.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"check_only": true,
			"valid":      true,
		})
		return
	}
	// Route through the engine so the full lifecycle fires:
	// approval gate, pre/post hooks, EnsureReadBeforeMutation.
	toolResult, err := s.engine.CallToolFromSource(r.Context(), "apply_patch", map[string]any{
		"patch":   patch,
		"dry_run": false,
	}, engine.SourceWeb)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, " denied:") {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": errStr})
		} else {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": errStr})
		}
		return
	}
	if req.CheckOnly {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"check_only": true,
			"valid":      toolResult.Output != "" && !strings.Contains(toolResult.Output, "FAIL"),
		})
		return
	}
	// Extract changed files from the tool result data.
	var changed []string
	if files, ok := toolResult.Data["files"].([]map[string]any); ok {
		for _, f := range files {
			if p, ok := f["path"].(string); ok {
				if err2 := f["error"]; err2 == nil {
					changed = append(changed, p)
				}
			}
		}
	}
	if len(changed) == 0 {
		changed, err = gitChangedFilesWeb(r.Context(), root, 24)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"check_only":    false,
		"changed_files": changed,
	})
}
