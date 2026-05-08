package supervisor

// executor.go — execution-plan builder for a supervisor run. Walks
// the input tasks, normalises them, optionally synthesises a survey
// or verification task, then derives the DAG layers + lane caps used
// by the scheduler and visualisers.
//
// Companion siblings (extracted to keep this file scannable):
//
//   - executor_synth.go     AutoSurvey + AutoVerify task synthesis,
//                           idempotent against planner output via
//                           hasSurveyTask / hasVerificationTask
//   - executor_normalize.go normalizeTasks + the small string /
//                           worker-class / verification / confidence
//                           cleanup helpers (addUnique, cleanStrings,
//                           containsAny live here too)

import (
	"sort"
	"strings"
)

type ExecutionOptions struct {
	AutoSurvey  bool
	AutoVerify  bool
	MaxParallel int
}

type PlannedTask struct {
	Task
	Layer      int
	Dependents []string
	IsRoot     bool
	IsLeaf     bool
	IsAuto     bool
}

type ExecutionPlan struct {
	RunID          string
	Task           string
	Status         string
	Reason         string
	Tasks          []PlannedTask
	Layers         [][]string
	MaxParallel    int
	Roots          []string
	Leaves         []string
	WorkerCounts   map[string]int
	LaneCaps       map[string]int
	LaneOrder      []string
	SurveyID       string
	VerificationID string
}

// BuildExecutionPlan normalizes a supervisor run into an execution plan:
// tasks are cleaned, a verification task can be synthesized, and DAG layers
// are derived for safe scheduling/visualization.
func BuildExecutionPlan(run Run, opts ExecutionOptions) ExecutionPlan {
	tasks := normalizeTasks(run.Tasks)
	if opts.AutoSurvey {
		if extra := synthesizeSurveyTask(tasks); extra != nil {
			tasks = prependSurveyTask(tasks, *extra)
		}
	}
	if opts.AutoVerify {
		if extra := synthesizeVerificationTask(tasks); extra != nil {
			tasks = append(tasks, *extra)
		}
	}

	layers, depsByID, childrenByID := topoLayers(tasks)
	planned := make([]PlannedTask, 0, len(tasks))
	workerCounts := map[string]int{}
	var roots []string
	var leaves []string
	surveyID := ""
	verificationID := ""

	layerByID := map[string]int{}
	for layerIdx, layer := range layers {
		for _, id := range layer {
			layerByID[id] = layerIdx
		}
	}

	for _, task := range tasks {
		id := strings.TrimSpace(task.ID)
		deps := depsByID[id]
		children := childrenByID[id]
		if len(deps) == 0 {
			roots = append(roots, id)
		}
		if len(children) == 0 {
			leaves = append(leaves, id)
		}
		if verificationID == "" && task.IsAuto() {
			if strings.EqualFold(string(task.Verification), string(VerifyRequired)) || strings.EqualFold(string(task.Verification), string(VerifyDeep)) {
				if strings.EqualFold(strings.TrimSpace(task.ProviderTag), "test") || strings.EqualFold(strings.TrimSpace(task.ProviderTag), "review") {
					verificationID = id
				}
			}
		}
		if surveyID == "" && task.IsAuto() {
			if task.WorkerClass == WorkerResearcher || task.WorkerClass == WorkerPlanner {
				if strings.EqualFold(strings.TrimSpace(task.ProviderTag), "research") || strings.EqualFold(strings.TrimSpace(task.ProviderTag), "plan") {
					surveyID = id
				}
			}
		}
		workerCounts[string(task.WorkerClass)]++
		planned = append(planned, PlannedTask{
			Task:       task,
			Layer:      layerByID[id],
			Dependents: append([]string(nil), children...),
			IsRoot:     len(deps) == 0,
			IsLeaf:     len(children) == 0,
			IsAuto:     task.IsAuto(),
		})
	}

	sort.Strings(roots)
	sort.Strings(leaves)
	sort.Slice(planned, func(i, j int) bool {
		if planned[i].Layer == planned[j].Layer {
			return planned[i].ID < planned[j].ID
		}
		return planned[i].Layer < planned[j].Layer
	})

	maxParallel := opts.MaxParallel
	if maxParallel <= 0 {
		maxParallel = 1
	}
	laneCaps, laneOrder := deriveLanePolicy(tasks, maxParallel)
	return ExecutionPlan{
		RunID:          run.ID,
		Task:           run.Task,
		Status:         run.Status,
		Reason:         run.Reason,
		Tasks:          planned,
		Layers:         layers,
		MaxParallel:    maxParallel,
		Roots:          roots,
		Leaves:         leaves,
		WorkerCounts:   workerCounts,
		LaneCaps:       laneCaps,
		LaneOrder:      laneOrder,
		SurveyID:       surveyID,
		VerificationID: verificationID,
	}
}

