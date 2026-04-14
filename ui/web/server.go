package web

import (
	"context"
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

	"github.com/dontfuckmycode/dfmc/internal/config"
	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
	"gopkg.in/yaml.v3"
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
	MagicDoc   bool   `json:"magicdoc"`

	MagicDocPath     string `json:"magicdoc_path"`
	MagicDocTitle    string `json:"magicdoc_title"`
	MagicDocHotspots int    `json:"magicdoc_hotspots"`
	MagicDocDeps     int    `json:"magicdoc_deps"`
	MagicDocRecent   int    `json:"magicdoc_recent"`
}

type ToolExecRequest struct {
	Params map[string]any `json:"params"`
}

type SkillExecRequest struct {
	Input   string `json:"input"`
	Message string `json:"message"`
}

type ConversationLoadRequest struct {
	ID string `json:"id"`
}

type ConversationBranchRequest struct {
	Name string `json:"name"`
}

type PromptRenderRequest struct {
	Type         string            `json:"type"`
	Task         string            `json:"task"`
	Language     string            `json:"language"`
	Profile      string            `json:"profile"`
	Query        string            `json:"query"`
	ContextFiles string            `json:"context_files"`
	Vars         map[string]string `json:"vars"`
}

type MagicDocUpdateRequest struct {
	Path     string `json:"path"`
	Title    string `json:"title"`
	Hotspots int    `json:"hotspots"`
	Deps     int    `json:"deps"`
	Recent   int    `json:"recent"`
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
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	s.mux.HandleFunc("POST /api/v1/chat", s.handleChat)
	s.mux.HandleFunc("GET /api/v1/codemap", s.handleCodeMap)
	s.mux.HandleFunc("GET /api/v1/context/budget", s.handleContextBudget)
	s.mux.HandleFunc("GET /api/v1/context/brief", s.handleContextBrief)
	s.mux.HandleFunc("GET /api/v1/providers", s.handleProviders)
	s.mux.HandleFunc("GET /api/v1/skills", s.handleSkills)
	s.mux.HandleFunc("GET /api/v1/tools", s.handleTools)
	s.mux.HandleFunc("POST /api/v1/tools/{name}", s.handleToolExec)
	s.mux.HandleFunc("POST /api/v1/skills/{name}", s.handleSkillExec)
	s.mux.HandleFunc("POST /api/v1/analyze", s.handleAnalyze)
	s.mux.HandleFunc("GET /api/v1/memory", s.handleMemory)
	s.mux.HandleFunc("GET /api/v1/conversation", s.handleConversationActive)
	s.mux.HandleFunc("POST /api/v1/conversation/new", s.handleConversationNew)
	s.mux.HandleFunc("POST /api/v1/conversation/save", s.handleConversationSave)
	s.mux.HandleFunc("POST /api/v1/conversation/load", s.handleConversationLoad)
	s.mux.HandleFunc("GET /api/v1/conversation/branches", s.handleConversationBranches)
	s.mux.HandleFunc("POST /api/v1/conversation/branches/create", s.handleConversationBranchCreate)
	s.mux.HandleFunc("POST /api/v1/conversation/branches/switch", s.handleConversationBranchSwitch)
	s.mux.HandleFunc("GET /api/v1/conversation/branches/compare", s.handleConversationBranchCompare)
	s.mux.HandleFunc("GET /api/v1/prompts", s.handlePrompts)
	s.mux.HandleFunc("GET /api/v1/prompts/stats", s.handlePromptStats)
	s.mux.HandleFunc("POST /api/v1/prompts/render", s.handlePromptRender)
	s.mux.HandleFunc("GET /api/v1/magicdoc", s.handleMagicDocShow)
	s.mux.HandleFunc("POST /api/v1/magicdoc/update", s.handleMagicDocUpdate)
	s.mux.HandleFunc("GET /api/v1/conversations", s.handleConversations)
	s.mux.HandleFunc("GET /api/v1/conversations/search", s.handleConversationSearch)
	s.mux.HandleFunc("GET /api/v1/files", s.handleFiles)
	s.mux.HandleFunc("GET /api/v1/files/{path...}", s.handleFileContent)
	s.mux.HandleFunc("GET /ws", s.handleWebSocket)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
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

func (s *Server) handleContextBudget(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	preview := s.engine.ContextBudgetPreview(query)
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleContextBrief(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(s.engine.Status().ProjectRoot)
	if root == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "project root is not set"})
		return
	}
	maxWords := 240
	if raw := strings.TrimSpace(r.URL.Query().Get("max_words")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			maxWords = n
		}
	}
	pathFlag := strings.TrimSpace(r.URL.Query().Get("path"))
	path := resolveMagicDocPath(root, pathFlag)
	raw, err := os.ReadFile(path)
	exists := err == nil
	brief := loadProjectBriefForPromptRender(root, pathFlag, maxWords)
	if brief == "" {
		brief = "(none)"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":       filepath.ToSlash(path),
		"exists":     exists,
		"max_words":  maxWords,
		"word_count": len(strings.Fields(strings.TrimSpace(brief))),
		"brief":      brief,
		"size_bytes": func() int {
			if !exists {
				return 0
			}
			return len(raw)
		}(),
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
	items := []map[string]any{
		{"name": "review", "source": "builtin", "builtin": true},
		{"name": "explain", "source": "builtin", "builtin": true},
		{"name": "refactor", "source": "builtin", "builtin": true},
		{"name": "test", "source": "builtin", "builtin": true},
		{"name": "doc", "source": "builtin", "builtin": true},
	}
	seen := map[string]struct{}{
		"review":   {},
		"explain":  {},
		"refactor": {},
		"test":     {},
		"doc":      {},
	}

	roots := []struct {
		path   string
		source string
	}{
		{path: filepath.Join(s.engine.Status().ProjectRoot, ".dfmc", "skills"), source: "project"},
		{path: filepath.Join(config.UserConfigDir(), "skills"), source: "global"},
	}
	for _, root := range roots {
		files, _ := filepath.Glob(filepath.Join(root.path, "*.y*ml"))
		for _, p := range files {
			name := strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
			key := strings.ToLower(strings.TrimSpace(name))
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			items = append(items, map[string]any{
				"name":    name,
				"source":  root.source,
				"builtin": false,
				"path":    p,
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(fmt.Sprint(items[i]["name"])) < strings.ToLower(fmt.Sprint(items[j]["name"]))
	})
	writeJSON(w, http.StatusOK, map[string]any{"skills": items})
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

func (s *Server) handlePrompts(w http.ResponseWriter, _ *http.Request) {
	lib := promptlib.New()
	_ = lib.LoadOverrides(s.engine.Status().ProjectRoot)
	writeJSON(w, http.StatusOK, map[string]any{
		"prompts": lib.List(),
	})
}

func (s *Server) handlePromptStats(w http.ResponseWriter, r *http.Request) {
	lib := promptlib.New()
	_ = lib.LoadOverrides(s.engine.Status().ProjectRoot)

	maxTemplateTokens := 450
	if raw := strings.TrimSpace(r.URL.Query().Get("max_template_tokens")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			maxTemplateTokens = n
		}
	}
	allowVars := []string{}
	for _, entry := range r.URL.Query()["allow_var"] {
		for _, part := range strings.Split(entry, ",") {
			if p := strings.TrimSpace(part); p != "" {
				allowVars = append(allowVars, p)
			}
		}
	}

	report := promptlib.BuildStatsReport(lib.List(), promptlib.StatsOptions{
		MaxTemplateTokens: maxTemplateTokens,
		AllowVars:         allowVars,
	})
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handlePromptRender(w http.ResponseWriter, r *http.Request) {
	req := PromptRenderRequest{
		Type:     "system",
		Task:     "auto",
		Language: "auto",
		Profile:  "auto",
		Vars:     map[string]string{},
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	if strings.TrimSpace(req.Type) == "" {
		req.Type = "system"
	}
	resolvedTask := strings.TrimSpace(req.Task)
	if strings.EqualFold(resolvedTask, "auto") || resolvedTask == "" {
		resolvedTask = promptlib.DetectTask(req.Query)
	}
	resolvedLang := strings.TrimSpace(req.Language)
	if strings.EqualFold(resolvedLang, "auto") || resolvedLang == "" {
		resolvedLang = promptlib.InferLanguage(req.Query, nil)
	}
	resolvedProfile := strings.TrimSpace(req.Profile)
	if strings.EqualFold(resolvedProfile, "auto") || resolvedProfile == "" {
		resolvedProfile = "compact"
		q := strings.ToLower(strings.TrimSpace(req.Query))
		if strings.Contains(q, "detaylı") || strings.Contains(q, "detailed") || strings.Contains(q, "deep") || resolvedTask == "security" || resolvedTask == "review" || resolvedTask == "planning" {
			resolvedProfile = "deep"
		}
	}
	runtimeHints := s.engine.PromptRuntime()
	vars := map[string]string{
		"project_root":     s.engine.Status().ProjectRoot,
		"task":             resolvedTask,
		"language":         resolvedLang,
		"profile":          resolvedProfile,
		"project_brief":    loadProjectBriefForPromptRender(s.engine.Status().ProjectRoot, "", 240),
		"user_query":       strings.TrimSpace(req.Query),
		"context_files":    strings.TrimSpace(req.ContextFiles),
		"injected_context": "(none)",
		"tools_overview":   strings.Join(s.engine.ListTools(), ", "),
		"tool_call_policy": ctxmgr.BuildToolCallPolicy(resolvedTask, runtimeHints),
		"response_policy":  ctxmgr.BuildResponsePolicy(resolvedTask, resolvedProfile),
	}
	for k, v := range req.Vars {
		vars[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}

	lib := promptlib.New()
	_ = lib.LoadOverrides(s.engine.Status().ProjectRoot)
	prompt := lib.Render(promptlib.RenderRequest{
		Type:     req.Type,
		Task:     resolvedTask,
		Language: resolvedLang,
		Profile:  resolvedProfile,
		Vars:     vars,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"type":     req.Type,
		"task":     resolvedTask,
		"language": resolvedLang,
		"profile":  resolvedProfile,
		"vars":     vars,
		"prompt":   prompt,
	})
}

func (s *Server) handleMagicDocShow(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(s.engine.Status().ProjectRoot)
	if root == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "project root is not set"})
		return
	}
	target := resolveMagicDocPath(root, strings.TrimSpace(r.URL.Query().Get("path")))
	data, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusOK, map[string]any{
				"path":    filepath.ToSlash(target),
				"exists":  false,
				"content": "",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    filepath.ToSlash(target),
		"exists":  true,
		"content": string(data),
	})
}

func (s *Server) handleMagicDocUpdate(w http.ResponseWriter, r *http.Request) {
	root := strings.TrimSpace(s.engine.Status().ProjectRoot)
	if root == "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "project root is not set"})
		return
	}
	req := MagicDocUpdateRequest{
		Title:    "DFMC Project Brief",
		Hotspots: 8,
		Deps:     8,
		Recent:   5,
	}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	out, err := s.updateMagicDoc(r.Context(), root, req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
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
	if req.MagicDoc {
		root := strings.TrimSpace(report.ProjectRoot)
		if root == "" {
			root = strings.TrimSpace(s.engine.Status().ProjectRoot)
		}
		magic, err := s.updateMagicDoc(r.Context(), root, MagicDocUpdateRequest{
			Path:     req.MagicDocPath,
			Title:    req.MagicDocTitle,
			Hotspots: req.MagicDocHotspots,
			Deps:     req.MagicDocDeps,
			Recent:   req.MagicDocRecent,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"report":   report,
			"magicdoc": magic,
		})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) updateMagicDoc(ctx context.Context, root string, req MagicDocUpdateRequest) (map[string]any, error) {
	target := resolveMagicDocPath(root, strings.TrimSpace(req.Path))
	content, err := buildMagicDocContentForWeb(ctx, s.engine, root, strings.TrimSpace(req.Title), req.Hotspots, req.Deps, req.Recent)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	prev, _ := os.ReadFile(target)
	updated := string(prev) != content
	if updated {
		if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
			return nil, err
		}
	}
	return map[string]any{
		"status":  "ok",
		"path":    filepath.ToSlash(target),
		"updated": updated,
		"bytes":   len(content),
	}, nil
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
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "skill name is required"})
		return
	}

	req := SkillExecRequest{}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	input := strings.TrimSpace(req.Input)
	if input == "" {
		input = strings.TrimSpace(req.Message)
	}

	prompt, source, ok := s.resolveSkillPrompt(name, input)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("skill not found: %s", name)})
		return
	}

	answer, err := s.engine.Ask(r.Context(), prompt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"skill":  name,
		"source": source,
		"input":  input,
		"answer": answer,
	})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "streaming not supported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	eventType := "*"
	if t := strings.TrimSpace(r.URL.Query().Get("type")); t != "" {
		eventType = t
	}
	ch := s.engine.EventBus.Subscribe(eventType)
	defer s.engine.EventBus.Unsubscribe(eventType, ch)

	writeSSE(w, flusher, map[string]any{
		"type": "connected",
		"ts":   time.Now().UTC().Format(time.RFC3339),
	})

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			writeSSE(w, flusher, map[string]any{
				"type":    "event",
				"event":   ev.Type,
				"source":  ev.Source,
				"payload": ev.Payload,
				"ts":      ev.Timestamp.UTC().Format(time.RFC3339),
			})
		case <-ticker.C:
			writeSSE(w, flusher, map[string]any{
				"type": "ping",
				"ts":   time.Now().UTC().Format(time.RFC3339),
			})
		}
	}
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

