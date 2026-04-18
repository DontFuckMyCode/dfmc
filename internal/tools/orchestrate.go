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

// runParallel fans the plan's subtasks out to concurrent sub-agents. Bounded
// by `parallel`. A per-subtask failure records the error on that stage but
// does not cancel siblings — the caller decides what to do with partial
// results.
func (t *OrchestrateTool) runParallel(
	ctx context.Context,
	runner SubagentRunner,
	plan planning.Plan,
	role string,
	subSteps, parallel int,
) []map[string]any {
	if parallel > len(plan.Subtasks) {
		parallel = len(plan.Subtasks)
	}
	stages := make([]map[string]any, len(plan.Subtasks))
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for i, sub := range plan.Subtasks {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, s planning.Subtask) {
			defer wg.Done()
			defer func() { <-sem }()
			stage, _ := t.runStage(ctx, runner, SubagentRequest{
				Task:     subagentPromptFor(s, nil),
				Role:     role,
				MaxSteps: subSteps,
			}, s.Title, s.Hint)
			stages[idx] = stage
		}(i, sub)
	}
	wg.Wait()
	return stages
}

// runSequential runs subtasks one at a time, threading each stage's summary
// into the next stage's prompt so the chain accumulates context.
func (t *OrchestrateTool) runSequential(
	ctx context.Context,
	runner SubagentRunner,
	plan planning.Plan,
	role string,
	subSteps int,
) []map[string]any {
	stages := make([]map[string]any, 0, len(plan.Subtasks))
	priorSummaries := make([]string, 0, len(plan.Subtasks))
	for _, sub := range plan.Subtasks {
		if ctx.Err() != nil {
			break
		}
		stage, _ := t.runStage(ctx, runner, SubagentRequest{
			Task:     subagentPromptFor(sub, priorSummaries),
			Role:     role,
			MaxSteps: subSteps,
		}, sub.Title, sub.Hint)
		stages = append(stages, stage)
		if summary, _ := stage["summary"].(string); strings.TrimSpace(summary) != "" {
			priorSummaries = append(priorSummaries, summary)
		}
		// Hard stop on error: the next stage presumably depends on this
		// stage's output, so running it on a stale/empty prior summary is
		// worse than short-circuiting.
		if stage["error"] != nil {
			break
		}
	}
	return stages
}

// runStage invokes the sub-agent for one subtask and packages the result
// (or error) into a stage map ready to be returned in Result.Data.
func (t *OrchestrateTool) runStage(
	ctx context.Context,
	runner SubagentRunner,
	req SubagentRequest,
	title, hint string,
) (map[string]any, error) {
	stage := map[string]any{
		"title": title,
		"hint":  hint,
	}
	res, err := runner.RunSubagent(ctx, req)
	stage["tool_calls"] = res.ToolCalls
	stage["duration_ms"] = res.DurationMs
	if err != nil {
		stage["error"] = err.Error()
		return stage, err
	}
	stage["summary"] = strings.TrimSpace(res.Summary)
	return stage, nil
}

// subagentPromptFor shapes the text handed to a sub-agent. Parallel stages
// get just the subtask; sequential stages also get a "Prior findings"
// section so later stages can build on earlier ones.
func subagentPromptFor(sub planning.Subtask, priorSummaries []string) string {
	var b strings.Builder
	body := strings.TrimSpace(sub.Description)
	if body == "" {
		body = strings.TrimSpace(sub.Title)
	}
	b.WriteString(body)
	if len(priorSummaries) > 0 {
		b.WriteString("\n\nPrior stage findings (use these; do not redo the work):\n")
		for i, s := range priorSummaries {
			trimmed := strings.TrimSpace(s)
			if trimmed == "" {
				continue
			}
			b.WriteString(fmt.Sprintf("[stage %d]\n%s\n\n", i+1, trimmed))
		}
	}
	return b.String()
}

// assembleResult collects stage maps into a Result, with a compact Output
// line per stage for the model's tool_result content and the full detail
// in Data.
func assembleResult(task, mode string, plan planning.Plan, stages []map[string]any, parallel int, omittedSubtasks int) Result {
	lines := make([]string, 0, len(stages))
	aggregated := make([]string, 0, len(stages))
	for i, stage := range stages {
		lines = append(lines, fmt.Sprintf("#%d %s", i+1, stageSummaryLine(stage)))
		if summary, _ := stage["summary"].(string); strings.TrimSpace(summary) != "" {
			title, _ := stage["title"].(string)
			header := strings.TrimSpace(title)
			if header == "" {
				header = fmt.Sprintf("stage %d", i+1)
			}
			aggregated = append(aggregated, fmt.Sprintf("### %s\n%s", header, summary))
		}
	}
	if omittedSubtasks > 0 {
		lines = append(lines, fmt.Sprintf("[orchestrate capped auto-split at %d stages; omitted %d additional subtasks]", maxOrchestrateAutoSubtasks, omittedSubtasks))
	}
	return Result{
		Output: strings.Join(lines, "\n"),
		Data: map[string]any{
			"task":             task,
			"mode":             mode,
			"subtask_count":    len(stages),
			"parallel":         parallel,
			"confidence":       plan.Confidence,
			"stages":           stages,
			"aggregated":       strings.Join(aggregated, "\n\n"),
			"omitted_subtasks": omittedSubtasks,
		},
	}
}

// stageSummaryLine produces one short line for Result.Output. Errored stages
// surface the error; successful stages show the title and duration.
func stageSummaryLine(stage map[string]any) string {
	title, _ := stage["title"].(string)
	if title == "" {
		title = "subtask"
	}
	dur, _ := stage["duration_ms"].(int64)
	if errMsg, ok := stage["error"].(string); ok && errMsg != "" {
		return fmt.Sprintf("%s: FAIL %s", title, errMsg)
	}
	return fmt.Sprintf("%s: OK (%dms)", title, dur)
}

// firstStageError returns the first stage's error — useful for signalling
// a degraded orchestration to the model even though siblings may have
// succeeded. Execute's caller still gets the full stage data via Result.
func firstStageError(stages []map[string]any) error {
	for _, stage := range stages {
		if errMsg, ok := stage["error"].(string); ok && errMsg != "" {
			return fmt.Errorf("%s", errMsg)
		}
	}
	return nil
}
