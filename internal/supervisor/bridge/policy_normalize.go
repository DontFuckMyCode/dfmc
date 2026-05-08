// policy_normalize.go — drive TODO normalization. Sibling of policy.go
// which keeps the profile-selection surface (SelectDriveProfile,
// SelectDriveProfiles) plus the vendor preference / scoring helpers
// (vendorPreference, pickProfileByVendor, vendorMatchScore) and the
// shared cleanStrings / containsAny utilities.
//
// Splitting normalization out keeps policy.go scoped to "given a TODO,
// pick a profile" while this file owns "given a TODO, fill in the
// missing role / skills / verification / allowedTools so we don't
// rely entirely on planner quality." Both files share cleanStrings
// and containsAny, which live in policy.go.

package bridge

import (
	"strings"

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