func loadProjectBriefForPromptRender(projectRoot, pathFlag string, maxWords int) string {
	root := strings.TrimSpace(projectRoot)
	if root == "" || maxWords <= 0 {
		return "(none)"
	}
	path := resolveMagicDocPath(root, pathFlag)
	data, err := os.ReadFile(path)
	if err != nil {
		return "(none)"
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "(none)"
	}
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "```") {
			continue
		}
		filtered = append(filtered, t)
		if len(filtered) >= 48 {
			break
		}
	}
	if len(filtered) == 0 {
		return "(none)"
	}
	return trimWordsForWeb(strings.Join(filtered, "\n"), maxWords)
}

func trimWordsForWeb(text string, maxWords int) string {
	if maxWords <= 0 {
		return ""
	}
	words := strings.Fields(strings.TrimSpace(text))
	if len(words) <= maxWords {
		return strings.TrimSpace(text)
	}
	return strings.Join(words[:maxWords], " ")
}

func resolveMagicDocPath(projectRoot, pathFlag string) string {
	if strings.TrimSpace(pathFlag) == "" {
		return filepath.Join(projectRoot, ".dfmc", "magic", "MAGIC_DOC.md")
	}
	if filepath.IsAbs(pathFlag) {
		return pathFlag
	}
	return filepath.Join(projectRoot, pathFlag)
}

