package tools

import (
	"context"
	"fmt"
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
		{"not-array", "stage1,stage2", "must be a JSON array"},
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

// TestOrchestrateDAGPerStageModelOverride: each stage's optional `model`
// must reach SubagentRequest.Model verbatim so downstream RunSubagent can
// route that stage to a specific provider profile. The returned stage
// record must also echo the model so callers can confirm routing.
func TestOrchestrateDAGPerStageModelOverride(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	runner := &recordingRunner{failAtCall: -1}
	eng.SetSubagentRunner(runner)

	res, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"stages": dagStagesParam(
				map[string]any{"id": "scan", "task": "list files", "model": "deepseek"},
				map[string]any{"id": "synth", "task": "summarize", "depends_on": depSlice("scan"), "model": "anthropic"},
			),
		},
	})
	if err != nil {
		t.Fatalf("orchestrate dag: %v", err)
	}

	gotModels := map[string]string{}
	for _, call := range runner.calls {
		head := call.Task
		if i := strings.Index(head, "\n"); i >= 0 {
			head = head[:i]
		}
		gotModels[strings.TrimSpace(head)] = call.Model
	}
	if gotModels["list files"] != "deepseek" {
		t.Fatalf("scan stage model=%q, want deepseek (all=%+v)", gotModels["list files"], gotModels)
	}
	if gotModels["summarize"] != "anthropic" {
		t.Fatalf("synth stage model=%q, want anthropic (all=%+v)", gotModels["summarize"], gotModels)
	}

	stages, _ := res.Data["stages"].([]map[string]any)
	if len(stages) != 2 {
		t.Fatalf("want 2 stage records, got %d", len(stages))
	}
	for _, s := range stages {
		id, _ := s["id"].(string)
		model, _ := s["model"].(string)
		switch id {
		case "scan":
			if model != "deepseek" {
				t.Fatalf("scan record model=%q, want deepseek", model)
			}
		case "synth":
			if model != "anthropic" {
				t.Fatalf("synth record model=%q, want anthropic", model)
			}
		default:
			t.Fatalf("unexpected stage id %q", id)
		}
	}
}

// TestOrchestrateDAGRaceReturnsWinner: a stage with race:["fast","slow"]
// must fan out two sub-agents, keep the fast one's summary, and record
// race_winner="fast" plus the full candidate list. The slow sub-agent
// must see its context cancelled so it doesn't keep spending after the
// race is decided.
func TestOrchestrateDAGRaceReturnsWinner(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	runner := &perModelRunner{
		delays: map[string]time.Duration{
			"fast": 10 * time.Millisecond,
			"slow": 300 * time.Millisecond,
		},
	}
	eng.SetSubagentRunner(runner)

	res, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"stages": dagStagesParam(
				map[string]any{"id": "synth", "task": "write report", "race": []any{"fast", "slow"}},
			),
		},
	})
	if err != nil {
		t.Fatalf("orchestrate race: %v", err)
	}
	stages, _ := res.Data["stages"].([]map[string]any)
	if len(stages) != 1 {
		t.Fatalf("want 1 stage, got %d", len(stages))
	}
	winner, _ := stages[0]["race_winner"].(string)
	if winner != "fast" {
		t.Fatalf("race_winner=%q, want fast (stage=%+v)", winner, stages[0])
	}
	cands, _ := stages[0]["race_candidates"].([]string)
	if len(cands) != 2 {
		t.Fatalf("race_candidates=%v, want 2 entries", cands)
	}
	summary, _ := stages[0]["summary"].(string)
	if !strings.Contains(summary, "fast-reply") {
		t.Fatalf("winner summary=%q, want fast-reply marker", summary)
	}

	// The slow sub-agent must have been cancelled; give it a brief window
	// to propagate the cancellation before asserting.
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&runner.slowCancelled) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&runner.slowCancelled) < 1 {
		t.Fatalf("slow candidate was not cancelled after the fast one won")
	}
}

