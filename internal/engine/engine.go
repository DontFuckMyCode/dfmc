// engine.go is the skeleton of the Engine. It owns construction,
// lifecycle, and shared state only. Domain methods live in sibling
// files so this one stays small enough to hold the whole mental
// model of "what is an engine?" in your head:
//
//   - engine_tools.go       ListTools, CallTool, tool exec lifecycle
//                           (approval + pre/post hooks + panic guard).
//   - engine_context.go     Context budgeting / recommendations /
//                           tuning, buildContextChunks, reserve
//                           breakdown, compression & task profile.
//   - engine_prompt.go      Prompt recommendations, PromptRuntime
//                           resolver, buildSystemPrompt, system
//                           blocks assembly.
//   - engine_ask.go         Ask / AskRaced / AskWithMetadata /
//                           StreamAsk, history budgeting & summary,
//                           indexCodebase, recordInteraction.
//   - engine_passthrough.go Status, Memory*, Conversation*, provider
//                           status, SetProviderModel / SetVerbose /
//                           ReloadConfig.
//   - engine_analyze.go     Analyze pipeline: dead-code detection,
//                           complexity scoring, language-aware text
//                           strippers.
//
// Status / report / option types live in status_types.go; the native
// agent loop lives in agent_loop_native*.go.

package engine

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/hooks"
	"github.com/dontfuckmycode/dfmc/internal/intent"
	"github.com/dontfuckmycode/dfmc/internal/memory"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/security"
	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type EngineState int

const (
	StateCreated EngineState = iota
	StateInitializing
	StateReady
	StateServing
	StateShuttingDown
	StateStopped
)

type Engine struct {
	Config       *config.Config
	Storage      *storage.Store
	EventBus     *EventBus
	ProjectRoot  string
	AST          *ast.Engine
	CodeMap      *codemap.Engine
	Context      *ctxmgr.Manager
	Providers    *provider.Router
	Tools        *tools.Engine
	Memory       *memory.Store
	Conversation *conversation.Manager
	Security     *security.Scanner
	// Hooks dispatches user-configured shell commands on lifecycle events
	// (user_prompt_submit, pre_tool, post_tool, session_start/end). A nil
	// value is safe — Fire is a no-op on nil.
	Hooks *hooks.Dispatcher

	// Intent is the state-aware request normalizer that runs before each
	// Ask. Built in Init from Config.Intent + Providers; nil-safe in
	// every consumer (a nil router falls back to the raw input). See
	// internal/intent for the routing semantics.
	Intent *intent.Router

	providerOverride string
	modelOverride    string
	verbose          bool

	// Lock ordering (MUST be held in this order to avoid deadlocks):
	//   1. agentMu   — agent lifecycle + parked state
	//   2. mu        — general state (state, lastContextIn, background)
	// Never acquire agentMu while holding mu. Shutdown only touches mu;
	// the agent loop only touches agentMu and reads state via State()
	// which takes mu independently.
	mu    sync.RWMutex
	state EngineState

	lastContextIn ContextInStatus

	// memoryDegraded is set when Memory.Load() failed during Init. The
	// engine keeps running with an empty in-memory store so the user
	// isn't hard-blocked, but Status surfaces the flag so the TUI can
	// show a "memory disabled" notice and downstream code can decide
	// not to rely on historical recall.
	memoryDegraded bool
	memoryLoadErr  string

	// Background lifecycle. indexCancel cancels the initial codebase
	// indexer; indexWG waits for it (and any other engine-owned
	// goroutines) to finish before Shutdown tears down AST / CodeMap /
	// Storage. Without these, Shutdown could race with a still-running
	// indexer touching the CodeMap after it's been closed.
	backgroundCtx    context.Context
	backgroundCancel context.CancelFunc
	indexCancel      context.CancelFunc
	indexWG          sync.WaitGroup

	agentMu         sync.Mutex
	agentParked     *parkedAgentState
	agentNotesQueue []string
	// Project config snapshot used for pre-ask auto-reload. When the user
	// edits .dfmc/config.yaml while the TUI is already running, the next ask
	// should not keep using stale provider/tool state until they remember to
	// type /reload manually.
	configProjectPath    string
	configProjectModTime time.Time
	// subagentInFlight counts active RunSubagent calls. The first subagent
	// to start stashes the parent's parked state under subagentStashed;
	// nested/concurrent subagents bump the counter without touching it.
	// The last subagent to finish restores it. Guarded by agentMu.
	subagentInFlight int
	subagentStashed  *parkedAgentState
}

