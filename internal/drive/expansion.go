package drive

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

type expansionOutput struct {
	SpawnTodos []Todo `json:"spawn_todos"`
}

const maxSpawnedTodosPerParent = 10

func parseSpawnedTodos(raw string, parent Todo, existing []Todo) (string, []Todo, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil, nil
	}
	candidates := extractJSONObjectCandidates(raw)
	if len(candidates) == 0 {
		return raw, nil, nil
	}
	var lastErr error
	for _, candidate := range candidates {
		var out expansionOutput
		if err := json.Unmarshal([]byte(candidate), &out); err != nil {
			continue
		}
		if len(out.SpawnTodos) == 0 {
			continue
		}
		todos, err := normalizeSpawnedTodos(out.SpawnTodos, parent, existing)
		if err != nil {
			lastErr = err
			continue
		}
		return cleanupSpawnPayload(raw, candidate), todos, nil
	}
	if lastErr != nil {
		return raw, nil, lastErr
	}
	return raw, nil, nil
}

func normalizeSpawnedTodos(proposals []Todo, parent Todo, existing []Todo) ([]Todo, error) {
	if len(proposals) == 0 {
		return nil, nil
	}
	if len(proposals) > maxSpawnedTodosPerParent {
		proposals = append([]Todo(nil), proposals[:maxSpawnedTodosPerParent]...)
	}
	existingIDs := map[string]struct{}{}
	for _, todo := range existing {
		existingIDs[todo.ID] = struct{}{}
	}
	nextN := nextTodoNumber(existing)
	localToGlobal := map[string]string{}
	for i, proposal := range proposals {
		local := strings.TrimSpace(proposal.ID)
		if local == "" {
			local = fmt.Sprintf("local-%d", i+1)
		}
		if _, ok := localToGlobal[local]; ok {
			local = fmt.Sprintf("%s-%d", local, i+1)
		}
		nextN++
		localToGlobal[local] = fmt.Sprintf("T%d", nextN)
	}

	out := make([]Todo, 0, len(proposals))
	for i, proposal := range proposals {
		local := strings.TrimSpace(proposal.ID)
		if local == "" {
			local = fmt.Sprintf("local-%d", i+1)
		}
		id := localToGlobal[local]
		todo := Todo{
			ID:           id,
			ParentID:     parent.ID,
			Origin:       "worker",
			Title:        strings.TrimSpace(proposal.Title),
			Detail:       strings.TrimSpace(proposal.Detail),
			DependsOn:    remapSpawnDependsOn(proposal.DependsOn, parent.ID, existingIDs, localToGlobal, id),
			FileScope:    cleanList(proposal.FileScope),
			ProviderTag:  strings.TrimSpace(proposal.ProviderTag),
			WorkerClass:  normalizeWorkerClass(proposal.WorkerClass, proposal.ProviderTag),
			Skills:       cleanList(proposal.Skills),
			AllowedTools: cleanList(proposal.AllowedTools),
			Labels:       cleanList(append(proposal.Labels, "spawned")),
			Verification: normalizeVerification(proposal.Verification, proposal.ProviderTag, proposal.WorkerClass),
			Confidence:   clampConfidence(proposal.Confidence),
			Status:       TodoPending,
		}
		if todo.ProviderTag == "" {
			todo.ProviderTag = "code"
		}
		todo.Kind = normalizeTodoKind(proposal.Kind, todo.Verification, todo.ProviderTag, todo.WorkerClass)
		todo.ReadOnly = normalizeTodoReadOnly(proposal.ReadOnly, todo.WorkerClass, todo.Kind, todo.AllowedTools)
		if todo.Title == "" {
			return nil, fmt.Errorf("spawn_todos[%d].title is empty", i)
		}
		if todo.Detail == "" {
			return nil, fmt.Errorf("spawn_todos[%d].detail is empty", i)
		}
		out = append(out, todo)
	}

	combined := make([]Todo, 0, len(existing)+len(out))
	combined = append(combined, existing...)
	combined = append(combined, out...)
	if err := validateTodos(combined); err != nil {
		return nil, err
	}
	return out, nil
}

func remapSpawnDependsOn(raw []string, parentID string, existingIDs map[string]struct{}, localToGlobal map[string]string, selfID string) []string {
	deps := make([]string, 0, len(raw)+1)
	seen := map[string]struct{}{}
	add := func(dep string) {
		dep = strings.TrimSpace(dep)
		if dep == "" || dep == selfID {
			return
		}
		key := strings.ToLower(dep)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		deps = append(deps, dep)
	}
	for _, dep := range raw {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		if mapped, ok := localToGlobal[dep]; ok {
			add(mapped)
			continue
		}
		if _, ok := existingIDs[dep]; ok {
			add(dep)
		}
	}
	add(parentID)
	sort.Strings(deps)
	return deps
}

