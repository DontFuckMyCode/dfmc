package supervisor

import (
	"fmt"
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

func normalizeTasks(tasks []Task) []Task {
	if len(tasks) == 0 {
		return nil
	}
	out := make([]Task, 0, len(tasks))
	seen := map[string]struct{}{}
	for _, task := range tasks {
		task.ID = strings.TrimSpace(task.ID)
		if task.ID == "" {
			continue
		}
		if _, ok := seen[task.ID]; ok {
			continue
		}
		seen[task.ID] = struct{}{}
		task.Title = strings.TrimSpace(task.Title)
		task.Detail = strings.TrimSpace(task.Detail)
		task.ProviderTag = strings.TrimSpace(task.ProviderTag)
		task.WorkerClass = WorkerClass(normalizeWorkerClass(string(task.WorkerClass)))
		task.Skills = cleanStrings(task.Skills)
		task.AllowedTools = cleanStrings(task.AllowedTools)
		task.Labels = cleanStrings(task.Labels)
		task.Verification = VerificationStatus(normalizeVerification(string(task.Verification)))
		task.Confidence = clampConfidence(task.Confidence)
		task.DependsOn = cleanStrings(task.DependsOn)
		task.FileScope = cleanStrings(task.FileScope)
		if task.State == "" {
			task.State = TaskPending
		}
		out = append(out, task)
	}
	return out
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

func synthesizeVerificationTask(tasks []Task) *Task {
	if len(tasks) == 0 || hasVerificationTask(tasks) {
		return nil
	}
	targets := verificationCandidates(tasks)
	if len(targets) == 0 {
		return nil
	}

	deps := make([]string, 0, len(targets))
	scopeSet := map[string]struct{}{}
	labels := []string{"verification", "supervisor"}
	skills := []string{"test", "review"}
	worker := WorkerTester
	providerTag := "test"
	verification := VerifyRequired
	title := "Verification pass"
	deep := false

	for _, task := range targets {
		deps = append(deps, task.ID)
		for _, f := range task.FileScope {
			scopeSet[f] = struct{}{}
		}
		for _, label := range task.Labels {
			addUnique(&labels, label)
		}
		for _, skill := range task.Skills {
			if strings.EqualFold(skill, "audit") {
				addUnique(&skills, "audit")
			}
		}
		if task.Verification == VerifyDeep || task.WorkerClass == WorkerSecurity {
			deep = true
		}
	}

	if deep {
		title = "Deep verification pass"
		worker = WorkerVerifier
		providerTag = "review"
		verification = VerifyDeep
		addUnique(&skills, "audit")
	}

	sort.Strings(deps)
	scope := make([]string, 0, len(scopeSet))
	for f := range scopeSet {
		scope = append(scope, f)
	}
	sort.Strings(scope)

	detail := fmt.Sprintf(
		"Verify the outcomes of tasks %s. Run the smallest relevant test/build/lint commands first, then confirm the claimed behavior matches the changed code and report residual risk.",
		strings.Join(deps, ", "),
	)
	if len(scope) > 0 {
		detail += " Focus on these files first: " + strings.Join(scope, ", ") + "."
	}
	if deep {
		detail += " Include a deeper regression/security sanity pass."
	}

	return &Task{
		ID:           nextVerificationTaskID(tasks),
		Title:        title,
		Detail:       detail,
		State:        TaskPending,
		DependsOn:    deps,
		FileScope:    scope,
		ReadOnly:     true,
		ProviderTag:  providerTag,
		WorkerClass:  worker,
		Skills:       cleanStrings(skills),
		AllowedTools: []string{"read_file", "grep_codebase", "glob", "find_symbol", "codemap", "ast_query", "list_dir", "run_command"},
		Labels:       cleanStrings(labels),
		Verification: verification,
		Confidence:   1,
	}
}

func synthesizeSurveyTask(tasks []Task) *Task {
	if len(tasks) == 0 || hasSurveyTask(tasks) {
		return nil
	}
	targets := surveyTargets(tasks)
	if len(targets) == 0 {
		return nil
	}
	scopeSet := map[string]struct{}{}
	labels := []string{"survey", "supervisor"}
	for _, task := range targets {
		for _, f := range task.FileScope {
			scopeSet[f] = struct{}{}
		}
		for _, label := range task.Labels {
			addUnique(&labels, label)
		}
	}
	scope := make([]string, 0, len(scopeSet))
	for f := range scopeSet {
		scope = append(scope, f)
	}
	sort.Strings(scope)

	detail := "Survey the relevant code paths before implementation or review begins. Identify the true entry points, the files most likely to change, and any constraints the later tasks should respect."
	if len(scope) > 0 {
		detail += " Start with these files if present: " + strings.Join(scope, ", ") + "."
	}

	return &Task{
		ID:           nextSurveyTaskID(tasks),
		Title:        "Initial codebase survey",
		Detail:       detail,
		State:        TaskPending,
		DependsOn:    nil,
		FileScope:    scope,
		ReadOnly:     true,
		ProviderTag:  "research",
		WorkerClass:  WorkerResearcher,
		Skills:       []string{"onboard"},
		AllowedTools: []string{"read_file", "grep_codebase", "glob", "find_symbol", "codemap", "ast_query", "list_dir"},
		Labels:       cleanStrings(labels),
		Verification: VerifyLight,
		Confidence:   1,
	}
}

func prependSurveyTask(tasks []Task, survey Task) []Task {
	out := make([]Task, 0, len(tasks)+1)
	out = append(out, survey)
	for _, task := range tasks {
		task.DependsOn = append([]string(nil), task.DependsOn...)
		if len(task.DependsOn) == 0 && task.ID != survey.ID {
			task.DependsOn = append(task.DependsOn, survey.ID)
		}
		out = append(out, task)
	}
	return out
}

func hasVerificationTask(tasks []Task) bool {
	for _, task := range tasks {
		if task.WorkerClass == WorkerTester || task.WorkerClass == WorkerSecurity {
			if containsAny(strings.ToLower(task.Title+"\n"+task.Detail), []string{"verify", "verification", "regression", "test", "build", "lint"}) {
				return true
			}
		}
		if task.ProviderTag == "test" || task.ProviderTag == "review" {
			if containsAny(strings.ToLower(task.Title+"\n"+task.Detail), []string{"verify", "verification", "regression", "test", "build", "lint"}) {
				return true
			}
		}
	}
	return false
}

func hasSurveyTask(tasks []Task) bool {
	for _, task := range tasks {
		if task.WorkerClass == WorkerResearcher || task.WorkerClass == WorkerPlanner {
			if containsAny(strings.ToLower(task.Title+"\n"+task.Detail), []string{"survey", "map", "inventory", "understand", "entry point", "read the relevant files"}) {
				return true
			}
		}
		if task.ProviderTag == "research" || task.ProviderTag == "plan" {
			if containsAny(strings.ToLower(task.Title+"\n"+task.Detail), []string{"survey", "map", "inventory", "understand", "entry point", "read the relevant files"}) {
				return true
			}
		}
	}
	return false
}

func surveyTargets(tasks []Task) []Task {
	out := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		switch task.WorkerClass {
		case WorkerPlanner, WorkerResearcher:
			continue
		}
		out = append(out, task)
	}
	return out
}

func verificationCandidates(tasks []Task) []Task {
	out := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		switch task.WorkerClass {
		case WorkerPlanner, WorkerResearcher, WorkerSynthesizer:
			continue
		}
		if task.Verification == VerifyNone {
			continue
		}
		out = append(out, task)
	}
	return out
}

