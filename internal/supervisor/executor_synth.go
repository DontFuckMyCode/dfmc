package supervisor

// executor_synth.go — auto-synthesis of survey + verification tasks
// for a supervisor run. Companion siblings:
//
//   - executor.go               BuildExecutionPlan + types + topo
//                               layering + lane-policy derivation
//   - executor_normalize.go     normalizeTasks + the small string /
//                               worker-class / verification / confidence
//                               normalizers
//
// AutoSurvey prepends an initial codebase-survey task that every
// non-discovery task fans out from. AutoVerify appends a final
// verification task that depends on every code/review/test target.
// Both helpers are no-ops when the planner already produced an
// equivalent task (hasSurveyTask / hasVerificationTask), so the
// caller can flip the flag on without worrying about duplicates.

import (
	"fmt"
	"sort"
	"strings"
)

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