// Status / report / option types live in status_types.go — keeping
// engine.go for the runtime (Engine struct, lifecycle methods, hot
// loop) instead of mixing JSON-shape data carriers in alongside.

func New(cfg *config.Config) (*Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	return &Engine{
		Config:   cfg,
		EventBus: NewEventBus(),
		state:    StateCreated,
	}, nil
}

func (e *Engine) Init(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	e.setState(StateInitializing)
	e.EventBus.Publish(Event{Type: "engine:initializing", Source: "engine"})

	// Wire SafeGo's panic observer so background-goroutine panics that
	// would otherwise be log-only land on the EventBus as runtime:panic.
	// The TUI activity log + web /ws stream + remote subscribers can
	// then surface them instead of users only seeing a quiet log line.
	// Stack is truncated via the same helper as tool panics so a 10 KiB
	// runtime dump doesn't bloat the activity feed.
	types.SetSafeGoPanicObserver(func(name string, recovered any, stack []byte) {
		e.EventBus.Publish(Event{
			Type:   "runtime:panic",
			Source: "safego",
			Payload: map[string]any{
				"name":  name,
				"panic": fmt.Sprintf("%v", recovered),
				"stack": truncateStackForError(stack),
			},
		})
	})

	store, err := storage.Open(e.Config.DataDir())
	if err != nil {
		return fmt.Errorf("storage init failed: %w", err)
	}
	e.Storage = store
	e.AST = ast.NewWithCacheSize(e.Config.AST.CacheSize)
	e.CodeMap = codemap.New(e.AST)
	e.Context = ctxmgr.New(e.CodeMap)
	e.Tools = tools.New(*e.Config)
	e.Tools.SetSubagentRunner(e)
	// Wire tool self-narration: the tools.Engine strips the optional
	// `_reason` virtual field from every params map before dispatch and
	// hands the text to this callback. We translate that into a
	// tool:reasoning event so TUI/web/CLI surfaces can render the WHY
	// above each tool result. Disabled when agent.tool_reasoning="off".
	if e.toolReasoningEnabled() {
		e.Tools.SetReasoningPublisher(func(toolName, reason string) {
			e.EventBus.Publish(Event{
				Type:   "tool:reasoning",
				Source: "engine",
				Payload: map[string]any{
					"tool":   toolName,
					"reason": reason,
				},
			})
		})
	}
	e.Memory = memory.New(e.Storage)
	// Memory.Load failure is non-fatal — a corrupt or missing memory
	// db must not stop the user from running dfmc. But silently
	// proceeding with an empty store masks real data loss; flag
	// degradation so Status surfaces it and publish an event so the
	// TUI can render a notice next to the chat header.
	if err := e.Memory.Load(); err != nil {
		e.mu.Lock()
		e.memoryDegraded = true
		e.memoryLoadErr = err.Error()
		e.mu.Unlock()
		e.EventBus.Publish(Event{
			Type:   "memory:degraded",
			Source: "engine",
			Payload: map[string]any{
				"reason": err.Error(),
			},
		})
	}
	e.Conversation = conversation.New(e.Storage)
	e.Security = security.New()

	e.Providers, err = provider.NewRouter(e.Config.Providers)
	if err != nil {
		return fmt.Errorf("provider router init failed: %w", err)
	}
	e.attachProviderObservers(e.Providers)

	backgroundCtx, backgroundCancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.backgroundCtx = backgroundCtx
	e.backgroundCancel = backgroundCancel
	e.mu.Unlock()

	// Intent router runs a small classifier before each Ask to disambiguate
	// resume vs. new vs. clarify and rewrite vague messages ("devam et",
	// "fix it") into self-contained instructions for the main model.
	// nil-safe in every consumer; fail-open by default.
	e.Intent = intent.NewRouter(e.Config.Intent, func(name string) (provider.Provider, bool) {
		if e.Providers == nil {
			return nil, false
		}
		return e.Providers.Get(name)
	})

	// Hook dispatcher with observer that relays every hook outcome
	// through the engine's event bus, so the TUI / Web UI / remote
	// clients can render hook activity in the same timeline as tool
	// calls and engine lifecycle events.
	e.Hooks = hooks.New(e.Config.Hooks, func(r hooks.Report) {
		e.EventBus.Publish(Event{
			Type:   "hook:run",
			Source: "hooks",
			Payload: map[string]any{
				"event":       string(r.Event),
				"name":        r.Name,
				"command":     r.Command,
				"exit_code":   r.ExitCode,
				"duration_ms": r.Duration.Milliseconds(),
				"err":         errString(r.Err),
			},
		})
	})

	e.ProjectRoot = config.FindProjectRoot("")
	e.refreshProjectConfigSnapshot(e.projectConfigPath())
	if e.ProjectRoot != "" {
		// Derive a cancellable child context so Shutdown can tell the
		// indexer to stop, then Wait for it before tearing down the
		// Storage / AST / CodeMap it's reading from. Without this we
		// could panic when the indexer writes to a closed store.
		indexCtx, cancel := context.WithCancel(ctx)
		e.mu.Lock()
		e.indexCancel = cancel
		e.mu.Unlock()
		e.indexWG.Add(1)
		go func() {
			defer e.indexWG.Done()
			e.indexCodebase(indexCtx)
		}()
	}

	// Fire session_start AFTER ProjectRoot is resolved so hooks have
	// access to the right cwd via os.Getwd() even if the user launched
	// dfmc from a subdirectory.
	e.Hooks.Fire(ctx, hooks.EventSessionStart, hooks.Payload{
		"project_root": e.ProjectRoot,
	})

	e.setState(StateReady)
	e.EventBus.Publish(Event{Type: "engine:ready", Source: "engine"})
	return nil
}

