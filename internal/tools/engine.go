package tools

// engine.go — top-level Engine type and the Execute() entrypoint
// every tool dispatch funnels through. The New() constructor + the
// long default-registry build-out live in
// engine_register_defaults.go. Other collaborator siblings (each owns
// one mutex + one concern):
//
//   registry.go        — Register / Get / List / Specs / Spec /
//                        Search / MetaSpecs / BackendSpecs (e.mu).
//   lifecycle.go       — Close / clearSessionState / LockPath
//                        (e.lifecycleMu, ErrEngineClosed, toolCloser).
//   timeout.go         — toolTimeout, ToolTimeoutError, the
//                        selfManagedTimeoutTools allowlist.
//   failure_tracker.go — trackFailure / clearFailure +
//                        toolFailureKey (e.failureMu).
//   snapshot_cache.go  — read-before-mutate gate, read-snapshot LRU,
//                        recordReadSnapshot / EnsureReadBeforeMutation
//                        (e.readMu).
//
// Mutex acquisition order — DO NOT REORDER:
//
//     lifecycleMu  (lifecycle gate; held by Close, RLock by Register/Execute)
//       └─ mu     (registry; brief Lock/RLock per call)
//       └─ failureMu (independent of mu; failure ledger only)
//       └─ readMu    (independent of mu; snapshot LRU only)
//
// Execute() takes lifecycleMu.RLock first, then briefly Lock/RLocks
// the per-concern mutexes via the helpers above. failureMu and readMu
// are never held while calling out to a tool.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/mcp"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

type Request struct {
	ProjectRoot string         `json:"project_root"`
	Params      map[string]any `json:"params,omitempty"`
}

