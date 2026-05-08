package tools

// orchestrate_runners.go — fan-out runners + result assembly for the
// orchestrate tool. Sibling of orchestrate.go which keeps the
// OrchestrateTool struct, NewOrchestrateTool/Name/Description/Spec/
// SetRunner/SetMaxParallelCeiling plumbing, and the Execute pipeline
// that dispatches between explicit-DAG mode (runDAG in
// orchestrate_dag.go) and auto-split mode (runParallel / runSequential
// here).
//
// These live in a sibling because Execute is mostly routing — the
// real work is in the runners and the per-stage assembly helpers.

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/planning"
)

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
	res, err := runSubagentRetrying(ctx, runner, req, defaultSubagentRetryAttempts)
	stage["tool_calls"] = res.ToolCalls
	stage["duration_ms"] = res.DurationMs
	if err != nil {
		stage["error"] = err.Error()
		return stage, err
	}
	if attempts, ok := res.Data["attempts"]; ok {
		stage["attempts"] = attempts
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
