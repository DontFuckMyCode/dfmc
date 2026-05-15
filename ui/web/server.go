// Package web hosts the embedded DFMC HTTP surface (`dfmc serve`).
//
// server.go keeps Server construction, lifecycle, and route wiring. JSON
// request types live in server_types.go; origin/host helpers and the
// browser-hardening response middleware live in server_origin.go; the
// request-pipeline middleware (rate limiter, bearer auth, content-type,
// body-size cap, trusted-proxy resolution) lives in server_middleware.go.
// Per-domain handlers split into siblings:
//
//   - server_status.go       handleStatus + approval/hooks summarisers
//   - server_chat.go         handleAsk, handleChat, handleWebSocket, writeSSEWithDeadline/writeJSON
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
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

type Server struct {
	engine         *engine.Engine
	mux            *http.ServeMux
	addr           string
	auth           string
	token          string
	allowedOrigins []string
	allowedHosts   []string
	trustedProxies []string
	wsConnLimiter  *wsConnLimiter
	// limiter is the per-IP rate limiter shared across every handler
	// invocation. Created lazily on first Handler() call so test
	// fixtures that never invoke Handler() pay zero cost; cached so
	// repeated Handler() calls (or callers wrapping it in additional
	// middleware layers) reuse the same goroutine. Close() stops it.
	limiter   *perIPLimiter
	limiterMu sync.Mutex
}

func New(eng *engine.Engine, host string, port int) *Server {
	authMode := "none"
	allowedOrigins := []string{"http://127.0.0.1", "http://localhost"}
	allowedHosts := []string{"127.0.0.1", "localhost"}
	if eng != nil && eng.Config != nil {
		authMode = strings.ToLower(strings.TrimSpace(eng.Config.Web.Auth))
		if len(eng.Config.Web.AllowedOrigins) > 0 {
			allowedOrigins = eng.Config.Web.AllowedOrigins
			if slices.Contains(allowedOrigins, "*") {
				fmt.Fprintf(os.Stderr, "[DFMC] WARNING: allowed_origins contains \"*\" which disables origin checking — rejecting all origins for WebSocket upgrades.\n")
			}
		}
		if len(eng.Config.Web.AllowedHosts) > 0 {
			allowedHosts = eng.Config.Web.AllowedHosts
		}
	}
	// HIGH-002 fix: trust configured proxies; fall back to loopback-only.
	trustedProxies := []string{"127.0.0.1", "localhost", "::1"}
	if eng != nil && eng.Config != nil && len(eng.Config.Web.TrustedProxies) > 0 {
		trustedProxies = eng.Config.Web.TrustedProxies
	}
	host = normalizeBindHost(authMode, host)
	s := &Server{
		engine:         eng,
		mux:            http.NewServeMux(),
		addr:           fmt.Sprintf("%s:%d", host, port),
		auth:           authMode,
		token:          strings.TrimSpace(os.Getenv("DFMC_WEB_TOKEN")),
		allowedOrigins: allowedOrigins,
		allowedHosts:   allowedHosts,
		trustedProxies: trustedProxies,
		wsConnLimiter:  newWSConnLimiter(wsGlobalConnCap, wsPerIPConnCap),
	}
	// Register a deny-by-default approver so a publicly-reachable serve
	// doesn't silently run gated tools; DFMC_APPROVE=yes|no lets the
	// operator pick behaviour per serve process (same semantics as CLI).
	eng.SetApprover(newWebApprover())
	s.setupRoutes()
	return s
}

func (s *Server) SetBearerToken(token string) {
	if s == nil {
		return
	}
	s.token = strings.TrimSpace(token)
}

func (s *Server) SetAllowedOrigins(origins []string) {
	if s == nil {
		return
	}
	s.allowedOrigins = origins
}

func (s *Server) SetAllowedHosts(hosts []string) {
	if s == nil {
		return
	}
	s.allowedHosts = hosts
}

