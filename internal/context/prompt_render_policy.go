package context

// prompt_render_policy.go — tool-call and response policy paragraphs
// that ride along inside the system block.
// Companion siblings:
//
//   - prompt_render.go         profile/role/budget/injected-context core
//   - prompt_render_tools.go   tool-list summarization
//   - prompt_render_brief.go   project brief loading + section selection
//
// BuildToolCallPolicy renders the discipline rules around when/how to
// reach for tools, parameterized by the active task and the runtime's
// provider tool-style. BuildResponsePolicy shapes output depth/order
// based on profile + task. toolStyleProtocol is the one-liner used by
// both the policy text and the runtime header.

import (
	"strconv"
	"strings"
)

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
