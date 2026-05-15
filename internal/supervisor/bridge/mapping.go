package bridge

// mapping.go — drive.Run/Todo → supervisor.Run/Task conversion used by
// cli_drive_render.go's `supervisor.BuildExecutionPlan` call so the
// CLI can render Drive runs through the same executor that powers the
// real engine. The reverse direction (supervisor → drive) and helper
// pieces lived here historically; they were removed when the
// orchestrator coordinator was deleted as dead code.

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

// TaskFromDriveTodo converts a drive.Todo into the supervisor.Task
// shape the executor consumes. Normalizes worker class + verification
// state strings so unknown values fall back to safe defaults.
func TaskFromDriveTodo(todo drive.Todo) supervisor.Task {
	return supervisor.Task{
		ID:            todo.ID,
		ParentID:      todo.ParentID,
		Origin:        todo.Origin,
		Title:         todo.Title,
		Detail:        todo.Detail,
		State:         supervisor.TaskState(todo.Status),
		DependsOn:     append([]string(nil), todo.DependsOn...),
		FileScope:     append([]string(nil), todo.FileScope...),
		ReadOnly:      todo.ReadOnly,
		ProviderTag:   todo.ProviderTag,
		WorkerClass:   supervisor.WorkerClass(normalizeWorkerClass(todo.WorkerClass)),
		Skills:        append([]string(nil), todo.Skills...),
		AllowedTools:  append([]string(nil), todo.AllowedTools...),
		Labels:        append([]string(nil), todo.Labels...),
		Verification:  supervisor.VerificationStatus(normalizeVerification(todo.Verification)),
		Confidence:    todo.Confidence,
		Summary:       todo.Brief,
		Error:         todo.Error,
		BlockedReason: string(todo.BlockedReason),
		Attempts:      todo.Attempts,
		StartedAt:     todo.StartedAt,
		EndedAt:       todo.EndedAt,
	}
}

// RunFromDrive lifts a drive.Run into the supervisor.Run shape so the
// executor can plan/dispatch its todos. Nil-safe.
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
	case string(supervisor.WorkerVerifier):
		return string(supervisor.WorkerVerifier)
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
