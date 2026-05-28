// Pins the engine wrapper's tool:timeout event: when a tool's
// per-Execute deadline fires, the wrapper must publish a distinct
// telemetry event so operators can see the gate triggering without
// grepping tool:error messages for substring matches.

package engine

import (
	"context"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// sleeperTool is a tools.Tool that blocks until ctx is cancelled —
// guaranteed to trip the engine timeout when one is configured.
type sleeperTool struct{}

func (sleeperTool) Name() string        { return "engine_sleeper" }
func (sleeperTool) Description() string { return "blocks until ctx cancel" }
func (sleeperTool) Execute(ctx context.Context, _ tools.Request) (tools.Result, error) {
	<-ctx.Done()
	return tools.Result{}, ctx.Err()
}

func TestExecuteToolWithLifecycle_PublishesTimeoutEvent(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agent.ToolTimeouts = map[string]int{"engine_sleeper": 1}

	te := tools.NewFromConfig(cfg)
	te.Register(sleeperTool{})

	bus := NewEventBus()
	eng := &Engine{
		Config:   cfg,
		EventBus: bus,
		Tools:    te,
	}

	evCh := bus.Subscribe("tool:timeout")

	_, err := eng.executeToolWithLifecycle(context.Background(), "engine_sleeper", map[string]any{}, "agent")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	// One event must arrive promptly. Drain with a generous bound so a
	// slow CI runner doesn't flake; the timeout is configured at 1s, so
	// 5s is comfortable headroom.
	select {
	case ev := <-evCh:
		if ev.Type != "tool:timeout" {
			t.Fatalf("expected tool:timeout, got %s", ev.Type)
		}
		payload, ok := ev.Payload.(map[string]any)
		if !ok {
			t.Fatalf("payload should be map[string]any, got %T", ev.Payload)
		}
		if got, _ := payload["name"].(string); got != "engine_sleeper" {
			t.Errorf("payload name=%q, want engine_sleeper", got)
		}
		// limit_ms should be 1000 (1 second).
		if got, _ := payload["limit_ms"].(int64); got != 1000 {
			t.Errorf("payload limit_ms=%v, want 1000", payload["limit_ms"])
		}
		if got, _ := payload["source"].(string); got != "agent" {
			t.Errorf("payload source=%q, want agent", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tool:timeout event never arrived")
	}
}
