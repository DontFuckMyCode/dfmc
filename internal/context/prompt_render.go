// prompt_render.go - prompt profile, role, and budget resolution for
// the context manager. These are the knobs that turn the user's query +
// task + provider capabilities into concrete rendering limits used by
// BuildSystemPromptBundle:
//
//   - ResolvePromptProfile / ResolvePromptRole: pick "compact" vs "deep"
//     and a persona (security_auditor, planner, debugger...) from query
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

func summarizeTools(tools []string, limit int, task string) string {
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
	sort.SliceStable(clean, func(i, j int) bool {
		gi := toolGroupOrder(toolGroup(clean[i]))
		gj := toolGroupOrder(toolGroup(clean[j]))
		if gi != gj {
			return gi < gj
		}
		pi := toolPriority(clean[i], task)
		pj := toolPriority(clean[j], task)
		if pi == pj {
			return clean[i] < clean[j]
		}
		return pi > pj
	})
	if limit > 0 && len(clean) > limit {
		clean = clean[:limit]
	}
	lines := make([]string, 0, len(clean)+8)
	currentGroup := ""
	for _, n := range clean {
		group := toolGroup(n)
		if group != currentGroup {
			if group != "" {
				lines = append(lines, "["+group+"]")
			}
			currentGroup = group
		}
		marker := ""
		if toolPriority(n, task) >= 30 {
			marker = " (recommended)"
		}
		lines = append(lines, "- "+n+marker+" - "+toolOneLine(n))
	}
	return strings.Join(lines, "\n")
}

func toolGroupOrder(group string) int {
	switch group {
	case "Read/search":
		return 10
	case "Edit":
		return 20
	case "Execute/verify":
		return 30
	case "Git":
		return 40
	case "Planning/subagents":
		return 50
	case "Meta bridge":
		return 60
	case "Web":
		return 70
	default:
		return 90
	}
}

func toolPriority(name, task string) int {
	n := strings.ToLower(strings.TrimSpace(name))
	score := 0
	for _, item := range []string{"read_file", "grep_codebase", "glob", "list_dir", "tool_search", "tool_help", "tool_call", "tool_batch_call"} {
		if n == item {
			score += 20
			break
		}
	}
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security", "review":
		for _, item := range []string{"git_diff", "git_status", "read_file", "grep_codebase", "ast_query", "codemap"} {
			if n == item {
				score += 30
				break
			}
		}
	case "refactor", "debug":
		for _, item := range []string{"read_file", "grep_codebase", "find_symbol", "ast_query", "edit_file", "apply_patch", "run_command"} {
			if n == item {
				score += 30
				break
			}
		}
	case "test":
		for _, item := range []string{"run_command", "read_file", "grep_codebase", "write_file", "edit_file"} {
			if n == item {
				score += 30
				break
			}
		}
	case "planning":
		for _, item := range []string{"task_split", "orchestrate", "delegate_task", "todo_write", "grep_codebase", "codemap"} {
			if n == item {
				score += 30
				break
			}
		}
	}
	return score
}

func toolGroup(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_file", "list_dir", "glob", "grep_codebase", "find_symbol", "codemap", "ast_query", "semantic_search", "project_info", "disk_usage":
		return "Read/search"
	case "write_file", "edit_file", "apply_patch", "symbol_rename", "symbol_move":
		return "Edit"
	case "run_command", "benchmark", "patch_validation":
		return "Execute/verify"
	case "git_status", "git_diff", "git_log", "git_blame", "git_branch", "git_commit", "git_worktree_add", "git_worktree_list", "git_worktree_remove", "gh_pr":
		return "Git"
	case "task_split", "orchestrate", "delegate_task", "todo_write", "think":
		return "Planning/subagents"
	case "tool_search", "tool_help", "tool_call", "tool_batch_call":
		return "Meta bridge"
	case "web_fetch", "web_search":
		return "Web"
	default:
		return "Other"
	}
}

