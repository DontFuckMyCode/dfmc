package tools

// orchestrate.go — one-call split-and-fan-out.
//
// The model already has the pieces it needs to orchestrate multi-subtask
// work: `task_split` decomposes the ask, `delegate_task` runs one subtask,
// `tool_batch_call` fans N calls out in parallel. But stitching them
// requires three round-trips and the model often forgets the pattern and
// runs everything sequentially in its own loop.
//
// `orchestrate` collapses the whole dance into a single backend tool call:
//
//   1. Deterministic split (planning.SplitTask — offline, no LLM).
//   2. If the query doesn't cleanly split (count≤1 or low confidence), run
//      a single sub-agent on the original task. That keeps the tool safe
//      to call on anything — it degrades to plain `delegate_task`.
//   3. Independent subtasks (Plan.Parallel=true) fan out to concurrent
//      sub-agents, bounded by `max_parallel` (default 4, ceiling cfg
//      ParallelBatchSize). First-error does NOT abort siblings; partial
//      results come back for the model to decide.
//   4. Sequential subtasks (Plan.Parallel=false, "first X then Y") run one
//      at a time with prior summaries threaded into the next prompt so
//      each stage sees what the previous one found.

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/planning"
)


// defaultOrchestrateParallel is the fallback concurrency ceiling. Low on
// purpose — the default config has ParallelBatchSize=4 and we want to match.
const (
	defaultOrchestrateParallel = 4
	maxOrchestrateAutoSubtasks = 8
)

// OrchestrateTool runs the split/fan-out/aggregate pipeline in one call.
type OrchestrateTool struct {
	mu     sync.RWMutex
	runner SubagentRunner
	// maxParallelCeiling comes from Engine config (ParallelBatchSize). 0
	// means "use the per-call `max_parallel` as given".
	maxParallelCeiling int
}

func NewOrchestrateTool() *OrchestrateTool { return &OrchestrateTool{} }
func (t *OrchestrateTool) Name() string    { return "orchestrate" }
func (t *OrchestrateTool) Description() string {
	return "Decompose a task, fan out sub-agents (parallel or sequential), and aggregate their summaries."
}

func (t *OrchestrateTool) SetRunner(r SubagentRunner) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runner = r
}

func (t *OrchestrateTool) SetMaxParallelCeiling(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maxParallelCeiling = n
}

func (t *OrchestrateTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "orchestrate",
		Title:   "Orchestrate multi-subtask work",
		Summary: "One-shot decomposition + sub-agent fan-out + summary aggregation.",
		Purpose: "Prefer this over task_split+tool_batch_call(delegate_task) when the ask clearly has multiple units. It saves two round-trips and guarantees safe concurrency.",
		Prompt: `Single-call orchestrator. Pipeline:
1. Runs the deterministic task_split internally. No LLM round-trip.
2. If the task doesn't split (count=1 or confidence<0.4), runs a single sub-agent on the whole task — so it's safe to call on anything.
3. Parallel subtasks (conjunction splits): fan out concurrently, bounded by ` + "`max_parallel`" + ` (default 4).
4. Sequential subtasks (stage splits "first X then Y"): chain — stage N's prompt receives prior summaries so each stage builds on what came before.

When to use:
- "Survey A, audit B, and document C" — clear conjunction, parallelizable.
- "First map the router, then grep all call sites, finally summarize" — sequential chain.
- Any multi-deliverable ask where you'd otherwise burn multiple rounds coordinating delegate_task calls yourself.

When NOT to use:
- Focused single-shot edits. The splitter returns count=1, one sub-agent runs, but you paid for a fresh context for no gain.
- When you actually need the intermediate tool output visible to the main loop. Sub-agents return summaries only.`,
		Risk: RiskExecute,
		Tags: []string{"meta", "planning", "subagent", "orchestration"},
		Args: []Arg{
			{Name: "task", Type: ArgString, Description: "Free-text task for the deterministic splitter. Required unless `stages` is given."},
			{Name: "stages", Type: ArgArray, Description: "Explicit DAG: array of {id, task, depends_on?, title?, hint?, model?, race?}. When provided, skips the text splitter and runs stages in topological layers. Optional per-stage `model` routes that stage to a specific provider profile. Optional `race` is a 2-3 element provider list that runs the stage on each in parallel and keeps the first success — costs N×, use sparingly (e.g., the final synthesis only). `model` and `race` are mutually exclusive."},
			{Name: "max_parallel", Type: ArgInteger, Default: defaultOrchestrateParallel, Description: "Max concurrent sub-agents (clamped by engine ParallelBatchSize)."},
			{Name: "role", Type: ArgString, Description: "Optional role label passed to every sub-agent."},
			{Name: "sub_max_steps", Type: ArgInteger, Default: 0, Description: "Per-sub-agent step budget (0=engine default)."},
			{Name: "force_sequential", Type: ArgBoolean, Default: false, Description: "Run subtasks sequentially (one at a time) even when they could parallelise. Applies to both text-split and DAG modes."},
		},
		Returns:  "{task, mode(single|parallel|sequential|dag), subtask_count, aggregated, stages:[{id?,title,hint,summary,tool_calls,duration_ms,error?,skipped?,model?,race_winner?,race_candidates?}]}",
		Examples: []string{`{"task":"Survey engine.go, map the router, and document the manager"}`, `{"task":"First list the test files, then run the ones under internal/engine","force_sequential":true}`, `{"stages":[{"id":"perf","task":"audit perf in engine.go"},{"id":"sec","task":"audit security in engine.go"},{"id":"report","task":"write a report combining both","depends_on":["perf","sec"]}]}`, `{"stages":[{"id":"scan","task":"list suspicious files","model":"deepseek"},{"id":"synth","task":"write findings report","depends_on":["scan"],"model":"anthropic"}]}`},
		CostHint: "io-bound",
	}
}

