// Package web hosts the embedded DFMC HTTP surface (`dfmc serve`).
//
// server.go keeps construction and wiring only; handlers live in sibling
// files grouped by domain:
//
//   - server_status.go       handleStatus + approval/hooks summarisers
//   - server_chat.go         handleAsk, handleChat, handleWebSocket, writeSSE/writeJSON
//   - server_context.go      context/prompts/magicdoc/analyze + shared helpers
//   - server_tools_skills.go tools, skills, providers, commands, codemap, memory
//   - server_conversation.go conversation list/search/load/save/undo + branches
//   - server_workspace.go    workspace diff/patch/apply + git shell helpers
//   - server_files.go        file listing / content + path-traversal guard
//   - server_admin.go        scan / doctor / hooks / config — CLI parity
//
// The 925-line workbench UI lives in static/index.html and is pulled in via
// //go:embed below; renderWorkbenchHTML simply surfaces it as a string.

package web

import (
	_ "embed"
	"fmt"
	"net/http"

	"github.com/dontfuckmycode/dfmc/internal/engine"
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
	Message       string   `json:"message"`
	Race          bool     `json:"race,omitempty"`
	RaceProviders []string `json:"race_providers,omitempty"`
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
	s.mux.HandleFunc("GET /api/v1/scan", s.handleScan)
	s.mux.HandleFunc("GET /api/v1/doctor", s.handleDoctor)
	s.mux.HandleFunc("GET /api/v1/hooks", s.handleHooks)
	s.mux.HandleFunc("GET /api/v1/config", s.handleConfigGet)
	s.mux.HandleFunc("GET /ws", s.handleWebSocket)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) Start() error {
	fmt.Printf("DFMC Web API listening on http://%s\n", s.addr)
	return http.ListenAndServe(s.addr, s.Handler())
}

// Handler returns the server's root http.Handler with every request
// wrapped in a body-size limiter. Callers that front the server with
// additional middleware (bearer-token auth in the CLI's `dfmc serve`)
// keep composing on top of this — the limiter sits at the bottom so
// huge bodies can never slip past before auth decisions are made.
func (s *Server) Handler() http.Handler {
	return limitRequestBodySize(s.mux, maxRequestBodyBytes)
}

// maxRequestBodyBytes caps the size of a single POST/PUT/PATCH body.
// 4 MiB is generous for any chat message or workspace patch the CLI
// would ever send (typical is < 100 KB); the cap exists so a
// malicious or buggy client can't exhaust memory streaming endless
// JSON into a single Decode call. Overflow surfaces as 413 from the
// stdlib's http.MaxBytesReader automatically.
const maxRequestBodyBytes int64 = 4 * 1024 * 1024

func limitRequestBodySize(h http.Handler, max int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				r.Body = http.MaxBytesReader(w, r.Body, max)
			}
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderWorkbenchHTML()))
}

//go:embed static/index.html
var workbenchHTML string

func renderWorkbenchHTML() string {
	return workbenchHTML
}
