package tools

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// dagStagesParam builds the []any shape the real tool call would carry so
// tests exercise the exact parsing/validation path the LLM would hit.
func dagStagesParam(entries ...map[string]any) []any {
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, e)
	}
	return out
}

func depSlice(ids ...string) []any {
	out := make([]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, id)
	}
	return out
}

// TestOrchestrateDAGDiamondRunsJoinAfterBranches: A → (B, C) → D. B and C
// must run concurrently, D must wait for both, and D's prompt must carry
// both B's and C's summaries (its direct deps).
func TestOrchestrateDAGDiamondRunsJoinAfterBranches(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.Agent.ParallelBatchSize = 4
	eng := New(cfg)
	runner := &recordingRunner{
		sleep:      15 * time.Millisecond,
		failAtCall: -1,
		summary: func(req SubagentRequest) string {
			// echo back the task so we can assert what each stage was handed.
			return "summary-of:" + req.Task
		},
	}
	eng.SetSubagentRunner(runner)

	res, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"stages": dagStagesParam(
				map[string]any{"id": "A", "task": "map imports"},
				map[string]any{"id": "B", "task": "audit perf", "depends_on": depSlice("A")},
				map[string]any{"id": "C", "task": "audit sec", "depends_on": depSlice("A")},
				map[string]any{"id": "D", "task": "write report", "depends_on": depSlice("B", "C")},
			),
		},
	})
	if err != nil {
		t.Fatalf("orchestrate dag: %v", err)
	}
	if mode, _ := res.Data["mode"].(string); mode != "dag" {
		t.Fatalf("expected mode=dag, got %q", mode)
	}
	// Peak parallelism must be >=2 (B and C in middle layer run together).
	if got := atomic.LoadInt32(&runner.peak); got < 2 {
		t.Fatalf("expected peak>=2 for middle layer, got %d", got)
	}
	layers, ok := res.Data["layers"].([][]string)
	if !ok || len(layers) != 3 {
		t.Fatalf("expected 3 layers, got %#v", res.Data["layers"])
	}
	if len(layers[0]) != 1 || layers[0][0] != "A" {
		t.Fatalf("layer0 must be [A], got %v", layers[0])
	}
	if len(layers[1]) != 2 {
		t.Fatalf("layer1 must have 2 stages (B,C), got %v", layers[1])
	}
	if len(layers[2]) != 1 || layers[2][0] != "D" {
		t.Fatalf("layer2 must be [D], got %v", layers[2])
	}
	// D's prompt must include both B's and C's summaries — locate D's call.
	var dTask string
	for _, call := range runner.calls {
		if strings.HasPrefix(call.Task, "write report") {
			dTask = call.Task
			break
		}
	}
	if dTask == "" {
		t.Fatalf("never saw D's sub-agent call; calls=%+v", runner.calls)
	}
	for _, needle := range []string{"[B]", "[C]", "Prior stage findings"} {
		if !strings.Contains(dTask, needle) {
			t.Fatalf("D's prompt missing %q:\n%s", needle, dTask)
		}
	}
}

// TestOrchestrateDAGCycleRejected: A→B→A must be refused with a clear
// error identifying the stuck ids.
func TestOrchestrateDAGCycleRejected(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	eng.SetSubagentRunner(&recordingRunner{failAtCall: -1})

	_, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"stages": dagStagesParam(
				map[string]any{"id": "A", "task": "a", "depends_on": depSlice("B")},
				map[string]any{"id": "B", "task": "b", "depends_on": depSlice("A")},
			),
		},
	})
	if err == nil {
		t.Fatalf("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error must mention cycle, got %v", err)
	}
}