func (t *OrchestrateTool) Execute(ctx context.Context, req Request) (Result, error) {
	task := strings.TrimSpace(asString(req.Params, "task", ""))

	// Reject the empty call up-front (before runner check) so the model
	// gets the param-shape feedback even on engines without a sub-agent
	// runner wired. Either `task` (for the deterministic splitter) or
	// `stages` (for the explicit DAG) is mandatory; everything else is
	// optional and clamped.
	_, hasStages := req.Params["stages"]
	if task == "" && !hasStages {
		return Result{}, missingParamError("orchestrate", "task` or `stages", req.Params,
			`{"task":"Survey engine.go, audit router, document manager"} or {"stages":[{"id":"a","task":"..."},{"id":"b","task":"...","depends_on":["a"]}]}`,
			`Pass "task" (string) for the auto-splitter, OR "stages" (array of {id,task,depends_on?}) for an explicit DAG. The two are mutually exclusive — stages wins when both are set.`)
	}

	t.mu.RLock()
	runner := t.runner
	ceiling := t.maxParallelCeiling
	t.mu.RUnlock()
	if runner == nil {
		return Result{}, fmt.Errorf("orchestrate requires a sub-agent runner; call the provider-native agent loop via dfmc chat/ask")
	}

	maxParallel := asInt(req.Params, "max_parallel", defaultOrchestrateParallel)
	if maxParallel <= 0 {
		maxParallel = defaultOrchestrateParallel
	}
	if ceiling > 0 && maxParallel > ceiling {
		maxParallel = ceiling
	}
	role := strings.TrimSpace(asString(req.Params, "role", ""))
	subSteps := asInt(req.Params, "sub_max_steps", 0)
	forceSequential := asBool(req.Params, "force_sequential", false)

	// Explicit DAG mode short-circuits the text splitter. The caller has
	// already expressed the shape they want, so there's nothing to infer.
	if rawStages, ok := req.Params["stages"]; ok && rawStages != nil {
		stages, err := parseOrchestrateStages(rawStages)
		if err != nil {
			return Result{}, fmt.Errorf("invalid stages: %w", err)
		}
		if len(stages) > 0 {
			layers, err := validateStagesDAG(stages)
			if err != nil {
				return Result{}, fmt.Errorf("stages dag: %w", err)
			}
			parallel := maxParallel
			if forceSequential {
				parallel = 1
			}
			stageResults := t.runDAG(ctx, runner, stages, layers, role, subSteps, parallel)
			return assembleDAGResult(task, stages, layers, stageResults, parallel), firstStageError(stageResults)
		}
	}

	plan := planning.SplitTask(task)
	omittedSubtasks := 0
	if len(plan.Subtasks) > maxOrchestrateAutoSubtasks {
		omittedSubtasks = len(plan.Subtasks) - maxOrchestrateAutoSubtasks
		plan.Subtasks = append([]planning.Subtask(nil), plan.Subtasks[:maxOrchestrateAutoSubtasks]...)
	}

	// Degenerate case: nothing to fan out. Run a single sub-agent so the
	// tool is always safe to call and still provides a context-isolated
	// answer.
	if len(plan.Subtasks) <= 1 || plan.Confidence < 0.4 {
		stage, err := t.runStage(ctx, runner, SubagentRequest{
			Task: task, Role: role, MaxSteps: subSteps,
		}, "task", "")
		stages := []map[string]any{stage}
		return Result{
			Output: stageSummaryLine(stage),
			Data: map[string]any{
				"task":          task,
				"mode":          "single",
				"subtask_count": 1,
				"stages":        stages,
				"aggregated":    stage["summary"],
				"split_reason":  fmt.Sprintf("count=%d confidence=%.2f", len(plan.Subtasks), plan.Confidence),
			},
		}, err
	}

	parallel := plan.Parallel && !forceSequential
	if parallel {
		stages := t.runParallel(ctx, runner, plan, role, subSteps, maxParallel)
		return assembleResult(task, "parallel", plan, stages, maxParallel, omittedSubtasks), firstStageError(stages)
	}

	stages := t.runSequential(ctx, runner, plan, role, subSteps)
	return assembleResult(task, "sequential", plan, stages, 1, omittedSubtasks), firstStageError(stages)
}

// runParallel / runSequential / runStage / subagentPromptFor /
// assembleResult / stageSummaryLine / firstStageError live in
// orchestrate_runners.go.
