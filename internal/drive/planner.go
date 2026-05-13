// Planner: turns a free-form task into a JSON DAG of TODOs.
//
// The planner LLM call is intentionally minimal - no tool loop, no
// conversation history, no codebase context auto-injection. The model
// only sees the planner system prompt + the task, and must return
// strictly-shaped JSON. That keeps planner runs cheap (a single
// completion against any provider) and predictable (no tool failures,
// no parking, no sub-agents).
//
// Why not just use todo_write? todo_write is a runtime helper used
// during execution to track in-progress items; it has no notion of
// dependencies or file scope, so it can't shape the scheduler. The
// drive planner produces the dependency graph the scheduler needs.

package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// plannerSystemPrompt is the canonical instruction sent to the planner
// model. Kept as a const (not config) because changing this is a real
// behavior change - operators who want a different planner prompt
// should fork the package, not patch a yaml file. The shape contract
// is documented inline so a model with no prior exposure can comply.
const plannerSystemPrompt = `You are the planner for an autonomous coding agent. The user gives you a coding task. You break it into an ordered DAG of small TODOs that another LLM (the executor) will work through one by one.

Output STRICTLY a JSON object matching this shape - nothing else (no prose, no markdown fences):

{
  "todos": [
    {
      "id": "T1",
      "title": "Short imperative title (under 80 chars)",
      "detail": "Concrete instructions for the executor - what to read, what to change, what to verify. 1-3 sentences.",
      "depends_on": [],
      "file_scope": ["relative/path/from/repo/root.go"],
      "read_only": false,
      "provider_tag": "code",
      "worker_class": "coder",
      "skills": ["debug"],
      "allowed_tools": ["read_file", "grep_codebase", "run_command"],
      "labels": ["api", "critical-path"],
      "verification": "required",
      "confidence": 0.84
    }
  ]
}

Rules:
- ids are short, unique, prefixed "T" (T1, T2, ...). depends_on references earlier ids only.
- title is what the user will see in the progress chip. Keep it under 80 chars.
- detail is the prompt the executor will run. Be concrete: name files, name functions, state success criteria.
- file_scope lists the files the TODO will read or write (best effort - used by the scheduler to avoid parallel conflicts). Empty is allowed.
- read_only is optional. Set it true for survey/review/verification TODOs that must not mutate files, especially when file_scope is empty.
- provider_tag is one of: "plan" | "code" | "review" | "test" | "research". Default "code".
- worker_class is optional but preferred. Use one of: "planner" | "researcher" | "coder" | "reviewer" | "tester" | "security" | "synthesizer".
- skills is optional. Use short builtin capability names when helpful (e.g. "debug", "review", "audit", "test", "doc", "generate", "onboard").
- allowed_tools is optional. Use it only when the TODO should strongly prefer a narrow tool set.
- labels is optional. Use a few short tags that help a later supervisor or UI understand the work.
- verification is optional. Use "none" | "required" | "light" | "deep". Default "required" for code/test/review/security work, otherwise "light".
- confidence is optional. Float in [0,1] expressing how confident you are this TODO is well-scoped and correctly decomposed.
- 3 to 12 TODOs. Fewer is better when the task is small. Never exceed 20.
- The first TODO is usually a discovery step (read the relevant files, understand the existing shape) before any modifications.
- The last TODO is usually a verification step (run the tests, build, lint).
- If the task is unclear or under-specified, return: {"todos": [], "error": "<one sentence explaining what is missing>"}.
`

// PlannerOutput is the JSON envelope the planner LLM emits. Mirrors
// the shape documented in plannerSystemPrompt; deserialized in
// parsePlannerOutput.
type plannerOutput struct {
	Todos []Todo `json:"todos"`
	Error string `json:"error,omitempty"`
}

