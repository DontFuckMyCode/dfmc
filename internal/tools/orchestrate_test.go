package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// recordingRunner captures every RunSubagent call so tests can assert task
// ordering, per-stage prompts, and peak parallelism.
type recordingRunner struct {
	mu         sync.Mutex
	calls      []SubagentRequest
	sleep      time.Duration
	inFlight   int32
	peak       int32
	summary    func(req SubagentRequest) string
	failAtCall int // -1 = never
	callCount  int32
}

func (r *recordingRunner) RunSubagent(ctx context.Context, req SubagentRequest) (SubagentResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req)
	r.mu.Unlock()

	idx := atomic.AddInt32(&r.callCount, 1)
	now := atomic.AddInt32(&r.inFlight, 1)
	defer atomic.AddInt32(&r.inFlight, -1)
	for {
		p := atomic.LoadInt32(&r.peak)
		if now <= p || atomic.CompareAndSwapInt32(&r.peak, p, now) {
			break
		}
	}

	if r.sleep > 0 {
		select {
		case <-time.After(r.sleep):
		case <-ctx.Done():
			return SubagentResult{}, ctx.Err()
		}
	}
	if r.failAtCall > 0 && int(idx) == r.failAtCall {
		return SubagentResult{DurationMs: 5}, fmt.Errorf("stage %d boom", idx)
	}
	summary := ""
	if r.summary != nil {
		summary = r.summary(req)
	} else {
		summary = "done: " + req.Task
	}
	return SubagentResult{Summary: summary, ToolCalls: 2, DurationMs: 5}, nil
}

// TestOrchestrateParallelFansOutIndependentSubtasks: a conjunction-split
// task must execute sub-agents concurrently — peak parallelism ≥ 2 and
// wall-clock under a serial run.
func TestOrchestrateParallelFansOutIndependentSubtasks(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.Agent.ParallelBatchSize = 4
	eng := New(cfg)
	runner := &recordingRunner{sleep: 30 * time.Millisecond, failAtCall: -1}
	eng.SetSubagentRunner(runner)

	task := "survey engine.go, and map the router, and document the manager"
	start := time.Now()
	res, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{"task": task},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if mode, _ := res.Data["mode"].(string); mode != "parallel" {
		t.Fatalf("expected mode=parallel, got %q (data=%+v)", mode, res.Data)
	}
	if got := atomic.LoadInt32(&runner.peak); got < 2 {
		t.Fatalf("expected parallelism ≥2, got peak=%d", got)
	}
	// 3 stages × 30ms serial = 90ms. Parallel should finish well under.
	if elapsed >= 75*time.Millisecond {
		t.Fatalf("elapsed=%s suggests sequential execution", elapsed)
	}
	stages, _ := res.Data["stages"].([]map[string]any)
	if len(stages) < 2 {
		t.Fatalf("expected ≥2 stages, got %d", len(stages))
	}
}

// TestOrchestrateSequentialChainThreadsPriorSummaries: stage-split task
// ("first X then Y") must run one at a time AND thread the previous stage's
// summary into the next stage's prompt.
func TestOrchestrateSequentialChainThreadsPriorSummaries(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	runner := &recordingRunner{
		sleep:      5 * time.Millisecond,
		failAtCall: -1,
		summary: func(req SubagentRequest) string {
			return fmt.Sprintf("stage-summary for len=%d", len(req.Task))
		},
	}
	eng.SetSubagentRunner(runner)

	task := "first map the imports, then grep for cycles, then write the fix"
	res, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{"task": task},
	})
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if mode, _ := res.Data["mode"].(string); mode != "sequential" {
		t.Fatalf("expected mode=sequential, got %q (data=%+v)", mode, res.Data)
	}
	if got := atomic.LoadInt32(&runner.peak); got != 1 {
		t.Fatalf("sequential run must peak at 1, got %d", got)
	}
	if len(runner.calls) < 3 {
		t.Fatalf("expected 3 stage calls, got %d", len(runner.calls))
	}
	// Stage 2 and 3 must carry the "Prior stage findings" marker — stage 1
	// must not (no prior findings to carry).
	if strings.Contains(runner.calls[0].Task, "Prior stage findings") {
		t.Fatalf("stage 1 should not carry prior findings: %q", runner.calls[0].Task)
	}
	for i := 1; i < len(runner.calls); i++ {
		if !strings.Contains(runner.calls[i].Task, "Prior stage findings") {
			t.Fatalf("stage %d prompt missing prior findings section:\n%s", i+1, runner.calls[i].Task)
		}
	}
}

