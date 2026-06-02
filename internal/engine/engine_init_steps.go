package engine

import (
	"context"
	"fmt"

	"github.com/dontfuckmycode/dfmc/internal/applog"
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
	"github.com/dontfuckmycode/dfmc/internal/toolhistory"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (e *Engine) initAppLogAndPanicObserver() {
	if appLog, err := applog.New(applog.Config{DataDir: e.Config.DataDir()}); err == nil {
		e.AppLog = appLog.WithComponent("engine").WithOperation("init")
	} else if e.AppLog != nil {
		e.AppLog.Error("applog init failed", err)
	}
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
}

func (e *Engine) initStorageBackedServices() error {
	store, err := storage.Open(e.Config.DataDir())
	if err != nil {
		if e.AppLog != nil {
			e.AppLog.Error("storage init failed", err)
		}
		return fmt.Errorf("storage init failed: %w", err)
	}
	e.Storage = store
	if pl, perr := providerlog.New(e.Config.DataDir()); perr == nil && pl != nil {
		e.ProviderLog = pl
		e.EventBus.SubscribeFunc("provider:complete", func(ev Event) {
			pl.Record(ev.Payload)
		})
	}
	if lp, lperr := toolhistory.InitLearnedPatterns(e.Config.DataDir()); lperr == nil && lp != nil {
		e.LearnedPatterns = lp
		if projectPatterns := e.Config.ProjectLearnedPatternsDir(); projectPatterns != "" {
			if pp, pperr := toolhistory.InitLearnedPatterns(projectPatterns); pperr == nil && pp != nil {
				pp.MergeFrom(lp)
				e.LearnedPatterns = pp
			}
		}
	}
	return nil
}

func (e *Engine) initToolingStack() error {
	e.AST = ast.NewWithCacheSize(e.Config.AST.CacheSize)
	e.CodeMap = codemap.New(e.AST, &e.Config.Codemap)
	e.Context = ctxmgr.New(e.CodeMap)
	e.Tools = tools.New(tools.ToToolsConfigSubset(e.Config))
	tools.ConfigureRetryWindow(e.Config.Agent.RetryWindowSize)
	e.Tools.SetTaskStore(taskstore.NewStore(e.Storage.DB()))
	e.Tools.SetSubagentRunner(e)
	e.Tools.SetCodemap(e.CodeMap)
	if err := loadMCPClients(e.Config, e.Tools); err != nil {
		if e.AppLog != nil {
			e.AppLog.Error("mcp clients init failed", err)
		}
		return fmt.Errorf("mcp clients: %w", err)
	}
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
	return nil
}

func (e *Engine) initMemoryConversationAndDomainServices() {
	e.Memory = memory.New(e.Storage)
	if err := e.Memory.Load(); err != nil {
		e.mu.Lock()
		e.memoryDegraded = true
		e.memoryLoadErr = err.Error()
		e.mu.Unlock()
		if e.AppLog != nil {
			e.AppLog.Warn("memory load degraded", map[string]any{"reason": err.Error()})
		}
		e.EventBus.Publish(Event{
			Type:   "memory:degraded",
			Source: "engine",
			Payload: map[string]any{
				"reason": err.Error(),
			},
		})
	}
	e.Conversation = conversation.New(e.Storage)
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
}

func (e *Engine) initProviderRouter() error {
	router, err := provider.NewRouter(e.Config.Providers)
	if err != nil {
		if e.AppLog != nil {
			e.AppLog.Error("provider router init failed", err)
		}
		return fmt.Errorf("provider router init failed: %w", err)
	}
	e.Providers = router
	e.attachProviderObservers(e.Providers)
	return nil
}

func (e *Engine) initBackgroundContext(ctx context.Context) {
	backgroundCtx, backgroundCancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.backgroundCtx = backgroundCtx
	e.backgroundCancel = backgroundCancel
	e.mu.Unlock()
}

func (e *Engine) initIntentAndHooks() {
	e.Intent = intent.NewRouter(e.Config.Intent, func(name string) (provider.Provider, bool) {
		if e.Providers == nil {
			return nil, false
		}
		return e.Providers.Get(name)
	})
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
}

func (e *Engine) initProjectRuntime(ctx context.Context) {
	e.ProjectRoot = config.FindProjectRoot("")
	e.Config.SetProjectRoot(e.ProjectRoot)
	e.refreshProjectConfigSnapshot(e.projectConfigPath())
	if e.ProjectRoot != "" {
		indexCtx, cancel := context.WithCancel(ctx)
		e.mu.Lock()
		e.indexCancel = cancel
		e.mu.Unlock()
		// Capture the root for the background indexer so it never reads the
		// mutable e.ProjectRoot field (which callers/tests may overwrite
		// after Init returns).
		root := e.ProjectRoot
		e.indexWG.Go(func() {
			e.indexCodebase(indexCtx, root)
		})
	}
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
	e.Hooks.Fire(ctx, hooks.EventSessionStart, hooks.Payload{
		"project_root": e.ProjectRoot,
	})
	e.StartUpdateChecker(e.backgroundCtx, e.Version)
}