// runPlanner makes the LLM call and parses the response. Returns the
// list of TODOs (ready for scheduling) on success, an error otherwise.
// The error includes the raw model output for debugging when JSON
// parsing fails - without that, debugging a planner that consistently
// produces bad output is nearly impossible.
func runPlanner(ctx context.Context, runner Runner, task, model string) ([]Todo, error) {
	if strings.TrimSpace(task) == "" {
		return nil, fmt.Errorf("planner: task is empty")
	}
	resp, err := runner.PlannerCall(ctx, PlannerRequest{
		Model:  model,
		System: plannerSystemPrompt,
		User:   task,
	})
	if err != nil {
		return nil, fmt.Errorf("planner call failed: %w", err)
	}
	todos, perr := parsePlannerOutput(resp.Text)
	if perr != nil {
		return nil, fmt.Errorf("planner output unparseable (model=%s): %w\n--- raw output ---\n%s\n--- end ---",
			resp.Model, perr, truncate(resp.Text, 2000))
	}
	if err := validateTodos(todos); err != nil {
		return nil, fmt.Errorf("planner output invalid: %w", err)
	}
	return todos, nil
}

// parsePlannerOutput pulls the JSON envelope out of whatever the model
// returned and unmarshals it. Strips ```json fences, handles leading
// prose, and tries each balanced JSON object in order until one matches
// the planner envelope.
func parsePlannerOutput(raw string) ([]Todo, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("planner returned empty output")
	}
	if strings.Contains(trimmed, "```") {
		trimmed = stripCodeFence(trimmed)
	}
	candidates := extractJSONObjectCandidates(trimmed)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no JSON object found in planner output")
	}
	var lastErr error
	for _, candidate := range candidates {
		if len(candidate) > 1<<20 {
			lastErr = fmt.Errorf("payload too large: %d bytes", len(candidate))
			continue
		}
		var out plannerOutput
		if err := json.Unmarshal([]byte(candidate), &out); err != nil {
			lastErr = fmt.Errorf("json decode: %w", err)
			continue
		}
		if out.Error != "" {
			return nil, fmt.Errorf("planner refused: %s", out.Error)
		}
		if len(out.Todos) == 0 {
			lastErr = fmt.Errorf("planner returned zero TODOs (task likely too small or under-specified)")
			continue
		}
		for i := range out.Todos {
			t := &out.Todos[i]
			t.ID = strings.TrimSpace(t.ID)
			t.ParentID = strings.TrimSpace(t.ParentID)
			t.Origin = normalizeTodoOrigin(t.Origin)
			t.Kind = normalizeTodoKind(t.Kind, t.Verification, t.ProviderTag, t.WorkerClass)
			t.Title = strings.TrimSpace(t.Title)
			t.Detail = strings.TrimSpace(t.Detail)
			t.DependsOn = cleanList(t.DependsOn)
			t.FileScope = cleanFileScope(t.FileScope)
			t.ProviderTag = strings.TrimSpace(t.ProviderTag)
			if t.ProviderTag == "" {
				t.ProviderTag = "code"
			}
			t.WorkerClass = normalizeWorkerClass(t.WorkerClass, t.ProviderTag)
			t.Skills = cleanList(t.Skills)
			t.AllowedTools = cleanList(t.AllowedTools)
			t.Labels = cleanList(t.Labels)
			t.Verification = normalizeVerification(t.Verification, t.ProviderTag, t.WorkerClass)
			t.Kind = normalizeTodoKind(t.Kind, t.Verification, t.ProviderTag, t.WorkerClass)
			t.ReadOnly = normalizeTodoReadOnly(t.ReadOnly, t.WorkerClass, t.Kind, t.AllowedTools)
			t.Confidence = clampConfidence(t.Confidence)
			t.Status = TodoPending
		}
		return out.Todos, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no valid planner JSON object found")
}

// extractJSONObjectCandidates + findBalancedJSONObjectEnd +
// validateTodos + detectCycle + stripCodeFence + truncate live in
// planner_validate.go.
