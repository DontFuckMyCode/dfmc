// prompt_render.go — prompt profile, role, and budget resolution for
// the context manager. These are the knobs that turn the user's query +
// task + provider capabilities into concrete rendering limits used by
// BuildSystemPromptBundle:
//
//   - ResolvePromptProfile / ResolvePromptRole: pick "compact" vs "deep"
//     and a persona (security_auditor, planner, debugger…) from query
//     keywords and task label. Small-window providers get compact by
//     default even when the task would normally request deep.
//   - PromptRenderBudget / ResolvePromptRenderBudget: the per-section
//     caps (context files, tool list, injected blocks/lines/tokens,
//     project brief) scaled by profile, task, latency preference, and
//     provider context window.
//   - BuildInjectedContext / BuildInjectedContextWithBudget: pull
//     [[file:...]] markers and fenced code from the query, extract, and
//     trim to the injected-token budget.
//   - PromptTokenBudget / TrimPromptToBudget: the outer ceiling on the
//     whole rendered prompt.
//   - BuildToolCallPolicy / BuildResponsePolicy: policy paragraphs that
//     ride along in the system block. Tool-style overlay picks wording
//     for anthropic-style tool_use vs openai function-calling.
//   - containsAnyFold / summarizeTools / loadProjectBrief: small helpers
//     the resolvers compose.
//
// Extracted from manager.go to keep the main file focused on retrieval.

package context

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func ResolvePromptProfile(query, task string, runtime PromptRuntime) string {
	q := strings.ToLower(strings.TrimSpace(query))
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
		ToolList:           16,
		InjectedBlocks:     2,
		InjectedLines:      80,
		InjectedTokens:     320,
		ProjectBriefTokens: 180,
	}
	if strings.EqualFold(strings.TrimSpace(profile), "deep") {
		b.ContextFiles = 16
		b.ToolList = 24
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
		b.ToolList = max(8, int(float64(b.ToolList)*0.72))
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
		b.ToolList = max(6, int(float64(b.ToolList)*scale))
		b.InjectedLines = max(24, int(float64(b.InjectedLines)*scale))
		b.InjectedTokens = max(100, int(float64(b.InjectedTokens)*scale))
		b.ProjectBriefTokens = max(80, int(float64(b.ProjectBriefTokens)*scale))
	}
	return b
}

func BuildInjectedContext(projectRoot, query, task, profile string, runtime PromptRuntime) string {
	resolvedProfile := strings.TrimSpace(profile)
	if resolvedProfile == "" {
		resolvedProfile = ResolvePromptProfile(query, task, runtime)
	}
	limits := ResolvePromptRenderBudget(task, resolvedProfile, runtime)
	return BuildInjectedContextWithBudget(projectRoot, query, limits)
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

func summarizeTools(tools []string, limit int) string {
	if len(tools) == 0 {
		return "(none)"
	}
	clean := make([]string, 0, len(tools))
	seen := map[string]struct{}{}
	for _, name := range tools {
		n := strings.TrimSpace(name)
		if n == "" {
			continue
		}
		k := strings.ToLower(n)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		clean = append(clean, n)
	}
	sort.Strings(clean)
	if limit > 0 && len(clean) > limit {
		clean = clean[:limit]
	}
	lines := make([]string, 0, len(clean))
	for _, n := range clean {
		lines = append(lines, "- "+n)
	}
	return strings.Join(lines, "\n")
}

func loadProjectBrief(projectRoot string, maxTokens int) string {
	root := strings.TrimSpace(projectRoot)
	if root == "" || maxTokens <= 0 {
		return "(none)"
	}
	path := filepath.Join(root, ".dfmc", "magic", "MAGIC_DOC.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "(none)"
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "(none)"
	}
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "```") {
			continue
		}
		filtered = append(filtered, t)
		if len(filtered) >= 48 {
			break
		}
	}
	if len(filtered) == 0 {
		return "(none)"
	}
	return trimToTokenBudget(strings.Join(filtered, "\n"), maxTokens)
}

func BuildToolCallPolicy(task string, runtime PromptRuntime) string {
	lines := []string{
		"Discipline: call tools only when they reduce uncertainty; keep calls narrow; reuse prior outputs; validate edits with the smallest test.",
		"Prefer dedicated tools over run_command: read_file (not cat), grep_codebase (not grep/rg), glob (not find), edit_file/apply_patch (not sed/awk), write_file (not echo>), web_fetch (not curl), ast_query for outlines. Use run_command only for build/test/lint/git/deps.",
		"Parallelism: independent calls → one tool_batch_call; dependent commands → chain with && in a single run_command; never split a command across newlines; don't retry failing calls without changing inputs.",
		"Mutation safety: read_file before edit_file/write_file (engine rejects blind mutations); multi-hunk edits → apply_patch (use dry_run when non-trivial); validate with targeted test/vet/tsc before declaring done.",
		"Git/shell safety: never --no-verify or --no-gpg-sign without user consent; never force-push main or reset --hard without authorization; stage files by name (not add -A/.); after a pre-commit hook fails, fix and create a NEW commit (never --amend); HEREDOC for multi-line commit messages.",
	}
	switch strings.TrimSpace(strings.ToLower(runtime.ToolStyle)) {
	case "function-calling":
		lines = append(lines, "Protocol: strict function-call JSON matching schema; no prose inside arguments.")
	case "tool_use":
		lines = append(lines, "Protocol: tool_use blocks with strict JSON input; pair each tool_result with its tool_use id.")
	case "none":
		lines = append(lines, "Protocol: no native tool-calling; rely on provided context and direct reasoning.")
	default:
		lines = append(lines, "Protocol: follow provider-native tool format exactly; schema fidelity over verbosity.")
	}
	if runtime.MaxContext > 0 {
		toolOutputBudget := runtime.MaxContext / 5
		toolOutputBudget = max(96, toolOutputBudget)
		lines = append(lines, "Keep cumulative tool output near "+strconv.Itoa(toolOutputBudget)+" tokens unless risk requires deeper evidence.")
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security":
		lines = append(lines, "Security overlay: collect concrete evidence before remediation edits; report exploitability conditions and confidence.")
	case "review":
		lines = append(lines, "Review overlay: anchor findings to file:line evidence; prioritize high-severity and high-confidence issues.")
	}
	return strings.Join(lines, "\n")
}

func BuildResponsePolicy(task, profile string) string {
	depth := "compact"
	if strings.EqualFold(strings.TrimSpace(profile), "deep") {
		depth = "deep"
	}
	lines := []string{
		"- Output depth: " + depth,
		"- Maximize signal density; avoid filler text.",
		"- Keep assumptions explicit and short.",
		"- Prefer precise file references for code claims.",
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "review", "security":
		lines = append(lines,
			"- Order findings by severity first.",
			"- Include impact and concrete fix guidance per finding.")
	case "planning":
		lines = append(lines,
			"- Provide phased execution plan with checkpoints.")
	default:
		lines = append(lines,
			"- Start with short answer, then critical details.")
	}
	return strings.Join(lines, "\n")
}
