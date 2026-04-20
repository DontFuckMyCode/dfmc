package bridge

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

func TaskFromDriveTodo(todo drive.Todo) supervisor.Task {
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
		WorkerClass:  supervisor.WorkerClass(normalizeWorkerClass(todo.WorkerClass)),
		Skills:       append([]string(nil), todo.Skills...),
		AllowedTools: append([]string(nil), todo.AllowedTools...),
		Labels:       append([]string(nil), todo.Labels...),
		Verification: supervisor.VerificationStatus(normalizeVerification(todo.Verification)),
		Confidence:   todo.Confidence,
		Summary:      todo.Brief,
		Error:        todo.Error,
		BlockedReason: string(todo.BlockedReason),
		Attempts:     todo.Attempts,
		StartedAt:    todo.StartedAt,
		EndedAt:      todo.EndedAt,
	}
}

func TaskToDriveTodo(task supervisor.Task) drive.Todo {
	return drive.Todo{
		ID:           task.ID,
		ParentID:     task.ParentID,
		Title:        task.Title,
		Detail:       task.Detail,
		DependsOn:    append([]string(nil), task.DependsOn...),
		FileScope:    append([]string(nil), task.FileScope...),
		ReadOnly:     task.ReadOnly,
		ProviderTag:  strings.TrimSpace(task.ProviderTag),
		WorkerClass:  normalizeWorkerClass(string(task.WorkerClass)),
		Skills:       append([]string(nil), task.Skills...),
		AllowedTools: append([]string(nil), task.AllowedTools...),
		Labels:       append([]string(nil), task.Labels...),
		Verification: normalizeVerification(string(task.Verification)),
		Confidence:   clampConfidence(task.Confidence),
		Status:       drive.TodoStatus(task.State),
		Brief:        task.Summary,
		Error:        task.Error,
		BlockedReason: drive.BlockReason(strings.TrimSpace(task.BlockedReason)),
		Attempts:     task.Attempts,
		StartedAt:    task.StartedAt,
		EndedAt:      task.EndedAt,
	}
}

func RunFromDrive(run *drive.Run) supervisor.Run {
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
		out.Tasks = append(out.Tasks, TaskFromDriveTodo(todo))
	}
	return out
}

func RunToDrive(run supervisor.Run) *drive.Run {
	out := &drive.Run{
		ID:        run.ID,
		Task:      run.Task,
		Status:    drive.RunStatus(run.Status),
		Reason:    run.Reason,
		CreatedAt: run.CreatedAt,
		EndedAt:   run.EndedAt,
		Todos:     make([]drive.Todo, 0, len(run.Tasks)),
	}
	for _, task := range run.Tasks {
		out.Todos = append(out.Todos, TaskToDriveTodo(task))
	}
	return out
}

func normalizeWorkerClass(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(supervisor.WorkerPlanner):
		return string(supervisor.WorkerPlanner)
	case string(supervisor.WorkerResearcher):
		return string(supervisor.WorkerResearcher)
	case string(supervisor.WorkerReviewer):
		return string(supervisor.WorkerReviewer)
	case string(supervisor.WorkerTester):
		return string(supervisor.WorkerTester)
	case string(supervisor.WorkerSecurity):
		return string(supervisor.WorkerSecurity)
	case string(supervisor.WorkerSynthesizer):
		return string(supervisor.WorkerSynthesizer)
	default:
		return string(supervisor.WorkerCoder)
	}
}

func normalizeVerification(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(supervisor.VerifyNone):
		return string(supervisor.VerifyNone)
	case string(supervisor.VerifyLight):
		return string(supervisor.VerifyLight)
	case string(supervisor.VerifyDeep):
		return string(supervisor.VerifyDeep)
	default:
		return string(supervisor.VerifyRequired)
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
