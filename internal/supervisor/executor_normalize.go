package supervisor

// executor_normalize.go — input cleanup helpers used by the
// execution-plan builder and the auto-synthesis path. Companion
// siblings:
//
//   - executor.go        BuildExecutionPlan + types + topo layering +
//                        lane-policy derivation
//   - executor_synth.go  AutoSurvey + AutoVerify task synthesis
//
// normalizeTasks is the canonical "trim, dedupe, default" pass for an
// inbound []Task. The smaller helpers (normalizeWorkerClass /
// normalizeVerification / clampConfidence / cleanStrings / addUnique /
// containsAny) live here too because nothing outside this package
// uses them and keeping them next to normalizeTasks makes the
// "what counts as a clean task" contract obvious.

import "strings"

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
