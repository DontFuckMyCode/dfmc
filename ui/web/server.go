package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type Server struct {
	engine *engine.Engine
	mux    *http.ServeMux
	addr   string
}

type ChatRequest struct {
	Message string `json:"message"`
}

type AnalyzeRequest struct {
	Path       string `json:"path"`
	Full       bool   `json:"full"`
	Security   bool   `json:"security"`
	Complexity bool   `json:"complexity"`
	DeadCode   bool   `json:"dead_code"`
}

type ToolExecRequest struct {
	Params map[string]any `json:"params"`
}

func New(eng *engine.Engine, host string, port int) *Server {
	s := &Server{
		engine: eng,
		mux:    http.NewServeMux(),
		addr:   fmt.Sprintf("%s:%d", host, port),
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	s.mux.HandleFunc("POST /api/v1/chat", s.handleChat)
	s.mux.HandleFunc("GET /api/v1/codemap", s.handleCodeMap)
	s.mux.HandleFunc("GET /api/v1/providers", s.handleProviders)
	s.mux.HandleFunc("GET /api/v1/skills", s.handleSkills)
	s.mux.HandleFunc("GET /api/v1/tools", s.handleTools)
	s.mux.HandleFunc("POST /api/v1/tools/{name}", s.handleToolExec)
	s.mux.HandleFunc("POST /api/v1/skills/{name}", s.handleSkillExec)
	s.mux.HandleFunc("POST /api/v1/analyze", s.handleAnalyze)
	s.mux.HandleFunc("GET /api/v1/memory", s.handleMemory)
	s.mux.HandleFunc("GET /api/v1/files", s.handleFiles)
	s.mux.HandleFunc("GET /api/v1/files/{path...}", s.handleFileContent)
	s.mux.HandleFunc("GET /ws", s.handleWebSocket)
}

func (s *Server) Start() error {
	fmt.Printf("DFMC Web API listening on http://%s\n", s.addr)
	return http.ListenAndServe(s.addr, s.mux)
}

func (s *Server) Handler() http.Handler {
	return s.mux
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html>
<html>
<head><meta charset="utf-8"><title>DFMC</title></head>
<body style="font-family: ui-monospace, SFMono-Regular, Menlo, monospace; padding: 24px;">
<h2>DFMC Web API</h2>
<p>Use <code>/api/v1/status</code>, <code>/api/v1/chat</code>, <code>/api/v1/codemap</code>, <code>/api/v1/files</code>.</p>
</body>
</html>`))
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Status())
}

func (s *Server) handleCodeMap(w http.ResponseWriter, _ *http.Request) {
	if s.engine.CodeMap == nil || s.engine.CodeMap.Graph() == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"nodes": []any{},
			"edges": []any{},
		})
		return
	}
	graph := s.engine.CodeMap.Graph()
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": graph.Nodes(),
		"edges": graph.Edges(),
	})
}

func (s *Server) handleTools(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"tools": s.engine.ListTools(),
	})
}

func (s *Server) handleProviders(w http.ResponseWriter, _ *http.Request) {
	status := s.engine.Status()
	names := make([]string, 0, len(s.engine.Config.Providers.Profiles)+1)
	seen := map[string]struct{}{}
	for name := range s.engine.Config.Providers.Profiles {
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if _, ok := seen["offline"]; !ok {
		names = append(names, "offline")
	}
	sort.Strings(names)

	items := make([]map[string]any, 0, len(names))
	for _, name := range names {
		item := map[string]any{
			"name":   name,
			"active": strings.EqualFold(name, status.Provider),
		}
		if prof, ok := s.engine.Config.Providers.Profiles[name]; ok {
			item["model"] = prof.Model
			item["configured"] = strings.TrimSpace(prof.APIKey) != "" || strings.TrimSpace(prof.BaseURL) != ""
		} else {
			item["configured"] = name == "offline"
		}
		items = append(items, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"current_provider": status.Provider,
		"current_model":    status.Model,
		"providers":        items,
	})
}

func (s *Server) handleSkills(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"skills": []any{},
	})
}

func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	tier := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("tier")))
	limit := 50
	if tier == "working" {
		writeJSON(w, http.StatusOK, s.engine.MemoryWorking())
		return
	}
	memTier := types.MemoryEpisodic
	if tier == "semantic" {
		memTier = types.MemorySemantic
	}
	items, err := s.engine.MemoryList(memTier, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	root := s.engine.Status().ProjectRoot
	if strings.TrimSpace(root) == "" {
		writeJSON(w, http.StatusOK, map[string]any{"root": "", "files": []any{}})
		return
	}
	limit := 500
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	files, err := listFiles(root, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"root":  filepath.ToSlash(root),
		"files": files,
	})
}

func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	root := s.engine.Status().ProjectRoot
	if strings.TrimSpace(root) == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "project root is not set"})
		return
	}
	rel := strings.TrimSpace(r.PathValue("path"))
	if rel == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "path is required"})
		return
	}
	target, err := resolvePathWithinRoot(root, rel)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}

	info, err := os.Stat(target)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]any{"error": err.Error()})
		return
	}
	if info.IsDir() {
		entries, err := os.ReadDir(target)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		items := make([]string, 0, len(entries))
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			items = append(items, name)
		}
		sort.Strings(items)
		writeJSON(w, http.StatusOK, map[string]any{
			"path":    filepath.ToSlash(rel),
			"type":    "dir",
			"entries": items,
		})
		return
	}

	data, err := os.ReadFile(target)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    filepath.ToSlash(rel),
		"type":    "file",
		"size":    len(data),
		"content": string(data),
	})
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	req := AnalyzeRequest{}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	report, err := s.engine.AnalyzeWithOptions(r.Context(), engine.AnalyzeOptions{
		Path:       req.Path,
		Full:       req.Full,
		Security:   req.Security,
		Complexity: req.Complexity,
		DeadCode:   req.DeadCode,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleToolExec(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "tool name is required"})
		return
	}
	req := ToolExecRequest{Params: map[string]any{}}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	if req.Params == nil {
		req.Params = map[string]any{}
	}
	res, err := s.engine.CallTool(r.Context(), name, req.Params)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleSkillExec(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"error": fmt.Sprintf("skill execution is not implemented yet: %s", name),
	})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"error": "websocket endpoint is not implemented yet",
	})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming not supported"})
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "message is required"})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	stream, err := s.engine.StreamAsk(r.Context(), req.Message)
	if err != nil {
		writeSSE(w, flusher, map[string]any{
			"type":  "error",
			"error": err.Error(),
		})
		return
	}

	for ev := range stream {
		switch ev.Type {
		case provider.StreamDelta:
			writeSSE(w, flusher, map[string]any{
				"type":  "delta",
				"delta": ev.Delta,
			})
		case provider.StreamError:
			writeSSE(w, flusher, map[string]any{
				"type":  "error",
				"error": ev.Err.Error(),
			})
			return
		case provider.StreamDone:
			writeSSE(w, flusher, map[string]any{
				"type": "done",
				"ts":   time.Now().UTC().Format(time.RFC3339),
			})
			return
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, payload any) {
	data, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func listFiles(root string, limit int) ([]string, error) {
	out := make([]string, 0, limit)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".dfmc", "node_modules", "vendor", "dist", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, filepath.ToSlash(rel))
		if len(out) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}
	return out, nil
}

func resolvePathWithinRoot(root, rel string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := rel
	if !filepath.IsAbs(target) {
		target = filepath.Join(absRoot, rel)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root")
	}
	return absTarget, nil
}
