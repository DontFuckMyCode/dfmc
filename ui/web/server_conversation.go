// Conversation management handlers for the web API. Extracted from server.go
// to keep the construction/wiring lean. List/search/load/save/undo plus the
// branch family all live here because they share the conversation lifecycle
// semantics exposed via `dfmc conversation` on the CLI.

package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := s.engine.ConversationList()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"count":         len(items),
		"conversations": items,
	})
}

func (s *Server) handleConversationSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	limit := 20
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := s.engine.ConversationSearch(query, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"query":         query,
		"count":         len(items),
		"conversations": items,
	})
}

func (s *Server) handleConversationActive(w http.ResponseWriter, _ *http.Request) {
	active := s.engine.ConversationActive()
	if active == nil {
		writeJSON(w, http.StatusOK, map[string]any{"active": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         active.ID,
		"provider":   active.Provider,
		"model":      active.Model,
		"started_at": active.StartedAt,
		"branch":     active.Branch,
		"branches":   s.engine.ConversationBranchList(),
		"messages":   len(active.Messages()),
	})
}

func (s *Server) handleConversationNew(w http.ResponseWriter, _ *http.Request) {
	c := s.engine.ConversationStart()
	if c == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to start conversation"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         c.ID,
		"provider":   c.Provider,
		"model":      c.Model,
		"started_at": c.StartedAt,
		"branch":     c.Branch,
		"messages":   len(c.Messages()),
	})
}

func (s *Server) handleConversationSave(w http.ResponseWriter, _ *http.Request) {
	if err := s.engine.ConversationSave(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) handleConversationLoad(w http.ResponseWriter, r *http.Request) {
	req := ConversationLoadRequest{}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	req.ID = strings.TrimSpace(req.ID)
	if req.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "id is required"})
		return
	}
	c, err := s.engine.ConversationLoad(req.ID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":         c.ID,
		"provider":   c.Provider,
		"model":      c.Model,
		"started_at": c.StartedAt,
		"branch":     c.Branch,
		"messages":   len(c.Messages()),
	})
}

func (s *Server) handleConversationUndo(w http.ResponseWriter, _ *http.Request) {
	removed, err := s.engine.ConversationUndoLast()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"removed": removed,
	})
}

func (s *Server) handleConversationBranches(w http.ResponseWriter, _ *http.Request) {
	if s.engine.ConversationActive() == nil {
		_ = s.engine.ConversationStart()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"branches": s.engine.ConversationBranchList(),
	})
}

func (s *Server) handleConversationBranchCreate(w http.ResponseWriter, r *http.Request) {
	if s.engine.ConversationActive() == nil {
		_ = s.engine.ConversationStart()
	}
	req := ConversationBranchRequest{}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "branch name is required"})
		return
	}
	if err := s.engine.ConversationBranchCreate(name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"branch": name,
	})
}

func (s *Server) handleConversationBranchSwitch(w http.ResponseWriter, r *http.Request) {
	if s.engine.ConversationActive() == nil {
		_ = s.engine.ConversationStart()
	}
	req := ConversationBranchRequest{}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "branch name is required"})
		return
	}
	if err := s.engine.ConversationBranchSwitch(name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"branch": name,
	})
}

func (s *Server) handleConversationBranchCompare(w http.ResponseWriter, r *http.Request) {
	a := strings.TrimSpace(r.URL.Query().Get("a"))
	b := strings.TrimSpace(r.URL.Query().Get("b"))
	if a == "" || b == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "query parameters a and b are required"})
		return
	}
	comp, err := s.engine.ConversationBranchCompare(a, b)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, comp)
}
