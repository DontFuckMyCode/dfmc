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
	"crypto/subtle"
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

type Server struct {
	engine *engine.Engine
	mux    *http.ServeMux
	addr   string
	auth   string
	token  string
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

// securityHeaders adds browser-enforced security boundaries to every
// response. The embedded workbench is self-contained, so we lock down
// CSP to 'self' only and set standard hardening headers.
func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		h.ServeHTTP(w, r)
	})
}

func New(eng *engine.Engine, host string, port int) *Server {
	authMode := "none"
	if eng != nil && eng.Config != nil {
		authMode = strings.ToLower(strings.TrimSpace(eng.Config.Web.Auth))
	}
	host = normalizeBindHost(authMode, host)
	s := &Server{
		engine: eng,
		mux:    http.NewServeMux(),
		addr:   fmt.Sprintf("%s:%d", host, port),
		auth:   authMode,
		token:  strings.TrimSpace(os.Getenv("DFMC_WEB_TOKEN")),
	}
	// Register a deny-by-default approver so a publicly-reachable serve
	// doesn't silently run gated tools; DFMC_APPROVE=yes|no lets the
	// operator pick behaviour per serve process (same semantics as CLI).
	eng.SetApprover(newWebApprover())
	s.setupRoutes()
	return s
}

func normalizeBindHost(authMode, host string) string {
	if strings.EqualFold(strings.TrimSpace(authMode), "none") && !isLoopbackBindHost(host) {
		return "127.0.0.1"
	}
	if strings.EqualFold(strings.TrimSpace(authMode), "token") && !isLoopbackBindHost(host) {
		fmt.Fprintf(os.Stderr, "[DFMC] WARNING: auth=token with non-loopback bind (%s) exposes the agent on all interfaces. Use --host 127.0.0.1 or set auth=none.\n", host)
	}
	return host
}

// isLoopbackBindHost reports whether a host value binds only to the local
// machine. Empty string is treated as non-loopback because Go binds that
// to every interface.
func isLoopbackBindHost(host string) bool {
	h := strings.TrimSpace(host)
	if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	h = strings.ToLower(h)
	switch h {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func (s *Server) SetBearerToken(token string) {
	if s == nil {
		return
	}
	s.token = strings.TrimSpace(token)
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
	handler := limitRequestBodySize(s.mux, maxRequestBodyBytes)
	handler = securityHeaders(handler)
	// Rate-limit all endpoints: 30 requests/sec per IP with burst of 60.
	limiter := newPerIPLimiter(30, 60)
	handler = rateLimitMiddleware(limiter)(handler)
	if strings.EqualFold(strings.TrimSpace(s.auth), "token") {
		handler = bearerTokenMiddleware(handler, s.token)
	}
	return handler
}

const (
	serverReadHeaderTimeout = 5 * time.Second
	serverReadTimeout       = 30 * time.Second
	serverWriteTimeout      = 2 * time.Minute
	serverIdleTimeout       = 2 * time.Minute
	serverMaxHeaderBytes    = 1 << 20
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

// perIPLimiter provides a basic per-IP rate limiter using a token-bucket
// algorithm. Each client IP gets its own bucket. Buckets for IPs not seen
// in over 10 minutes are garbage-collected periodically.
type perIPLimiter struct {
	mu      sync.Mutex
	buckets map[string]*rate.Limiter
	rate    rate.Limit
	burst   int
}

func newPerIPLimiter(r rate.Limit, burst int) *perIPLimiter {
	return &perIPLimiter{buckets: make(map[string]*rate.Limiter), rate: r, burst: burst}
}

func (l *perIPLimiter) get(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[ip]
	if !ok {
		b = rate.NewLimiter(l.rate, l.burst)
		l.buckets[ip] = b
	}
	return b
}

func (l *perIPLimiter) Allow(ip string) bool {
	return l.get(ip).Allow()
}

func rateLimitMiddleware(limiter *perIPLimiter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow(clientIPKey(r)) {
				writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate limit exceeded"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIPKey extracts the client IP for rate-limit bucketing.
// X-Forwarded-For is trusted only when the request originates from a known
// local proxy (e.g. nginx on localhost). Remote clients cannot spoof this
// header because they cannot establish a connection through the proxy without
// first passing the bearer-token auth gate.
func clientIPKey(r *http.Request) string {
	if r == nil {
		return ""
	}
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		for _, part := range strings.Split(forwarded, ",") {
			if ip := strings.TrimSpace(part); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderWorkbenchHTML()))
}

// bearerTokenMiddleware validates bearer tokens using constant-time
// comparison to prevent timing side-channels. All authenticated
// surfaces, including the /ws SSE stream, must present the bearer
// token in the Authorization header so secrets never ride in URLs.
func bearerTokenMiddleware(next http.Handler, token string) http.Handler {
	rawToken := strings.TrimSpace(token)
	expected := "Bearer " + rawToken
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/" && rawToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		if got := strings.TrimSpace(r.Header.Get("Authorization")); rawToken != "" && subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
	})
}

//go:embed static/index.html
var workbenchHTML string

func renderWorkbenchHTML() string {
	return workbenchHTML
}