func toolOneLine(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "read_file":
		return "read focused file ranges; prefer this over shell cat/type"
	case "grep_codebase":
		return "search code text by pattern before choosing files"
	case "glob":
		return "find paths by file pattern"
	case "list_dir":
		return "inspect directory contents"
	case "find_symbol":
		return "locate symbol definitions/usages"
	case "codemap":
		return "inspect project graph and dependency structure"
	case "ast_query":
		return "get AST-backed outlines or structural matches"
	case "edit_file":
		return "replace exact text after reading the file"
	case "write_file":
		return "create or replace a file when full content is known"
	case "apply_patch":
		return "apply multi-hunk diffs; use dry run for risky changes"
	case "run_command":
		return "run build/test/lint/dependency commands; no shell chains"
	case "git_status":
		return "inspect worktree state"
	case "git_diff":
		return "inspect scoped diffs before/after edits"
	case "task_split":
		return "split broad asks into sequential/parallel subtasks"
	case "orchestrate":
		return "fan out sub-agent work; supports DAG stages, per-stage model routing, and race candidates"
	case "delegate_task":
		return "spawn one bounded sub-agent for focused independent work"
	case "todo_write":
		return "record/update visible task state"
	case "tool_search":
		return "discover backend tools by keyword"
	case "tool_help":
		return "fetch exact tool signature before guessing args"
	case "tool_call":
		return "call one backend tool through the meta bridge"
	case "tool_batch_call":
		return "call several independent backend tools in one bounded batch"
	case "web_fetch":
		return "fetch a URL when current external docs are needed"
	case "web_search":
		return "search the web when local context is insufficient"
	default:
		return "registered backend tool; use tool_help for exact schema"
	}
}

func loadProjectBrief(projectRoot, query, task string, maxTokens int) string {
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
	sections := projectBriefSections(text)
	selected := selectProjectBriefSections(sections, query, task, 4)
	if len(selected) == 0 {
		selected = firstProjectBriefLines(text, 48)
	}
	if len(selected) == 0 {
		return "(none)"
	}
	return trimToTokenBudget(strings.Join(selected, "\n"), maxTokens)
}

type projectBriefSection struct {
	Index int
	Title string
	Lines []string
}

func projectBriefSections(text string) []projectBriefSection {
	rawLines := strings.Split(text, "\n")
	sections := make([]projectBriefSection, 0)
	current := projectBriefSection{Index: 0}
	flush := func() {
		if len(current.Lines) == 0 {
			return
		}
		sections = append(sections, current)
		current = projectBriefSection{Index: len(sections)}
	}
	for _, line := range rawLines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "```") {
			continue
		}
		if strings.HasPrefix(t, "#") {
			flush()
			current.Title = strings.TrimSpace(strings.TrimLeft(t, "#"))
			current.Lines = append(current.Lines, t)
			continue
		}
		current.Lines = append(current.Lines, t)
	}
	flush()
	return sections
}

func selectProjectBriefSections(sections []projectBriefSection, query, task string, limit int) []string {
	if len(sections) == 0 || limit <= 0 {
		return nil
	}
	terms := append(tokenizeQuery(query), projectBriefTaskTerms(task)...)
	type scored struct {
		section projectBriefSection
		score   int
	}
	scoredSections := make([]scored, 0, len(sections))
	for _, section := range sections {
		score := scoreProjectBriefSection(section, terms)
		if score <= 0 {
			continue
		}
		scoredSections = append(scoredSections, scored{section: section, score: score})
	}
	if len(scoredSections) == 0 {
		return nil
	}
	sort.SliceStable(scoredSections, func(i, j int) bool {
		if scoredSections[i].score != scoredSections[j].score {
			return scoredSections[i].score > scoredSections[j].score
		}
		return scoredSections[i].section.Index < scoredSections[j].section.Index
	})
	if len(scoredSections) > limit {
		scoredSections = scoredSections[:limit]
	}
	sort.SliceStable(scoredSections, func(i, j int) bool {
		return scoredSections[i].section.Index < scoredSections[j].section.Index
	})
	out := []string{"Project brief filtered for task=" + strings.TrimSpace(task) + ":"}
	for _, item := range scoredSections {
		out = append(out, item.section.Lines...)
	}
	return out
}

func scoreProjectBriefSection(section projectBriefSection, terms []string) int {
	if len(terms) == 0 {
		return 0
	}
	title := strings.ToLower(section.Title)
	body := strings.ToLower(strings.Join(section.Lines, "\n"))
	score := 0
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if len(term) < 3 {
			continue
		}
		if strings.Contains(title, term) {
			score += 4
		}
		if strings.Contains(body, term) {
			score++
		}
	}
	return score
}

