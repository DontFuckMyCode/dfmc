package tools

// orchestrate_dag.go — explicit-dependency mode for orchestrate. Schema
// parsing + DAG validation only; the layered execution side (runDAG,
// assembleDAGResult, runRaceStage, dagPromptFor) lives in
// orchestrate_dag_run.go.
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
	"fmt"
	"sort"
	"strings"
)

// orchestrateStage is one node in the DAG. IDs are free-form strings
// chosen by the caller; the only requirement is uniqueness within the
// request.
// maxRaceCandidates caps the stage-level race fan-out. Race runs N full
// sub-agent loops in parallel and keeps the first success — so N×cost in
// the worst case. 3 gives enough diversity (two stronger providers + one
// fallback) without letting a single tool call fan out N sub-agents across
// 6-provider catalogs.
const maxRaceCandidates = 3
const maxOrchestrateDAGStages = 16

type orchestrateStage struct {
	ID        string
	Task      string
	Title     string
	Hint      string
	Model     string
	Race      []string
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
	example := `{"stages":[{"id":"plan","task":"sketch the api"},{"id":"impl","task":"implement it","depends_on":["plan"]}]}`
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf(
			"orchestrate: stages must be a JSON array of {id, task, depends_on?, model?, race?} objects, got %T. "+
				"Correct shape: %s",
			raw, example)
	}
	if len(list) == 0 {
		return nil, nil
	}
	if len(list) > maxOrchestrateDAGStages {
		return nil, fmt.Errorf("stages has %d entries; cap is %d. Split the workflow into smaller DAGs or use text task splitting for broad surveys", len(list), maxOrchestrateDAGStages)
	}
	out := make([]orchestrateStage, 0, len(list))
	for i, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(
				"orchestrate: stages[%d] must be an object {id, task, ...}, got %T. Full shape: %s",
				i, item, example)
		}
		id := strings.TrimSpace(asString(m, "id", ""))
		if id == "" {
			return nil, fmt.Errorf(
				"orchestrate: stages[%d].id is required (used as the dependency reference). Full shape: %s",
				i, example)
		}
		task := strings.TrimSpace(asString(m, "task", ""))
		if task == "" {
			return nil, fmt.Errorf(
				"orchestrate: stages[%d].task is required (the prompt the sub-agent will run). Full shape: %s",
				i, example)
		}
		deps, err := parseDepsList(m["depends_on"])
		if err != nil {
			return nil, fmt.Errorf("stages[%d].depends_on: %w", i, err)
		}
		race, err := parseRaceList(m["race"])
		if err != nil {
			return nil, fmt.Errorf("stages[%d].race: %w", i, err)
		}
		model := strings.TrimSpace(asString(m, "model", ""))
		if len(race) > 0 && model != "" {
			return nil, fmt.Errorf("stages[%d]: model and race are mutually exclusive — pick one", i)
		}
		out = append(out, orchestrateStage{
			ID:        id,
			Task:      task,
			Title:     strings.TrimSpace(asString(m, "title", "")),
			Hint:      strings.TrimSpace(asString(m, "hint", "")),
			Model:     model,
			Race:      race,
			DependsOn: deps,
		})
	}
	return out, nil
}

// parseRaceList parses stages[i].race — an optional []any of provider names
// to race for this stage. Deduped, trimmed, capped at maxRaceCandidates so
// a malformed or oversized list can't fan out unbounded sub-agents. Returns
// (nil, nil) for nil/empty input; any non-array shape is a fail-fast error.
func parseRaceList(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array of provider names, got %T", raw)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(list))
	for i, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("[%d] must be a string, got %T", i, item)
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) > maxRaceCandidates {
		return nil, fmt.Errorf("race has %d candidates; cap is %d (each runs a full sub-agent)", len(out), maxRaceCandidates)
	}
	if len(out) == 1 {
		return nil, fmt.Errorf("race with one candidate is pointless — use `model` instead")
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
