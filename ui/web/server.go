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
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/commands"
	"github.com/dontfuckmycode/dfmc/internal/config"
	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/security"
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

// AskRequest is the body of POST /api/v1/ask — a single-turn,
// non-streaming completion. Race mode fans the same prompt out to multiple
// providers in parallel and returns the first success; the winner's name
// comes back in the response so the caller can log or display it.
type AskRequest struct {
	Message        string   `json:"message"`
	Race           bool     `json:"race,omitempty"`
	RaceProviders  []string `json:"race_providers,omitempty"`
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

type WorkspaceApplyRequest struct {
	Patch     string `json:"patch"`
	Source    string `json:"source"`
	CheckOnly bool   `json:"check_only"`
}

type PromptRenderRequest struct {
	Type              string            `json:"type"`
	Task              string            `json:"task"`
	Language          string            `json:"language"`
	Profile           string            `json:"profile"`
	Role              string            `json:"role"`
	Query             string            `json:"query"`
	ContextFiles      string            `json:"context_files"`
	Vars              map[string]string `json:"vars"`
	RuntimeProvider   string            `json:"runtime_provider"`
	RuntimeModel      string            `json:"runtime_model"`
	RuntimeToolStyle  string            `json:"runtime_tool_style"`
	RuntimeMaxContext int               `json:"runtime_max_context"`
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
	// Register a deny-by-default approver so a publicly-reachable serve
	// doesn't silently run gated tools; DFMC_APPROVE=yes|no lets the
	// operator pick behaviour per serve process (same semantics as CLI).
	eng.SetApprover(newWebApprover())
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	s.mux.HandleFunc("GET /", s.handleIndex)
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	s.mux.HandleFunc("GET /api/v1/commands", s.handleCommands)
	s.mux.HandleFunc("GET /api/v1/commands/{name}", s.handleCommandDetail)
	s.mux.HandleFunc("POST /api/v1/chat", s.handleChat)
	s.mux.HandleFunc("POST /api/v1/ask", s.handleAsk)
	s.mux.HandleFunc("GET /api/v1/codemap", s.handleCodeMap)
	s.mux.HandleFunc("GET /api/v1/context/budget", s.handleContextBudget)
	s.mux.HandleFunc("GET /api/v1/context/recommend", s.handleContextRecommend)
	s.mux.HandleFunc("GET /api/v1/context/brief", s.handleContextBrief)
	s.mux.HandleFunc("GET /api/v1/providers", s.handleProviders)
	s.mux.HandleFunc("GET /api/v1/skills", s.handleSkills)
	s.mux.HandleFunc("GET /api/v1/tools", s.handleTools)
	s.mux.HandleFunc("GET /api/v1/tools/{name}", s.handleToolSpec)
	s.mux.HandleFunc("POST /api/v1/tools/{name}", s.handleToolExec)
	s.mux.HandleFunc("POST /api/v1/skills/{name}", s.handleSkillExec)
	s.mux.HandleFunc("POST /api/v1/analyze", s.handleAnalyze)
	s.mux.HandleFunc("GET /api/v1/memory", s.handleMemory)
	s.mux.HandleFunc("GET /api/v1/conversation", s.handleConversationActive)
	s.mux.HandleFunc("POST /api/v1/conversation/new", s.handleConversationNew)
	s.mux.HandleFunc("POST /api/v1/conversation/save", s.handleConversationSave)
	s.mux.HandleFunc("POST /api/v1/conversation/load", s.handleConversationLoad)
	s.mux.HandleFunc("POST /api/v1/conversation/undo", s.handleConversationUndo)
	s.mux.HandleFunc("GET /api/v1/conversation/branches", s.handleConversationBranches)
	s.mux.HandleFunc("POST /api/v1/conversation/branches/create", s.handleConversationBranchCreate)
	s.mux.HandleFunc("POST /api/v1/conversation/branches/switch", s.handleConversationBranchSwitch)
	s.mux.HandleFunc("GET /api/v1/conversation/branches/compare", s.handleConversationBranchCompare)
	s.mux.HandleFunc("GET /api/v1/prompts", s.handlePrompts)
	s.mux.HandleFunc("GET /api/v1/prompts/stats", s.handlePromptStats)
	s.mux.HandleFunc("GET /api/v1/prompts/recommend", s.handlePromptRecommend)
	s.mux.HandleFunc("POST /api/v1/prompts/render", s.handlePromptRender)
	s.mux.HandleFunc("GET /api/v1/magicdoc", s.handleMagicDocShow)
	s.mux.HandleFunc("POST /api/v1/magicdoc/update", s.handleMagicDocUpdate)
	s.mux.HandleFunc("GET /api/v1/conversations", s.handleConversations)
	s.mux.HandleFunc("GET /api/v1/conversations/search", s.handleConversationSearch)
	s.mux.HandleFunc("GET /api/v1/workspace/diff", s.handleWorkspaceDiff)
	s.mux.HandleFunc("GET /api/v1/workspace/patch", s.handleWorkspacePatch)
	s.mux.HandleFunc("POST /api/v1/workspace/apply", s.handleWorkspaceApply)
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
	_, _ = w.Write([]byte(renderWorkbenchHTML()))
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	st := s.engine.Status()

	// Gate and hooks mirror the CLI `dfmc status` payload so operators who
	// hit the HTTP surface see the same posture signals. We wrap Status
	// instead of embedding these fields on the engine.Status struct to
	// keep that contract stable for existing consumers.
	payload := map[string]any{
		"state":            st.State,
		"project_root":     st.ProjectRoot,
		"provider":         st.Provider,
		"model":            st.Model,
		"provider_profile": st.ProviderProfile,
		"models_dev_cache": st.ModelsDevCache,
		"context_in":       st.ContextIn,
		"ast_backend":      st.ASTBackend,
		"ast_reason":       st.ASTReason,
		"ast_languages":    st.ASTLanguages,
		"ast_metrics":      st.ASTMetrics,
		"codemap_metrics":  st.CodeMap,
		"approval_gate":    s.approvalGateSummary(),
		"hooks":            s.hooksSummary(),
		"recent_denials":   len(s.engine.RecentDenials()),
	}
	writeJSON(w, http.StatusOK, payload)
}

type webApprovalGateSummary struct {
	Active   bool     `json:"active"`
	Wildcard bool     `json:"wildcard"`
	Count    int      `json:"count"`
	Tools    []string `json:"tools,omitempty"`
}

func (s *Server) approvalGateSummary() webApprovalGateSummary {
	out := webApprovalGateSummary{}
	if s.engine == nil || s.engine.Config == nil {
		return out
	}
	raw := s.engine.Config.Tools.RequireApproval
	tools := make([]string, 0, len(raw))
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == "*" {
			out.Wildcard = true
			continue
		}
		tools = append(tools, entry)
	}
	sort.Strings(tools)
	out.Tools = tools
	out.Count = len(tools)
	if out.Wildcard {
		out.Count = -1
	}
	out.Active = out.Wildcard || len(tools) > 0
	return out
}

