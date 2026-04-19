package drive

import (
	"fmt"
	"sort"
	"strings"
)

// synthesizeVerificationTodo creates a deterministic supervisor-generated
// verification TODO when the planned graph ends without an explicit verifier.
// The generated TODO depends on every high-risk work item so the normal
// scheduler runs it as the final pass.
func synthesizeVerificationTodo(todos []Todo) *Todo {
	if len(todos) == 0 || hasExplicitVerificationTodo(todos) {
		return nil
	}
	targets := verificationTargets(todos)
	if len(targets) == 0 {
		return nil
	}

	deps := make([]string, 0, len(targets))
	scopeSet := map[string]struct{}{}
	labels := []string{"verification", "supervisor"}
	skills := []string{"test", "review"}
	workerClass := "tester"
	providerTag := "test"
	verification := "required"
	title := "Verification pass"
	detailBits := make([]string, 0, 4)

	needsDeep := false
	needsAudit := false
	for _, t := range targets {
		deps = append(deps, t.ID)
		for _, f := range t.FileScope {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			scopeSet[f] = struct{}{}
		}
		for _, label := range t.Labels {
			addUnique(&labels, label)
		}
		for _, skill := range t.Skills {
			if strings.EqualFold(skill, "audit") {
				needsAudit = true
			}
		}
		if strings.EqualFold(t.Verification, "deep") || strings.EqualFold(t.WorkerClass, "security") {
			needsDeep = true
		}
	}
	if needsDeep {
		title = "Deep verification pass"
		verification = "deep"
		workerClass = "security"
		providerTag = "review"
		addUnique(&skills, "audit")
	}
	if needsAudit {
		addUnique(&skills, "audit")
	}

	sort.Strings(deps)
	scope := make([]string, 0, len(scopeSet))
	for f := range scopeSet {
		scope = append(scope, f)
	}
	sort.Strings(scope)

	detailBits = append(detailBits,
		"Verify the outcomes of the completed implementation tasks before the drive run is considered done.",
		fmt.Sprintf("Check the claims made by TODOs %s and run the smallest relevant test/build/lint commands.", strings.Join(deps, ", ")),
	)
	if len(scope) > 0 {
		detailBits = append(detailBits, "Focus on these files first: "+strings.Join(scope, ", ")+".")
	}
	if needsDeep {
		detailBits = append(detailBits, "Perform a deeper regression/security sanity pass and confirm no obvious behavior or trust-boundary regression slipped through.")
	} else {
		detailBits = append(detailBits, "Confirm the changed path works and report any residual risk or missing coverage.")
	}

	return &Todo{
		ID:           nextVerificationID(todos),
		Origin:       "supervisor",
		Kind:         "verify",
		Title:        title,
		Detail:       strings.Join(detailBits, " "),
		DependsOn:    deps,
		FileScope:    scope,
		ReadOnly:     true,
		ProviderTag:  providerTag,
		WorkerClass:  workerClass,
		Skills:       cleanList(skills),
		AllowedTools: []string{"read_file", "grep_codebase", "glob", "find_symbol", "codemap", "ast_query", "list_dir", "run_command"},
		Labels:       cleanList(labels),
		Verification: verification,
		Confidence:   1,
		Status:       TodoPending,
	}
}

func hasExplicitVerificationTodo(todos []Todo) bool {
	for _, t := range todos {
		if strings.EqualFold(t.Kind, "verify") {
			return true
		}
		if strings.EqualFold(t.WorkerClass, "tester") || strings.EqualFold(t.WorkerClass, "security") {
			if containsVerificationLanguage(t.Title, t.Detail) {
				return true
			}
		}
		if strings.EqualFold(t.ProviderTag, "test") || strings.EqualFold(t.ProviderTag, "review") {
			if containsVerificationLanguage(t.Title, t.Detail) {
				return true
			}
		}
	}
	return false
}

func verificationTargets(todos []Todo) []Todo {
	out := make([]Todo, 0, len(todos))
	for _, t := range todos {
		if strings.EqualFold(t.Kind, "verify") {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(t.WorkerClass)) {
		case "planner", "researcher", "documenter", "synthesizer":
			continue
		}
		switch strings.ToLower(strings.TrimSpace(t.Verification)) {
		case "", "none":
			continue
		}
		out = append(out, t)
	}
	return out
}

func containsVerificationLanguage(parts ...string) bool {
	text := strings.ToLower(strings.TrimSpace(strings.Join(parts, "\n")))
	for _, needle := range []string{"verify", "verification", "regression", "test", "build", "lint", "sanity check"} {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func nextVerificationID(todos []Todo) string {
	maxN := 0
	for _, t := range todos {
		id := strings.TrimSpace(t.ID)
		if len(id) < 3 || !strings.EqualFold(id[:2], "SV") {
			continue
		}
		n := 0
		for i := 2; i < len(id); i++ {
			c := id[i]
			if c < '0' || c > '9' {
				n = 0
				break
			}
			n = n*10 + int(c-'0')
		}
		if n > maxN {
			maxN = n
		}
	}
	return fmt.Sprintf("SV%d", maxN+1)
}

func addUnique(dst *[]string, item string) {
	item = strings.TrimSpace(item)
	if item == "" {
		return
	}
	for _, existing := range *dst {
		if strings.EqualFold(existing, item) {
			return
		}
	}
	*dst = append(*dst, item)
}
