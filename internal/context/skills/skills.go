// skills.go - configurable skill definitions for prompt role resolution.
//
// This package provides a pluggable skill system that replaces hardcoded
// task->role mappings in prompt_render.go. New skills can be added via:
//
//   1. Code:   skills.Register(Skill{...})
//   2. Config: skills.yaml with skill definitions
//
// Usage:
//   role := skills.Resolve(query, task)   // returns role string
//   profile := skills.ResolveProfile(...)  // returns "deep" or "compact"

package skills

import (
	"strings"
)

// Skill represents one skill domain with its associated role, default profile,
// keywords, and tool requirements.
type Skill struct {
	Name     string   // unique identifier: "security", "refactor", "debug"
	Role     string   // persona name: "security_auditor", "refactorer"
	Profile  string   // default profile: "deep" or "compact"
	Keywords []string // query keywords that trigger this skill (case-insensitive)
	Tools    []string // recommended tools for this skill
}

// DefaultSkills returns the built-in skill definitions.
// These mirror the hardcoded mappings in prompt_render.go.
func DefaultSkills() []Skill {
	return []Skill{
		{
			Name:     "security",
			Role:     "security_auditor",
			Profile:  "deep",
			Keywords: []string{"security", "audit", "vulnerability", "exploit", "threat", "auth", "permission", "access control"},
			Tools:    []string{"grep_codebase", "audit", "find_symbol"},
		},
		{
			Name:     "review",
			Role:     "code_reviewer",
			Profile:  "deep",
			Keywords: []string{"review", "pr", "pull request", "feedback", "critique"},
			Tools:    []string{"grep_codebase", "find_symbol", "explain"},
		},
		{
			Name:     "planning",
			Role:     "planner",
			Profile:  "deep",
			Keywords: []string{"plan", "planning", "architecture", "design", "proposal", "roadmap"},
			Tools:    []string{"grep_codebase", "glob", "explain"},
		},
		{
			Name:     "debug",
			Role:     "debugger",
			Profile:  "compact",
			Keywords: []string{"debug", "bug", "crash", "error", "issue", "panic", "stack trace", "fix"},
			Tools:    []string{"grep_codebase", "explain", "find_symbol"},
		},
		{
			Name:     "refactor",
			Role:     "refactorer",
			Profile:  "deep",
			Keywords: []string{"refactor", "restructure", "cleanup", "improve", "technical debt", "reorganize"},
			Tools:    []string{"grep_codebase", "edit_file", "apply_patch", "find_symbol"},
		},
		{
			Name:     "test",
			Role:     "test_engineer",
			Profile:  "deep",
			Keywords: []string{"test", "testing", "coverage", "unit test", "integration test", "benchmark"},
			Tools:    []string{"grep_codebase", "edit_file", "run_command", "find_symbol"},
		},
		{
			Name:     "doc",
			Role:     "documenter",
			Profile:  "compact",
			Keywords: []string{"doc", "document", "documentation", "readme", "comments", "spec"},
			Tools:    []string{"grep_codebase", "read_file", "edit_file"},
		},
		{
			Name:     "synthesize",
			Role:     "synthesizer",
			Profile:  "compact",
			Keywords: []string{"synthesize", "synthesis", "summarize", "summary", "overview", "combine", "merge"},
			Tools:    []string{"grep_codebase", "read_file", "glob"},
		},
		{
			Name:     "research",
			Role:     "researcher",
			Profile:  "deep",
			Keywords: []string{"research", "survey", "inventory", "explore", "discover", "analyze", "investigate"},
			Tools:    []string{"grep_codebase", "glob", "read_file", "tool_search"},
		},
		{
			Name:     "verify",
			Role:     "verifier",
			Profile:  "deep",
			Keywords: []string{"verify", "verification", "validate", "check", "assert", "confirm"},
			Tools:    []string{"run_command", "grep_codebase", "audit"},
		},
		{
			Name:     "architect",
			Role:     "architect",
			Profile:  "deep",
			Keywords: []string{"architect", "architecture", "system design", "high-level", "boundary"},
			Tools:    []string{"grep_codebase", "glob", "read_file", "explain"},
		},
	}
}

// Resolve returns the role for a given query and task.
// It first checks task-based mapping, then keyword matching.
// Falls back to "generalist" if no match.
func Resolve(query, task string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	t := strings.ToLower(strings.TrimSpace(task))

	// 1. Task-based exact match
	if role := resolveByTask(t); role != "" {
		return role
	}

	// 2. Keyword-based fuzzy match
	for _, skill := range registry {
		for _, kw := range skill.Keywords {
			if strings.Contains(q, strings.ToLower(kw)) {
				return skill.Role
			}
		}
	}

	return "generalist"
}

// resolveByTask maps task names directly to roles.
func resolveByTask(task string) string {
	for _, skill := range registry {
		if skill.Name == task {
			return skill.Role
		}
	}
	return ""
}

// ResolveProfile returns "deep" or "compact" based on query keywords and runtime hints.
func ResolveProfile(query, task string, opts ...ProfileOption) string {
	q := strings.ToLower(strings.TrimSpace(query))

	// Check explicit profile hints in query
	if profile := extractProfileHint(q); profile != "" {
		return profile
	}

	// Check skill-based profile preference
	for _, skill := range registry {
		for _, kw := range skill.Keywords {
			if strings.Contains(q, strings.ToLower(kw)) {
				return skill.Profile
			}
		}
		if skill.Name == strings.ToLower(task) {
			return skill.Profile
		}
	}

	return "compact"
}

// extractProfileHint looks for #profile: or #tier: markers.
func extractProfileHint(query string) string {
	for _, raw := range strings.Split(query, "\n") {
		line := strings.TrimSpace(strings.ToLower(raw))
		if line == "" {
			continue
		}
		for _, prefix := range []string{"#tier:", "#profile:", "tier:", "profile:"} {
			if strings.HasPrefix(line, prefix) {
				value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				switch value {
				case "fast", "compact", "low-latency", "low_latency":
					return "compact"
				case "thorough", "deep", "exhaustive", "quality":
					return "deep"
				case "balanced", "normal":
					return "" // neutral, let other factors decide
				}
			}
		}
		break
	}
	return ""
}

// ProfileOption configures profile resolution behavior.
type ProfileOption func(*profileConfig)

type profileConfig struct {
	lowLatency bool
	maxContext int
}

// WithLowLatency sets low-latency mode.
func WithLowLatency(b bool) ProfileOption {
	return func(c *profileConfig) { c.lowLatency = b }
}

// WithMaxContext sets the provider context window.
func WithMaxContext(n int) ProfileOption {
	return func(c *profileConfig) { c.maxContext = n }
}

// GetTools returns the recommended tools for a skill.
func GetTools(skillName string) []string {
	for _, skill := range registry {
		if skill.Name == skillName {
			return skill.Tools
		}
	}
	return nil
}