// TestOrchestrateFallsBackForUnsplittableTask: a single-shot ask collapses
// to mode=single and fires exactly one sub-agent on the original task.
func TestOrchestrateFallsBackForUnsplittableTask(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	runner := &recordingRunner{failAtCall: -1}
	eng.SetSubagentRunner(runner)

	task := "fix the off-by-one in token counting"
	res, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{"task": task},
	})
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if mode, _ := res.Data["mode"].(string); mode != "single" {
		t.Fatalf("expected mode=single, got %q", mode)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 sub-agent call, got %d", len(runner.calls))
	}
	if runner.calls[0].Task != task {
		t.Fatalf("fallback should pass the original task verbatim, got %q", runner.calls[0].Task)
	}
}

// TestOrchestrateForceSequentialDisablesFanOut: even when the splitter
// marks the subtasks parallel, force_sequential=true must serialise them.
func TestOrchestrateForceSequentialDisablesFanOut(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	runner := &recordingRunner{sleep: 5 * time.Millisecond, failAtCall: -1}
	eng.SetSubagentRunner(runner)

	res, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"task":             "survey engine.go, and map the router, and document the manager",
			"force_sequential": true,
		},
	})
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if mode, _ := res.Data["mode"].(string); mode != "sequential" {
		t.Fatalf("expected mode=sequential under force_sequential, got %q", mode)
	}
	if got := atomic.LoadInt32(&runner.peak); got != 1 {
		t.Fatalf("force_sequential must peak at 1, got %d", got)
	}
}

// TestOrchestrateParallelStageFailureDoesNotAbortSiblings: a per-stage
// failure records the error but lets other parallel stages complete. The
// caller receives a non-nil error AND the partial stage data.
func TestOrchestrateParallelStageFailureDoesNotAbortSiblings(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.Agent.ParallelBatchSize = 4
	eng := New(cfg)
	runner := &recordingRunner{sleep: 10 * time.Millisecond, failAtCall: 2}
	eng.SetSubagentRunner(runner)

	res, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{"task": "survey a.go, and map b.go, and doc c.go"},
	})
	if err == nil {
		t.Fatalf("expected non-nil error when a stage fails")
	}
	stages, _ := res.Data["stages"].([]map[string]any)
	if len(stages) != 3 {
		t.Fatalf("expected 3 stages, got %d", len(stages))
	}
	failed := 0
	succeeded := 0
	for _, stage := range stages {
		if _, has := stage["error"]; has {
			failed++
		} else {
			succeeded++
		}
	}
	if failed != 1 || succeeded != 2 {
		t.Fatalf("expected 1 fail + 2 ok, got %d/%d", failed, succeeded)
	}
}

// TestOrchestrateRequiresRunner: calling orchestrate without a runner
// returns a clear error rather than silently spinning.
func TestOrchestrateRequiresRunner(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	// intentionally no SetSubagentRunner call.

	_, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{"task": "whatever"},
	})
	if err == nil || !strings.Contains(err.Error(), "runner") {
		t.Fatalf("expected runner error, got %v", err)
	}
}

// TestOrchestrateRespectsMaxParallelCeiling: the engine config's
// ParallelBatchSize must clamp the per-call max_parallel — otherwise a
// single model call could spawn 50 sub-agents and melt the machine.
func TestOrchestrateRespectsMaxParallelCeiling(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.Agent.ParallelBatchSize = 2
	eng := New(cfg)
	runner := &recordingRunner{sleep: 20 * time.Millisecond, failAtCall: -1}
	eng.SetSubagentRunner(runner)

	res, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"task":         "do: 1) a 2) b 3) c 4) d 5) e",
			"max_parallel": 99,
		},
	})
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if mode, _ := res.Data["mode"].(string); mode != "parallel" {
		t.Fatalf("expected parallel mode, got %q", mode)
	}
	if got := atomic.LoadInt32(&runner.peak); got > 2 {
		t.Fatalf("parallelism exceeded engine ceiling: peak=%d, ceiling=2", got)
	}
}
