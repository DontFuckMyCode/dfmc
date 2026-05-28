package tools

// lifecycle.go — Engine open/close + session-state reset. The engine
// is one-shot: once Close has run, Register/Execute return
// ErrEngineClosed and any per-tool cached state (AST parse caches,
// failure ledger, snapshot LRU, per-path locks) is dropped.
//
// Close uses lifecycleMu to serialise the close transition itself,
// then takes a registry read snapshot under e.mu so a long-running
// closer on one tool doesn't block lookups for another. Tools that
// need a teardown hook implement toolCloser.

import (
	"errors"
	"sync"
)

// ErrEngineClosed is returned when callers try to execute a tool after the
// tools engine has begun shutdown.
var ErrEngineClosed = errors.New("tools engine is closed")

type toolCloser interface {
	Close() error
}

// Close releases per-tool cached state held for the life of the tools engine.
// Most tools are stateless, but AST-backed tools retain parse caches that can
// otherwise live until process exit in long-running TUI/web sessions.
func (e *Engine) Close() error {
	e.lifecycleMu.Lock()
	if e.closed {
		e.lifecycleMu.Unlock()
		return nil
	}
	e.closed = true
	e.lifecycleMu.Unlock()

	e.mu.RLock()
	// Registry is append-only at runtime; there is no Unregister path, so
	// taking a snapshot of closers under the read lock is sufficient.
	closers := make([]toolCloser, 0, len(e.registry))
	for _, tool := range e.registry {
		if closer, ok := tool.(toolCloser); ok {
			closers = append(closers, closer)
		}
	}
	e.mu.RUnlock()

	var errs []error
	for _, closer := range closers {
		if err := closer.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	// Stop MCP bridge subprocesses. The bridge isn't in the registry
	// (mcpToolAdapter wrappers around it are), so the toolCloser sweep
	// doesn't reach it. Optional via interface assertion so test bridges
	// that don't own subprocesses (FakeBridge in engine_methods_test.go)
	// don't need to implement Close.
	if e.mcpBridge != nil {
		if c, ok := e.mcpBridge.(interface{ Close() error }); ok {
			if err := c.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	e.clearSessionState()
	return errors.Join(errs...)
}

func (e *Engine) clearSessionState() {
	e.failureMu.Lock()
	e.recentFailures = map[string]int{}
	e.recentFailOrder = nil
	e.failureOrderIdx = map[string]int{}
	e.failureMu.Unlock()

	e.readMu.Lock()
	e.readSnapshots = map[string]string{}
	e.readSnapshotLRU = nil
	e.readMu.Unlock()

	e.pathLocks.Clear()
}

// LockPath returns a release function for the per-path lock covering abs.
// Empty abs is a no-op (returns a nop release). Used by edit_file, write_file,
// and apply_patch to serialise the read-gate → write window per target file.
func (e *Engine) LockPath(abs string) func() {
	if abs == "" {
		return func() {}
	}
	// Load or create the per-path mutex.
	lock, _ := e.pathLocks.LoadOrStore(abs, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	return func() { mu.Unlock() }
}
