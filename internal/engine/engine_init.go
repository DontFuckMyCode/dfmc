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

import "context"

func (e *Engine) Init(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	e.setState(StateInitializing)

	// Structured application logger — best-effort; init failure is not fatal.
	e.initAppLogAndPanicObserver()
	e.EventBus.Publish(Event{Type: "engine:initializing", Source: "engine"})
	if err := e.initStorageBackedServices(); err != nil {
		return err
	}
	if err := e.initToolingStack(); err != nil {
		return err
	}
	e.initMemoryConversationAndDomainServices()
	if err := e.initProviderRouter(); err != nil {
		return err
	}
	e.initBackgroundContext(ctx)
	e.initIntentAndHooks()
	e.initProjectRuntime(ctx)

	if e.AppLog != nil {
		e.AppLog.Info("engine init completed")
	}
	e.setState(StateReady)
	e.EventBus.Publish(Event{Type: "engine:ready", Source: "engine"})
	return nil
}
