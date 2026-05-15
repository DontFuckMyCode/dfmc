// prompt_render.go - prompt profile, role, and budget resolution for
// the context manager. These are the knobs that turn the user's query +
// task + provider capabilities into concrete rendering limits used by
// BuildSystemPromptBundle.
//
// Companion siblings (extracted to keep the resolver core lean):
//
//   - prompt_render_tools.go   tool-list summarization (summarizeTools,
//                              toolGroup, toolPriority, toolOneLine)
//   - prompt_render_brief.go   project brief loading + section scoring
//                              (loadProjectBrief and friends)
//   - prompt_render_policy.go  tool-call + response policy paragraphs
//                              (BuildToolCallPolicy, BuildResponsePolicy)
//
// What stays here:
//
//   - ResolvePromptProfile / promptProfileOverride / ResolvePromptRole:
//     pick "compact" vs "deep" and a persona (security_auditor,
//     planner, debugger...) from query keywords and task label.
//     Small-window providers get compact by default even when the task
//     would normally request deep.
//   - PromptRenderBudget / ResolvePromptRenderBudget: the per-section
//     caps (context files, tool list, injected blocks/lines/tokens,
//     project brief) scaled by profile, task, latency preference, and
//     provider context window.
//   - BuildInjectedContextWithBudget: pull [[file:...]] markers and
//     fenced code from the query, extract, and trim to the
//     injected-token budget.
//   - PromptTokenBudget / TrimPromptToBudget: outer ceiling on the
//     whole rendered prompt.
//   - containsAnyFold: small fold helper shared by the resolvers.

package context

import (
	"os"
	"strings"
)

func ResolvePromptProfile(query, task string, runtime PromptRuntime) string {
	q := strings.ToLower(strings.TrimSpace(query))
	if override := promptProfileOverride(q); override != "" {
		if override == "balanced" {
			if runtime.MaxContext > 0 && runtime.MaxContext <= 12000 {
				return "compact"
			}
			switch strings.ToLower(strings.TrimSpace(task)) {
			case "security", "review", "planning":
				return "deep"
			default:
				return "compact"
			}
		}
		return override
	}
	if containsAnyFold(q, []string{"detailed", "deep", "thorough", "exhaustive", "in-depth"}) {
		return "deep"
	}
	if containsAnyFold(q, []string{"compact", "short", "minimal", "brief", "concise", "summary"}) {
		return "compact"
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security", "review", "planning":
		if runtime.MaxContext > 0 && runtime.MaxContext <= 12000 {
			return "compact"
		}
		return "deep"
	}
	if runtime.LowLatency {
		return "compact"
	}
	if runtime.MaxContext > 0 && runtime.MaxContext <= 12000 {
		return "compact"
	}
	return "compact"
}

func promptProfileOverride(query string) string {
	for _, raw := range strings.Split(query, "\n") {
		line := strings.TrimSpace(strings.ToLower(raw))
		if line == "" {
			continue
		}
		for _, prefix := range []string{"#tier:", "#profile:", "tier:", "profile:"} {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			switch value {
			case "fast", "compact", "low-latency", "low_latency":
				return "compact"
			case "thorough", "deep", "exhaustive", "quality":
				return "deep"
			case "balanced", "normal":
				return "balanced"
			}
		}
		break
	}
	if env := strings.TrimSpace(strings.ToLower(os.Getenv("DFMC_PROFILE"))); env != "" {
		switch env {
		case "fast", "compact", "low-latency", "low_latency":
			return "compact"
		case "thorough", "deep", "exhaustive", "quality":
			return "deep"
		case "balanced", "normal":
			return "balanced"
		}
	}
	return ""
}

func ResolvePromptRole(query, task string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	if containsAnyFold(q, []string{"architect", "architecture", "system design"}) {
		return "architect"
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security":
		return "security_auditor"
	case "review":
		return "code_reviewer"
	case "planning":
		return "planner"
	case "debug":
		return "debugger"
	case "refactor":
		return "refactorer"
	case "test":
		return "test_engineer"
	case "doc", "document":
		return "documenter"
	case "synthesize", "synthesis", "summarize":
		return "synthesizer"
	case "research", "survey", "inventory":
		return "researcher"
	case "verify", "verification":
		return "verifier"
	default:
		return "generalist"
	}
}