func nextVerificationTaskID(tasks []Task) string {
	maxN := 0
	for _, task := range tasks {
		id := strings.TrimSpace(task.ID)
		if len(id) < 3 || !strings.EqualFold(id[:2], "SV") {
			continue
		}
		n := 0
		ok := true
		for i := 2; i < len(id); i++ {
			if id[i] < '0' || id[i] > '9' {
				ok = false
				break
			}
			n = n*10 + int(id[i]-'0')
		}
		if ok && n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("SV%d", maxN+1)
}

func nextSurveyTaskID(tasks []Task) string {
	maxN := 0
	for _, task := range tasks {
		id := strings.TrimSpace(task.ID)
		if len(id) < 2 || (id[0] != 'S' && id[0] != 's') {
			continue
		}
		n := 0
		ok := true
		for i := 1; i < len(id); i++ {
			if id[i] < '0' || id[i] > '9' {
				ok = false
				break
			}
			n = n*10 + int(id[i]-'0')
		}
		if ok && n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("S%d", maxN+1)
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

func addUnique(dst *[]string, item string) {
	item = strings.TrimSpace(item)
	if item == "" {
		return
	}
	for _, existing := range *dst {
		if strings.EqualFold(existing, item) {
			return
		}
	}
	*dst = append(*dst, item)
}

func normalizeWorkerClass(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(WorkerPlanner):
		return string(WorkerPlanner)
	case string(WorkerResearcher):
		return string(WorkerResearcher)
	case string(WorkerReviewer):
		return string(WorkerReviewer)
	case string(WorkerTester):
		return string(WorkerTester)
	case string(WorkerSecurity):
		return string(WorkerSecurity)
	case string(WorkerSynthesizer):
		return string(WorkerSynthesizer)
	default:
		return string(WorkerCoder)
	}
}

func normalizeVerification(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(VerifyNone):
		return string(VerifyNone)
	case string(VerifyLight):
		return string(VerifyLight)
	case string(VerifyDeep):
		return string(VerifyDeep)
	default:
		return string(VerifyRequired)
	}
}

func clampConfidence(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func cleanStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func containsAny(in string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(in, term) {
			return true
		}
	}
	return false
}
