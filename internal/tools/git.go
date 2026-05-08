package tools

// git.go — first-class git surface that keeps the model out of shell quoting
// hell. Every tool routes through runGit(), which pins the working directory
// to the project root, applies a timeout, refuses destructive flags, and
// returns structured data alongside the raw output. Prefer these over
// run_command + "git ..." because the blocklist + argv construction are
// handled for you.
//
// The shared runner (runGit, blockedGitArg, rejectGitFlagInjection, the
// timeout/allowlist helpers, and the small output/argv utilities) lives
// in git_runner.go; this file holds the read-only tool implementations:
// git_status, git_diff, git_branch, git_log.

import (
	"context"
	"path/filepath"
	"strings"
)

// ---------------------------------------------------------------------------
// git_status
// ---------------------------------------------------------------------------

type GitStatusTool struct{}

func NewGitStatusTool() *GitStatusTool { return &GitStatusTool{} }
func (t *GitStatusTool) Name() string  { return "git_status" }
func (t *GitStatusTool) Description() string {
	return "Show the working tree status in porcelain form."
}

func (t *GitStatusTool) Execute(ctx context.Context, req Request) (Result, error) {
	timeout := resolveGitTimeout(req.Params)
	args := []string{"status", "--porcelain=v1", "--branch"}
	allowed := map[string]struct{}{}
	if asBool(req.Params, "force", false) {
		allowed["--force"] = struct{}{}
	}
	stdout, stderr, exit, err := runGitWithAllowedBlockedArgs(ctx, req.ProjectRoot, timeout, allowed, args...)
	if err != nil {
		return Result{Output: joinGitOutput(stdout, stderr), Data: map[string]any{"exit_code": exit}}, err
	}
	branch, staged, modified, untracked := parseGitStatus(stdout)
	return Result{
		Output: strings.TrimSpace(stdout),
		Data: map[string]any{
			"branch":    branch,
			"staged":    staged,
			"modified":  modified,
			"untracked": untracked,
			"clean":     len(staged) == 0 && len(modified) == 0 && len(untracked) == 0,
			"exit_code": exit,
		},
	}, nil
}

func (t *GitStatusTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "git_status",
		Title:   "Git status",
		Summary: "Working-tree status in porcelain v1 with branch line.",
		Purpose: "Check what's staged, modified, or untracked before committing or reviewing changes.",
		Prompt: `Preferred over ` + "`run_command git status`" + `. Returns structured {branch, staged[], modified[], untracked[], clean} so you don't have to parse porcelain yourself.

Rules:
- Read-only; safe to call freely.
- If ` + "`clean=true`" + `, there is nothing to commit — don't fabricate a diff.
- The ` + "`branch`" + ` field comes from the ` + "`## branch`" + ` porcelain line; may include ahead/behind counts.`,
		Risk:       RiskRead,
		Tags:       []string{"git", "vcs", "read"},
		Returns:    "{branch, staged[], modified[], untracked[], clean, exit_code}",
		Idempotent: true,
		CostHint:   "cheap",
	}
}

// parseGitStatus reads `git status --porcelain=v1 --branch` output.
// Lines look like:
//
//	## main...origin/main [ahead 1]
//	M  path/to/staged.go
//	 M path/to/modified.go
//	?? path/to/untracked.go
func parseGitStatus(raw string) (branch string, staged, modified, untracked []string) {
	staged = []string{}
	modified = []string{}
	untracked = []string{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "## ") {
			branch = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			continue
		}
		if len(line) < 3 {
			continue
		}
		xy := line[:2]
		path := strings.TrimSpace(line[3:])
		path = filepath.ToSlash(path)
		switch {
		case xy == "??":
			untracked = append(untracked, path)
		case xy[0] != ' ' && xy[0] != '?':
			staged = append(staged, path)
			if xy[1] != ' ' {
				modified = append(modified, path)
			}
		case xy[1] != ' ':
			modified = append(modified, path)
		}
	}
	return branch, staged, modified, untracked
}

// ---------------------------------------------------------------------------
// git_diff
// ---------------------------------------------------------------------------

type GitDiffTool struct{}

func NewGitDiffTool() *GitDiffTool  { return &GitDiffTool{} }
func (t *GitDiffTool) Name() string { return "git_diff" }
func (t *GitDiffTool) Description() string {
	return "Show a unified diff for the working tree or a revision."
}

func (t *GitDiffTool) Execute(ctx context.Context, req Request) (Result, error) {
	timeout := resolveGitTimeout(req.Params)
	args := []string{"diff", "--no-color"}
	if asBool(req.Params, "staged", false) || asBool(req.Params, "cached", false) {
		args = append(args, "--cached")
	}
	if asBool(req.Params, "stat", false) {
		args = append(args, "--stat")
	}
	if rev := strings.TrimSpace(asString(req.Params, "revision", "")); rev != "" {
		if err := rejectGitFlagInjection("revision", rev); err != nil {
			return Result{}, err
		}
		args = append(args, rev)
	}
	if paths := stringSliceArg(req.Params["paths"]); len(paths) > 0 {
		for _, p := range paths {
			if err := rejectGitFlagInjection("path", p); err != nil {
				return Result{}, err
			}
		}
		args = append(args, "--")
		args = append(args, paths...)
	} else if p := strings.TrimSpace(asString(req.Params, "path", "")); p != "" {
		if err := rejectGitFlagInjection("path", p); err != nil {
			return Result{}, err
		}
		args = append(args, "--", p)
	}
	stdout, stderr, exit, err := runGit(ctx, req.ProjectRoot, timeout, args...)
	if err != nil {
		return Result{Output: joinGitOutput(stdout, stderr), Data: map[string]any{"exit_code": exit}}, err
	}
	return Result{
		Output: strings.TrimRight(stdout, "\n"),
		Data: map[string]any{
			"bytes":     len(stdout),
			"exit_code": exit,
			"empty":     strings.TrimSpace(stdout) == "",
		},
	}, nil
}

func (t *GitDiffTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "git_diff",
		Title:   "Git diff",
		Summary: "Unified diff of the working tree, a revision, or a path subset.",
		Purpose: "Review changes before committing, or inspect a specific revision/path.",
		Prompt: `Preferred over ` + "`run_command git diff`" + `. Supports ` + "`staged=true`" + ` for ` + "`--cached`" + `, ` + "`stat=true`" + ` for the summary line, and scoping via ` + "`path`" + ` or ` + "`paths[]`" + `.

Rules:
- ` + "`empty=true`" + ` in the data means no differences — do not hallucinate hunks.
- Pass ` + "`revision`" + ` to diff against a ref (e.g. ` + "`HEAD~1`" + `, ` + "`main..HEAD`" + `).
- For per-file context, use ` + "`path`" + ` or ` + "`paths[]`" + ` to scope the output.`,
		Risk: RiskRead,
		Tags: []string{"git", "vcs", "diff", "read"},
		Args: []Arg{
			{Name: "staged", Type: ArgBoolean, Description: "Diff the staging area instead of the working tree.", Default: false},
			{Name: "stat", Type: ArgBoolean, Description: "Show --stat summary line.", Default: false},
			{Name: "revision", Type: ArgString, Description: "Optional revision range (e.g. main..HEAD)."},
			{Name: "path", Type: ArgString, Description: "Restrict diff to a single path."},
			{Name: "paths", Type: ArgArray, Description: "Restrict diff to multiple paths.", Items: &Arg{Type: ArgString}},
		},
		Returns:    "Diff text plus {bytes, empty, exit_code}.",
		Idempotent: true,
		CostHint:   "cheap",
	}
}

// GitBranchTool + GitLogTool live in git_log.go.
