// Tests for the engine-side wiring of tool self-narration. The actual
// strip + publisher mechanics live in internal/tools and are pinned by
// reason_test.go there. Here we cover only the engine's responsibilities:
//   - the publisher is installed when agent.tool_reasoning is enabled
//   - it's NOT installed (or fires nothing) when the knob is "off"
//   - published reasons land on the EventBus as tool:reasoning events
//   - the system-prompt notice is included only when enabled

package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func newReasoningTestEngine(t *testing.T, knob string) *Engine {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cfg := config.DefaultConfig()
	cfg.Agent.ToolReasoning = knob
	eng, err := New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { eng.Shutdown() })
	return eng
}

// TestToolReasoningEnabledByDefault: zero config / "auto" / "on" all
// enable; "off" / "false" / "no" / "0" disable. Pins the parser so a
// future refactor can't silently flip the default.
func TestToolReasoningEnabledByDefault(t *testing.T) {
	cases := map[string]bool{
		"":         true,
		"auto":     true,
		"on":       true,
		"true":     true,
		"AUTO":     true,
		"  on  ":   true,
		"off":      false,
		"OFF":      false,
		"false":    false,
		"no":       false,
		"0":        false,
		"disabled": false,
	}
	for knob, want := range cases {
		cfg := &config.Config{Agent: config.AgentConfig{ToolReasoning: knob}}
		e := &Engine{Config: cfg}
		if got := e.toolReasoningEnabled(); got != want {
			t.Errorf("knob %q: got %v, want %v", knob, got, want)
		}
	}
	// Nil engine / nil config must not panic and must return true (the
	// safe default — surface narration unless explicitly silenced).
	var nilEng *Engine
	if !nilEng.toolReasoningEnabled() {
		t.Error("nil engine must default to enabled")
	}
}

// TestToolReasoningPublishesEvent: when enabled, calling a tool with a
// `_reason` arg via CallTool publishes a tool:reasoning event on the
// EventBus.
func TestToolReasoningPublishesEvent(t *testing.T) {
	eng := newReasoningTestEngine(t, "auto")
	ch := eng.EventBus.Subscribe("tool:reasoning")
	defer eng.EventBus.Unsubscribe("tool:reasoning", ch)

	// list_dir is the cheapest builtin that won't fail in an empty
	// project — it lists the project root.
	_, err := eng.CallTool(context.Background(), "list_dir", map[string]any{
		"path":            ".",
		tools.ReasonField: "checking the layout before reading any files",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	select {
	case ev := <-ch:
		payload, ok := ev.Payload.(map[string]any)
		if !ok {
			t.Fatalf("payload is not map[string]any: %T", ev.Payload)
		}
		if payload["tool"] != "list_dir" {
			t.Errorf("tool field = %v, want list_dir", payload["tool"])
		}
		reason, _ := payload["reason"].(string)
		if !strings.Contains(reason, "checking the layout") {
			t.Errorf("reason text not preserved: %q", reason)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive tool:reasoning event within 1s")
	}
}

// TestToolReasoningOffSuppressesEvent: with the knob off, the publisher
// is never installed, so passing _reason still strips the field but no
// event lands on the bus.
func TestToolReasoningOffSuppressesEvent(t *testing.T) {
	eng := newReasoningTestEngine(t, "off")
	ch := eng.EventBus.Subscribe("tool:reasoning")
	defer eng.EventBus.Unsubscribe("tool:reasoning", ch)

	_, err := eng.CallTool(context.Background(), "list_dir", map[string]any{
		"path":            ".",
		tools.ReasonField: "should be silenced",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	// Give a brief window for any (incorrect) publish to land, then
	// assert nothing arrived.
	select {
	case ev := <-ch:
		t.Fatalf("expected no event with knob=off, got %+v", ev)
	case <-time.After(150 * time.Millisecond):
		// expected: no event
	}
}

// TestToolReasoningSystemNoticeGated: the cacheable system block that
// nudges the model to fill _reason is included when enabled, omitted
// when off. Important because it's a token cost and a behaviour change.
func TestToolReasoningSystemNoticeGated(t *testing.T) {
	on := newReasoningTestEngine(t, "auto")
	_, blocksOn := on.buildSystemPrompt("hello", nil)
	if !hasSystemBlockLabel(blocksOn, "tool-reasoning") {
		t.Error("with knob=auto the tool-reasoning system block must be present")
	}

	off := newReasoningTestEngine(t, "off")
	_, blocksOff := off.buildSystemPrompt("hello", nil)
	if hasSystemBlockLabel(blocksOff, "tool-reasoning") {
		t.Error("with knob=off the tool-reasoning system block must be omitted")
	}
}

func hasSystemBlockLabel(blocks []provider.SystemBlock, label string) bool {
	for _, b := range blocks {
		if b.Label == label {
			return true
		}
	}
	return false
}
