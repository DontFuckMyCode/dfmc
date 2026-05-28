package tools

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// TestExecuteHonorsToolTimeout pins the engine-level per-tool deadline.
// A slow non-self-managed tool must abort with the self-teaching timeout
// error rather than blocking the agent loop indefinitely.
func TestExecuteHonorsToolTimeout(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agent.ToolTimeouts = map[string]int{"slow_tool": 1} // 1 second

	eng := NewFromConfig(cfg)
	var inFlight, peak, order int32
	tool := &sleepTool{nameStr: "slow_tool", sleep: 5 * time.Second, inFlight: &inFlight, peak: &peak, order: &order}
	eng.Register(tool)

	start := time.Now()
	_, err := eng.Execute(context.Background(), "slow_tool", Request{ProjectRoot: t.TempDir()})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded") || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected self-teaching timeout error, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "agent.tool_timeouts.slow_tool") {
		t.Fatalf("expected error to teach the override path, got %q", err.Error())
	}
	// Sanity: the error must arrive promptly, not after the full 5s sleep.
	if elapsed > 3*time.Second {
		t.Fatalf("timeout fired too late: %v", elapsed)
	}
}

// TestSelfManagedToolsBypassEngineTimeout asserts that tools listed in
// selfManagedTimeoutTools are NOT wrapped by the engine timeout, because
// they own their own deadline (run_command's per-call timeout, web HTTP
// client's 20s, recursive sub-agent loops).
func TestSelfManagedToolsBypassEngineTimeout(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agent.ToolDefaultTimeoutSeconds = 1 // very tight default

	eng := NewFromConfig(cfg)
	if got := eng.toolTimeout("run_command"); got != 0 {
		t.Errorf("run_command must bypass engine timeout, got %v", got)
	}
	if got := eng.toolTimeout("web_fetch"); got != 0 {
		t.Errorf("web_fetch must bypass engine timeout, got %v", got)
	}
	if got := eng.toolTimeout("delegate_task"); got != 0 {
		t.Errorf("delegate_task must bypass engine timeout, got %v", got)
	}
	if got := eng.toolTimeout("orchestrate"); got != 0 {
		t.Errorf("orchestrate must bypass engine timeout, got %v", got)
	}
}

// TestZeroOverrideOptsOut asserts a per-tool 0 override disables the
// timeout for that tool, even when the default is set.
func TestZeroOverrideOptsOut(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agent.ToolDefaultTimeoutSeconds = 30
	cfg.Agent.ToolTimeouts = map[string]int{"my_tool": 0}

	eng := NewFromConfig(cfg)
	if got := eng.toolTimeout("my_tool"); got != 0 {
		t.Errorf("explicit 0 override must opt out, got %v", got)
	}
	// Other tools still inherit the default.
	if got := eng.toolTimeout("read_file"); got != 30*time.Second {
		t.Errorf("read_file default = %v, want 30s", got)
	}
}

// TestDefaultTimeoutDisabledWhenZero asserts that a 0 default disables
// the engine-level timeout entirely (the agent loop's outer caps still
// apply).
func TestDefaultTimeoutDisabledWhenZero(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agent.ToolDefaultTimeoutSeconds = 0

	eng := NewFromConfig(cfg)
	if got := eng.toolTimeout("read_file"); got != 0 {
		t.Errorf("0 default must disable engine timeout, got %v", got)
	}
}

// quietSleepTool is used to assert that a fast tool completes successfully
// even when a generous timeout is configured.
type quietSleepTool struct {
	name  string
	sleep time.Duration
}

func (q *quietSleepTool) Name() string        { return q.name }
func (q *quietSleepTool) Description() string { return "quiet sleeper" }
func (q *quietSleepTool) Execute(ctx context.Context, _ Request) (Result, error) {
	select {
	case <-time.After(q.sleep):
		return Result{Output: "ok"}, nil
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// TestExecuteSucceedsWithinTimeout makes sure the wrapping doesn't
// regress the happy path — fast tools must return cleanly.
func TestExecuteSucceedsWithinTimeout(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agent.ToolTimeouts = map[string]int{"quick_tool": 5}

	eng := NewFromConfig(cfg)
	eng.Register(&quietSleepTool{name: "quick_tool", sleep: 50 * time.Millisecond})

	res, err := eng.Execute(context.Background(), "quick_tool", Request{ProjectRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if res.Output != "ok" {
		t.Errorf("expected output 'ok', got %q", res.Output)
	}
}

// TestToolTimeoutErrorIsTyped pins the typed sentinel: when a tool
// exceeds its deadline, the returned error must be a *ToolTimeoutError
// so the engine wrapper can publish a distinct tool:timeout event via
// errors.As. Also asserts the error still unwraps to
// context.DeadlineExceeded so generic transient classifiers (subagent
// retry layer) keep recognising it.
func TestToolTimeoutErrorIsTyped(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agent.ToolTimeouts = map[string]int{"slow_typed": 1}

	eng := NewFromConfig(cfg)
	var inFlight, peak, order int32
	eng.Register(&sleepTool{nameStr: "slow_typed", sleep: 5 * time.Second, inFlight: &inFlight, peak: &peak, order: &order})

	_, err := eng.Execute(context.Background(), "slow_typed", Request{ProjectRoot: t.TempDir()})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	var tte *ToolTimeoutError
	if !errors.As(err, &tte) {
		t.Fatalf("expected *ToolTimeoutError, got %T: %v", err, err)
	}
	if tte.Name != "slow_typed" {
		t.Errorf("Name=%q, want slow_typed", tte.Name)
	}
	if tte.Limit != time.Second {
		t.Errorf("Limit=%v, want 1s", tte.Limit)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Error("ToolTimeoutError must unwrap to context.DeadlineExceeded so generic classifiers still match")
	}
}

// Compile-time sanity: keep references so unused-import linter is happy
// across go1.21+.
var _ = atomic.AddInt32
