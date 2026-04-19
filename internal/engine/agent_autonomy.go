package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/planning"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type autonomyPreflight struct {
	Plan       planning.Plan
	Directive  string
	TodoSeeded bool
	Scope      string
	Mode       string
}

const (
	autoAutonomyKickoffConfidence         = 0.40
	autoAutonomySequentialKickoffMinSteps = 3
	autoAutonomyPreflightConfidence       = 0.55
	aggressiveAutonomyPreflightConfidence = 0.40
)

func (e *Engine) autonomousPlanningMode() string {
	if e == nil || e.Config == nil {
		return "auto"
	}
	switch strings.ToLower(strings.TrimSpace(e.Config.Agent.AutonomousPlanning)) {
	case "off", "false", "no", "0", "manual":
		return "off"
	case "aggressive", "aggr", "strict", "force":
		return "aggressive"
	default:
		return "auto"
	}
}

func (e *Engine) autonomousPlanningEnabled() bool {
	return e.autonomousPlanningMode() != "off"
}

func (e *Engine) prepareAutonomyPreflight(ctx context.Context, question string, scope string, seedTodos bool) *autonomyPreflight {
	if e == nil || !e.autonomousPlanningEnabled() {
		return nil
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return nil
	}
	mode := e.autonomousPlanningMode()
	plan := planning.SplitTask(question)
	threshold := autoAutonomyPreflightConfidence
	if mode == "aggressive" {
		threshold = aggressiveAutonomyPreflightConfidence
	}
	if len(plan.Subtasks) < autoAutonomySequentialKickoffMinSteps || plan.Confidence < threshold {
		return nil
	}

	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "top_level"
	}
	out := &autonomyPreflight{Plan: plan, Scope: scope, Mode: mode}
	if seedTodos {
		out.TodoSeeded = e.seedAutonomyTodos(ctx, plan)
	}
	out.Directive = renderAutonomyDirective(plan, out.TodoSeeded, mode)
	e.publishAgentLoopEvent("agent:autonomy:plan", map[string]any{
		"task":          truncateSingleLineLocal(question, 200),
		"scope":         scope,
		"mode":          mode,
		"subtask_count": len(plan.Subtasks),
		"parallel":      plan.Parallel,
		"confidence":    plan.Confidence,
		"todo_seeded":   out.TodoSeeded,
		"subtasks":      autonomySubtaskTitles(plan),
	})
	return out
}

func (e *Engine) seedAutonomyTodos(ctx context.Context, plan planning.Plan) bool {
	if e == nil || e.Tools == nil || len(plan.Subtasks) == 0 {
		return false
	}
	todos := make([]any, 0, len(plan.Subtasks))
	for i, sub := range plan.Subtasks {
		status := "pending"
		if i == 0 {
			status = "in_progress"
		}
		content := strings.TrimSpace(sub.Description)
		if content == "" {
			content = strings.TrimSpace(sub.Title)
		}
		item := map[string]any{
			"content": content,
			"status":  status,
		}
		if title := strings.TrimSpace(sub.Title); title != "" {
			item["active_form"] = title
		}
		todos = append(todos, item)
	}
	_, err := e.Tools.Execute(ctx, "todo_write", toolRequest(e.ProjectRoot, map[string]any{
		"action": "set",
		"todos":  todos,
	}))
	return err == nil
}

func renderAutonomyDirective(plan planning.Plan, todoSeeded bool, planningMode string) string {
	mode := "sequential"
	if plan.Parallel {
		mode = "parallel"
	}
	lines := []string{
		fmt.Sprintf("Deterministic preflight split this request into %d subtasks (mode=%s, confidence=%.2f).", len(plan.Subtasks), mode, plan.Confidence),
		"Stay autonomous: keep reading, editing, verifying, and researching until the task is actually complete or you are truly blocked.",
	}
	if strings.EqualFold(strings.TrimSpace(planningMode), "aggressive") {
		lines = append(lines, "This session is in aggressive autonomy mode: act on the plan immediately, do not stop after one read, and do not answer early while clear subtasks remain.")
	}
	if todoSeeded {
		lines = append(lines, "The session todo list has already been pre-seeded from this plan. Keep it current instead of recreating it from scratch.")
	} else {
		lines = append(lines, "If the work expands further, sync todo_write early so progress stays visible.")
	}
	if plan.Parallel {
		lines = append(lines,
			"Because the subtasks are parallelizable, prefer orchestrate for one-shot fan-out. If you need tighter control, use delegate_task or tool_batch_call(delegate_task) for read-heavy surveys and keep the main loop focused on integration, edits, and verification.",
		)
	} else {
		lines = append(lines,
			"Treat these as ordered stages. Prefer orchestrate with force_sequential=true, or delegate only the read-heavy survey stages while keeping the main loop responsible for integration and final edits.",
		)
	}
	if strings.EqualFold(strings.TrimSpace(planningMode), "aggressive") {
		lines = append(lines, "Start executing now: if the split is parallelizable, orchestrate on the first tool round; otherwise begin stage 1 immediately and keep todo state moving as evidence comes in.")
	}
	lines = append(lines, "Preflight subtasks:")
	for i, sub := range plan.Subtasks {
		title := strings.TrimSpace(sub.Title)
		if title == "" {
			title = strings.TrimSpace(sub.Description)
		}
		lines = append(lines, fmt.Sprintf("%d. [%s] %s", i+1, strings.TrimSpace(sub.Hint), truncateSingleLineLocal(title, 120)))
	}
	return strings.Join(lines, "\n")
}

