package drive

// planner_normalize.go — post-parse normalizers that turn a raw planner
// JSON envelope into the canonical Todo shape the scheduler/executor
// depend on. Every Todo field that has a "default" or that the planner
// might emit in a slightly off form gets washed through one of these
// helpers in parsePlannerOutput. Kept separate so the planner.go
// orchestration (LLM call + JSON extraction + validation) reads as a
// flow rather than a wall of switch tables.

import "strings"

func normalizeWorkerClass(raw, providerTag string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "planner", "researcher", "coder", "reviewer", "tester", "security", "synthesizer":
		return v
	}
	switch strings.ToLower(strings.TrimSpace(providerTag)) {
	case "plan":
		return "planner"
	case "review":
		return "reviewer"
	case "test":
		return "tester"
	case "research":
		return "researcher"
	default:
		return "coder"
	}
}

func normalizeVerification(raw, providerTag, workerClass string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "none", "required", "light", "deep":
		return v
	}
	switch strings.ToLower(strings.TrimSpace(workerClass)) {
	case "reviewer", "tester", "security", "coder":
		return "required"
	}
	switch strings.ToLower(strings.TrimSpace(providerTag)) {
	case "review", "test", "code":
		return "required"
	default:
		return "light"
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

func cleanList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cleanFileScope(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		item = normalizeScope(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeTodoOrigin(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "supervisor":
		return "supervisor"
	case "worker":
		return "worker"
	default:
		return "planner"
	}
}

func normalizeTodoKind(raw, verification, providerTag, workerClass string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "verify", "work", "survey", "synth":
		return v
	}
	if strings.EqualFold(strings.TrimSpace(verification), "deep") && strings.EqualFold(strings.TrimSpace(workerClass), "security") {
		return "verify"
	}
	switch strings.ToLower(strings.TrimSpace(providerTag)) {
	case "test", "review":
		return "verify"
	case "research", "plan":
		return "survey"
	default:
		if strings.EqualFold(strings.TrimSpace(workerClass), "synthesizer") {
			return "synth"
		}
		return "work"
	}
}

func normalizeTodoReadOnly(explicit bool, workerClass, kind string, allowedTools []string) bool {
	if explicit {
		return true
	}
	if len(allowedTools) > 0 {
		return !allowsMutatingTools(allowedTools)
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "survey":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(workerClass)) {
	case "planner", "researcher":
		return true
	default:
		return false
	}
}

func allowsMutatingTools(tools []string) bool {
	for _, tool := range tools {
		switch strings.ToLower(strings.TrimSpace(tool)) {
		case "edit_file", "apply_patch", "write_file", "todo_write":
			return true
		}
	}
	return false
}