// ListTools / CallTool and the shared tool exec lifecycle live in
// engine_tools.go.

func (e *Engine) StartServing() {
	e.setState(StateServing)
	e.EventBus.Publish(Event{Type: "engine:serving", Source: "engine"})
}

func (e *Engine) Shutdown() {
	e.setState(StateShuttingDown)
	e.EventBus.Publish(Event{Type: "engine:shutdown", Source: "engine"})

	// Cancel and join background goroutines (initial codebase indexer,
	// anything else started via indexWG.Add) BEFORE tearing down the
	// stores they're reading from. The indexer writes into CodeMap
	// which in turn touches AST; closing Storage mid-write panics.
	e.mu.Lock()
	backgroundCancel := e.backgroundCancel
	e.backgroundCancel = nil
	e.backgroundCtx = nil
	cancel := e.indexCancel
	e.indexCancel = nil
	e.mu.Unlock()
	if backgroundCancel != nil {
		backgroundCancel()
	}
	if cancel != nil {
		cancel()
	}
	e.indexWG.Wait()

	// session_end fires after background goroutines have stopped so
	// hooks see a quiesced state, but before we close Storage — hooks
	// legitimately read conversation/memory here.
	if e.Hooks != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		e.Hooks.Fire(ctx, hooks.EventSessionEnd, hooks.Payload{
			"project_root": e.ProjectRoot,
		})
		if ctx.Err() == context.DeadlineExceeded {
			fmt.Fprintf(os.Stderr, "dfmc: warning: session_end hook timed out after 5s\n")
		}
	}

	// Persist and close in stage order. We deliberately keep this method
	// void because main.go's `defer eng.Shutdown()` shouldn't have to
	// branch on an error — but we can't silently swallow failures here
	// either. Disk-full / permission-denied during conversation save or
	// memory persist used to vanish into _ = err. Now each stage that
	// fails publishes an event AND prints to stderr so the user sees the
	// data-loss before the process exits.
	if e.Conversation != nil {
		if err := e.Conversation.SaveActive(); err != nil {
			e.reportShutdownError("save_conversation", err)
		}
	}
	if e.Memory != nil {
		if err := e.Memory.Persist(); err != nil {
			e.reportShutdownError("persist_memory", err)
		}
	}
	if e.Tools != nil {
		if err := e.Tools.Close(); err != nil {
			e.reportShutdownError("close_tools", err)
		}
	}
	if e.Storage != nil {
		if err := e.Storage.Close(); err != nil {
			e.reportShutdownError("close_storage", err)
		}
	}

	// Drop *Engine-keyed slots from the package-level approver/denials
	// maps so a host that creates/destroys engines (web server, TUI,
	// integration tests) doesn't leak entries forever. Without this the
	// pinned *Engine pointer also defeats GC of every object the engine
	// transitively holds. See REPORT.md #1.
	e.cleanupApproverState()

	e.setState(StateStopped)
	e.EventBus.Publish(Event{Type: "engine:stopped", Source: "engine"})
}