type PromptRenderBudget struct {
	ContextFiles       int
	ToolList           int
	InjectedBlocks     int
	InjectedLines      int
	InjectedTokens     int
	ProjectBriefTokens int
}

func ResolvePromptRenderBudget(task, profile string, runtime PromptRuntime) PromptRenderBudget {
	b := PromptRenderBudget{
		ContextFiles:       10,
		ToolList:           24,
		InjectedBlocks:     2,
		InjectedLines:      80,
		InjectedTokens:     320,
		ProjectBriefTokens: 180,
	}
	if strings.EqualFold(strings.TrimSpace(profile), "deep") {
		b.ContextFiles = 16
		b.ToolList = 32
		b.InjectedBlocks = 3
		b.InjectedLines = 140
		b.InjectedTokens = 700
		b.ProjectBriefTokens = 320
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security", "review":
		b.ContextFiles += 2
		b.InjectedTokens += 140
	case "planning":
		b.ContextFiles += 2
	}
	if runtime.LowLatency {
		b.ContextFiles = max(4, int(float64(b.ContextFiles)*0.72))
		b.ToolList = max(12, int(float64(b.ToolList)*0.72))
		b.InjectedBlocks = max(1, b.InjectedBlocks-1)
		b.InjectedLines = max(28, int(float64(b.InjectedLines)*0.65))
		b.InjectedTokens = max(120, int(float64(b.InjectedTokens)*0.65))
		b.ProjectBriefTokens = max(90, int(float64(b.ProjectBriefTokens)*0.68))
	}
	if runtime.MaxContext > 0 {
		scale := float64(runtime.MaxContext) / 128000.0
		if scale > 1.0 {
			scale = 1.0
		}
		if scale < 0.22 {
			scale = 0.22
		}
		b.ContextFiles = max(3, int(float64(b.ContextFiles)*scale))
		b.ToolList = max(12, int(float64(b.ToolList)*scale))
		b.InjectedLines = max(24, int(float64(b.InjectedLines)*scale))
		b.InjectedTokens = max(100, int(float64(b.InjectedTokens)*scale))
		b.ProjectBriefTokens = max(80, int(float64(b.ProjectBriefTokens)*scale))
	}
	return b
}

func BuildInjectedContextWithBudget(projectRoot, query string, limits PromptRenderBudget) string {
	injected := extractInjectedContext(projectRoot, query, limits.InjectedBlocks, limits.InjectedLines)
	if limits.InjectedTokens > 0 {
		injected = trimToTokenBudget(injected, limits.InjectedTokens)
	}
	return injected
}

func PromptTokenBudget(task, profile string, runtime PromptRuntime) int {
	// Base contract (honesty, failure modes, output format, refusals) runs
	// ~450 stable tokens before any dynamic section. Budgets below bake that
	// in and leave meaningful differential room for injected context.
	budget := 1100
	if strings.EqualFold(strings.TrimSpace(profile), "deep") {
		budget = 1800
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security", "review":
		budget += 260
	case "planning":
		budget += 160
	case "doc":
		budget -= 60
	}
	if runtime.LowLatency {
		budget = int(float64(budget) * 0.85)
	}
	if runtime.MaxContext > 0 {
		cap := runtime.MaxContext / 4
		if cap > 3400 {
			cap = 3400
		}
		if cap < 720 {
			cap = 720
		}
		if budget > cap {
			budget = cap
		}
	}
	if budget < 720 {
		budget = 720
	}
	return budget
}

func TrimPromptToBudget(prompt string, maxTokens int) string {
	return trimToTokenBudget(prompt, maxTokens)
}

func containsAnyFold(in string, terms []string) bool {
	for _, t := range terms {
		v := strings.TrimSpace(strings.ToLower(t))
		if v == "" {
			continue
		}
		if strings.Contains(in, v) {
			return true
		}
	}
	return false
}
