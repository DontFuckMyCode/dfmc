package drive

import (
	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

func applySupervisorPlan(run *Run, autoSurvey, autoVerify bool, maxParallel int) {
	if run == nil {
		return
	}
	existing := indexTodosByID(run.Todos)
	plan := supervisor.BuildExecutionPlan(
		runToSupervisor(run),
		supervisor.ExecutionOptions{
			AutoVerify:  autoVerify,
			AutoSurvey:  autoSurvey,
			MaxParallel: maxParallel,
		},
	)
	run.Todos = mergeSupervisorTodos(tasksToTodos(plan.Tasks), existing)
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

func mergeSupervisorTodos(planned []Todo, existing map[string]Todo) []Todo {
	if len(planned) == 0 {
		return nil
	}
	out := make([]Todo, 0, len(planned))
	for _, todo := range planned {
		if prev, ok := existing[todo.ID]; ok {
			if prev.Origin != "" {
				todo.Origin = prev.Origin
			}
			if prev.Kind != "" {
				todo.Kind = prev.Kind
			}
			todo.Status = prev.Status
			todo.Brief = prev.Brief
			todo.Error = prev.Error
			todo.Attempts = prev.Attempts
			todo.StartedAt = prev.StartedAt
			todo.EndedAt = prev.EndedAt
		}
		out = append(out, todo)
	}
	return out
}

func indexTodosByID(todos []Todo) map[string]Todo {
	if len(todos) == 0 {
		return nil
	}
	out := make(map[string]Todo, len(todos))
	for _, todo := range todos {
		if todo.ID == "" {
			continue
		}
		out[todo.ID] = todo
	}
	return out
}

func tasksToTodos(tasks []supervisor.PlannedTask) []Todo {
	out := make([]Todo, 0, len(tasks))
	for _, task := range tasks {
		todo := taskToTodo(task.Task)
		if task.IsAuto {
			todo.Origin = "supervisor"
		}
		if todo.Origin == "" {
			todo.Origin = "planner"
		}
		if todo.Kind == "" {
			if task.WorkerClass == supervisor.WorkerTester || task.WorkerClass == supervisor.WorkerSecurity {
				todo.Kind = "verify"
			} else {
				todo.Kind = "work"
			}
		}
		out = append(out, todo)
	}
	return out
}

func runToSupervisor(run *Run) supervisor.Run {
	if run == nil {
		return supervisor.Run{}
	}
	out := supervisor.Run{
		ID:        run.ID,
		Task:      run.Task,
		Status:    string(run.Status),
		Reason:    run.Reason,
		CreatedAt: run.CreatedAt,
		EndedAt:   run.EndedAt,
		Tasks:     make([]supervisor.Task, 0, len(run.Todos)),
	}
	for _, todo := range run.Todos {
		out.Tasks = append(out.Tasks, todoToTask(todo))
	}
	return out
}

func todoToTask(todo Todo) supervisor.Task {
	return supervisor.Task{
		ID:           todo.ID,
		ParentID:     todo.ParentID,
		Title:        todo.Title,
		Detail:       todo.Detail,
		State:        supervisor.TaskState(todo.Status),
		DependsOn:    append([]string(nil), todo.DependsOn...),
		FileScope:    append([]string(nil), todo.FileScope...),
		ReadOnly:     todo.ReadOnly,
		ProviderTag:  todo.ProviderTag,
		WorkerClass:  supervisor.WorkerClass(supervisorNormalizeWorkerClass(todo.WorkerClass)),
		Skills:       append([]string(nil), todo.Skills...),
		AllowedTools: append([]string(nil), todo.AllowedTools...),
		Labels:       append([]string(nil), todo.Labels...),
		Verification: supervisor.VerificationStatus(supervisorNormalizeVerification(todo.Verification)),
		Confidence:   supervisorClampConfidence(todo.Confidence),
		Summary:      todo.Brief,
		Error:        todo.Error,
		Attempts:     todo.Attempts,
		StartedAt:    todo.StartedAt,
		EndedAt:      todo.EndedAt,
	}
}

func taskToTodo(task supervisor.Task) Todo {
	return Todo{
		ID:           task.ID,
		ParentID:     task.ParentID,
		Title:        task.Title,
		Detail:       task.Detail,
		DependsOn:    append([]string(nil), task.DependsOn...),
		FileScope:    append([]string(nil), task.FileScope...),
		ReadOnly:     task.ReadOnly,
		ProviderTag:  task.ProviderTag,
		WorkerClass:  supervisorNormalizeWorkerClass(string(task.WorkerClass)),
		Skills:       append([]string(nil), task.Skills...),
		AllowedTools: append([]string(nil), task.AllowedTools...),
		Labels:       append([]string(nil), task.Labels...),
		Verification: supervisorNormalizeVerification(string(task.Verification)),
		Confidence:   supervisorClampConfidence(task.Confidence),
		Status:       TodoStatus(task.State),
		Brief:        task.Summary,
		Error:        task.Error,
		Attempts:     task.Attempts,
		StartedAt:    task.StartedAt,
		EndedAt:      task.EndedAt,
	}
}

func supervisorNormalizeWorkerClass(raw string) string {
	switch raw {
	case "planner", "researcher", "reviewer", "tester", "security", "synthesizer":
		return raw
	default:
		return "coder"
	}
}

func supervisorNormalizeVerification(raw string) string {
	switch raw {
	case "none", "light", "deep":
		return raw
	default:
		return "required"
	}
}

func supervisorClampConfidence(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func cloneLayers(in [][]string) [][]string {
	if len(in) == 0 {
		return nil
	}
	out := make([][]string, len(in))
	for i, layer := range in {
		out[i] = append([]string(nil), layer...)
	}
	return out
}

func cloneWorkerCounts(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