func projectBriefTaskTerms(task string) []string {
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security":
		return []string{"security", "auth", "secret", "credential", "vulnerab", "risk", "audit", "threat", "token"}
	case "review":
		return []string{"review", "bug", "risk", "hotspot", "todo", "quality", "regression"}
	case "refactor":
		return []string{"refactor", "architecture", "design", "cleanup", "debt", "module"}
	case "test":
		return []string{"test", "coverage", "fixture", "mock", "benchmark"}
	case "doc":
		return []string{"doc", "readme", "usage", "guide", "manual"}
	case "planning":
		return []string{"plan", "roadmap", "milestone", "priority", "next"}
	default:
		return nil
	}
}

func firstProjectBriefLines(text string, limit int) []string {
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, min(len(lines), max(0, limit)))
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "```") {
			continue
		}
		filtered = append(filtered, t)
		if len(filtered) >= limit {
			break
		}
	}
	return filtered
}

func BuildToolCallPolicy(task string, runtime PromptRuntime) string {
	lines := []string{
		"Tool Calling Protocol:",
		"Provider protocol: " + toolStyleProtocol(runtime.ToolStyle),
		"Runtime budget: keep cumulative tool output near " + strconv.Itoa(max(96, runtime.MaxContext/5)) + " tokens unless risk requires deeper evidence.",
		"1. Before each call, decide the smallest missing fact the tool can prove.",
		"2. Every model-initiated tool call MUST include `_reason` in args (<=140 chars): why this tool, why now, expected signal.",
		"3. Call one tool at a time unless the calls are independent reads; then use one bounded tool_batch_call.",
		"4. Never repeat the same tool with the same arguments; narrow the query/range or switch tools.",
		"5. Prefer focused reads over broad dumps; read the smallest useful line range/file set.",
		"6. After run_command, inspect stdout/stderr and exit status before editing, deleting, or declaring success.",
		"Discipline: call tools only when they reduce uncertainty; keep calls narrow; reuse prior outputs; validate edits with the smallest test.",
		"Prefer dedicated tools over run_command: read_file (not cat), grep_codebase (not grep/rg), glob (not find), edit_file/apply_patch (not sed/awk), write_file (not echo>), web_fetch (not curl), ast_query for outlines. Use run_command only for build/test/lint/git/deps.",
		"Shell boundary: run_command does not invoke a shell by default; dependent steps should be separate tool calls unless you explicitly run a shell binary and accept its platform-specific syntax.",
		"Mutation safety: read_file before edit_file/write_file (engine rejects blind mutations); multi-hunk edits -> apply_patch (use dry_run when non-trivial); validate with targeted test/vet/tsc before declaring done.",
		"Git/shell safety: never --no-verify or --no-gpg-sign without user consent; never force-push main or reset --hard without authorization; stage files by name (not add -A/.); after a pre-commit hook fails, fix and create a NEW commit (never --amend); HEREDOC for multi-line commit messages.",
	}
	switch strings.TrimSpace(strings.ToLower(runtime.ToolStyle)) {
	case "function-calling":
		lines = append(lines, "Protocol detail: strict function-call JSON matching schema; no prose inside arguments.")
	case "tool_use":
		lines = append(lines, "Protocol detail: tool_use blocks with strict JSON input; pair each tool_result with its tool_use id.")
	case "none":
		lines = append(lines, "Protocol detail: no native tool-calling; rely on provided context and direct reasoning.")
	default:
		lines = append(lines, "Protocol detail: follow provider-native tool format exactly; schema fidelity over verbosity.")
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

func toolStyleProtocol(style string) string {
	switch strings.TrimSpace(strings.ToLower(style)) {
	case "function-calling":
		return "strict function-call JSON matching schema; no prose inside arguments."
	case "tool_use":
		return "tool_use blocks with strict JSON input; pair each tool_result with its tool_use id."
	case "none":
		return "no native tool-calling; rely on provided context and direct reasoning."
	default:
		return "follow provider-native tool format exactly; schema fidelity over verbosity."
	}
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
