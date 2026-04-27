// Pure helpers used by the dispatch loop. Extracted from driver.go
// so the main file stays focused on lifecycle/construction and the
// loop file stays focused on the dispatcher. Nothing here touches
// the Driver receiver — these are stateless utilities.

package drive

import "strings"

// briefSoFar stitches together the per-TODO Brief fields of every
// completed TODO that precedes idx. Used to seed the executor's
// sub-agent so it has cheap context on what's already been done
// without dragging the parent transcript along.
func briefSoFar(todos []Todo, untilIdx int) string {
	var b strings.Builder
	for i := 0; i < untilIdx; i++ {
		t := todos[i]
		if t.Status == TodoDone && t.Brief != "" {
			b.WriteString("- ")
			b.WriteString(t.ID)
			b.WriteString(" (")
			b.WriteString(t.Title)
			b.WriteString("): ")
			b.WriteString(t.Brief)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// planSummary is a lightweight projection of the TODO list for the
// drive:plan:done event. The full Todo objects would carry too much
// detail for a UI chip; this keeps payloads small.
func planSummary(todos []Todo) []map[string]any {
	out := make([]map[string]any, 0, len(todos))
	for _, t := range todos {
		out = append(out, map[string]any{
			"id":            t.ID,
			"title":         t.Title,
			"deps":          t.DependsOn,
			"origin":        t.Origin,
			"kind":          t.Kind,
			"tag":           t.ProviderTag,
			"worker_class":  t.WorkerClass,
			"skills":        t.Skills,
			"verification":  t.Verification,
			"confidence":    t.Confidence,
			"allowed_tools": t.AllowedTools,
			"status":        string(t.Status),
		})
	}
	return out
}

func executorRoleFor(workerClass string) string {
	switch strings.ToLower(strings.TrimSpace(workerClass)) {
	case "planner":
		return "planner"
	case "researcher":
		return "researcher"
	case "reviewer":
		return "code_reviewer"
	case "tester":
		return "test_engineer"
	case "security":
		return "security_auditor"
	case "synthesizer":
		return "synthesizer"
	case "verifier":
		return "verifier"
	case "coder":
		fallthrough
	default:
		return "drive-executor"
	}
}

func executorStepBudgetFor(todo Todo) int {
	if todo.Budget > 0 {
		return todo.Budget
	}
	switch todoLane(todo) {
	case "discovery":
		return 6
	case "review":
		return 7
	case "verify":
		if strings.EqualFold(strings.TrimSpace(todo.Verification), "deep") {
			return 10
		}
		return 8
	case "synthesize":
		return 6
	default:
		if len(todo.FileScope) >= 3 {
			return 14
		}
		return 12
	}
}

// reasonByID retrieves the per-TODO Error/reason for the skipped
// event payload. Avoids a second pass over the slice in the caller.
func reasonByID(todos []Todo, id string) string {
	for _, t := range todos {
		if t.ID == id {
			return t.Error
		}
	}
	return ""
}

// collectIDsHead returns up to n IDs from the head of the slice. Used
// for the truncation warning event so the user sees which TODOs got
// dropped without dumping the full list.
func collectIDsHead(todos []Todo, n int) []string {
	if n > len(todos) {
		n = len(todos)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, todos[i].ID)
	}
	return out
}
