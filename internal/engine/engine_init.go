package engine

// engine_init.go — Engine.Init: wires up storage, AST, codemap,
// context manager, tools registry (with task store + subagent runner +
// codemap + MCP external clients + reasoning publisher), memory store,
// conversation manager, security scanner, langintel registry,
// provider router, intent router, hook dispatcher, project root
// resolution, the initial codebase indexer goroutine, and the
// session_start hook. Pulled out of engine.go so the lifecycle file
// stays under "scan in one read" length. Companion siblings:
//
//   - engine.go              Engine struct + EngineState constants +
//                            New + StartServing + Shutdown +
//                            publishShutdownError + State + setState +
//                            requireReady + BackgroundContext +
//                            StartBackgroundTask
//   - engine_tools.go        ListTools / CallTool / tool exec
//                            lifecycle (approval + pre/post hooks +
//                            panic guard)
//   - engine_context.go etc. domain methods (see engine.go header)

import (
	"context"
	"fmt"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/hooks"
	"github.com/dontfuckmycode/dfmc/internal/intent"
	"github.com/dontfuckmycode/dfmc/internal/langintel"
	"github.com/dontfuckmycode/dfmc/internal/memory"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/providerlog"
	"github.com/dontfuckmycode/dfmc/internal/security"
	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

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
	// Provider call archive: every provider:complete event lands as a
	// JSONL row under <data-dir>/provider_calls/{YYYY-MM-DD}.jsonl. The
	// engine owns the subscription so providerlog stays free of any
	// engine import (would cycle). nil-safe: a missing/unwritable dir
	// returns a nil logger and the subscription is skipped.
	if pl, perr := providerlog.New(e.Config.DataDir()); perr == nil && pl != nil {
		e.ProviderLog = pl
		e.EventBus.SubscribeFunc("provider:complete", func(ev Event) {
			pl.Record(ev.Payload)
		})
	}
	e.AST = ast.NewWithCacheSize(e.Config.AST.CacheSize)
	e.CodeMap = codemap.New(e.AST)
	e.Context = ctxmgr.New(e.CodeMap)
	e.Tools = tools.New(*e.Config)
	// Size the subagent-retry ring buffer from config before any retry
	// activity could fire. Idempotent at the same size, so a hot-reload
	// that doesn't change the value is a no-op.
	tools.ConfigureRetryWindow(e.Config.Agent.RetryWindowSize)
	// Task store is bbolt-backed independent task persistence. Wired after
	// tools.New so e.Tools is non-nil. The TodoWriteTool falls back to
	// in-memory when the store is nil (e.g. tests that construct
	// tools.Engine directly without going through engine.Init).
	e.Tools.SetTaskStore(taskstore.NewStore(e.Storage.DB()))
	e.Tools.SetSubagentRunner(e)
	e.Tools.SetCodemap(e.CodeMap)
	// Load MCP external servers after native tools are registered so the
	// bridge adapter can add MCP tools to the same registry without
	// replacing any native tools with the same name.
	if err := loadMCPClients(e.Config, e.Tools); err != nil {
		return fmt.Errorf("mcp clients: %w", err)
	}
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
	// Surface async-save failures on the bus so the TUI / web can
	// render "your last turn didn't persist" instead of having the
	// error vanish into log.Printf — the user otherwise has no
	// signal when crash-before-shutdown actually loses work.
	e.Conversation.SetErrorReporter(func(stage string, err error) {
		if e.EventBus == nil || err == nil {
			return
		}
		e.EventBus.Publish(Event{
			Type:   "conversation:save:error",
			Source: "engine",
			Payload: map[string]any{
				"stage": stage,
				"error": err.Error(),
			},
		})
	})
	e.Security = security.New()
	e.LangIntel = langintel.NewRegistry()

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

	// VULN-036: warn if config files are group/world-writable — a hostile
	// co-tenant on a shared host could inject hook commands via a
	// world-readable config. Fire after ProjectRoot is resolved but before
	// session_start hooks so the warning precedes any hook execution.
	for _, path := range []string{e.globalConfigPath(), e.projectConfigPath()} {
		if path == "" {
			continue
		}
		if msg := hooks.CheckConfigPermissions(path); msg != "" {
			e.EventBus.Publish(Event{
				Type:   "security:config_permissions",
				Source: "engine",
				Payload: map[string]any{
					"path":   path,
					"status": "warn",
					"msg":    msg,
				},
			})
		}
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
