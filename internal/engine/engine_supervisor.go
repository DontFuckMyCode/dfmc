package engine

import (
	"context"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

// NewSupervisor creates a supervisor.Coordinator bound to this engine.
// The caller configures the run and plan externally, then passes them here.
// The supervisor's worker function is wired to the drive executor so tasks
// go through the same sub-agent surface as regular drive TODOs.
//
// The returned supervisor is not running — call s.Run(ctx) to start it.
// The engine holds the supervisor reference in e.activeSupervisor for the
// duration of the run so sub-agent budget accounting uses the shared pool.
func (e *Engine) NewSupervisor(run *supervisor.Run, plan *supervisor.ExecutionPlan, budget supervisor.BudgetPool) *supervisor.Supervisor {
	sup := supervisor.NewSupervisor(run, plan, &budget)
	sup.SetWorkerFunc(func(ctx context.Context, req supervisor.ExecuteTaskRequest) (supervisor.ExecuteTaskResponse, error) {
		driveReq := drive.ExecuteTodoRequest{
			TodoID:            req.TaskID,
			ProviderTag:       req.ProviderTag,
			Title:             req.Title,
			Detail:            req.Detail,
			Brief:             req.Brief,
			Role:              req.Role,
			Skills:            req.Skills,
			Labels:            req.Labels,
			Verification:      req.Verification,
			Model:             req.Model,
			ProfileCandidates: req.ProfileCandidates,
			AllowedTools:      req.AllowedTools,
			MaxSteps:          req.MaxSteps,
		}
		// Use the drive runner from the engine's drive adapter
		dr := e.newDriveRunner()
		result, err := dr.ExecuteTodo(ctx, driveReq)
		if err != nil {
			return supervisor.ExecuteTaskResponse{}, err
		}
		return supervisor.ExecuteTaskResponse{
			Summary:         result.Summary,
			ToolCalls:       result.ToolCalls,
			DurationMs:      result.DurationMs,
			Provider:        result.Provider,
			Model:           result.Model,
			Attempts:        result.Attempts,
			TokensUsed:      int(result.DurationMs / 100), // rough estimate; real accounting needs instrumentation
			FallbackUsed:   result.FallbackUsed,
			FallbackReasons: result.FallbackReasons,
		}, nil
	})
	return sup
}

// newDriveRunner returns a drive.Runner backed by this engine.
// This is the same adapter used by drive.Driver.
func (e *Engine) newDriveRunner() drive.Runner {
	return &engineDriveRunner{e: e}
}

type engineDriveRunner struct {
	e *Engine
}

func (r *engineDriveRunner) PlannerCall(ctx context.Context, req drive.PlannerRequest) (drive.PlannerResponse, error) {
	return r.e.NewDriveRunner().PlannerCall(ctx, req)
}

func (r *engineDriveRunner) ExecuteTodo(ctx context.Context, req drive.ExecuteTodoRequest) (drive.ExecuteTodoResponse, error) {
	return r.e.NewDriveRunner().ExecuteTodo(ctx, req)
}

func (r *engineDriveRunner) BeginAutoApprove(tools []string) func() {
	return r.e.NewDriveRunner().BeginAutoApprove(tools)
}

// SetSupervisor registers the active supervisor for budget accounting.
// Sub-agent budget halving uses the supervisor pool when non-nil.
// Called by the supervisor start path; cleared when the supervisor finishes.
func (e *Engine) SetSupervisor(supervisor interface {
	AllocTokens(int) int
	RestoreTokens(int)
}) {
	e.activeSupervisor = supervisor
}

// ClearSupervisor removes the active supervisor reference after a run ends.
func (e *Engine) ClearSupervisor() {
	e.activeSupervisor = nil
}

// SupervisorStatus returns the current supervisor state, or a zero-valued
// struct if no supervisor is active.
func (e *Engine) SupervisorStatus() supervisor.SupervisorStatus {
	// If the engine's active supervisor implements the Status() method,
	// call it. Otherwise return zero status.
	type statuser interface {
		Status() supervisor.SupervisorStatus
	}
	if s, ok := e.activeSupervisor.(statuser); ok {
		return s.Status()
	}
	return supervisor.SupervisorStatus{}
}

// ActiveSupervisorBudget returns the remaining token budget if a supervisor
// is active and has a token cap, or -1 for unlimited.
func (e *Engine) ActiveSupervisorBudget() int {
	type budgeter interface {
		Remaining() int
	}
	if e.activeSupervisor == nil {
		return -1
	}
	if b, ok := e.activeSupervisor.(budgeter); ok {
		return b.Remaining()
	}
	return -1
}

// IsSupervising returns true when a supervisor run is currently active.
func (e *Engine) IsSupervising() bool {
	return e.activeSupervisor != nil
}

// BuildSupervisorPlan converts a drive.Run to supervisor types and builds
// the execution plan. Returns a supervisor.Run + plan pair ready to pass
// to NewSupervisor.
func BuildSupervisorPlan(run *drive.Run, autoSurvey, autoVerify bool, maxParallel int) (*supervisor.Run, *supervisor.ExecutionPlan) {
	supTasks := make([]supervisor.Task, len(run.Todos))
	for i, todo := range run.Todos {
		supTasks[i] = driveTodoToSupervisorTask(todo)
	}
	supRun := supervisor.Run{
		ID:        run.ID,
		Task:      run.Task,
		Status:    string(run.Status),
		Reason:    run.Reason,
		CreatedAt: run.CreatedAt,
		EndedAt:   run.EndedAt,
		Tasks:     supTasks,
	}
	plan := supervisor.BuildExecutionPlan(
		supRun,
		supervisor.ExecutionOptions{
			AutoSurvey:  autoSurvey,
			AutoVerify:  autoVerify,
			MaxParallel: maxParallel,
		},
	)
	return &supRun, &plan
}

// driveTodoToSupervisorTask converts a drive.Todo to a supervisor.Task.
// Keep in sync with drive/supervision.go:todoToTask.
func driveTodoToSupervisorTask(todo drive.Todo) supervisor.Task {
	supTask := supervisor.Task{
		ID:           todo.ID,
		ParentID:     todo.ParentID,
		Title:        todo.Title,
		Detail:       todo.Detail,
		State:        supervisor.TaskState(todo.Status),
		DependsOn:    append([]string(nil), todo.DependsOn...),
		FileScope:    append([]string(nil), todo.FileScope...),
		ReadOnly:     todo.ReadOnly,
		ProviderTag:  todo.ProviderTag,
		WorkerClass:  supervisor.WorkerClass(driveNormalizeWorkerClass(todo.WorkerClass)),
		Skills:       append([]string(nil), todo.Skills...),
		AllowedTools: append([]string(nil), todo.AllowedTools...),
		Labels:       append([]string(nil), todo.Labels...),
		Verification: supervisor.VerificationStatus(driveNormalizeVerification(todo.Verification)),
		Confidence:   driveClampConfidence(todo.Confidence),
		Summary:      todo.Brief,
		Error:        todo.Error,
		Attempts:     todo.Attempts,
		StartedAt:    todo.StartedAt,
		EndedAt:      todo.EndedAt,
		LastContext:  todo.LastContext,
	}
	return supTask
}

func driveNormalizeWorkerClass(raw string) string {
	switch raw {
	case "planner", "researcher", "coder", "reviewer", "tester", "security", "synthesizer", "verifier":
		return raw
	default:
		return "coder"
	}
}

func driveNormalizeVerification(raw string) string {
	switch raw {
	case "none", "light", "deep":
		return raw
	default:
		return "required"
	}
}

func driveClampConfidence(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