// reportShutdownError surfaces a Shutdown-stage failure on both the
// EventBus (so live observers like the TUI and web /ws stream see it)
// and stderr (so the operator sees it after the process exits, even if
// the bus is no longer being read by then).
func (e *Engine) reportShutdownError(stage string, err error) {
	if err == nil {
		return
	}
	e.EventBus.Publish(Event{
		Type:   "engine:shutdown_error",
		Source: "engine",
		Payload: map[string]any{
			"stage": stage,
			"error": err.Error(),
		},
	})
	fmt.Fprintf(os.Stderr, "dfmc: shutdown %s failed: %v\n", stage, err)
}

func (e *Engine) State() EngineState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

// Status + cloneContextInStatus live in engine_passthrough.go.
// ContextBudgetPreview / Recommendations / TuningSuggestions live in
// engine_context.go. PromptRecommendation lives in engine_prompt.go.
// SetProviderModel / SetVerbose / ReloadConfig / provider / model and
// provider-status helpers live in engine_passthrough.go.
// buildContextChunks / contextBuildOptions* / setLastContextInStatus /
// buildContextInStatus / normalizeContextPathForStatus /
// explainContextFileReason / contextPathMatchesMention /
// detectContextTask / contextTaskProfile + helpers /
// providerMaxContext* / normalizeContextCompression and compression
// rank helpers / contextReserveBreakdown* live in engine_context.go.
// promptRuntime / PromptRuntime / promptRuntimeWithOverrides /
// promptRuntimeForProvider live in engine_prompt.go.
// buildRequestMessages / conversation history budgeting / trim helpers
// / buildHistorySummary / latestOmittedByRole / recentUserQuestions /
// topTermsFromMessages / topFileMentions live in engine_ask.go.

func (e *Engine) setState(state EngineState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = state
}

func (e *Engine) requireReady(op string) error {
	if e == nil {
		return fmt.Errorf("engine is nil")
	}
	state := e.State()
	switch state {
	case StateReady, StateServing, StateShuttingDown:
		return nil
	default:
		if state == StateCreated || state == StateInitializing {
			if e.Tools != nil || e.Providers != nil || e.Conversation != nil || e.Memory != nil || e.AST != nil || e.CodeMap != nil {
				return nil
			}
		}
		if strings.TrimSpace(op) == "" {
			op = "operation"
		}
		return fmt.Errorf("engine not initialized for %s (state=%v)", op, state)
	}
}

// BackgroundContext returns the engine-owned lifecycle context for
// long-lived work started from UI/API surfaces. It is cancelled during
// Shutdown so callers can stop before Storage and other subsystems are
// torn down.
func (e *Engine) BackgroundContext() context.Context {
	e.mu.RLock()
	ctx := e.backgroundCtx
	e.mu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// StartBackgroundTask runs fn under the engine's lifecycle context and
// joins it during Shutdown via indexWG. The function should return
// promptly when ctx is cancelled.
func (e *Engine) StartBackgroundTask(name string, fn func(context.Context)) {
	if fn == nil {
		return
	}
	ctx := e.BackgroundContext()
	e.indexWG.Add(1)
	types.SafeGo(name, func() {
		defer e.indexWG.Done()
		fn(ctx)
	})
}

// indexCodebase / Ask / AskRaced / AskWithMetadata / StreamAsk /
// recordInteraction live in engine_ask.go.
// buildSystemPrompt / memoryDegradedSystemNotice / bundleToSystemBlocks
// live in engine_prompt.go.
// Memory* and Conversation* passthroughs live in engine_passthrough.go.
// ensureIndexed / Analyze / AnalyzeWithOptions / collectSourceFiles /
// detectDeadCode / computeComplexity and the text-stripper /
// complexity helpers / minInt / maxInt / estimateTokens live in
// engine_analyze.go.