func autonomySubtaskTitles(plan planning.Plan) []string {
	out := make([]string, 0, len(plan.Subtasks))
	for _, sub := range plan.Subtasks {
		title := strings.TrimSpace(sub.Title)
		if title == "" {
			title = strings.TrimSpace(sub.Description)
		}
		if title == "" {
			continue
		}
		out = append(out, title)
	}
	return out
}

func buildAutonomySystemSection(preflight *autonomyPreflight) *provider.SystemBlock {
	if preflight == nil {
		return nil
	}
	text := strings.TrimSpace(preflight.Directive)
	if text == "" {
		return nil
	}
	return &provider.SystemBlock{
		Label:     "autonomy",
		Text:      "[DFMC autonomy preflight]\n" + text,
		Cacheable: false,
	}
}

func shouldAutoKickoffAutonomy(preflight *autonomyPreflight) bool {
	if preflight == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(preflight.Mode), "aggressive") {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(preflight.Scope), "top_level") {
		return false
	}
	if len(preflight.Plan.Subtasks) < autoAutonomySequentialKickoffMinSteps {
		return false
	}
	return preflight.Plan.Confidence >= autoAutonomyKickoffConfidence
}

func (e *Engine) maybeAutoKickoffAutonomy(
	ctx context.Context,
	question string,
	preflight *autonomyPreflight,
	lim agentLimits,
) ([]provider.Message, []nativeToolTrace) {
	if e == nil || e.Tools == nil || !shouldAutoKickoffAutonomy(preflight) {
		return nil, nil
	}
	params := map[string]any{
		"task": question,
	}
	parallelKickoff := preflight.Plan.Parallel
	if parallelKickoff {
		if parallel := e.parallelBatchSize(); parallel > 0 {
			params["max_parallel"] = parallel
		}
	} else {
		params["force_sequential"] = true
	}
	call := provider.ToolCall{
		ID:   "auto_orchestrate_kickoff",
		Name: "tool_call",
		Input: map[string]any{
			"name": "orchestrate",
			"args": params,
		},
	}
	trace := nativeToolTrace{
		Call:       call,
		Provider:   e.provider(),
		Model:      e.model(),
		Step:       0,
		OccurredAt: time.Now(),
	}
	e.publishAgentLoopEvent("agent:autonomy:kickoff", map[string]any{
		"tool":          "orchestrate",
		"scope":         preflight.Scope,
		"mode":          preflight.Mode,
		"parallel":      parallelKickoff,
		"confidence":    preflight.Plan.Confidence,
		"subtask_count": len(preflight.Plan.Subtasks),
	})
	e.publishNativeToolCall(trace)
	res, err := e.Tools.Execute(ctx, "orchestrate", toolRequest(e.ProjectRoot, params))
	if err != nil {
		trace.Err = err.Error()
	} else {
		trace.Result = res
	}
	content, isErr := formatNativeToolResultPayloadWithLimits(trace.Result, err, lim.MaxResultChars, lim.MaxDataChars)
	e.publishNativeToolResultWithPayload(trace, content)
	return []provider.Message{
			{
				Role:      types.RoleAssistant,
				ToolCalls: []provider.ToolCall{call},
			},
			{
				Role:       types.RoleUser,
				Content:    content,
				ToolCallID: call.ID,
				ToolName:   call.Name,
				ToolError:  isErr,
			},
		},
		[]nativeToolTrace{trace}
}

func toolRequest(projectRoot string, params map[string]any) tools.Request {
	return tools.Request{
		ProjectRoot: projectRoot,
		Params:      params,
	}
}

func truncateSingleLineLocal(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " "))
	if n <= 0 || len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "..."
}
