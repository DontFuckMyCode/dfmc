// Pin the contract that cmd/dfmc/main.go relies on: Engine.Shutdown
// must be safe to call after engine.New succeeds even if Init never
// ran or only partially initialized subsystems. Without this, the
// `defer eng.Shutdown()` we added to main.go would panic on any init
// failure and the process would crash before the user saw the
// formatted error — much worse than the leaked store lock the defer
// was added to fix.

package engine

import (
	"testing"

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