type webHooksSummary struct {
	Total    int            `json:"total"`
	PerEvent map[string]int `json:"per_event,omitempty"`
}

func (s *Server) hooksSummary() webHooksSummary {
	out := webHooksSummary{PerEvent: map[string]int{}}
	if s.engine == nil || s.engine.Hooks == nil {
		return out
	}
	inv := s.engine.Hooks.Inventory()
	for event, entries := range inv {
		key := strings.TrimSpace(string(event))
		if key == "" {
			continue
		}
		out.PerEvent[key] = len(entries)
		out.Total += len(entries)
	}
	return out
}

func (s *Server) handleCommands(w http.ResponseWriter, _ *http.Request) {
	reg := commands.DefaultRegistry()
	writeJSON(w, http.StatusOK, map[string]any{
		"groups": reg.ListByCategory(commands.SurfaceWeb),
	})
}

func (s *Server) handleCommandDetail(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "command name is required"})
		return
	}
	reg := commands.DefaultRegistry()
	cmd, ok := reg.Lookup(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("command not found: %s", name)})
		return
	}
	writeJSON(w, http.StatusOK, cmd)
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
	runtimeHints := s.engine.PromptRuntime()
	if p := strings.TrimSpace(r.URL.Query().Get("runtime_provider")); p != "" {
		runtimeHints.Provider = p
	}
	if m := strings.TrimSpace(r.URL.Query().Get("runtime_model")); m != "" {
		runtimeHints.Model = m
	}
	if ts := strings.TrimSpace(r.URL.Query().Get("runtime_tool_style")); ts != "" {
		runtimeHints.ToolStyle = ts
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("runtime_max_context")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			runtimeHints.MaxContext = n
		}
	}
	preview := s.engine.ContextBudgetPreviewWithRuntime(query, runtimeHints)
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) handleContextRecommend(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	runtimeHints := s.engine.PromptRuntime()
	if p := strings.TrimSpace(r.URL.Query().Get("runtime_provider")); p != "" {
		runtimeHints.Provider = p
	}
	if m := strings.TrimSpace(r.URL.Query().Get("runtime_model")); m != "" {
		runtimeHints.Model = m
	}
	if ts := strings.TrimSpace(r.URL.Query().Get("runtime_tool_style")); ts != "" {
		runtimeHints.ToolStyle = ts
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("runtime_max_context")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			runtimeHints.MaxContext = n
		}
	}
	preview := s.engine.ContextBudgetPreviewWithRuntime(query, runtimeHints)
	recs := s.engine.ContextRecommendationsWithRuntime(query, runtimeHints)
	tuning := s.engine.ContextTuningSuggestionsWithRuntime(query, runtimeHints)
	writeJSON(w, http.StatusOK, map[string]any{
		"query":              query,
		"preview":            preview,
		"recommendations":    recs,
		"tuning_suggestions": tuning,
	})
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

// handleToolSpec serves the structured ToolSpec for a single tool so the
// workbench (and any scripting consumer) can render parameter shape and
// risk without duplicating the CLI pretty-printer.
func (s *Server) handleToolSpec(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "tool name is required"})
		return
	}
	if s.engine == nil || s.engine.Tools == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "tools engine not initialized"})
		return
	}
	spec, ok := s.engine.Tools.Spec(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("unknown tool: %s", name)})
		return
	}
	writeJSON(w, http.StatusOK, spec)
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

func (s *Server) handleWorkspaceDiff(w http.ResponseWriter, _ *http.Request) {
	root := strings.TrimSpace(s.engine.Status().ProjectRoot)
	if root == "" {
		root = "."
	}
	diff, err := gitWorkingDiffWeb(root, 200_000)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	changed, err := gitChangedFilesWeb(root, 24)
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
	if err := applyUnifiedDiffWeb(root, patch, req.CheckOnly); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if req.CheckOnly {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"check_only": true,
			"valid":      true,
		})
		return
	}
	changed, err := gitChangedFilesWeb(root, 24)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"check_only":    false,
		"changed_files": changed,
	})
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

func (s *Server) handlePromptRecommend(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	runtimeHints := s.engine.PromptRuntime()
	if p := strings.TrimSpace(r.URL.Query().Get("runtime_provider")); p != "" {
		runtimeHints.Provider = p
	}
	if m := strings.TrimSpace(r.URL.Query().Get("runtime_model")); m != "" {
		runtimeHints.Model = m
	}
	if ts := strings.TrimSpace(r.URL.Query().Get("runtime_tool_style")); ts != "" {
		runtimeHints.ToolStyle = ts
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("runtime_max_context")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			runtimeHints.MaxContext = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"query":          query,
		"recommendation": s.engine.PromptRecommendationWithRuntime(query, runtimeHints),
	})
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
	runtimeHints := s.engine.PromptRuntime()
	if p := strings.TrimSpace(req.RuntimeProvider); p != "" {
		runtimeHints.Provider = p
	}
	if m := strings.TrimSpace(req.RuntimeModel); m != "" {
		runtimeHints.Model = m
	}
	if ts := strings.TrimSpace(req.RuntimeToolStyle); ts != "" {
		runtimeHints.ToolStyle = ts
	}
	if req.RuntimeMaxContext > 0 {
		runtimeHints.MaxContext = req.RuntimeMaxContext
	}
	if strings.EqualFold(resolvedProfile, "auto") || resolvedProfile == "" {
		resolvedProfile = ctxmgr.ResolvePromptProfile(req.Query, resolvedTask, runtimeHints)
	}
	resolvedRole := strings.TrimSpace(req.Role)
	if strings.EqualFold(resolvedRole, "auto") || resolvedRole == "" {
		resolvedRole = ctxmgr.ResolvePromptRole(req.Query, resolvedTask)
	}
	budget := ctxmgr.ResolvePromptRenderBudget(resolvedTask, resolvedProfile, runtimeHints)
	vars := map[string]string{
		"project_root":     s.engine.Status().ProjectRoot,
		"task":             resolvedTask,
		"language":         resolvedLang,
		"profile":          resolvedProfile,
		"role":             resolvedRole,
		"project_brief":    loadProjectBriefForPromptRender(s.engine.Status().ProjectRoot, "", budget.ProjectBriefTokens),
		"user_query":       strings.TrimSpace(req.Query),
		"context_files":    strings.TrimSpace(req.ContextFiles),
		"injected_context": ctxmgr.BuildInjectedContextWithBudget(s.engine.Status().ProjectRoot, req.Query, budget),
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
		Role:     resolvedRole,
		Vars:     vars,
	})
	promptBudgetTokens := ctxmgr.PromptTokenBudget(resolvedTask, resolvedProfile, runtimeHints)
	trimmed := false
	if promptBudgetTokens > 0 {
		before := promptlib.EstimateTokens(prompt)
		prompt = strings.TrimSpace(ctxmgr.TrimPromptToBudget(prompt, promptBudgetTokens))
		after := promptlib.EstimateTokens(prompt)
		trimmed = after < before
	}
	promptTokensEstimate := promptlib.EstimateTokens(prompt)
	writeJSON(w, http.StatusOK, map[string]any{
		"type":                   req.Type,
		"task":                   resolvedTask,
		"language":               resolvedLang,
		"profile":                resolvedProfile,
		"role":                   resolvedRole,
		"vars":                   vars,
		"prompt":                 prompt,
		"prompt_tokens_estimate": promptTokensEstimate,
		"prompt_budget_tokens":   promptBudgetTokens,
		"prompt_trimmed":         trimmed,
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

// handleAsk answers a single-turn prompt. Unlike /api/v1/chat, which
// streams and stays in the agent tool loop, this endpoint is one-shot: it
// returns when the first provider reply comes back. When req.Race is true,
// the router fans out to every candidate concurrently and the winner's
// name is included in the response.
func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	var req AskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	msg := strings.TrimSpace(req.Message)
	if msg == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "message is required"})
		return
	}
	if req.Race {
		answer, winner, err := s.engine.AskRaced(r.Context(), msg, req.RaceProviders)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"answer":     answer,
			"winner":     winner,
			"candidates": req.RaceProviders,
			"mode":       "race",
		})
		return
	}
	answer, err := s.engine.Ask(r.Context(), msg)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"answer": answer,
		"mode":   "single",
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