func (s *Server) SetTrustedProxies(proxies []string) {
	if s == nil {
		return
	}
	s.trustedProxies = proxies
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
	s.mux.HandleFunc("GET /api/v1/context/gc", s.handleContextGC)
	s.mux.HandleFunc("POST /api/v1/context/gc", s.handleContextGC)
	s.mux.HandleFunc("GET /api/v1/providers", s.handleProviders)
	s.mux.HandleFunc("GET /api/v1/langintel", s.handleLangIntel)
	s.mux.HandleFunc("GET /api/v1/skills", s.handleSkills)
	s.mux.HandleFunc("GET /api/v1/agents", s.handleAgents)
	s.mux.HandleFunc("GET /api/v1/tools", s.handleTools)
	s.mux.HandleFunc("GET /api/v1/tools/{name}", s.handleToolSpec)
	s.mux.HandleFunc("POST /api/v1/tools/{name}", s.handleToolExec)
	s.mux.HandleFunc("POST /api/v1/tools/{name}/toggle", s.handleToolToggle)
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
	s.mux.HandleFunc("POST /api/v1/drive", s.handleDriveStart)
	s.mux.HandleFunc("GET /api/v1/drive", s.handleDriveList)
	s.mux.HandleFunc("GET /api/v1/drive/{id}", s.handleDriveShow)
	s.mux.HandleFunc("POST /api/v1/drive/{id}/resume", s.handleDriveResume)
	s.mux.HandleFunc("POST /api/v1/drive/{id}/stop", s.handleDriveStop)
	s.mux.HandleFunc("GET /api/v1/drive/active", s.handleDriveActive)
	s.mux.HandleFunc("DELETE /api/v1/drive/{id}", s.handleDriveDelete)

	// Task store CRUD
	s.mux.HandleFunc("GET /api/v1/tasks", s.handleTaskList)
	s.mux.HandleFunc("POST /api/v1/tasks", s.handleTaskCreate)
	s.mux.HandleFunc("GET /api/v1/tasks/{id}", s.handleTaskShow)
	s.mux.HandleFunc("PATCH /api/v1/tasks/{id}", s.handleTaskUpdate)
	s.mux.HandleFunc("DELETE /api/v1/tasks/{id}", s.handleTaskDelete)
	s.mux.HandleFunc("GET /ws", s.handleWebSocket)
	s.mux.HandleFunc("GET /api/v1/ws", s.handleWebSocketUpgrade)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) Start() error {
	fmt.Printf("DFMC Web API listening on http://%s\n", s.addr)
	return NewHTTPServer(s.addr, s.Handler()).ListenAndServe()
}

// Handler returns the server's root http.Handler with every request
// wrapped in a body-size limiter. Callers that front the server with
// additional middleware (bearer-token auth in the CLI's `dfmc serve`)
// keep composing on top of this — the limiter sits at the bottom so
// huge bodies can never slip past before auth decisions are made.
func (s *Server) Handler() http.Handler {
	// VULN-050: reject non-JSON content types on state-changing requests
	// before the body is decoded. Must be outermost so body-size limits
	// still apply even to rejected payloads.
	handler := contentTypeEnforcementMiddleware(s.mux)
	handler = limitRequestBodySize(handler, maxRequestBodyBytes)
	handler = hostAllowlistMiddleware(handler, s.allowedHosts)
	handler = securityHeaders(handler)
	// Rate-limit all endpoints: 30 requests/sec per IP with burst of 60.
	// Cache the limiter on the Server so repeated Handler() calls (each
	// httptest.NewServer in the test suite invokes Handler() at least
	// once) reuse the same goroutine. The previous implementation
	// allocated a fresh limiter — and a fresh background gc goroutine
	// that was never stopped — on every call.
	handler = rateLimitMiddleware(s, s.acquireLimiter())(handler)
	if strings.EqualFold(strings.TrimSpace(s.auth), "token") {
		handler = bearerTokenMiddleware(handler, s.token)
	}
	return handler
}

// acquireLimiter returns the Server's rate limiter, lazily creating
// it on first use. Concurrent callers see one limiter instance.
func (s *Server) acquireLimiter() *perIPLimiter {
	s.limiterMu.Lock()
	defer s.limiterMu.Unlock()
	if s.limiter == nil {
		s.limiter = newPerIPLimiter(30, 60)
	}
	return s.limiter
}

// Close releases server-owned background resources. Currently stops
// the rate-limiter gc goroutine; cheap to extend if future background
// tasks land on the Server. Safe to call multiple times (Stop is
// idempotent) and safe to call without ever having invoked Handler()
// — acquireLimiter is lazy so an unused Server allocates nothing to
// release.
func (s *Server) Close() error {
	s.limiterMu.Lock()
	limiter := s.limiter
	s.limiterMu.Unlock()
	limiter.Stop()
	return nil
}

const (
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 30 * time.Second
	serverWriteTimeout      = 2 * time.Minute
	serverIdleTimeout       = 2 * time.Minute
	serverMaxHeaderBytes    = 1 << 20
	taskListLimitMax        = 500
)

// NewHTTPServer applies the timeout and header-size hardening we want on
// every DFMC HTTP surface. Streaming handlers such as /ws clear the write
// deadline explicitly so long-lived SSE connections still work.
func NewHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		ReadTimeout:       serverReadTimeout,
		WriteTimeout:      serverWriteTimeout,
		IdleTimeout:       serverIdleTimeout,
		MaxHeaderBytes:    serverMaxHeaderBytes,
	}
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
