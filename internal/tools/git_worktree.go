// git_worktree.go — the git_worktree_* tool family. Three tools that
// share the porcelain parser and all route through runGit() with the
// same flag-injection / path-containment guards as the rest of git.go:
//
//   - GitWorktreeListTool: enumerates linked worktrees via
//     `git worktree list --porcelain`, parsed into {path,head,branch,…}.
//   - GitWorktreeAddTool: `git worktree add` with optional -b newbranch
//     and ref. Path must resolve inside the project root; branch names
//     are blocklist-checked and flag-injection-guarded.
//   - GitWorktreeRemoveTool: `git worktree remove [--force]`. Does not
//     touch the underlying branch; the user is told to remove that
//     separately when needed.
//
// Extracted from git.go to keep the main git file focused on the
// single-repo tools.

package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

type GitWorktreeListTool struct{}

func NewGitWorktreeListTool() *GitWorktreeListTool { return &GitWorktreeListTool{} }
func (t *GitWorktreeListTool) Name() string        { return "git_worktree_list" }
func (t *GitWorktreeListTool) Description() string {
	return "List active git worktrees for the current repository."
}

func (t *GitWorktreeListTool) Execute(ctx context.Context, req Request) (Result, error) {
	timeout := resolveGitTimeout(req.Params)
	stdout, stderr, exit, err := runGit(ctx, req.ProjectRoot, timeout, "worktree", "list", "--porcelain")
	if err != nil {
		return Result{Output: joinGitOutput(stdout, stderr), Data: map[string]any{"exit_code": exit}}, err
	}
	trees := parseWorktreeList(stdout)
	return Result{
		Output: strings.TrimSpace(stdout),
		Data: map[string]any{
			"worktrees": trees,
			"count":     len(trees),
			"exit_code": exit,
		},
	}, nil
}

func parseWorktreeList(raw string) []map[string]string {
	var out []map[string]string
	var current map[string]string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if current != nil {
				out = append(out, current)
				current = nil
			}
			continue
		}
		key, value, ok := strings.Cut(trimmed, " ")
		if !ok {
			key = trimmed
			value = ""
		}
		if current == nil {
			current = map[string]string{}
		}
		switch key {
		case "worktree":
			current["path"] = filepath.ToSlash(value)
		case "HEAD":
			current["head"] = value
		case "branch":
			current["branch"] = value
		case "bare":
			current["bare"] = "true"
		case "detached":
			current["detached"] = "true"
		}
	}
	if current != nil {
		out = append(out, current)
	}
	return out
}

func (t *GitWorktreeListTool) Spec() ToolSpec {
	return ToolSpec{
		Name:       "git_worktree_list",
		Title:      "List git worktrees",
		Summary:    "Enumerate all worktrees linked to this repository.",
		Purpose:    "Discover parallel checkouts before creating a new one.",
		Risk:       RiskRead,
		Tags:       []string{"git", "vcs", "worktree", "read"},
		Returns:    "{worktrees[{path,head,branch,detached?,bare?}], count, exit_code}.",
		Idempotent: true,
		CostHint:   "cheap",
	}
}

type GitWorktreeAddTool struct{}

func NewGitWorktreeAddTool() *GitWorktreeAddTool { return &GitWorktreeAddTool{} }
func (t *GitWorktreeAddTool) Name() string       { return "git_worktree_add" }
func (t *GitWorktreeAddTool) Description() string {
	return "Create a new git worktree at <path> for <ref> (optionally creating a new branch)."
}

func (t *GitWorktreeAddTool) Execute(ctx context.Context, req Request) (Result, error) {
	timeout := resolveGitTimeout(req.Params)
	path := strings.TrimSpace(asString(req.Params, "path", ""))
	if path == "" {
		return Result{}, missingParamError("git_worktree_add", "path", req.Params,
			`{"path":"../wt-feature","branch":"feature/x","ref":"main"} or {"path":"../wt-fix","ref":"HEAD~1"}`,
			`path is the new worktree directory (relative to project root or absolute). Optional branch creates a new branch, ref selects the starting point (default HEAD). To remove a worktree later use git_worktree_remove.`)
	}
	absPath, err := EnsureWithinRoot(req.ProjectRoot, path)
	if err != nil {
		return Result{}, err
	}

	args := []string{"worktree", "add"}
	if branch := strings.TrimSpace(asString(req.Params, "new_branch", "")); branch != "" {
		if blockedBranchName(branch) {
			return Result{}, fmt.Errorf("branch name blocked by policy: %s", branch)
		}
		if err := rejectGitFlagInjection("new_branch", branch); err != nil {
			return Result{}, err
		}
		args = append(args, "-b", branch)
	}
	args = append(args, absPath)
	if ref := strings.TrimSpace(asString(req.Params, "ref", "")); ref != "" {
		if err := rejectGitFlagInjection("ref", ref); err != nil {
			return Result{}, err
		}
		args = append(args, ref)
	}

	stdout, stderr, exit, err := runGit(ctx, req.ProjectRoot, timeout, args...)
	if err != nil {
		return Result{Output: joinGitOutput(stdout, stderr), Data: map[string]any{"exit_code": exit}}, err
	}
	return Result{
		Output: strings.TrimSpace(joinGitOutput(stdout, stderr)),
		Data: map[string]any{
			"path":      filepath.ToSlash(absPath),
			"exit_code": exit,
		},
	}, nil
}

