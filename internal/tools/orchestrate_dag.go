package tools

// orchestrate_dag.go — explicit-dependency mode for orchestrate.
//
// The text-split path in orchestrate.go handles the common cases (flat
// parallel fan-out, or a "first X then Y" chain). It doesn't express the
// shapes that show up once tasks get real: "map A, B, C in parallel, then
// reconcile using all three" (join), "audit perf, audit security, then
// synthesize a report that reads both" (diamond), "build an index, then
// three analyses run against it, then merge" (tree).
//
// DAG mode is opt-in: callers pass a `stages` array — each stage is an
// object with `id`, `task`, and optional `depends_on` pointing at other
// stage ids. The scheduler topologically sorts stages into parallelism
// layers (Kahn's algorithm) and runs each layer with the existing semaphore
// ceiling. A stage's prompt receives its *direct* dependencies' summaries
// as prior findings — not the transitive closure, because that blows up
// the prompt quickly and the caller can always add more direct deps if
// they actually need more context.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// orchestrateStage is one node in the DAG. IDs are free-form strings
// chosen by the caller; the only requirement is uniqueness within the
// request.
type orchestrateStage struct {
	ID        string
	Task      string
	Title     string
	Hint      string
	Model     string
	DependsOn []string
}

// parseOrchestrateStages converts the `stages` param into a validated slice.
// Accepts []any whose elements are map[string]any; rejects any other shape
// with a descriptive error so the model can self-correct. Empty input
// returns (nil, nil) — the caller then falls back to the text splitter.
func parseOrchestrateStages(raw any) ([]orchestrateStage, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("stages must be an array, got %T", raw)
	}
	if len(list) == 0 {
		return nil, nil
	}
	out := make([]orchestrateStage, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("stages[%d] must be an object, got %T", i, item)
		}
		id := strings.TrimSpace(asString(m, "id", ""))
		if id == "" {
			return nil, fmt.Errorf("stages[%d].id is required", i)
		}
		task := strings.TrimSpace(asString(m, "task", ""))
		if task == "" {
			return nil, fmt.Errorf("stages[%d].task is required", i)
		}
		deps, err := parseDepsList(m["depends_on"])
		if err != nil {
			return nil, fmt.Errorf("stages[%d].depends_on: %w", i, err)
		}
		out = append(out, orchestrateStage{
			ID:        id,
			Task:      task,
			Title:     strings.TrimSpace(asString(m, "title", "")),
			Hint:      strings.TrimSpace(asString(m, "hint", "")),
			Model:     strings.TrimSpace(asString(m, "model", "")),
			DependsOn: deps,
		})
	}
	return out, nil
}

// parseDepsList accepts either []any of strings or nil. Any other shape
// (single string, map, etc.) is an error — keeping the schema narrow
// prevents ambiguity when the model hallucinates a wrong shape.
func parseDepsList(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array of stage ids, got %T", raw)
	}
	out := make([]string, 0, len(list))
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("[%d] must be a string, got %T", i, item)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("[%d] is empty", i)
		}
		out = append(out, s)
	}
	return out, nil
}

// validateStagesDAG ensures the graph is usable: unique ids, no references
// to unknown stages, no self-loops, no cycles. Returns the Kahn-sorted
// layers (each layer is a slice of stage indices into the input) so the
// caller doesn't have to recompute the topology.
func validateStagesDAG(stages []orchestrateStage) ([][]int, error) {
	if len(stages) == 0 {
		return nil, fmt.Errorf("stages is empty")
	}
	idIndex := make(map[string]int, len(stages))
	for i, s := range stages {
		if _, dup := idIndex[s.ID]; dup {
			return nil, fmt.Errorf("duplicate stage id %q", s.ID)
		}
		idIndex[s.ID] = i
	}
	indeg := make([]int, len(stages))
	// dependents[i] = indices of stages that depend on stage i
	dependents := make([][]int, len(stages))
	for i, s := range stages {
		seen := map[string]struct{}{}
		for _, dep := range s.DependsOn {
			if dep == s.ID {
				return nil, fmt.Errorf("stage %q depends on itself", s.ID)
			}
			if _, dup := seen[dep]; dup {
				return nil, fmt.Errorf("stage %q lists dep %q twice", s.ID, dep)
			}
			seen[dep] = struct{}{}
			depIdx, ok := idIndex[dep]
			if !ok {
				return nil, fmt.Errorf("stage %q depends on unknown stage %q", s.ID, dep)
			}
			dependents[depIdx] = append(dependents[depIdx], i)
			indeg[i]++
		}
	}
	// Kahn: emit layers. Each iteration collects all nodes with indeg==0
	// at that moment, then decrements their dependents.
	layers := [][]int{}
	remaining := len(stages)
	for remaining > 0 {
		layer := []int{}
		for i, d := range indeg {
			if d == 0 {
				layer = append(layer, i)
				indeg[i] = -1 // mark consumed
			}
		}
		if len(layer) == 0 {
			// Everyone still has indeg>0 → cycle exists. Build the
			// offender list for the error so the caller can fix it.
			var stuck []string
			for i, d := range indeg {
				if d > 0 {
					stuck = append(stuck, stages[i].ID)
				}
			}
			sort.Strings(stuck)
			return nil, fmt.Errorf("cycle in stages dag; unresolvable: %s", strings.Join(stuck, ","))
		}
		// Stable ordering within a layer so Output is deterministic.
		sort.Ints(layer)
		for _, idx := range layer {
			for _, next := range dependents[idx] {
				indeg[next]--
			}
		}
		layers = append(layers, layer)
		remaining -= len(layer)
	}
	return layers, nil
}

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
				stageResult, err := t.runStage(ctx, runner, SubagentRequest{
					Task:     dagPromptFor(s, priors),
					Role:     role,
					MaxSteps: subSteps,
					Model:    s.Model,
				}, title, s.Hint)
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
