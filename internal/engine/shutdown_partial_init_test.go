// Pin the contract that cmd/dfmc/main.go relies on: Engine.Shutdown
// must be safe to call after engine.New succeeds even if Init never
// ran or only partially initialized subsystems. Without this, the
// `defer eng.Shutdown()` we added to main.go would panic on any init
// failure and the process would crash before the user saw the
// formatted error — much worse than the leaked store lock the defer
// was added to fix.

package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestShutdown_AfterNewWithoutInit_DoesNotPanic(t *testing.T) {
	cfg := config.DefaultConfig()
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	// No Init call — simulate config.Load OK + engine.New OK +
	// eng.Init failed before any subsystem was wired up.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Shutdown panicked on a never-initialized engine: %v", r)
		}
	}()
	eng.Shutdown()
}

// Calling Shutdown twice must also be a no-op rather than a panic
// because main.go's `defer eng.Shutdown()` will run alongside any
// explicit Shutdown the CLI command might perform (some long-running
// commands tear down explicitly and then return).
func TestShutdown_IsIdempotent(t *testing.T) {
	cfg := config.DefaultConfig()
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("second Shutdown panicked: %v", r)
		}
	}()
	eng.Shutdown()
	eng.Shutdown()
}

// Pin: Shutdown-stage failures must publish engine:shutdown_error
// events so live observers (TUI status, web /ws stream) can surface the
// data-loss instead of letting it die in stderr only. Drives the C2
// review fix — used to be `_ = err`, now wraps each persist call.
func TestShutdown_ErrorPublishesEvent(t *testing.T) {
	cfg := config.DefaultConfig()
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	defer eng.Shutdown()

	sub := eng.EventBus.Subscribe("engine:shutdown_error")
	defer eng.EventBus.Unsubscribe("engine:shutdown_error", sub)

	// Drive the helper directly — fault-injecting the real persist
	// pipeline would couple this test to internal/conversation +
	// internal/memory implementation details. The contract we care
	// about is "every reported error reaches the bus".
	eng.publishShutdownError("save_conversation", errors.New("disk full"))

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	select {
	case ev := <-sub:
		payload, _ := ev.Payload.(map[string]any)
		if got, _ := payload["stage"].(string); got != "save_conversation" {
			t.Fatalf("expected stage=save_conversation; got %q", got)
		}
		if got, _ := payload["error"].(string); got != "disk full" {
			t.Fatalf("expected error=disk full; got %q", got)
		}
	case <-ctx.Done():
		t.Fatalf("expected engine:shutdown_error event within 1s")
	}
}