func gitWorkingDiffWeb(projectRoot string, maxBytes int64) (string, error) {
	root, err := security.SanitizeGitRoot(projectRoot)
	if err != nil {
		return "", err
	}
	// cmd.Dir keeps the path out of git's CLI parser entirely; with
	// `-C <root>` a path that starts with `-` could be read as a
	// flag. exec.Command doesn't spawn a shell so classic CWE-78
	// injection isn't possible, but argument-injection hardening is
	// cheap and makes the static-scanner flag go away honestly.
	cmd := exec.Command("git", "diff", "--")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if ee := (&exec.ExitError{}); errors.As(err, &ee) {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	if maxBytes > 0 && int64(len(out)) > maxBytes {
		out = out[:maxBytes]
		return string(out) + "\n... [truncated]\n", nil
	}
	return string(out), nil
}

func latestAssistantUnifiedDiffWeb(active *conversation.Conversation) string {
	if active == nil {
		return ""
	}
	msgs := active.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != types.RoleAssistant {
			continue
		}
		if patch := extractUnifiedDiffWeb(msgs[i].Content); strings.TrimSpace(patch) != "" {
			return patch
		}
	}
	return ""
}

func extractUnifiedDiffWeb(in string) string {
	text := strings.TrimSpace(strings.ReplaceAll(in, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	for _, marker := range []string{"```diff", "```patch", "```"} {
		idx := 0
		for {
			start := strings.Index(text[idx:], marker)
			if start < 0 {
				break
			}
			start += idx
			blockStart := strings.Index(text[start:], "\n")
			if blockStart < 0 {
				break
			}
			blockStart += start + 1
			end := strings.Index(text[blockStart:], "\n```")
			if end < 0 {
				break
			}
			end += blockStart
			block := strings.TrimSpace(text[blockStart:end])
			if looksLikeUnifiedDiffWeb(block) {
				return block
			}
			idx = end + 4
		}
	}
	if looksLikeUnifiedDiffWeb(text) {
		return text
	}
	return ""
}

func looksLikeUnifiedDiffWeb(diff string) bool {
	d := "\n" + strings.TrimSpace(diff) + "\n"
	if strings.Contains(d, "\ndiff --git ") {
		return true
	}
	return strings.Contains(d, "\n--- ") && strings.Contains(d, "\n+++ ") && strings.Contains(d, "\n@@ ")
}

func applyUnifiedDiffWeb(projectRoot, patch string, checkOnly bool) error {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		root = "."
	}
	patch = strings.ReplaceAll(patch, "\r\n", "\n")
	if patch != "" && !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	args := []string{"-C", root, "apply", "--whitespace=nowarn", "--recount"}
	if checkOnly {
		args = append(args, "--check")
	}
	cmd := exec.Command("git", args...)
	cmd.Stdin = strings.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func gitChangedFilesWeb(projectRoot string, limit int) ([]string, error) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		root = "."
	}
	cmd := exec.Command("git", "-C", root, "status", "--short", "--")
	out, err := cmd.Output()
	if err != nil {
		if ee := (&exec.ExitError{}); errors.As(err, &ee) {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	text := strings.ReplaceAll(string(out), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	files := make([]string, 0, len(lines))
	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if len(raw) > 3 {
			files = append(files, strings.TrimSpace(raw[3:]))
		} else {
			files = append(files, strings.TrimSpace(raw))
		}
		if limit > 0 && len(files) >= limit {
			break
		}
	}
	return files, nil
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

func renderWorkbenchHTML() string {
	return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>DFMC Workbench</title>
<style>
:root {
	--bg: #0b1220;
	--panel: #121c2f;
	--panel-2: #19253c;
	--line: #2b3a57;
	--text: #edf2ff;
	--muted: #8da2c7;
	--accent: #f25f5c;
	--accent-2: #29c7ac;
	--accent-3: #66b3ff;
	--warn: #ffcb6b;
	--shadow: 0 24px 60px rgba(2, 10, 28, 0.45);
	--radius: 18px;
	--font-ui: "Segoe UI", "Inter", system-ui, sans-serif;
	--font-mono: "JetBrains Mono", "Cascadia Code", "SFMono-Regular", Consolas, monospace;
}
* { box-sizing: border-box; }
html, body { margin: 0; min-height: 100%; background:
	radial-gradient(circle at top left, rgba(102,179,255,0.18), transparent 28%),
	radial-gradient(circle at top right, rgba(242,95,92,0.16), transparent 24%),
	linear-gradient(180deg, #09101b 0%, #0b1220 100%);
	color: var(--text); font-family: var(--font-ui); }
body { padding: 24px; }
.shell { max-width: 1500px; margin: 0 auto; display: grid; gap: 18px; }
.hero {
	display: grid;
	grid-template-columns: 1.4fr 1fr;
	gap: 18px;
	align-items: stretch;
}
.panel {
	background: linear-gradient(180deg, rgba(18,28,47,0.96), rgba(12,19,32,0.96));
	border: 1px solid var(--line);
	border-radius: var(--radius);
	box-shadow: var(--shadow);
}
.hero-card { padding: 24px; position: relative; overflow: hidden; }
.hero-card::after {
	content: "";
	position: absolute;
	inset: auto -80px -80px auto;
	width: 220px;
	height: 220px;
	border-radius: 999px;
	background: radial-gradient(circle, rgba(41,199,172,0.16), transparent 68%);
}
.eyebrow {
	display: inline-flex;
	align-items: center;
	gap: 10px;
	padding: 6px 12px;
	border-radius: 999px;
	background: rgba(102,179,255,0.1);
	border: 1px solid rgba(102,179,255,0.24);
	color: #b9d8ff;
	font: 600 12px var(--font-mono);
	letter-spacing: 0.08em;
	text-transform: uppercase;
}
h1 {
	margin: 16px 0 12px;
	font-size: clamp(32px, 4vw, 56px);
	line-height: 0.95;
	letter-spacing: -0.05em;
}
.lede {
	max-width: 58ch;
	color: var(--muted);
	font-size: 15px;
	line-height: 1.65;
}
.chips, .stats-grid, .workspace, .stack, .header-row, .chat-controls, .action-row {
	display: flex;
	flex-wrap: wrap;
	gap: 10px;
}
.chips { margin-top: 18px; }
.chip, .metric {
	padding: 10px 12px;
	border-radius: 14px;
	background: rgba(255,255,255,0.03);
	border: 1px solid rgba(255,255,255,0.08);
}
.chip strong, .metric strong { display: block; font-size: 11px; color: var(--muted); text-transform: uppercase; letter-spacing: 0.08em; }
.chip span, .metric span { display: block; margin-top: 4px; font: 600 14px var(--font-mono); }
.stats-grid { align-content: start; padding: 24px; }
.metric { min-width: 140px; flex: 1 1 140px; }
.workspace {
	display: grid;
	grid-template-columns: minmax(320px, 1.15fr) minmax(420px, 1.6fr) minmax(300px, 1fr);
	gap: 18px;
}
.stack {
	display: grid;
	gap: 18px;
	align-content: start;
}
.pane-header {
	display: flex;
	align-items: center;
	justify-content: space-between;
	gap: 12px;
	padding: 16px 18px 0;
}
.pane-title {
	font: 700 14px var(--font-mono);
	text-transform: uppercase;
	letter-spacing: 0.08em;
	color: #d8e6ff;
}
.pane-subtitle {
	color: var(--muted);
	font-size: 12px;
}
.pane-body { padding: 16px 18px 18px; }
button, .button-link {
	border: 0;
	border-radius: 12px;
	padding: 10px 14px;
	font: 700 13px var(--font-mono);
	cursor: pointer;
	background: linear-gradient(135deg, var(--accent), #ff7b62);
	color: #150d10;
	text-decoration: none;
}
button.secondary {
	background: rgba(255,255,255,0.04);
	color: var(--text);
	border: 1px solid rgba(255,255,255,0.08);
}
button:disabled { opacity: 0.55; cursor: not-allowed; }
textarea, input, select {
	width: 100%;
	border-radius: 14px;
	border: 1px solid rgba(255,255,255,0.1);
	background: rgba(7, 13, 23, 0.85);
	color: var(--text);
	padding: 12px 14px;
	font: 500 14px var(--font-ui);
}
textarea {
	min-height: 132px;
	resize: vertical;
	font-family: var(--font-ui);
	line-height: 1.55;
}
.chat-controls { margin-top: 12px; align-items: center; }
.chat-controls > * { flex: 1 1 180px; }
.transcript {
	margin-top: 14px;
	display: grid;
	gap: 12px;
	max-height: 640px;
	overflow: auto;
	padding-right: 4px;
}
.message {
	border: 1px solid rgba(255,255,255,0.08);
	border-radius: 16px;
	padding: 14px;
	background: rgba(255,255,255,0.03);
}
.message.user { border-color: rgba(102,179,255,0.24); background: rgba(102,179,255,0.08); }
.message.assistant { border-color: rgba(41,199,172,0.22); background: rgba(41,199,172,0.06); }
.message.system { border-color: rgba(255,203,107,0.22); background: rgba(255,203,107,0.06); }
.message .role {
	font: 700 11px var(--font-mono);
	text-transform: uppercase;
	letter-spacing: 0.08em;
	color: var(--muted);
	margin-bottom: 8px;
}
.message pre {
	margin: 0;
	white-space: pre-wrap;
	word-break: break-word;
	font: 500 13px/1.6 var(--font-mono);
}
.list {
	display: grid;
	gap: 8px;
	max-height: 300px;
	overflow: auto;
}
.list-item {
	padding: 10px 12px;
	border-radius: 12px;
	background: rgba(255,255,255,0.03);
	border: 1px solid rgba(255,255,255,0.06);
	cursor: pointer;
	color: var(--text);
	font: 500 13px var(--font-mono);
}
.list-item:hover, .list-item.active {
	border-color: rgba(102,179,255,0.28);
	background: rgba(102,179,255,0.08);
}
.codebox {
	margin-top: 12px;
	padding: 14px;
	border-radius: 14px;
	background: rgba(5, 10, 18, 0.88);
	border: 1px solid rgba(255,255,255,0.08);
	max-height: 360px;
	overflow: auto;
	font: 500 12px/1.65 var(--font-mono);
	white-space: pre-wrap;
}
.activity-log {
	margin-top: 12px;
	padding: 10px 12px;
	border-radius: 14px;
	background: rgba(5, 10, 18, 0.88);
	border: 1px solid rgba(255,255,255,0.08);
	max-height: 320px;
	overflow: auto;
	font: 500 12px/1.55 var(--font-mono);
	display: grid;
	gap: 2px;
}
.activity-row {
	display: grid;
	grid-template-columns: 82px 26px 1fr;
	gap: 6px;
	padding: 2px 4px;
	border-radius: 6px;
	align-items: baseline;
}
.activity-row:hover { background: rgba(102,179,255,0.05); }
.activity-row .ts { color: var(--muted); }
.activity-row .kind { text-align: center; }
.activity-row.kind-agent .kind { color: var(--accent); }
.activity-row.kind-tool .kind { color: var(--accent-3); }
.activity-row.kind-stream .kind { color: var(--accent-2); }
.activity-row.kind-ctx .kind { color: var(--accent-2); }
.activity-row.kind-error { background: rgba(242,95,92,0.10); }
.activity-row.kind-error .kind { color: var(--accent); }
.activity-row.kind-index .kind { color: var(--muted); }
.activity-empty { color: var(--muted); padding: 6px 4px; }
.mini-grid {
	display: grid;
	grid-template-columns: repeat(2, minmax(0, 1fr));
	gap: 10px;
}
.kv {
	padding: 12px;
	border-radius: 14px;
	background: rgba(255,255,255,0.03);
	border: 1px solid rgba(255,255,255,0.06);
}
.kv strong {
	display: block;
	color: var(--muted);
	font: 700 11px var(--font-mono);
	text-transform: uppercase;
	letter-spacing: 0.08em;
}
.kv span {
	display: block;
	margin-top: 6px;
	font: 600 14px var(--font-mono);
	word-break: break-word;
}
.inline-note {
	margin-top: 12px;
	color: var(--muted);
	font-size: 12px;
	line-height: 1.5;
}
.graph-list { display: grid; gap: 8px; margin-top: 12px; }
.graph-item {
	padding: 10px 12px;
	border-radius: 12px;
	background: rgba(255,255,255,0.03);
	border: 1px solid rgba(255,255,255,0.06);
}
.graph-item strong {
	display: block;
	font: 600 13px var(--font-mono);
}
.graph-item span {
	display: block;
	margin-top: 4px;
	color: var(--muted);
	font-size: 12px;
}
.footer-note {
	padding: 0 4px 8px;
	color: var(--muted);
	font-size: 12px;
}
.pulse {
	display: inline-flex;
	align-items: center;
	gap: 8px;
	color: var(--muted);
	font: 600 12px var(--font-mono);
}
.pulse::before {
	content: "";
	width: 10px;
	height: 10px;
	border-radius: 999px;
	background: var(--accent-2);
	box-shadow: 0 0 0 rgba(41,199,172,0.4);
	animation: pulse 1.8s infinite;
}
@keyframes pulse {
	0% { box-shadow: 0 0 0 0 rgba(41,199,172,0.4); }
	70% { box-shadow: 0 0 0 12px rgba(41,199,172,0); }
	100% { box-shadow: 0 0 0 0 rgba(41,199,172,0); }
}
@media (max-width: 1200px) {
	.hero, .workspace { grid-template-columns: 1fr; }
}
@media (max-width: 720px) {
	body { padding: 14px; }
	.hero-card, .stats-grid, .pane-body { padding-left: 14px; padding-right: 14px; }
	.pane-header { padding-left: 14px; padding-right: 14px; }
	.mini-grid { grid-template-columns: 1fr; }
}
</style>
</head>
<body>
<div class="shell">
	<section class="hero">
		<div class="panel hero-card">
			<div class="eyebrow">DFMC Workbench</div>
			<h1>Your code deserves a live cockpit.</h1>
			<p class="lede">
				This is the first real operator surface for DFMC: inspect engine status, explore project files,
				stream chat responses, and watch codemap signals without leaving the browser.
			</p>
			<div class="chips">
				<div class="chip"><strong>Now</strong><span id="hero-provider">loading...</span></div>
				<div class="chip"><strong>Project</strong><span id="hero-project">detecting...</span></div>
				<div class="chip"><strong>AST</strong><span id="hero-ast">pending...</span></div>
			</div>
		</div>
		<div class="panel stats-grid" id="top-metrics">
			<div class="metric"><strong>State</strong><span id="metric-state">-</span></div>
			<div class="metric"><strong>Tools</strong><span id="metric-tools">-</span></div>
			<div class="metric"><strong>Skills</strong><span id="metric-skills">-</span></div>
			<div class="metric"><strong>Files</strong><span id="metric-files">-</span></div>
			<div class="metric"><strong>CodeMap</strong><span id="metric-codemap">-</span></div>
			<div class="metric"><strong>Context</strong><span id="metric-context">-</span></div>
			<div class="metric"><strong>Gate</strong><span id="metric-gate" title="Tool approval gate">-</span></div>
			<div class="metric"><strong>Hooks</strong><span id="metric-hooks" title="Lifecycle hooks registered">-</span></div>
		</div>
	</section>

	<section class="workspace">
		<div class="stack">
			<div class="panel">
				<div class="pane-header">
					<div>
						<div class="pane-title">Project Files</div>
						<div class="pane-subtitle">Browse and preview the live workspace.</div>
					</div>
					<button id="refresh-files" class="secondary" type="button">Refresh</button>
				</div>
				<div class="pane-body">
					<div class="list" id="file-list"></div>
					<div class="codebox" id="file-preview">Select a file to preview its contents.</div>
				</div>
			</div>
			<div class="panel">
				<div class="pane-header">
					<div>
						<div class="pane-title">Runtime Signals</div>
						<div class="pane-subtitle">Engine, provider, AST, and recent context pressure.</div>
					</div>
				</div>
				<div class="pane-body">
					<div class="mini-grid">
						<div class="kv"><strong>Provider</strong><span id="runtime-provider">-</span></div>
						<div class="kv"><strong>Model</strong><span id="runtime-model">-</span></div>
						<div class="kv"><strong>AST Backend</strong><span id="runtime-ast">-</span></div>
						<div class="kv"><strong>Recent Context</strong><span id="runtime-context">-</span></div>
					</div>
					<div class="inline-note" id="runtime-note">Waiting for status snapshot.</div>
				</div>
			</div>
			<div class="panel">
				<div class="pane-header">
					<div>
						<div class="pane-title">Patch Lab</div>
						<div class="pane-subtitle">See the worktree diff, load the latest assistant patch, then check or apply it.</div>
					</div>
				</div>
				<div class="pane-body">
					<div class="action-row">
						<button id="refresh-diff" class="secondary" type="button">Refresh Diff</button>
						<button id="load-latest-patch" class="secondary" type="button">Load Latest Patch</button>
						<button id="undo-chat" class="secondary" type="button">Undo Chat</button>
					</div>
					<div class="inline-note" id="patch-status">Patch lab is idle.</div>
					<textarea id="patch-editor" placeholder="Latest assistant unified diff will appear here, or paste your own patch."></textarea>
					<div class="action-row">
						<button id="check-patch" class="secondary" type="button">Check Patch</button>
						<button id="apply-patch" type="button">Apply Patch</button>
					</div>
					<div class="codebox" id="workspace-diff">Working tree diff will appear here.</div>
				</div>
			</div>
			<div class="panel">
				<div class="pane-header">
					<div>
						<div class="pane-title">Activity</div>
						<div class="pane-subtitle">Live firehose of engine, agent, tool, and context events.</div>
					</div>
					<div class="pulse" id="activity-status">idle</div>
				</div>
				<div class="pane-body">
					<div class="action-row">
						<button id="activity-clear" class="secondary" type="button">Clear</button>
						<button id="activity-follow" type="button">Pause follow</button>
						<span class="inline-note" id="activity-summary">0 events</span>
					</div>
					<div class="activity-log" id="activity-log" role="log" aria-live="polite"></div>
				</div>
			</div>
		</div>

		<div class="panel">
			<div class="pane-header">
				<div>
					<div class="pane-title">Live Chat</div>
					<div class="pane-subtitle">Stream answers directly from the engine over SSE.</div>
				</div>
				<div class="pulse" id="chat-status">idle</div>
			</div>
			<div class="pane-body">
				<label for="chat-input" class="pane-subtitle">Prompt</label>
				<textarea id="chat-input" placeholder="Ask DFMC to explain a flow, review a file, or plan a refactor."></textarea>
				<div class="chat-controls">
					<button id="chat-send" type="button">Send</button>
					<button id="chat-status-refresh" class="secondary" type="button">Refresh status</button>
					<input id="chat-hint" type="text" value="review internal/engine/engine.go" aria-label="quick hint">
				</div>
				<div class="transcript" id="chat-transcript">
					<div class="message system">
						<div class="role">system</div>
						<pre>Workbench ready. Ask something and the answer will stream here.</pre>
					</div>
				</div>
			</div>
		</div>

		<div class="stack">
			<div class="panel">
				<div class="pane-header">
					<div>
						<div class="pane-title">CodeMap Pulse</div>
						<div class="pane-subtitle">A quick structural read without leaving the page.</div>
					</div>
					<button id="refresh-codemap" class="secondary" type="button">Refresh</button>
				</div>
				<div class="pane-body">
					<div class="mini-grid">
						<div class="kv"><strong>Nodes</strong><span id="codemap-nodes">-</span></div>
						<div class="kv"><strong>Edges</strong><span id="codemap-edges">-</span></div>
					</div>
					<div class="graph-list" id="codemap-hotspots"></div>
				</div>
			</div>
			<div class="panel">
				<div class="pane-header">
					<div>
						<div class="pane-title">Capabilities</div>
						<div class="pane-subtitle">Available providers, tools, and built-in skills.</div>
					</div>
				</div>
				<div class="pane-body">
					<div class="mini-grid">
						<div class="kv"><strong>Providers</strong><span id="cap-providers">-</span></div>
						<div class="kv"><strong>Skills</strong><span id="cap-skills">-</span></div>
					</div>
					<div class="codebox" id="cap-tools">Loading tool catalog...</div>
					<div class="footer-note">This is the bridge toward a full TUI: one shared operator model, multiple frontends.</div>
				</div>
			</div>
		</div>
	</section>
</div>

<script>
const state = {
	activeFile: "",
	status: null,
};

function setText(id, value) {
	const node = document.getElementById(id);
	if (node) node.textContent = value;
}

function escapeHTML(value) {
	return String(value ?? "")
		.replace(/&/g, "&amp;")
		.replace(/</g, "&lt;")
		.replace(/>/g, "&gt;");
}

function addMessage(role, content) {
	const transcript = document.getElementById("chat-transcript");
	const wrapper = document.createElement("div");
	wrapper.className = "message " + role;
	wrapper.innerHTML = '<div class="role">' + escapeHTML(role) + '</div><pre>' + escapeHTML(content) + '</pre>';
	transcript.appendChild(wrapper);
	transcript.scrollTop = transcript.scrollHeight;
	return wrapper.querySelector("pre");
}

async function fetchJSON(url, options) {
	const resp = await fetch(url, options);
	if (!resp.ok) {
		const text = await resp.text();
		throw new Error(text || ("HTTP " + resp.status));
	}
	return resp.json();
}

function summarizeList(items, limit) {
	if (!Array.isArray(items) || !items.length) return "-";
	return items.slice(0, limit).join(", ");
}

async function loadStatus() {
	const data = await fetchJSON("/api/v1/status");
	state.status = data;
	setText("hero-provider", (data.provider || "-") + " / " + (data.model || "-"));
	setText("hero-project", data.project_root || "(no project)");
	setText("hero-ast", data.ast_backend || "unknown");
	setText("metric-state", String(data.state ?? "-"));
	setText("metric-tools", String(data.tools_count ?? "-"));
	setText("metric-skills", String(data.skills_count ?? "-"));
	setText("metric-context", data.context_budget ? (data.context_budget.max_tokens_total + " tok") : "-");
	const codemap = data.codemap_metrics || {};
	setText("metric-codemap", codemap.builds ? (codemap.builds + " builds") : "cold");
	setText("runtime-provider", data.provider || "-");
	setText("runtime-model", data.model || "-");
	setText("runtime-ast", data.ast_backend || "-");
	const astLanguages = Array.isArray(data.ast_languages) ? data.ast_languages.slice(0, 4).map(item => item.language + "=" + item.active) : [];
	setText("runtime-context", data.context_budget ? (data.context_budget.task + " / " + data.context_budget.max_files + " files") : "-");
	setText("runtime-note", astLanguages.length ? ("AST matrix: " + astLanguages.join(", ")) : "No AST capability data yet.");

	const gate = data.approval_gate || {};
	let gateLabel;
	if (gate.wildcard) {
		gateLabel = "on (*)";
	} else if (gate.active) {
		const tools = Array.isArray(gate.tools) ? gate.tools : [];
		const preview = tools.slice(0, 3).join(", ");
		gateLabel = tools.length > 3
			? (gate.count + ": " + preview + ", …")
			: (gate.count + ": " + preview);
	} else {
		gateLabel = "off";
	}
	const denials = Number(data.recent_denials || 0);
	if (denials > 0) {
		gateLabel += " · " + denials + " denied";
	}
	setText("metric-gate", gateLabel);

	const hooks = data.hooks || { total: 0, per_event: {} };
	if (!hooks.total) {
		setText("metric-hooks", "none");
	} else {
		const perEvent = hooks.per_event || {};
		const keys = Object.keys(perEvent).sort();
		const parts = keys.map(k => k + "=" + perEvent[k]);
		setText("metric-hooks", hooks.total + " (" + parts.slice(0, 3).join(", ") + (keys.length > 3 ? ", …" : "") + ")");
	}
}

async function loadProvidersAndSkills() {
	const [providers, skills, tools] = await Promise.all([
		fetchJSON("/api/v1/providers"),
		fetchJSON("/api/v1/skills"),
		fetchJSON("/api/v1/tools"),
	]);
	const providerNames = (providers.providers || []).map(item => item.name + (item.active ? "*" : ""));
	setText("cap-providers", summarizeList(providerNames, 6));
	const skillNames = (skills.skills || []).map(item => item.name);
	setText("cap-skills", summarizeList(skillNames, 6));
	const toolNames = (tools.tools || []).slice().sort();
	setText("metric-tools", String(toolNames.length));
	document.getElementById("cap-tools").textContent = toolNames.length
		? toolNames.join("\n")
		: "No tools registered.";
}

async function loadFiles() {
	const data = await fetchJSON("/api/v1/files?limit=80");
	const files = Array.isArray(data.files) ? data.files : [];
	setText("metric-files", String(files.length));
	const list = document.getElementById("file-list");
	list.innerHTML = "";
	if (!files.length) {
		const empty = document.createElement("div");
		empty.className = "list-item";
		empty.textContent = "No files found.";
		list.appendChild(empty);
		return;
	}
	files.slice(0, 60).forEach(path => {
		const item = document.createElement("button");
		item.type = "button";
		item.className = "list-item" + (path === state.activeFile ? " active" : "");
		item.textContent = path;
		item.addEventListener("click", () => openFile(path));
		list.appendChild(item);
	});
	if (!state.activeFile) {
		openFile(files[0]);
	}
}

async function openFile(path) {
	state.activeFile = path;
	for (const node of document.querySelectorAll("#file-list .list-item")) {
		node.classList.toggle("active", node.textContent === path);
	}
	const data = await fetchJSON("/api/v1/files/" + encodeURIComponent(path).replace(/%2F/g, "/"));
	document.getElementById("file-preview").textContent = data.content || "(empty file)";
}

async function loadCodeMap() {
	const data = await fetchJSON("/api/v1/codemap");
	const nodes = Array.isArray(data.nodes) ? data.nodes : [];
	const edges = Array.isArray(data.edges) ? data.edges : [];
	setText("codemap-nodes", String(nodes.length));
	setText("codemap-edges", String(edges.length));
	const hotspots = document.getElementById("codemap-hotspots");
	hotspots.innerHTML = "";
	const ranked = nodes
		.filter(node => node && node.name)
		.slice(0, 8);
	if (!ranked.length) {
		hotspots.innerHTML = '<div class="graph-item"><strong>CodeMap is still cold</strong><span>Run analysis or ask a question to warm it up.</span></div>';
		return;
	}
	for (const node of ranked) {
		const el = document.createElement("div");
		el.className = "graph-item";
		el.innerHTML = '<strong>' + escapeHTML(node.name || node.id || "node") + '</strong><span>' +
			escapeHTML((node.kind || "node") + (node.path ? " • " + node.path : "")) +
			'</span>';
		hotspots.appendChild(el);
	}
}

async function loadWorkspaceDiff() {
	const data = await fetchJSON("/api/v1/workspace/diff");
	const changed = Array.isArray(data.changed_files) ? data.changed_files : [];
	document.getElementById("workspace-diff").textContent = data.diff || "Working tree is clean.";
	setText("patch-status", data.clean
		? "Working tree is clean."
		: ("Changed files: " + (changed.length ? changed.join(", ") : "detected")));
}

async function loadLatestPatch() {
	const data = await fetchJSON("/api/v1/workspace/patch");
	document.getElementById("patch-editor").value = data.patch || "";
	setText("patch-status", data.patch ? "Loaded latest assistant patch." : "No assistant patch found yet.");
}

async function applyPatch(checkOnly) {
	const patch = document.getElementById("patch-editor").value.trim();
	const body = patch ? { patch, check_only: checkOnly } : { source: "latest", check_only: checkOnly };
	const data = await fetchJSON("/api/v1/workspace/apply", {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(body),
	});
	const changed = Array.isArray(data.changed_files) ? data.changed_files : [];
	setText("patch-status", checkOnly
		? "Patch check passed."
		: ("Patch applied" + (changed.length ? ": " + changed.join(", ") : ".")));
	await loadWorkspaceDiff();
}

async function undoConversation() {
	const data = await fetchJSON("/api/v1/conversation/undo", { method: "POST" });
	setText("patch-status", "Undone messages: " + String(data.removed ?? 0));
}

async function sendChat() {
	const input = document.getElementById("chat-input");
	const button = document.getElementById("chat-send");
	const hint = document.getElementById("chat-hint");
	const raw = input.value.trim() || hint.value.trim();
	if (!raw) return;

	addMessage("user", raw);
	const target = addMessage("assistant", "");
	button.disabled = true;
	setText("chat-status", "streaming");

	try {
		const resp = await fetch("/api/v1/chat", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ message: raw }),
		});
		if (!resp.ok || !resp.body) {
			throw new Error(await resp.text() || ("HTTP " + resp.status));
		}
		const reader = resp.body.getReader();
		const decoder = new TextDecoder();
		let buffer = "";
		while (true) {
			const { value, done } = await reader.read();
			if (done) break;
			buffer += decoder.decode(value, { stream: true });
			const parts = buffer.split("\n\n");
			buffer = parts.pop() || "";
			for (const part of parts) {
				const line = part.split("\n").find(item => item.startsWith("data: "));
				if (!line) continue;
				const payload = JSON.parse(line.slice(6));
				if (payload.type === "delta") {
					target.textContent += payload.delta || "";
				} else if (payload.type === "error") {
					target.textContent += "\n[error] " + (payload.error || "unknown");
				}
			}
		}
		setText("chat-status", "idle");
		input.value = "";
	} catch (err) {
		target.textContent = "[chat error] " + (err && err.message ? err.message : String(err));
		setText("chat-status", "error");
	} finally {
		button.disabled = false;
	}
}

async function boot() {
	try {
		await Promise.all([loadStatus(), loadProvidersAndSkills(), loadFiles(), loadCodeMap(), loadWorkspaceDiff()]);
	} catch (err) {
		addMessage("system", "Workbench load error: " + (err && err.message ? err.message : String(err)));
	}
	connectActivityStream();
}

// Activity panel — mirrors the TUI firehose. Consumes /ws (SSE) and
// classifies each event into a kind so the CSS can colour rows. Writes
// to the DOM via textContent only; payloads are event-source controlled.
const ACTIVITY_LIMIT = 500;
const activityState = {
	entries: [],
	follow: true,
	eventSource: null,
};

function classifyActivityEvent(ev) {
	const type = (ev && ev.event || "").toLowerCase();
	const payload = (ev && ev.payload) || {};
	const fallback = { kind: "info", icon: "\u00b7", text: type };
	if (!type) return fallback;
	if (type.startsWith("agent:")) {
		if (type === "agent:loop:start") {
			return { kind: "agent", icon: "\u25c9",
				text: "agent start \u00b7 " + (payload.provider || "") + "/" + (payload.model || "") + " max=" + (payload.max_tool_steps || 0) };
		}
		if (type === "agent:loop:thinking") {
			return { kind: "agent", icon: "\u25c9",
				text: "agent thinking \u00b7 " + (payload.step || 0) + "/" + (payload.max_tool_steps || 0) };
		}
		if (type === "agent:loop:end") {
			return { kind: "agent", icon: "\u25c9", text: "agent end \u00b7 " + (payload.reason || "done") };
		}
		if (type === "agent:loop:error") {
			return { kind: "error", icon: "\u2717", text: "agent error \u00b7 " + (payload.error || "") };
		}
		return { kind: "agent", icon: "\u25c9", text: type };
	}
	if (type.startsWith("tool:")) {
		if (type === "tool:call") {
			return { kind: "tool", icon: "\u25cc",
				text: "tool call \u00b7 " + (payload.tool || "tool") + (payload.step ? (" (step " + payload.step + ")") : "") };
		}
		if (type === "tool:result") {
			return { kind: "tool", icon: "\u25cc",
				text: "tool done \u00b7 " + (payload.tool || "tool") + " (" + (payload.duration_ms || 0) + "ms)" };
		}
		if (type === "tool:error") {
			return { kind: "error", icon: "\u2717",
				text: "tool failed \u00b7 " + (payload.tool || "tool") + " " + (payload.error || "") };
		}
	}
	if (type.startsWith("context:") || type.startsWith("ctx:")) {
		if (type === "context:lifecycle:compacted") {
			return { kind: "ctx", icon: "\u25c8",
				text: "context compacted \u00b7 " + (payload.tokens_before || 0) + " \u2192 " + (payload.tokens_after || 0) + " tok" };
		}
		return { kind: "ctx", icon: "\u25c8", text: type };
	}
	if (type.startsWith("index:")) {
		if (type === "index:done") return { kind: "index", icon: "\u25a4", text: "index done \u00b7 " + (payload.files || 0) + " files" };
		if (type === "index:error") return { kind: "error", icon: "\u2717", text: "index error \u00b7 " + (payload.error || "") };
		return { kind: "index", icon: "\u25a4", text: type };
	}
	if (type.startsWith("stream:")) return { kind: "stream", icon: "\u21e2", text: type };
	if (type.includes("error") || type.includes("fail")) {
		return { kind: "error", icon: "\u2717", text: type + (payload.error ? (" \u00b7 " + payload.error) : "") };
	}
	return fallback;
}

function renderActivityLog() {
	const el = document.getElementById("activity-log");
	if (!el) return;
	while (el.firstChild) el.removeChild(el.firstChild);
	if (activityState.entries.length === 0) {
		const empty = document.createElement("div");
		empty.className = "activity-empty";
		empty.textContent = "No events yet. Agent calls, tool use, context compaction, and index runs stream in here live.";
		el.appendChild(empty);
	} else {
		const frag = document.createDocumentFragment();
		for (const entry of activityState.entries) {
			const row = document.createElement("div");
			row.className = "activity-row kind-" + entry.kind;
			const ts = document.createElement("span"); ts.className = "ts"; ts.textContent = entry.ts;
			const kind = document.createElement("span"); kind.className = "kind"; kind.textContent = entry.icon;
			const text = document.createElement("span"); text.className = "text"; text.textContent = entry.text;
			row.appendChild(ts); row.appendChild(kind); row.appendChild(text);
			frag.appendChild(row);
		}
		el.appendChild(frag);
	}
	const summary = document.getElementById("activity-summary");
	if (summary) {
		const counts = activityState.entries.reduce((acc, e) => { acc[e.kind] = (acc[e.kind] || 0) + 1; return acc; }, {});
		summary.textContent = activityState.entries.length + " events \u00b7 tool=" + (counts.tool || 0)
			+ " agent=" + (counts.agent || 0) + " err=" + (counts.error || 0) + " ctx=" + (counts.ctx || 0);
	}
	if (activityState.follow) { el.scrollTop = el.scrollHeight; }
}

function pushActivityEntry(classified) {
	const d = new Date();
	const ts = String(d.getHours()).padStart(2,"0") + ":" + String(d.getMinutes()).padStart(2,"0") + ":" + String(d.getSeconds()).padStart(2,"0");
	const entry = { ts, kind: classified.kind, icon: classified.icon, text: classified.text };
	const last = activityState.entries[activityState.entries.length-1];
	if (last && last.kind === entry.kind && last.text === entry.text) return;
	activityState.entries.push(entry);
	if (activityState.entries.length > ACTIVITY_LIMIT) {
		activityState.entries.splice(0, activityState.entries.length - ACTIVITY_LIMIT);
	}
	renderActivityLog();
}

function setActivityStatus(text) {
	const el = document.getElementById("activity-status");
	if (el) el.textContent = text;
}

function connectActivityStream() {
	if (activityState.eventSource) return;
	try {
		const es = new EventSource("/ws");
		activityState.eventSource = es;
		setActivityStatus("connected");
		es.onmessage = (msg) => {
			try {
				const data = JSON.parse(msg.data);
				if (!data || data.type !== "event") return;
				pushActivityEntry(classifyActivityEvent(data));
			} catch (err) { /* swallow malformed frames */ }
		};
		es.onerror = () => {
			setActivityStatus("retrying...");
			if (es.readyState === 2) { activityState.eventSource = null; }
		};
	} catch (err) {
		setActivityStatus("unavailable");
	}
}

function toggleActivityFollow() {
	activityState.follow = !activityState.follow;
	const btn = document.getElementById("activity-follow");
	if (btn) btn.textContent = activityState.follow ? "Pause follow" : "Resume follow";
	if (activityState.follow) renderActivityLog();
}

function clearActivityLog() {
	activityState.entries = [];
	renderActivityLog();
}

document.getElementById("chat-send").addEventListener("click", sendChat);
document.getElementById("chat-status-refresh").addEventListener("click", () => Promise.all([loadStatus(), loadProvidersAndSkills()]));
document.getElementById("refresh-files").addEventListener("click", loadFiles);
document.getElementById("refresh-codemap").addEventListener("click", loadCodeMap);
document.getElementById("refresh-diff").addEventListener("click", loadWorkspaceDiff);
document.getElementById("load-latest-patch").addEventListener("click", loadLatestPatch);
document.getElementById("check-patch").addEventListener("click", () => applyPatch(true));
document.getElementById("apply-patch").addEventListener("click", () => applyPatch(false));
document.getElementById("undo-chat").addEventListener("click", undoConversation);
document.getElementById("activity-clear").addEventListener("click", clearActivityLog);
document.getElementById("activity-follow").addEventListener("click", toggleActivityFollow);
document.getElementById("chat-input").addEventListener("keydown", event => {
	if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
		event.preventDefault();
		sendChat();
	}
});
boot();
</script>
</body>
</html>`
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