// TestOrchestrateDAGUnknownDepRejected: referencing a stage id that does
// not exist must fail fast with a descriptive error.
func TestOrchestrateDAGUnknownDepRejected(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	eng.SetSubagentRunner(&recordingRunner{failAtCall: -1})

	_, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"stages": dagStagesParam(
				map[string]any{"id": "A", "task": "a"},
				map[string]any{"id": "B", "task": "b", "depends_on": depSlice("ghost")},
			),
		},
	})
	if err == nil {
		t.Fatalf("expected unknown-dep error, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("error must mention the unknown id, got %v", err)
	}
}

// TestOrchestrateDAGDuplicateIDRejected: two stages sharing an id must be
// refused — the summaries map would silently overwrite otherwise.
func TestOrchestrateDAGDuplicateIDRejected(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	eng.SetSubagentRunner(&recordingRunner{failAtCall: -1})

	_, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"stages": dagStagesParam(
				map[string]any{"id": "A", "task": "first"},
				map[string]any{"id": "A", "task": "second"},
			),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate id error, got %v", err)
	}
}

// TestOrchestrateDAGFailedDepSkipsDependents: when a stage errors, every
// transitive dependent must be marked skipped with a pointer to the
// offending dep — not run against empty input.
func TestOrchestrateDAGFailedDepSkipsDependents(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	// fail the second call (which will be B in layer 1, see ordering below).
	runner := &recordingRunner{failAtCall: 2}
	eng.SetSubagentRunner(runner)

	_, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"stages": dagStagesParam(
				map[string]any{"id": "A", "task": "first"},
				map[string]any{"id": "B", "task": "second", "depends_on": depSlice("A")},
				map[string]any{"id": "C", "task": "third", "depends_on": depSlice("B")},
			),
		},
	})
	if err == nil {
		t.Fatalf("expected error when a stage fails")
	}
	// C must NOT have been dispatched — only A (ok) + B (fail). 2 calls
	// total; if we see 3 the skip logic missed C.
	if got := atomic.LoadInt32(&runner.callCount); got != 2 {
		t.Fatalf("expected 2 sub-agent calls (A ok, B fail, C skipped), got %d", got)
	}
}

// TestOrchestrateDAGParallelismClampedByCeiling: a 3-wide independent
// fan-out with max_parallel=10 but engine ceiling=2 must never run more
// than 2 stages concurrently.
func TestOrchestrateDAGParallelismClampedByCeiling(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.Agent.ParallelBatchSize = 2
	eng := New(cfg)
	runner := &recordingRunner{sleep: 20 * time.Millisecond, failAtCall: -1}
	eng.SetSubagentRunner(runner)

	_, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"max_parallel": 10,
			"stages": dagStagesParam(
				map[string]any{"id": "A", "task": "a"},
				map[string]any{"id": "B", "task": "b"},
				map[string]any{"id": "C", "task": "c"},
			),
		},
	})
	if err != nil {
		t.Fatalf("orchestrate dag: %v", err)
	}
	if got := atomic.LoadInt32(&runner.peak); got > 2 {
		t.Fatalf("dag parallelism breached ceiling=2, got peak=%d", got)
	}
}

// TestOrchestrateDAGForceSequentialSerializes: force_sequential=true must
// reduce every layer's concurrency to 1 even when the DAG has a wide
// independent fan-out.
func TestOrchestrateDAGForceSequentialSerializes(t *testing.T) {
	cfg := *config.DefaultConfig()
	cfg.Agent.ParallelBatchSize = 8
	eng := New(cfg)
	runner := &recordingRunner{sleep: 10 * time.Millisecond, failAtCall: -1}
	eng.SetSubagentRunner(runner)

	_, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"force_sequential": true,
			"stages": dagStagesParam(
				map[string]any{"id": "A", "task": "a"},
				map[string]any{"id": "B", "task": "b"},
				map[string]any{"id": "C", "task": "c"},
			),
		},
	})
	if err != nil {
		t.Fatalf("orchestrate dag: %v", err)
	}
	if got := atomic.LoadInt32(&runner.peak); got != 1 {
		t.Fatalf("force_sequential must peak at 1, got %d", got)
	}
}

