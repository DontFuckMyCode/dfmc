// run_planner.go — plan-stage transition for a Drive run.
//
// runPlannerPhase encapsulates: planner LLM invocation, MaxTodos cap
// truncation (with EventRunWarning so the user sees what got dropped),
// supervisor-plan expansion (auto-survey / auto-verify), and the
// RunPlanning -> RunRunning status flip. Persistence + event publishing
// for both success and failure paths live here so the caller doesn't
// have to mirror the same branches.

package drive

import (
	"context"
	"fmt"
	"time"
)

// runPlannerPhase invokes the planner and prepares run.Todos for the
// executor. On planner error the run is stamped RunFailed and the
// matching EventPlanFailed + EventRunFailed events are published — the
// caller should treat a non-nil return as terminal and skip the
// executor stage.
func (d *Driver) runPlannerPhase(ctx context.Context, run *Run) error {
	// Preset-plan path: when the caller already filled run.Todos before
	// calling RunPrepared (e.g. spec_to_todo → TodosFromSpec → preset
	// run), there is nothing for the planner LLM to decide. Publish the
	// planner-stage events with a "preset" source so subscribers can
	// distinguish a literal-execution run from a planner-driven one,
	// then jump straight to the supervisor expansion + RunRunning flip.
	if len(run.Todos) > 0 {
		d.publish(EventPlanStart, map[string]any{
			"run_id": run.ID,
			"model":  d.cfg.PlannerModel,
			"source": "preset",
		})
		beforePlanCount := len(run.Todos)
		applySupervisorPlan(run, d.cfg.AutoSurvey, d.cfg.AutoVerify, d.cfg.MaxParallel)
		if len(run.Todos) > beforePlanCount {
			added := append([]Todo(nil), run.Todos[beforePlanCount:]...)
			d.publish(EventPlanAugment, map[string]any{
				"run_id": run.ID,
				"added":  len(added),
				"todos":  planSummary(added),
			})
		}
		run.Status = RunRunning
		d.persist(run)
		d.publish(EventPlanDone, map[string]any{
			"run_id":     run.ID,
			"todo_count": len(run.Todos),
			"todos":      planSummary(run.Todos),
			"plan":       run.Plan,
			"source":     "preset",
		})
		return nil
	}
	d.publish(EventPlanStart, map[string]any{"run_id": run.ID, "model": d.cfg.PlannerModel})
	if !d.plannerBreaker.Check() {
		err := ErrCircuitOpen
		run.Status = RunFailed
		run.Reason = "planner circuit breaker is open"
		run.EndedAt = time.Now()
		d.persist(run)
		d.publish(EventPlanFailed, map[string]any{"run_id": run.ID, "error": err.Error()})
		d.publish(EventRunFailed, map[string]any{"run_id": run.ID, "reason": run.Reason})
		return err
	}
	extraCtx := ""
	if d.cfg.PlannerContextProvider != nil {
		extraCtx = d.cfg.PlannerContextProvider.Context(run.Task)
	}
	todos, err := runPlanner(ctx, d.runner, run.Task, d.cfg.PlannerModel, extraCtx, d.cfg.PlannerFallbackModels)
	if err != nil {
		d.plannerBreaker.Record(false)
		run.Status = RunFailed
		run.Reason = fmt.Sprintf("plan failed: %v", err)
		run.EndedAt = time.Now()
		d.persist(run)
		d.publish(EventPlanFailed, map[string]any{"run_id": run.ID, "error": err.Error()})
		d.publish(EventRunFailed, map[string]any{"run_id": run.ID, "reason": run.Reason})
		return fmt.Errorf("plan failed: %w", err)
	}
	d.plannerBreaker.Record(true)
	if len(todos) > d.cfg.MaxTodos {
		// Truncate noisily — surface the cap so the user sees why later
		// TODOs are missing, instead of silently dropping work. Publishes
		// EventRunWarning (not EventPlanFailed): the plan succeeded, we
		// just dropped tail TODOs over the cap; listeners that gate on
		// plan:failed as a real failure signal shouldn't trip on this.
		truncated := todos[d.cfg.MaxTodos:]
		todos = todos[:d.cfg.MaxTodos]
		d.publish(EventRunWarning, map[string]any{
			"run_id":           run.ID,
			"warning":          "MaxTodos exceeded; truncated",
			"kept":             d.cfg.MaxTodos,
			"dropped":          len(truncated),
			"dropped_ids_head": collectIDsHead(truncated, 5),
		})
	}
	run.Todos = todos
	beforePlanCount := len(run.Todos)
	applySupervisorPlan(run, d.cfg.AutoSurvey, d.cfg.AutoVerify, d.cfg.MaxParallel)
	if len(run.Todos) > beforePlanCount {
		added := append([]Todo(nil), run.Todos[beforePlanCount:]...)
		d.publish(EventPlanAugment, map[string]any{
			"run_id": run.ID,
			"added":  len(added),
			"todos":  planSummary(added),
		})
	}
	run.Status = RunRunning
	d.persist(run)
	d.publish(EventPlanDone, map[string]any{
		"run_id":     run.ID,
		"todo_count": len(run.Todos),
		"todos":      planSummary(run.Todos),
		"plan":       run.Plan,
	})
	return nil
}
