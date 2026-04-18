// Planner: turns a free-form task into a JSON DAG of TODOs.
//
// The planner LLM call is intentionally minimal — no tool loop, no
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
	"regexp"
	"strings"
)

// plannerSystemPrompt is the canonical instruction sent to the planner
// model. Kept as a const (not config) because changing this is a real
// behavior change — operators who want a different planner prompt
// should fork the package, not patch a yaml file. The shape contract
// is documented inline so a model with no prior exposure can comply.
const plannerSystemPrompt = `You are the planner for an autonomous coding agent. The user gives you a coding task. You break it into an ordered DAG of small TODOs that another LLM (the executor) will work through one by one.

Output STRICTLY a JSON object matching this shape — nothing else (no prose, no markdown fences):

{
  "todos": [
    {
      "id": "T1",
      "title": "Short imperative title (under 80 chars)",
      "detail": "Concrete instructions for the executor — what to read, what to change, what to verify. 1-3 sentences.",
      "depends_on": [],
      "file_scope": ["relative/path/from/repo/root.go"],
      "provider_tag": "code"
    }
  ]
}

Rules:
- ids are short, unique, prefixed "T" (T1, T2, ...). depends_on references earlier ids only.
- title is what the user will see in the progress chip. Keep it under 80 chars.
- detail is the prompt the executor will run. Be concrete: name files, name functions, state success criteria.
- file_scope lists the files the TODO will read or write (best effort — used by the scheduler to avoid parallel conflicts). Empty is allowed.
- provider_tag is one of: "plan" | "code" | "review" | "test" | "research". Default "code".
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
// parsing fails — without that, debugging a planner that consistently
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

// jsonObjectRe extracts the first balanced { ... } block from text.
// Some models wrap their JSON in ```json fences or add a "Here is the
// plan:" preamble; this regex tolerates both. For deeply-nested or
// pathological output the standard library's json.Decoder catches it
// downstream — this is just a "find the JSON" first pass.
var jsonObjectRe = regexp.MustCompile(`(?s)\{.*\}`)

// parsePlannerOutput pulls the JSON envelope out of whatever the model
// returned and unmarshals it. Strips ```json fences, handles leading
// "here is the plan" prose. Returns the unmarshaled todos or a parse
// error (caller appends raw output).
func parsePlannerOutput(raw string) ([]Todo, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, fmt.Errorf("planner returned empty output")
	}
	// Strip code fences first so the regex matches the inner JSON.
	if strings.Contains(trimmed, "```") {
		trimmed = stripCodeFence(trimmed)
	}
	match := jsonObjectRe.FindString(trimmed)
	if match == "" {
		return nil, fmt.Errorf("no JSON object found in planner output")
	}
	var out plannerOutput
	if err := json.Unmarshal([]byte(match), &out); err != nil {
		return nil, fmt.Errorf("json decode: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("planner refused: %s", out.Error)
	}
	if len(out.Todos) == 0 {
		return nil, fmt.Errorf("planner returned zero TODOs (task likely too small or under-specified)")
	}
	// Normalize: trim whitespace on every string field, default
	// pending status, default provider tag.
	for i := range out.Todos {
		t := &out.Todos[i]
		t.ID = strings.TrimSpace(t.ID)
		t.Title = strings.TrimSpace(t.Title)
		t.Detail = strings.TrimSpace(t.Detail)
		t.ProviderTag = strings.TrimSpace(t.ProviderTag)
		if t.ProviderTag == "" {
			t.ProviderTag = "code"
		}
		t.Status = TodoPending
	}
	return out.Todos, nil
}

// validateTodos enforces the structural invariants the scheduler
// depends on: unique ids, dependency references that resolve, no
// self-loops or cycles. A planner that violates these would crash the
// scheduler later — fail loudly here with a message the model can
// learn from on retry.
func validateTodos(todos []Todo) error {
	if len(todos) == 0 {
		return fmt.Errorf("zero TODOs")
	}
	idSet := make(map[string]int, len(todos))
	for i, t := range todos {
		if t.ID == "" {
			return fmt.Errorf("todos[%d].id is empty", i)
		}
		if _, dup := idSet[t.ID]; dup {
			return fmt.Errorf("duplicate TODO id %q at index %d", t.ID, i)
		}
		idSet[t.ID] = i
		if t.Title == "" {
			return fmt.Errorf("todos[%d (%s)].title is empty", i, t.ID)
		}
		if t.Detail == "" {
			return fmt.Errorf("todos[%d (%s)].detail is empty — executor needs concrete instructions", i, t.ID)
		}
	}
	for _, t := range todos {
		for _, dep := range t.DependsOn {
			if dep == t.ID {
				return fmt.Errorf("TODO %q depends on itself", t.ID)
			}
			if _, ok := idSet[dep]; !ok {
				return fmt.Errorf("TODO %q depends on unknown id %q", t.ID, dep)
			}
		}
	}
	if cycle := detectCycle(todos, idSet); cycle != "" {
		return fmt.Errorf("dependency cycle detected: %s", cycle)
	}
	return nil
}

// detectCycle walks the dependency graph with iterative DFS and
// returns a human-readable cycle path on detection (empty string when
// the graph is acyclic). Iterative on purpose — Go's stack is plenty
// but explicit walks are easier to debug from event payloads.
func detectCycle(todos []Todo, idSet map[string]int) string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	state := make(map[string]int, len(todos))
	for _, t := range todos {
		state[t.ID] = white
	}
	var cyclePath []string
	var dfs func(id string, path []string) bool
	dfs = func(id string, path []string) bool {
		state[id] = gray
		path = append(path, id)
		idx := idSet[id]
		for _, dep := range todos[idx].DependsOn {
			switch state[dep] {
			case gray:
				cyclePath = append(path, dep)
				return true
			case white:
				if dfs(dep, path) {
					return true
				}
			}
		}
		state[id] = black
		return false
	}
	for _, t := range todos {
		if state[t.ID] == white {
			if dfs(t.ID, nil) {
				return strings.Join(cyclePath, " -> ")
			}
		}
	}
	return ""
}

// stripCodeFence removes the most common ``` wrappers (with or without
// a language tag) so the JSON regex sees clean text. Conservative —
// only strips a leading and trailing fence, doesn't try to handle
// multiple fenced blocks.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	// Drop the opening fence line.
	if nl := strings.Index(s, "\n"); nl >= 0 {
		s = s[nl+1:]
	}
	// Drop the trailing fence.
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// truncate is a small helper for log/error embedding so a 50KB raw
// model output doesn't pollute the error stream.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(truncated)"
}