func (t *GitWorktreeAddTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "git_worktree_add",
		Title:   "Add git worktree",
		Summary: "Create a new linked worktree at <path> for an existing ref or new branch.",
		Purpose: "Work on a second branch in parallel without juggling stashes.",
		Prompt: `Spawns a linked worktree. If ` + "`new_branch`" + ` is set, the worktree is created on a fresh branch forked from ` + "`ref`" + ` (default HEAD).

Rules:
- ` + "`path`" + ` must resolve inside the project root; the tool rejects escape attempts.
- Will NOT create a worktree on a branch that already has one (git's own guardrail).
- Branch names with ` + "`..`" + `, ` + "`~`" + `, ` + "`^`" + `, ` + "`:`" + ` or whitespace are rejected up front to avoid ref parsing ambiguity.
- Removing the worktree later is ` + "`git_worktree_remove`" + `, not a bare ` + "`rm -rf`" + `.`,
		Risk: RiskWrite,
		Tags: []string{"git", "vcs", "worktree", "write"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Target directory (must stay inside project root)."},
			{Name: "ref", Type: ArgString, Description: "Branch, tag, or commit to check out (default HEAD)."},
			{Name: "new_branch", Type: ArgString, Description: "If set, create a new branch with this name and check it out."},
		},
		Returns:  "{path, exit_code}.",
		CostHint: "io-bound",
	}
}

type GitWorktreeRemoveTool struct{}

func NewGitWorktreeRemoveTool() *GitWorktreeRemoveTool { return &GitWorktreeRemoveTool{} }
func (t *GitWorktreeRemoveTool) Name() string          { return "git_worktree_remove" }
func (t *GitWorktreeRemoveTool) Description() string {
	return "Remove a previously added git worktree."
}

func (t *GitWorktreeRemoveTool) Execute(ctx context.Context, req Request) (Result, error) {
	timeout := resolveGitTimeout(req.Params)
	path := strings.TrimSpace(asString(req.Params, "path", ""))
	if path == "" {
		return Result{}, missingParamError("git_worktree_remove", "path", req.Params,
			`{"path":"../wt-feature"} or {"path":"../wt-feature","force":true}`,
			`path is the worktree directory to remove (use git_worktree_list to see what exists). Pass force=true to remove a worktree with uncommitted changes — destructive, use sparingly.`)
	}
	if err := rejectGitFlagInjection("path", path); err != nil {
		return Result{}, err
	}
	// Worktrees may live outside the project root if the user created them
	// that way — but we still require the caller to pass a path we can
	// verify exists in `worktree list`.
	args := []string{"worktree", "remove"}
	if asBool(req.Params, "force", false) {
		args = append(args, "--force")
	}
	args = append(args, path)
	stdout, stderr, exit, err := runGit(ctx, req.ProjectRoot, timeout, args...)
	if err != nil {
		return Result{Output: joinGitOutput(stdout, stderr), Data: map[string]any{"exit_code": exit}}, err
	}
	return Result{
		Output: strings.TrimSpace(joinGitOutput(stdout, stderr)),
		Data: map[string]any{
			"path":      filepath.ToSlash(path),
			"exit_code": exit,
		},
	}, nil
}

func (t *GitWorktreeRemoveTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "git_worktree_remove",
		Title:   "Remove git worktree",
		Summary: "Detach and delete a linked worktree.",
		Purpose: "Clean up a worktree when its branch is merged or abandoned.",
		Prompt: `Wraps ` + "`git worktree remove`" + `. Pass ` + "`force=true`" + ` only when the worktree is known-dirty and you've confirmed there's nothing worth keeping.

Rules:
- Fails by default if the worktree has uncommitted changes — ` + "`force=true`" + ` overrides, but only do that with the user's blessing.
- Does NOT delete branches; to remove the branch too, use ` + "`run_command`" + ` with ` + "`git branch -d <name>`" + ` (never ` + "`-D`" + ` without authorization).`,
		Risk: RiskWrite,
		Tags: []string{"git", "vcs", "worktree", "write"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Path of the worktree to remove (as reported by git_worktree_list)."},
			{Name: "force", Type: ArgBoolean, Description: "Force-remove even with local changes.", Default: false},
		},
		Returns:  "{path, exit_code}.",
		CostHint: "io-bound",
	}
}