func cleanupSpawnPayload(raw, payload string) string {
	cleaned := strings.Replace(raw, payload, "", 1)
	cleaned = strings.ReplaceAll(cleaned, "```json", "")
	cleaned = strings.ReplaceAll(cleaned, "```", "")
	return strings.TrimSpace(cleaned)
}

func nextTodoNumber(todos []Todo) int {
	maxN := 0
	for _, todo := range todos {
		id := strings.TrimSpace(todo.ID)
		if len(id) < 2 || (id[0] != 'T' && id[0] != 't') {
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
	return maxN
}

func applySpawnedTodos(run *Run, parent Todo, spawned []Todo, maxTodos, maxParallel int) []Todo {
	if run == nil || len(spawned) == 0 {
		return nil
	}
	remaining := maxTodos - len(run.Todos)
	if maxTodos > 0 && remaining <= 0 {
		return nil
	}
	if maxTodos > 0 && len(spawned) > remaining {
		spawned = append([]Todo(nil), spawned[:remaining]...)
	}
	addedIDs := make([]string, 0, len(spawned))
	for _, todo := range spawned {
		addedIDs = append(addedIDs, todo.ID)
	}
	insertAt := len(run.Todos)
	if verificationIdx, ok := pendingVerificationIndex(run); ok {
		insertAt = verificationIdx
	}
	run.Todos = insertTodosAt(run.Todos, insertAt, spawned)
	if verificationIdx, ok := todoIndexByID(run.Todos, verificationID(run)); ok && run.Todos[verificationIdx].Status == TodoPending {
		for _, childID := range addedIDs {
			if spawnedTodoNeedsVerification(todoByID(run.Todos, childID)) {
				run.Todos[verificationIdx].DependsOn = append(run.Todos[verificationIdx].DependsOn, childID)
			}
		}
		run.Todos[verificationIdx].DependsOn = cleanList(run.Todos[verificationIdx].DependsOn)
		sort.Strings(run.Todos[verificationIdx].DependsOn)
	}
	refreshSupervisorSnapshot(run, maxParallel)
	return spawned
}

func refreshSupervisorSnapshot(run *Run, maxParallel int) {
	if run == nil {
		return
	}
	plan := supervisor.BuildExecutionPlan(runToSupervisor(run), supervisor.ExecutionOptions{MaxParallel: maxParallel})
	run.Plan = &ExecutionPlanSnapshot{
		Layers:         cloneLayers(plan.Layers),
		Roots:          append([]string(nil), plan.Roots...),
		Leaves:         append([]string(nil), plan.Leaves...),
		WorkerCounts:   cloneWorkerCounts(plan.WorkerCounts),
		LaneCaps:       cloneWorkerCounts(plan.LaneCaps),
		LaneOrder:      append([]string(nil), plan.LaneOrder...),
		SurveyID:       plan.SurveyID,
		VerificationID: plan.VerificationID,
		MaxParallel:    plan.MaxParallel,
	}
}

func pendingVerificationIndex(run *Run) (int, bool) {
	if run == nil {
		return -1, false
	}
	id := verificationID(run)
	if id == "" {
		return -1, false
	}
	idx, ok := todoIndexByID(run.Todos, id)
	if !ok || run.Todos[idx].Status != TodoPending {
		return -1, false
	}
	return idx, true
}

func verificationID(run *Run) string {
	if run == nil || run.Plan == nil {
		return ""
	}
	return strings.TrimSpace(run.Plan.VerificationID)
}

func spawnedTodoNeedsVerification(todo Todo) bool {
	if strings.EqualFold(strings.TrimSpace(todo.Verification), "none") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(todo.WorkerClass)) {
	case "planner", "researcher", "synthesizer":
		return false
	default:
		return true
	}
}

func insertTodosAt(base []Todo, idx int, extra []Todo) []Todo {
	if idx < 0 || idx > len(base) {
		idx = len(base)
	}
	out := make([]Todo, 0, len(base)+len(extra))
	out = append(out, base[:idx]...)
	out = append(out, extra...)
	out = append(out, base[idx:]...)
	return out
}

func todoIndexByID(todos []Todo, id string) (int, bool) {
	for i, todo := range todos {
		if todo.ID == id {
			return i, true
		}
	}
	return -1, false
}

func todoByID(todos []Todo, id string) Todo {
	for _, todo := range todos {
		if todo.ID == id {
			return todo
		}
	}
	return Todo{}
}
