package bridge

import (
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// NormalizeDriveExecution deterministically fills missing execution hints so
// drive TODOs do not rely entirely on planner quality.
func NormalizeDriveExecution(req drive.ExecuteTodoRequest) drive.ExecuteTodoRequest {
	req.Role = normalizeRole(req.Role, req.Skills, req.Title, req.Detail)
	req.Skills = normalizeSkills(req.Skills, req.Role, req.Title, req.Detail, req.Verification)
	req.Verification = normalizeExecutionVerification(req.Verification, req.Role, req.Skills)
	if len(req.AllowedTools) == 0 {
		req.AllowedTools = defaultAllowedTools(req.Role, req.Detail, req.Verification)
	} else {
		req.AllowedTools = cleanStrings(req.AllowedTools)
	}
	req.Labels = cleanStrings(req.Labels)
	return req
}

func SelectDriveProfile(req drive.ExecuteTodoRequest, profiles map[string]config.ModelConfig, fallback string) string {
	if picks := SelectDriveProfiles(req, profiles, fallback, 1); len(picks) > 0 {
		return picks[0]
	}
	return ""
}

func SelectDriveProfiles(req drive.ExecuteTodoRequest, profiles map[string]config.ModelConfig, fallback string, limit int) []string {
	if override := strings.TrimSpace(req.Model); override != "" {
		return []string{override}
	}
	if len(profiles) == 0 {
		fallback = strings.TrimSpace(fallback)
		if fallback == "" {
			return nil
		}
		return []string{fallback}
	}
	out := make([]string, 0, len(profiles))
	seen := map[string]struct{}{}
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[strings.ToLower(name)]; ok {
			return
		}
		seen[strings.ToLower(name)] = struct{}{}
		out = append(out, name)
	}
	for _, vendor := range vendorPreference(req.Role, req.Verification) {
		add(pickProfileByVendor(profiles, vendor))
	}
	fallback = strings.TrimSpace(fallback)
	if fallback != "" {
		add(fallback)
	}
	remaining := make([]string, 0, len(profiles))
	for name := range profiles {
		remaining = append(remaining, name)
	}
	sort.SliceStable(remaining, func(i, j int) bool {
		left := profiles[remaining[i]]
		right := profiles[remaining[j]]
		if left.MaxContext != right.MaxContext {
			return left.MaxContext > right.MaxContext
		}
		return remaining[i] < remaining[j]
	})
	for _, name := range remaining {
		add(name)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeRole(role string, skills []string, title, detail string) string {
	role = strings.TrimSpace(role)
	if role != "" {
		return role
	}
	text := strings.ToLower(strings.TrimSpace(title + "\n" + detail))
	for _, skill := range skills {
		switch strings.ToLower(strings.TrimSpace(skill)) {
		case "audit":
			return "security_auditor"
		case "review":
			return "code_reviewer"
		case "test":
			return "test_engineer"
		case "debug":
			return "debugger"
		case "doc", "onboard":
			return "documenter"
		}
	}
	switch {
	case containsAny(text, []string{"security", "vuln", "authz", "idor", "secret", "exploit", "audit"}):
		return "security_auditor"
	case containsAny(text, []string{"review", "regression", "behavior change", "risk"}):
		return "code_reviewer"
	case containsAny(text, []string{"test", "verify", "assert", "regression coverage"}):
		return "test_engineer"
	case containsAny(text, []string{"debug", "root cause", "reproduce", "panic", "trace"}):
		return "debugger"
	case containsAny(text, []string{"doc", "document", "writeup", "architecture"}):
		return "documenter"
	case containsAny(text, []string{"research", "survey", "map", "inventory"}):
		return "researcher"
	case containsAny(text, []string{"plan", "decompose", "todo", "roadmap"}):
		return "planner"
	case containsAny(text, []string{"summarize", "synthesize", "combine findings"}):
		return "synthesizer"
	default:
		return "drive-executor"
	}
}

func normalizeSkills(skills []string, role, title, detail, verification string) []string {
	out := cleanStrings(skills)
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		for _, existing := range out {
			if strings.EqualFold(existing, name) {
				return
			}
		}
		out = append(out, name)
	}
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "security_auditor":
		add("audit")
	case "code_reviewer":
		add("review")
	case "test_engineer":
		add("test")
	case "debugger":
		add("debug")
	case "documenter":
		add("doc")
	case "planner", "researcher":
		add("onboard")
	}
	text := strings.ToLower(strings.TrimSpace(title + "\n" + detail))
	if containsAny(text, []string{"generate", "implement", "create new", "scaffold"}) {
		add("generate")
	}
	if containsAny(text, []string{"debug", "panic", "trace", "reproduce"}) {
		add("debug")
	}
	if strings.EqualFold(strings.TrimSpace(verification), "deep") && containsAny(text, []string{"security", "auth", "token", "permission"}) {
		add("audit")
	}
	return out
}