// TestOrchestrateDAGLinearChainBehavesLikeSequential: a pure chain A→B→C
// must run one at a time and thread summaries forward, producing the
// same logical behaviour as the text-split sequential mode.
func TestOrchestrateDAGLinearChainBehavesLikeSequential(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	// Clean summaries so a transitive leak (A's content showing up inside
	// C's prompt *via B's summary*) would fail the assertion cleanly.
	// Default runner echoes the whole task, which bleeds earlier content
	// through.
	runner := &recordingRunner{
		sleep:      5 * time.Millisecond,
		failAtCall: -1,
		summary: func(req SubagentRequest) string {
			head := req.Task
			if i := strings.Index(head, "\n"); i >= 0 {
				head = head[:i]
			}
			return "summary[" + head + "]"
		},
	}
	eng.SetSubagentRunner(runner)

	_, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"stages": dagStagesParam(
				map[string]any{"id": "A", "task": "first"},
				map[string]any{"id": "B", "task": "second", "depends_on": depSlice("A")},
				map[string]any{"id": "C", "task": "third", "depends_on": depSlice("B")},
			),
		},
	})
	if err != nil {
		t.Fatalf("orchestrate dag: %v", err)
	}
	if got := atomic.LoadInt32(&runner.peak); got != 1 {
		t.Fatalf("linear chain must peak at 1, got %d", got)
	}
	if len(runner.calls) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(runner.calls))
	}
	// B must see A's summary; C must see B's summary.
	if !strings.Contains(runner.calls[1].Task, "[A] summary[first]") {
		t.Fatalf("B prompt missing A's summary:\n%s", runner.calls[1].Task)
	}
	if !strings.Contains(runner.calls[2].Task, "[B] summary[second]") {
		t.Fatalf("C prompt missing B's summary:\n%s", runner.calls[2].Task)
	}
	// C should NOT transitively carry A — direct-deps only, to keep
	// prompts tight. If the caller wants A in C, they add A as a direct
	// dep of C. With clean summaries, A's marker won't appear in C at all.
	if strings.Contains(runner.calls[2].Task, "summary[first]") {
		t.Fatalf("C prompt must not carry transitive A (direct-deps only):\n%s", runner.calls[2].Task)
	}
}

// TestOrchestrateDAGEmptyStagesFallsBackToTextSplit: stages=[] should NOT
// short-circuit the splitter — the text path still needs a chance.
func TestOrchestrateDAGEmptyStagesFallsBackToTextSplit(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	runner := &recordingRunner{failAtCall: -1}
	eng.SetSubagentRunner(runner)

	res, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"task":   "fix the off-by-one in token counting",
			"stages": []any{},
		},
	})
	if err != nil {
		t.Fatalf("orchestrate: %v", err)
	}
	if mode, _ := res.Data["mode"].(string); mode != "single" {
		t.Fatalf("empty stages should fall back to text splitter, got mode=%q", mode)
	}
}

// TestOrchestrateDAGMalformedStagesRejected: a shape the LLM might
// hallucinate (stages as string, or a stage missing `task`) produces a
// clear error the model can correct from.
func TestOrchestrateDAGMalformedStagesRejected(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	eng.SetSubagentRunner(&recordingRunner{failAtCall: -1})

	cases := []struct {
		name   string
		stages any
		want   string
	}{
		{"not-array", "stage1,stage2", "must be an array"},
		{"missing-task", dagStagesParam(map[string]any{"id": "A"}), "task is required"},
		{"missing-id", dagStagesParam(map[string]any{"task": "do thing"}), "id is required"},
		{"bad-deps-shape", dagStagesParam(map[string]any{"id": "A", "task": "t", "depends_on": "B,C"}), "must be an array"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eng.Execute(context.Background(), "orchestrate", Request{
				Params: map[string]any{"stages": tc.stages},
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("case %s: want error containing %q, got %v", tc.name, tc.want, err)
			}
		})
	}
}
