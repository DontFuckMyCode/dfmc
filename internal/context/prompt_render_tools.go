package context

// prompt_render_tools.go — tool-list summarization for the system prompt.
// Companion siblings:
//
//   - prompt_render.go         profile/role/budget/injected-context core
//   - prompt_render_brief.go   project brief loading + section selection
//   - prompt_render_policy.go  tool-call + response policy paragraphs
//
// summarizeTools formats the registered tool catalog into a grouped,
// prioritized markdown list, scoring each entry against the active task
// so high-signal tools surface first within their group.

import (
	"sort"
	"strings"
)

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
