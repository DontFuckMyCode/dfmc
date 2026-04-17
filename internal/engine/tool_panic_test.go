// Tests that a panic inside any tool's Execute is converted into a
// regular error instead of crashing the whole engine. The agent loop
// already knows how to surface tool errors back to the model with
// isError=true, so a recovered panic just looks like one more error
// from the loop's perspective.

package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// panicTool always panics from Execute. Used to drive the recovery
// path in executeToolWithPanicGuard.
type panicTool struct{ value any }

func (panicTool) Name() string        { return "panic_tool" }
func (panicTool) Description() string { return "panics for tests" }
func (p panicTool) Execute(_ context.Context, _ tools.Request) (tools.Result, error) {
	panic(p.value)
}

func TestExecuteToolWithPanicGuard_RecoversStringPanic(t *testing.T) {
	cfg := config.DefaultConfig()
	te := tools.New(*cfg)
	te.Register(panicTool{value: "kaboom"})
	eng := &Engine{
		Config:   cfg,
		EventBus: NewEventBus(),
		Tools:    te,
	}

	res, err := eng.executeToolWithPanicGuard(context.Background(), "panic_tool", nil)
	if err == nil {
		t.Fatal("expected panic to surface as error, got nil")
	}
	// The error must name the tool + the panic value so the agent has
	// something specific to retry against (not a generic "tool failed").
	if !strings.Contains(err.Error(), "panic_tool panicked") {
		t.Fatalf("error should name the tool: %v", err)
	}
	if !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("error should carry the panic value: %v", err)
	}
	// Result must be empty — partial state from a panicking tool can't
	// be trusted. The agent loop reads res.Output as the tool_result
	// payload and we don't want garbage in there.
	if res.Output != "" || res.Data != nil {
		t.Fatalf("recovered tool must return empty Result, got %+v", res)
	}
}

// Non-string panics (errors, structs) must also be recovered. The
// error message just carries the %v rendering — we don't try to
// preserve type identity through the recover boundary.
func TestExecuteToolWithPanicGuard_RecoversTypedPanic(t *testing.T) {
	cfg := config.DefaultConfig()
	te := tools.New(*cfg)
	te.Register(panicTool{value: struct{ Kind string }{Kind: "weird"}})
	eng := &Engine{
		Config:   cfg,
		EventBus: NewEventBus(),
		Tools:    te,
	}

	_, err := eng.executeToolWithPanicGuard(context.Background(), "panic_tool", nil)
	if err == nil || !strings.Contains(err.Error(), "weird") {
		t.Fatalf("typed panic should be recovered with value in message: %v", err)
	}
}

// A successful tool call must NOT alter return semantics — the guard
// is invisible on the happy path.
func TestExecuteToolWithPanicGuard_HappyPathPassesThrough(t *testing.T) {
	cfg := config.DefaultConfig()
	te := tools.New(*cfg)
	// list_dir is a simple read-only tool that always exists.
	eng := &Engine{
		Config:   cfg,
		EventBus: NewEventBus(),
		Tools:    te,
	}

	res, err := eng.executeToolWithPanicGuard(context.Background(), "list_dir", map[string]any{
		"path": ".",
	})
	if err != nil {
		t.Fatalf("happy-path call should not error: %v", err)
	}
	// list_dir on the test working directory may return an empty list
	// (depending on temp setup) but must not leak the panic-recovered
	// shape. Anything non-zero from Output or Data is real content,
	// which is fine.
	_ = res
}

// The guard must publish a tool:panicked event so observers (TUI
// notice strip, web SSE clients, log shippers) can surface the
// failure even if the agent loop swallows the error in a retry
// strategy.
func TestExecuteToolWithPanicGuard_PublishesToolPanickedEvent(t *testing.T) {
	cfg := config.DefaultConfig()
	te := tools.New(*cfg)
	te.Register(panicTool{value: "boom"})
	bus := NewEventBus()
	sub := bus.Subscribe("tool:panicked")
	defer bus.Unsubscribe("tool:panicked", sub)
	eng := &Engine{Config: cfg, EventBus: bus, Tools: te}

	_, err := eng.executeToolWithPanicGuard(context.Background(), "panic_tool", nil)
	if err == nil {
		t.Fatal("expected error from panicking tool")
	}
	select {
	case ev := <-sub:
		if ev.Type != "tool:panicked" {
			t.Fatalf("unexpected event type: %s", ev.Type)
		}
		payload, ok := ev.Payload.(map[string]any)
		if !ok || payload["name"] != "panic_tool" {
			t.Fatalf("event payload missing tool name: %#v", ev.Payload)
		}
	default:
		t.Fatal("tool:panicked event was not published")
	}
}
