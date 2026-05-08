package tools

// orchestrate_dag_run.go — execution side of the DAG orchestrator.
// runDAG walks the Kahn-sorted layers built by validateStagesDAG and
// dispatches each stage to a sub-agent (or a race fan-out across N
// providers when stage.Race is set), wiring direct dependencies'
// summaries into the prompt as prior findings. assembleDAGResult
// packages the per-stage outcomes into a single Result for the caller.
//
// Schema parsing + DAG validation (orchestrateStage type, constants,
// parseOrchestrateStages, parseRaceList, parseDepsList,
// validateStagesDAG) live in orchestrate_dag.go.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// runDAG runs the validated stages layer-by-layer. Within a layer, stages
// fan out to concurrent sub-agents bounded by `parallel`. A stage whose
// direct dep failed is marked "skipped: dep <id> failed" instead of being
// run against garbage input.
func (t *OrchestrateTool) runDAG(
	ctx context.Context,
	runner SubagentRunner,
	stages []orchestrateStage,
	layers [][]int,
	role string,
	subSteps, parallel int,
) []map[string]any {
	results := make([]map[string]any, len(stages))
	// summaries[id] holds the summary after the stage ran. Populated as
	// layers finish so the next layer can thread them into prompts.
	summaries := make(map[string]string, len(stages))
	// failed[id] marks stages that errored; dependents get skipped.
	failed := make(map[string]bool, len(stages))

	for _, layer := range layers {
		if ctx.Err() != nil {
			break
		}
		p := min(parallel, len(layer))
		if p <= 0 {
			p = 1
		}
		sem := make(chan struct{}, p)
		var wg sync.WaitGroup
		var mu sync.Mutex
		for _, idx := range layer {
			stage := stages[idx]
			// If any dep failed, short-circuit — the sub-agent would have
			// no input worth running on.
			var blocker string
			for _, dep := range stage.DependsOn {
				if failed[dep] {
					blocker = dep
					break
				}
			}
			if blocker != "" {
				results[idx] = map[string]any{
					"id":      stage.ID,
					"title":   stage.Title,
					"hint":    stage.Hint,
					"skipped": fmt.Sprintf("dep %q failed", blocker),
				}
				mu.Lock()
				failed[stage.ID] = true
				mu.Unlock()
				continue
			}
			// Gather prior summaries from direct deps in declaration order.
			priors := make([]string, 0, len(stage.DependsOn))
			for _, dep := range stage.DependsOn {
				if s, ok := summaries[dep]; ok && strings.TrimSpace(s) != "" {
					priors = append(priors, fmt.Sprintf("[%s] %s", dep, s))
				}
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(idx int, s orchestrateStage, priors []string) {
				defer wg.Done()
				defer func() { <-sem }()
				title := s.Title
				if title == "" {
					title = s.ID
				}
				var stageResult map[string]any
				var err error
				if len(s.Race) > 0 {
					stageResult, err = t.runRaceStage(ctx, runner, s, priors, title, role, subSteps)
				} else {
					stageResult, err = t.runStage(ctx, runner, SubagentRequest{
						Task:     dagPromptFor(s, priors),
						Role:     role,
						MaxSteps: subSteps,
						Model:    s.Model,
					}, title, s.Hint)
				}
				stageResult["id"] = s.ID
				if s.Model != "" {
					stageResult["model"] = s.Model
				}
				mu.Lock()
				results[idx] = stageResult
				if err != nil {
					failed[s.ID] = true
				} else if summary, _ := stageResult["summary"].(string); strings.TrimSpace(summary) != "" {
					summaries[s.ID] = summary
				}
				mu.Unlock()
			}(idx, stage, priors)
		}
		wg.Wait()
	}
	return results
}

// assembleDAGResult packages DAG stage outcomes into a Result. Output is a
// per-stage short line; Data surfaces the full stage records plus the
// derived layer layout so callers can see the concurrency shape that
// actually ran.
func assembleDAGResult(task string, stages []orchestrateStage, layers [][]int, results []map[string]any, parallel int) Result {
	lines := make([]string, 0, len(results))
	aggregated := make([]string, 0, len(results))
	for i, stage := range results {
		lines = append(lines, fmt.Sprintf("#%d %s", i+1, stageSummaryLine(stage)))
		if summary, _ := stage["summary"].(string); strings.TrimSpace(summary) != "" {
			header, _ := stage["title"].(string)
			if strings.TrimSpace(header) == "" {
				header, _ = stage["id"].(string)
			}
			if strings.TrimSpace(header) == "" {
				header = fmt.Sprintf("stage %d", i+1)
			}
			aggregated = append(aggregated, fmt.Sprintf("### %s\n%s", header, summary))
		}
	}
	layerIDs := make([][]string, len(layers))
	for i, layer := range layers {
		ids := make([]string, len(layer))
		for j, idx := range layer {
			ids[j] = stages[idx].ID
		}
		layerIDs[i] = ids
	}
	return Result{
		Output: strings.Join(lines, "\n"),
		Data: map[string]any{
			"task":          task,
			"mode":          "dag",
			"subtask_count": len(results),
			"parallel":      parallel,
			"stages":        results,
			"layers":        layerIDs,
			"aggregated":    strings.Join(aggregated, "\n\n"),
		},
	}
}

// runRaceStage fans one DAG stage out to N concurrent sub-agents (one per
// race candidate), each with a different Model. First success wins and the
// losers are cancelled via a child context. If every candidate errors, the
// joined error is returned so the caller can record the stage failure.
//
// The returned map carries the winner's summary plus `race_winner` and
// `race_candidates` for observability; the stage record looks like a normal
// single-runner stage otherwise, so downstream aggregation doesn't need to
// special-case it.
func (t *OrchestrateTool) runRaceStage(
	ctx context.Context,
	runner SubagentRunner,
	stage orchestrateStage,
	priors []string,
	title, role string,
	subSteps int,
) (map[string]any, error) {
	prompt := dagPromptFor(stage, priors)
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type outcome struct {
		model   string
		result  SubagentResult
		err     error
		elapsed int64
	}
	out := make(chan outcome, len(stage.Race))

	start := time.Now()
	for _, model := range stage.Race {
		go func(model string) {
			t0 := time.Now()
			res, err := runner.RunSubagent(raceCtx, SubagentRequest{
				Task:     prompt,
				Role:     role,
				MaxSteps: subSteps,
				Model:    model,
			})
			out <- outcome{
				model:   model,
				result:  res,
				err:     err,
				elapsed: time.Since(t0).Milliseconds(),
			}
		}(model)
	}

	var errs []error
	for range stage.Race {
		select {
		case <-ctx.Done():
			return map[string]any{
				"title":           title,
				"hint":            stage.Hint,
				"race_candidates": append([]string(nil), stage.Race...),
				"error":           ctx.Err().Error(),
				"duration_ms":     time.Since(start).Milliseconds(),
			}, ctx.Err()
		case r := <-out:
			if r.err == nil {
				cancel() // cancel in-flight losers
				return map[string]any{
					"title":           title,
					"hint":            stage.Hint,
					"summary":         strings.TrimSpace(r.result.Summary),
					"tool_calls":      r.result.ToolCalls,
					"duration_ms":     r.result.DurationMs,
					"race_winner":     r.model,
					"race_candidates": append([]string(nil), stage.Race...),
				}, nil
			}
			errs = append(errs, fmt.Errorf("%s: %w", r.model, r.err))
		}
	}
	return map[string]any{
		"title":           title,
		"hint":            stage.Hint,
		"race_candidates": append([]string(nil), stage.Race...),
		"error":           fmt.Errorf("race lost: %w", errors.Join(errs...)).Error(),
		"duration_ms":     time.Since(start).Milliseconds(),
	}, errors.Join(errs...)
}

// dagPromptFor shapes the prompt for a DAG stage. Similar to the text-
// split sequential path but keys the prior findings by dep id so the
// sub-agent knows which upstream task produced which summary.
func dagPromptFor(stage orchestrateStage, priors []string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(stage.Task))
	if len(priors) > 0 {
		b.WriteString("\n\nPrior stage findings (from dependencies; use these, do not redo):\n")
		for _, p := range priors {
			b.WriteString(p)
			b.WriteString("\n\n")
		}
	}
	return b.String()
}