func normalizeExecutionVerification(verification, role string, skills []string) string {
	v := strings.ToLower(strings.TrimSpace(verification))
	switch v {
	case "none", "light", "required", "deep":
		return v
	}
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "security_auditor":
		return "deep"
	case "code_reviewer", "test_engineer", "debugger":
		return "required"
	case "planner", "researcher", "documenter":
		return "light"
	}
	for _, skill := range skills {
		switch strings.ToLower(strings.TrimSpace(skill)) {
		case "audit":
			return "deep"
		case "review", "test", "debug", "generate":
			return "required"
		}
	}
	return "required"
}

func defaultAllowedTools(role, detail, verification string) []string {
	readOnly := []string{"read_file", "grep_codebase", "glob", "find_symbol", "codemap", "ast_query", "list_dir", "run_command"}
	writeCapable := []string{"read_file", "grep_codebase", "glob", "find_symbol", "codemap", "ast_query", "list_dir", "edit_file", "apply_patch", "write_file", "run_command"}
	role = strings.ToLower(strings.TrimSpace(role))
	detailText := strings.ToLower(strings.TrimSpace(detail))
	allowsWrites := containsAny(detailText, []string{"fix", "patch", "change", "edit", "update", "write", "implement", "refactor", "add", "remove", "rename"})
	switch role {
	case "security_auditor", "code_reviewer":
		if allowsWrites {
			return writeCapable
		}
		return readOnly
	case "researcher", "planner":
		return []string{"read_file", "grep_codebase", "glob", "find_symbol", "codemap", "ast_query", "list_dir"}
	case "documenter":
		return []string{"read_file", "grep_codebase", "find_symbol", "list_dir", "edit_file", "apply_patch", "write_file"}
	case "test_engineer":
		return writeCapable
	case "debugger":
		if strings.EqualFold(strings.TrimSpace(verification), "light") && !allowsWrites {
			return readOnly
		}
		return writeCapable
	default:
		return writeCapable
	}
}

func vendorPreference(role, verification string) []string {
	role = strings.ToLower(strings.TrimSpace(role))
	verification = strings.ToLower(strings.TrimSpace(verification))
	switch role {
	case "security_auditor", "code_reviewer", "synthesizer", "planner":
		return []string{"anthropic", "openai", "google", "kimi", "zai", "alibaba", "deepseek", "minimax"}
	case "researcher":
		return []string{"google", "openai", "anthropic", "deepseek", "kimi", "zai", "alibaba", "minimax"}
	case "test_engineer", "debugger":
		return []string{"openai", "anthropic", "google", "deepseek", "kimi", "zai", "alibaba", "minimax"}
	default:
		if verification == "deep" {
			return []string{"anthropic", "openai", "google", "kimi", "zai", "alibaba", "deepseek", "minimax"}
		}
		return []string{"anthropic", "openai", "google", "deepseek", "kimi", "zai", "alibaba", "minimax"}
	}
}

func pickProfileByVendor(profiles map[string]config.ModelConfig, vendor string) string {
	bestName := ""
	bestScore := -1
	for name, prof := range profiles {
		score := vendorMatchScore(name, prof.Model, vendor)
		if score <= 0 {
			continue
		}
		score = score*1_000_000 + prof.MaxContext
		if score > bestScore {
			bestScore = score
			bestName = name
		}
	}
	return bestName
}

func vendorMatchScore(name, model, vendor string) int {
	haystacks := []string{strings.ToLower(strings.TrimSpace(name)), strings.ToLower(strings.TrimSpace(model))}
	match := func(terms ...string) int {
		score := 0
		for _, hay := range haystacks {
			for _, term := range terms {
				if strings.Contains(hay, term) {
					score++
				}
			}
		}
		return score
	}
	switch vendor {
	case "anthropic":
		return match("anthropic", "claude")
	case "openai":
		return match("openai", "gpt")
	case "google":
		return match("google", "gemini")
	case "deepseek":
		return match("deepseek")
	case "kimi":
		return match("kimi", "moonshot")
	case "zai":
		return match("zai", "glm")
	case "alibaba":
		return match("alibaba", "qwen", "dashscope")
	case "minimax":
		return match("minimax")
	default:
		return 0
	}
}

func cleanStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]struct{}{}
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

func containsAny(in string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(in, term) {
			return true
		}
	}
	return false
}
