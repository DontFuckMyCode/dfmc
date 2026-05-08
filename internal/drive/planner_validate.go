// planner_validate.go — JSON-envelope extraction + structural
// validation for planner output. Sibling of planner.go which keeps
// the planner system prompt (the contract the model must follow), the
// runPlanner LLM dispatch, the parsePlannerOutput per-TODO normalize
// pipeline, and the plannerOutput JSON envelope type. Per-field
// normalizers (cleanList / cleanFileScope / normalizeTodoOrigin etc.)
// live in planner_normalize.go.
//
// Splitting these out keeps planner.go scoped to "what does the
// planner contract look like and how do we run one call" while this
// file owns the resilience patterns: pulling the JSON object out of
// whatever the model returned (with code-fence stripping and
// balanced-brace extraction over leading prose), and refusing to
// hand a structurally-broken DAG to the scheduler (cycles, dangling
// references, duplicate IDs would crash the scheduler later — fail
// loudly here with a message the model can learn from on retry).

package drive

import (
	"fmt"
	"strings"
)

func extractJSONObjectCandidates(raw string) []string {
	out := []string{}
	for start := 0; start < len(raw); start++ {
		if raw[start] != '{' {
			continue
		}
		if end, ok := findBalancedJSONObjectEnd(raw, start); ok {
			out = append(out, raw[start:end])
			start = end - 1
		}
	}
	return out
}

func findBalancedJSONObjectEnd(raw string, start int) (int, bool) {
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(raw); i++ {
		ch := raw[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i + 1, true
			}
			if depth < 0 {
				return 0, false
			}
		}
	}
	return 0, false
}

// validateTodos enforces the structural invariants the scheduler
// depends on: unique ids, dependency references that resolve, no
// self-loops or cycles. A planner that violates these would crash the
// scheduler later - fail loudly here with a message the model can
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
			return fmt.Errorf("todos[%d (%s)].detail is empty - executor needs concrete instructions", i, t.ID)
		}
		if t.Confidence < 0 || t.Confidence > 1 {
			return fmt.Errorf("todos[%d (%s)].confidence must be between 0 and 1", i, t.ID)
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
// the graph is acyclic). Iterative on purpose - Go's stack is plenty
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
// a language tag) so the JSON extractor sees clean text. Conservative -
// only strips a leading and trailing fence, does not try to handle
// multiple fenced blocks.
func stripCodeFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if nl := strings.Index(s, "\n"); nl >= 0 {
		s = s[nl+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// truncate is a small helper for log/error embedding so a 50KB raw
// model output does not pollute the error stream.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}