type Result struct {
	Success    bool           `json:"success"`
	Output     string         `json:"output,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	Truncated  bool           `json:"truncated,omitempty"`
	DurationMs int64          `json:"duration_ms"`
}

type Tool interface {
	Name() string
	Description() string
	Execute(ctx context.Context, req Request) (Result, error)
}

type Engine struct {
	lifecycleMu     sync.RWMutex
	mu              sync.RWMutex
	registry        map[string]Tool
	cfg             ToolsConfigSubset
	closed          bool
	activeLayers    []Layer // which non-meta layers are active; nil=all layers
	failureMu       sync.Mutex
	recentFailures  map[string]int
	recentFailOrder []string
	failureOrderIdx map[string]int // O(1) reverse lookup for clearFailure
	readMu          sync.RWMutex
	readSnapshots   map[string]string
	readSnapshotLRU []string
	// readSnapshotCap and recentFailureCap mirror cfg.Agent.ReadSnapshotCap
	// / RecentFailureCap; populated in New() so eviction loops don't have
	// to consult cfg on every call. Falls back to package constants when
	// the cfg value is zero (legacy behaviour preserved for callers that
	// build a tools.Engine without going through engine.Init).
	readSnapshotCap  int
	recentFailureCap int
	delegateTool     *DelegateTaskTool
	orchestrateTool  *OrchestrateTool

	// reasoningPublisher is the optional callback the higher-level engine
	// installs at construction to receive tool self-narration. Execute()
	// strips the virtual `_reason` arg from every params map and, when
	// non-empty, calls this with (toolName, reason). Nil-safe: when no
	// publisher is installed (tests, embedded use), the field is just
	// stripped silently. Atomic via the mu lock.
	reasoningPublisher ReasoningPublisher

	// taskStore is the SQLite-backed task persistence. Injected via
	// SetTaskStore so the package stays free of an engine-cycle.
	taskStore *taskstore.Store

	// codemap is the project codemap engine. Injected via SetCodemap
	// so the dependency_graph tool can query edges without importing
	// the engine package (which would create a cycle).
	codemap *codemap.Engine

	// mcpBridge exposes MCP server tools. Set by the engine-side MCP
	// bridge adapter after clients are loaded. Nil when no MCP servers
	// are configured.
	mcpBridge mcp.ToolBridge

	// pathLocks serialises concurrent (read-gate-check → write) operations
	// on the same absolute path. Sub-agent fan-out can touch the same file
	// from multiple goroutines; without serialisation the window between
	// EnsureReadBeforeMutation and os.WriteFile is a TOCTOU race.
	pathLocks sync.Map

	// disabled tracks tools that have been disabled at runtime. A disabled
	// tool is filtered out of Specs/Search/List and refused at Execute.
	disabled *disabledState
}

// ReasoningPublisher is the callback shape the higher-level engine wires
// into tools.Engine to surface tool-call self-narration. The engine
// translates these into `tool:reasoning` events on its EventBus so TUI/
// web/CLI can render the why above each tool result. Kept as a function
// type (not an interface) so the tools package doesn't import the
// engine package — that would create a cycle.
type ReasoningPublisher func(toolName, reason string)

type toolReasonContextKey struct{}

// SetReasoningPublisher installs the self-narration callback. Safe to
// call before or after registration; the publisher is consulted on
// every Execute(). Pass nil to disable.
func (e *Engine) SetReasoningPublisher(pub ReasoningPublisher) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reasoningPublisher = pub
}

func (e *Engine) SetTaskStore(store *taskstore.Store) {
	e.taskStore = store
	t, ok := e.Get("todo_write")
	if !ok {
		return
	}
	if tw, ok := t.(*TodoWriteTool); ok {
		tw.SetStore(store)
	}
}

// TaskStore returns the injected task store, or nil when no store was set.
func (e *Engine) TaskStore() *taskstore.Store {
	return e.taskStore
}

// SetCodemap injects the project codemap engine so the dependency_graph
// tool can query edges. Called by engine.Init after CodeMap is wired.
func (e *Engine) SetCodemap(cm *codemap.Engine) {
	e.codemap = cm
}

// SetMCPBridge installs the MCP bridge after external clients are loaded.
// The bridge exposes tools from one or more MCP servers as native tools.
func (e *Engine) SetMCPBridge(bridge mcp.ToolBridge) {
	e.mcpBridge = bridge
}

// SetSubagentRunner wires the delegate_task and orchestrate tools to the
// engine's sub-agent entry point. Engines call this once the agent loop is
// fully constructed.
func (e *Engine) SetSubagentRunner(r SubagentRunner) {
	if e.delegateTool != nil {
		e.delegateTool.SetRunner(r)
	}
	if e.orchestrateTool != nil {
		e.orchestrateTool.SetRunner(r)
	}
}

// TodoSnapshot returns the current todo list recorded by the todo_write
// tool, or nil when the tool is not registered. Safe for concurrent use;
// the slice returned is a copy, not the live state.
func (e *Engine) TodoSnapshot() []TodoItem {
	if e == nil {
		return nil
	}
	tool, ok := e.Get("todo_write")
	if !ok {
		return nil
	}
	tw, ok := tool.(*TodoWriteTool)
	if !ok {
		return nil
	}
	return tw.Snapshot()
}

func (e *Engine) Execute(ctx context.Context, name string, req Request) (Result, error) {
	e.lifecycleMu.RLock()
	if e.closed {
		e.lifecycleMu.RUnlock()
		return Result{}, ErrEngineClosed
	}
	defer e.lifecycleMu.RUnlock()

	start := time.Now()
	tool, ok := e.Get(name)
	if !ok {
		return Result{}, fmt.Errorf(
			"tool not found: %q. "+
				"Discover the right name with tool_search: "+
				`{"name":"tool_search","args":{"query":"%s"}}. `+
				"Common backend tools: read_file, write_file, edit_file, list_dir, grep_codebase, glob, find_symbol, codemap, ast_query, run_command, web_fetch, todo_write",
			name, name)
	}
	if e.disabled.IsDisabled(name) {
		return Result{}, fmt.Errorf("%w: %q is disabled and cannot be called. Enable it via the tools panel or dfmc tools enable %s", ErrToolDisabled, name, name)
	}

	projectRoot := strings.TrimSpace(req.ProjectRoot)
	if projectRoot == "" {
		cwd, _ := os.Getwd()
		projectRoot = cwd
	}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve project root: %w", err)
	}
	req.ProjectRoot = absRoot
	req.Params = normalizeToolParams(name, req.Params)
	// Self-narration: peel off the optional `_reason` virtual field and
	// publish it before the call so UIs can render the why before the
	// tool result appears. We strip even when no publisher is installed
	// so tools never see the field as unexpected input.
	if reason, ok := ExtractReason(req.Params); ok {
		e.mu.RLock()
		pub := e.reasoningPublisher
		e.mu.RUnlock()
		if pub != nil {
			pub(name, reason)
		}
		ctx = context.WithValue(ctx, toolReasonContextKey{}, reason)
	}
	if mode := readBeforeMutationMode(name); mode != readGateNone {
		path := asString(req.Params, "path", "")
		absPath, err := EnsureWithinRoot(req.ProjectRoot, path)
		if err != nil {
			return Result{}, err
		}
		if err := e.ensureReadBeforeMutationMode(absPath, mode); err != nil {
			return Result{}, err
		}
	}
	failureKey := toolFailureKey(name, req.Params)

	// Per-tool execution timeout. Tools listed in selfManagedTimeoutTools
	// own their own deadlines (run_command's per-call timeout, web client's
	// 20s HTTP timeout, recursive sub-agent loops); wrapping them with an
	// outer cap either fights internal timeouts or trims a legitimately
	// long inner agent run. Everything else gets cfg.Agent.ToolTimeouts[name]
	// or cfg.Agent.ToolDefaultTimeoutSeconds — defaults to 30s. 0 disables.
	timeout := e.toolTimeout(name)
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	res, err := tool.Execute(ctx, req)
	res.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		if timeout > 0 && (errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)) {
			// Wrap into a typed sentinel so the engine wrapper can publish
			// a distinct tool:timeout event AND the model still sees a
			// self-teaching message (cap + override path) via Error().
			err = &ToolTimeoutError{Name: name, Limit: timeout}
		}
		if n := e.trackFailure(failureKey); n >= 3 {
			return res, fmt.Errorf("tool %q failed repeatedly (%d times); change params or strategy", name, n)
		}
		return res, err
	}
	e.clearFailure(failureKey)
	res = e.compressToolOutput(req, res)
	e.recordReadSnapshot(name, req.ProjectRoot, req.Params, res)
	res.Success = true
	return res, nil
}

// EnsureWithinRoot, PathRelativeToRoot, isPathWithin, resolveExistingAncestor
// live in path_utils.go.