// TestOrchestrateDAGRaceAllFailReturnsJoinedError: when every candidate
// errors the stage must surface a joined error and the caller must be
// able to tell both failures happened (no silent partial success).
func TestOrchestrateDAGRaceAllFailReturnsJoinedError(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	runner := &perModelRunner{
		errs: map[string]string{
			"a": "boom-a",
			"b": "boom-b",
		},
	}
	eng.SetSubagentRunner(runner)

	_, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"stages": dagStagesParam(
				map[string]any{"id": "synth", "task": "t", "race": []any{"a", "b"}},
			),
		},
	})
	if err == nil {
		t.Fatalf("expected joined race error, got nil")
	}
	if !strings.Contains(err.Error(), "boom-a") || !strings.Contains(err.Error(), "boom-b") {
		t.Fatalf("expected both candidate errors in %v", err)
	}
}

// TestOrchestrateDAGRaceAndModelMutuallyExclusive: specifying both `model`
// and `race` on the same stage is ambiguous routing; the parser must
// refuse at config time so the model doesn't silently lose one of its
// hints.
func TestOrchestrateDAGRaceAndModelMutuallyExclusive(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	eng.SetSubagentRunner(&recordingRunner{failAtCall: -1})

	_, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"stages": dagStagesParam(
				map[string]any{"id": "A", "task": "t", "model": "x", "race": []any{"a", "b"}},
			),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}

// TestOrchestrateDAGRaceCapEnforced: a race list larger than the hard cap
// must be refused — the alternative is N× cost explosions from a single
// tool call.
func TestOrchestrateDAGRaceCapEnforced(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	eng.SetSubagentRunner(&recordingRunner{failAtCall: -1})

	_, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"stages": dagStagesParam(
				map[string]any{"id": "A", "task": "t", "race": []any{"a", "b", "c", "d"}},
			),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("expected cap error, got %v", err)
	}
}

// TestOrchestrateDAGRaceSingletonRejected: a one-element race has no
// racing semantics (nothing to compare against). The parser must nudge
// the caller toward `model` instead of silently running a single sub-agent.
func TestOrchestrateDAGRaceSingletonRejected(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	eng.SetSubagentRunner(&recordingRunner{failAtCall: -1})

	_, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"stages": dagStagesParam(
				map[string]any{"id": "A", "task": "t", "race": []any{"only"}},
			),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "pointless") {
		t.Fatalf("expected pointless-race error, got %v", err)
	}
}

// perModelRunner routes SubagentRequest.Model to a delay/error map so
// race tests can pick deterministic winners and track cancellation of
// losers. Safe for concurrent use.
type perModelRunner struct {
	delays        map[string]time.Duration
	errs          map[string]string
	slowCancelled int32
}

func (r *perModelRunner) RunSubagent(ctx context.Context, req SubagentRequest) (SubagentResult, error) {
	delay := r.delays[req.Model]
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			if req.Model == "slow" {
				atomic.AddInt32(&r.slowCancelled, 1)
			}
			return SubagentResult{}, ctx.Err()
		}
	}
	if msg, ok := r.errs[req.Model]; ok && msg != "" {
		return SubagentResult{}, fmt.Errorf("%s", msg)
	}
	return SubagentResult{
		Summary:    req.Model + "-reply",
		ToolCalls:  1,
		DurationMs: delay.Milliseconds(),
	}, nil
}

// TestOrchestrateDAGEmptyStageModelNotRecorded: stages without a `model`
// field must NOT have a "model" key on their record, so callers can tell
// an overridden stage from a default-routed one.
func TestOrchestrateDAGEmptyStageModelNotRecorded(t *testing.T) {
	cfg := *config.DefaultConfig()
	eng := New(cfg)
	eng.SetSubagentRunner(&recordingRunner{failAtCall: -1})

	res, err := eng.Execute(context.Background(), "orchestrate", Request{
		Params: map[string]any{
			"stages": dagStagesParam(
				map[string]any{"id": "A", "task": "plain"},
			),
		},
	})
	if err != nil {
		t.Fatalf("orchestrate dag: %v", err)
	}
	stages, _ := res.Data["stages"].([]map[string]any)
	if len(stages) != 1 {
		t.Fatalf("want 1 stage record, got %d", len(stages))
	}
	if _, has := stages[0]["model"]; has {
		t.Fatalf("unroutinted stage must not carry a model key: %+v", stages[0])
	}
}