type webDepStat struct {
	Module string
	Count  int
}

func collectDependencyStatsForWeb(eng *engine.Engine, limit int) []webDepStat {
	if eng == nil || eng.CodeMap == nil || eng.CodeMap.Graph() == nil {
		return nil
	}
	counts := map[string]int{}
	for _, e := range eng.CodeMap.Graph().Edges() {
		if e.Type != "imports" {
			continue
		}
		mod := strings.TrimSpace(strings.TrimPrefix(e.To, "module:"))
		if mod == "" {
			continue
		}
		counts[mod]++
	}
	out := make([]webDepStat, 0, len(counts))
	for mod, count := range counts {
		out = append(out, webDepStat{Module: mod, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].Module < out[j].Module
		}
		return out[i].Count > out[j].Count
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func buildMagicDocContentForWeb(ctx context.Context, eng *engine.Engine, projectRoot, title string, hotspotLimit, depLimit, recentLimit int) (string, error) {
	if hotspotLimit <= 0 {
		hotspotLimit = 8
	}
	if depLimit <= 0 {
		depLimit = 8
	}
	if recentLimit <= 0 {
		recentLimit = 5
	}
	if strings.TrimSpace(title) == "" {
		title = "DFMC Project Brief"
	}

	report, err := eng.AnalyzeWithOptions(ctx, engine.AnalyzeOptions{Path: projectRoot})
	if err != nil {
		return "", err
	}
	hotspots := report.HotSpots
	if len(hotspots) > hotspotLimit {
		hotspots = hotspots[:hotspotLimit]
	}
	deps := collectDependencyStatsForWeb(eng, depLimit)
	toolsList := eng.ListTools()
	sort.Strings(toolsList)

	w := eng.MemoryWorking()
	recentFiles := clipStringListForWeb(w.RecentFiles, recentLimit)

	active := eng.ConversationActive()
	conversationID := "(none)"
	conversationBranch := "(none)"
	messageCount := 0
	recentUser := []string{}
	recentAssistant := []string{}
	if active != nil {
		conversationID = strings.TrimSpace(active.ID)
		conversationBranch = strings.TrimSpace(active.Branch)
		msgs := active.Messages()
		messageCount = len(msgs)
		recentUser = recentMessagesForWeb(msgs, types.RoleUser, recentLimit)
		recentAssistant = recentMessagesForWeb(msgs, types.RoleAssistant, recentLimit)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# MAGIC DOC: %s\n\n", title)
	b.WriteString("_Current-state project brief optimized for low-token context reuse._\n\n")

	b.WriteString("## Current State\n")
	fmt.Fprintf(&b, "- Generated at: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Project root: `%s`\n", filepath.ToSlash(projectRoot))
	fmt.Fprintf(&b, "- Provider/model: `%s` / `%s`\n", eng.Status().Provider, eng.Status().Model)
	fmt.Fprintf(&b, "- Source files scanned: %d\n", report.Files)
	fmt.Fprintf(&b, "- Graph: nodes=%d edges=%d cycles=%d\n", report.Nodes, report.Edges, report.Cycles)

	b.WriteString("\n## Hotspots\n")
	if len(hotspots) == 0 {
		b.WriteString("- (none)\n")
	} else {
		for _, n := range hotspots {
			name := strings.TrimSpace(n.Name)
			if name == "" {
				name = strings.TrimSpace(n.ID)
			}
			kind := strings.TrimSpace(n.Kind)
			path := relativeProjectPathForWeb(projectRoot, strings.TrimSpace(n.Path))
			line := "- `" + name + "`"
			if kind != "" {
				line += " kind=" + kind
			}
			if path != "" {
				line += " path=`" + path + "`"
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n## Top Dependencies\n")
	if len(deps) == 0 {
		b.WriteString("- (none)\n")
	} else {
		for _, d := range deps {
			fmt.Fprintf(&b, "- `%s` (%d imports)\n", d.Module, d.Count)
		}
	}

	b.WriteString("\n## Conversation Snapshot\n")
	fmt.Fprintf(&b, "- Active conversation: `%s` (branch `%s`, %d messages)\n", fallbackStringForWeb(conversationID, "(none)"), fallbackStringForWeb(conversationBranch, "(none)"), messageCount)
	b.WriteString("- Recent user intents:\n")
	if len(recentUser) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, item := range recentUser {
			b.WriteString("  - " + item + "\n")
		}
	}
	b.WriteString("- Recent assistant outcomes:\n")
	if len(recentAssistant) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, item := range recentAssistant {
			b.WriteString("  - " + item + "\n")
		}
	}

	b.WriteString("\n## Active Surface\n")
	b.WriteString("- Recent context files:\n")
	if len(recentFiles) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, p := range recentFiles {
			b.WriteString("  - `" + relativeProjectPathForWeb(projectRoot, p) + "`\n")
		}
	}
	b.WriteString("- Registered tools:\n")
	if len(toolsList) == 0 {
		b.WriteString("  - (none)\n")
	} else {
		for _, name := range clipStringListForWeb(toolsList, 16) {
			b.WriteString("  - `" + name + "`\n")
		}
	}

	b.WriteString("\n## Workflow\n")
	b.WriteString("- Build: `go build ./cmd/dfmc`\n")
	b.WriteString("- Tests: `go test ./...`\n")
	b.WriteString("- Context budget preview: `go run ./cmd/dfmc context budget --query \"security audit\"`\n")
	b.WriteString("- Prompt preview: `go run ./cmd/dfmc prompt render --query \"review auth module\"`\n")
	b.WriteString("- Refresh this file: `go run ./cmd/dfmc magicdoc update`\n")

	return b.String(), nil
}

func recentMessagesForWeb(messages []types.Message, role types.MessageRole, limit int) []string {
	if limit <= 0 {
		return nil
	}
	out := make([]string, 0, limit)
	for i := len(messages) - 1; i >= 0 && len(out) < limit; i-- {
		if messages[i].Role != role {
			continue
		}
		text := strings.TrimSpace(strings.ReplaceAll(messages[i].Content, "\n", " "))
		if text == "" {
			continue
		}
		if len(text) > 160 {
			text = text[:160] + "..."
		}
		out = append(out, text)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func clipStringListForWeb(list []string, limit int) []string {
	if limit <= 0 || len(list) <= limit {
		out := make([]string, len(list))
		copy(out, list)
		return out
	}
	out := make([]string, limit)
	copy(out, list[:limit])
	return out
}

func relativeProjectPathForWeb(root, path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	absP, errP := filepath.Abs(p)
	absR, errR := filepath.Abs(strings.TrimSpace(root))
	if errP == nil && errR == nil && strings.TrimSpace(absR) != "" {
		if rel, err := filepath.Rel(absR, absP); err == nil {
			if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return filepath.ToSlash(rel)
			}
		}
	}
	return filepath.ToSlash(p)
}

func fallbackStringForWeb(v, alt string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return alt
	}
	return v
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

func (s *Server) resolveSkillPrompt(name, input string) (prompt string, source string, ok bool) {
	builtin := map[string]string{
		"review":   "Perform a strict code review. Prioritize bugs, risks, behavioral regressions, and missing tests.\n\nRequest:\n{input}",
		"explain":  "Explain the target code in a clear and structured way, including key flows and important caveats.\n\nRequest:\n{input}",
		"refactor": "Provide a safe refactor plan and concrete edits with minimal regression risk.\n\nRequest:\n{input}",
		"test":     "Create or improve automated tests for the target, including edge cases and failure paths.\n\nRequest:\n{input}",
		"doc":      "Write practical documentation for the requested code or module.\n\nRequest:\n{input}",
	}
	key := strings.ToLower(strings.TrimSpace(name))
	if tpl, exists := builtin[key]; exists {
		return applySkillPromptTemplate(tpl, input), "builtin", true
	}

	roots := []string{
		filepath.Join(s.engine.Status().ProjectRoot, ".dfmc", "skills"),
		filepath.Join(config.UserConfigDir(), "skills"),
	}
	for _, root := range roots {
		files, _ := filepath.Glob(filepath.Join(root, "*.y*ml"))
		for _, path := range files {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			raw := map[string]any{}
			if err := yaml.Unmarshal(data, &raw); err != nil {
				continue
			}
			skillName := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			if v, exists := raw["name"]; exists {
				n := strings.TrimSpace(fmt.Sprint(v))
				if n != "" {
					skillName = n
				}
			}
			if !strings.EqualFold(skillName, name) {
				continue
			}
			tpl := ""
			if v, exists := raw["prompt"]; exists {
				tpl = strings.TrimSpace(fmt.Sprint(v))
			}
			if tpl == "" {
				if v, exists := raw["template"]; exists {
					tpl = strings.TrimSpace(fmt.Sprint(v))
				}
			}
			if tpl == "" {
				return "", "", false
			}
			src := "project"
			if strings.Contains(strings.ToLower(path), strings.ToLower(filepath.Join(config.UserConfigDir(), "skills"))) {
				src = "global"
			}
			return applySkillPromptTemplate(tpl, input), src, true
		}
	}
	return "", "", false
}

func applySkillPromptTemplate(tpl, input string) string {
	p := strings.TrimSpace(tpl)
	if strings.Contains(p, "{input}") {
		return strings.ReplaceAll(p, "{input}", input)
	}
	if strings.TrimSpace(input) == "" {
		return p
	}
	if p == "" {
		return input
	}
	return p + "\n\nUser request:\n" + input
}