func (t Task) IsAuto() bool {
	for _, label := range t.Labels {
		if strings.EqualFold(strings.TrimSpace(label), "supervisor") {
			return true
		}
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(t.ID)), "v")
}

func topoLayers(tasks []Task) ([][]string, map[string][]string, map[string][]string) {
	if len(tasks) == 0 {
		return nil, map[string][]string{}, map[string][]string{}
	}
	indeg := map[string]int{}
	depsByID := map[string][]string{}
	childrenByID := map[string][]string{}
	byID := map[string]Task{}
	for _, task := range tasks {
		byID[task.ID] = task
		indeg[task.ID] = 0
	}
	for _, task := range tasks {
		for _, dep := range task.DependsOn {
			if _, ok := byID[dep]; !ok {
				continue
			}
			indeg[task.ID]++
			depsByID[task.ID] = append(depsByID[task.ID], dep)
			childrenByID[dep] = append(childrenByID[dep], task.ID)
		}
	}

	remaining := len(tasks)
	used := map[string]bool{}
	var layers [][]string
	for remaining > 0 {
		layer := make([]string, 0, remaining)
		for _, task := range tasks {
			if used[task.ID] {
				continue
			}
			if indeg[task.ID] == 0 {
				layer = append(layer, task.ID)
			}
		}
		if len(layer) == 0 {
			// Cycle or bad deps. Fall back to stable single-task layers so the
			// caller still gets a usable plan instead of panicking.
			for _, task := range tasks {
				if !used[task.ID] {
					layers = append(layers, []string{task.ID})
					used[task.ID] = true
				}
			}
			break
		}
		sort.Strings(layer)
		layers = append(layers, layer)
		for _, id := range layer {
			used[id] = true
			remaining--
			for _, child := range childrenByID[id] {
				indeg[child]--
			}
		}
	}

	for id := range depsByID {
		sort.Strings(depsByID[id])
	}
	for id := range childrenByID {
		sort.Strings(childrenByID[id])
	}
	return layers, depsByID, childrenByID
}

func deriveLanePolicy(tasks []Task, maxParallel int) (map[string]int, []string) {
	if maxParallel <= 0 {
		maxParallel = 1
	}
	present := map[string]struct{}{}
	for _, task := range tasks {
		present[taskLane(task)] = struct{}{}
	}
	if len(present) == 0 {
		return nil, nil
	}
	order := make([]string, 0, len(present))
	for _, lane := range []string{"discovery", "code", "review", "verify", "synthesize"} {
		if _, ok := present[lane]; ok {
			order = append(order, lane)
		}
	}
	caps := make(map[string]int, len(order))
	for _, lane := range order {
		switch lane {
		case "code":
			caps[lane] = maxParallel
		default:
			caps[lane] = 1
		}
	}
	return caps, order
}

func taskLane(task Task) string {
	switch task.WorkerClass {
	case WorkerPlanner, WorkerResearcher:
		return "discovery"
	case WorkerReviewer:
		return "review"
	case WorkerTester, WorkerSecurity:
		return "verify"
	case WorkerSynthesizer:
		return "synthesize"
	}
	switch strings.ToLower(strings.TrimSpace(task.ProviderTag)) {
	case "research", "plan":
		return "discovery"
	case "review":
		return "review"
	case "test":
		return "verify"
	case "synthesize":
		return "synthesize"
	default:
		return "code"
	}
}
