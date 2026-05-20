// engine_lifecycle.go — engine startup/shutdown teardown methods.
// Sibling of engine.go which keeps the Engine struct, the New
// constructor, the State enum, the small setState/requireReady
// helpers, and the BackgroundContext + StartBackgroundTask runtime
// helpers.
//
// Splitting the teardown side out keeps engine.go scoped to "what
// is an engine and how do we observe its state" while this file
// owns "how does it stop cleanly" — StartServing's state flip,
// Shutdown's stage-ordered drain (cancel background work → run
// session_end hook → persist conversation → persist memory → close
// tools → close storage → publish stopped event), and the
// publishShutdownError helper that surfaces stage-failures on both
// the EventBus (so live observers see it) and stderr (so operators
// see it after the process exits).

package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/hooks"
)

func (e *Engine) StartServing() {
	e.setState(StateServing)
	e.EventBus.Publish(Event{Type: "engine:serving", Source: "engine"})
}

func (e *Engine) Shutdown() error {
	// Idempotency guard: main.go registers two cleanup defers that
	// both call Shutdown (the first for panic-out-of-cli.Run safety,
	// the second to also cancel the signal context + stop the
	// Telegram bot). LIFO ordering means the second defer runs first
	// and closes Storage; the first defer then re-entered Shutdown
	// and Memory.Persist hit SQLite's "database not open" error
	// (the nil-check there passes -- SQLite keeps the handle non-nil
	// after Close). Short-circuiting on already-terminal state keeps
	// both defers safe without changing main.go's panic-safety shape.
	switch e.State() {
	case StateShuttingDown, StateStopped:
		return nil
	}
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

	// Persist and close in stage order. Each failure is collected into
	// errs, published on the EventBus, and printed to stderr so the
	// user sees the data-loss before the process exits. The returned
	// error (errors.Join of all failures, nil if none) lets callers
	// decide whether to log further or abort with a non-zero exit.
	var errs []error
	if e.Conversation != nil {
		// Drain async saves first so a goroutine scheduled by the
		// agent loop microseconds ago can't sneak past Storage.Close
		// below and try to write to a closed SQLite handle.
		e.Conversation.Close()
		if err := e.Conversation.SaveActive(); err != nil {
			errs = append(errs, fmt.Errorf("save_conversation: %w", err))
			if e.AppLog != nil {
				e.AppLog.Error("shutdown save_conversation failed", err)
			}
			e.publishShutdownError("save_conversation", err)
		}
	}
	if e.Memory != nil {
		if err := e.Memory.Persist(); err != nil {
			errs = append(errs, fmt.Errorf("persist_memory: %w", err))
			if e.AppLog != nil {
				e.AppLog.Error("shutdown persist_memory failed", err)
			}
			e.publishShutdownError("persist_memory", err)
		}
	}
	if e.Tools != nil {
		if err := e.Tools.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close_tools: %w", err))
			if e.AppLog != nil {
				e.AppLog.Error("shutdown close_tools failed", err)
			}
			e.publishShutdownError("close_tools", err)
		}
	}
	if e.Storage != nil {
		if err := e.Storage.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close_storage: %w", err))
			if e.AppLog != nil {
				e.AppLog.Error("shutdown close_storage failed", err)
			}
			e.publishShutdownError("close_storage", err)
		}
	}

	e.setState(StateStopped)
	e.EventBus.Publish(Event{Type: "engine:stopped", Source: "engine"})
	return errors.Join(errs...)
}

// publishShutdownError surfaces a Shutdown-stage failure on both the
// EventBus (so live observers like the TUI and web /ws stream see it)
// and stderr (so the operator sees it after the process exits, even if
// the bus is no longer being read by then).
func (e *Engine) publishShutdownError(stage string, err error) {
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
