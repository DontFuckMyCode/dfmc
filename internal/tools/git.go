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
	"fmt"
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

// ---------------------------------------------------------------------------
// git_branch
// ---------------------------------------------------------------------------

type GitBranchTool struct{}

func NewGitBranchTool() *GitBranchTool { return &GitBranchTool{} }
func (t *GitBranchTool) Name() string  { return "git_branch" }
func (t *GitBranchTool) Description() string {
	return "List local branches and report the current branch."
}

func (t *GitBranchTool) Execute(ctx context.Context, req Request) (Result, error) {
	timeout := resolveGitTimeout(req.Params)
	// Current branch.
	curOut, curErr, curExit, curCmdErr := runGit(ctx, req.ProjectRoot, timeout, "rev-parse", "--abbrev-ref", "HEAD")
	if curCmdErr != nil {
		return Result{Output: joinGitOutput(curOut, curErr), Data: map[string]any{"exit_code": curExit}}, curCmdErr
	}
	current := strings.TrimSpace(curOut)

	// All local branches.
	listOut, listErr, listExit, listCmdErr := runGit(ctx, req.ProjectRoot, timeout, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if listCmdErr != nil {
		return Result{Output: joinGitOutput(listOut, listErr), Data: map[string]any{"exit_code": listExit}}, listCmdErr
	}
	local := splitLines(listOut)

	include := asBool(req.Params, "include_remote", false)
	var remote []string
	if include {
		remoteOut, _, _, rErr := runGit(ctx, req.ProjectRoot, timeout, "for-each-ref", "--format=%(refname:short)", "refs/remotes")
		if rErr == nil {
			remote = splitLines(remoteOut)
		}
	}

	return Result{
		Output: strings.Join(local, "\n"),
		Data: map[string]any{
			"current": current,
			"local":   local,
			"remote":  remote,
			"count":   len(local),
		},
	}, nil
}

func (t *GitBranchTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "git_branch",
		Title:   "Git branch",
		Summary: "List branches and report current HEAD.",
		Purpose: "Discover which branch you're on and what local branches exist.",
		Prompt: `Returns {current, local[], remote[], count}. Pass ` + "`include_remote=true`" + ` to fetch refs/remotes too.

Rules:
- Read-only; does not check out or create branches.
- Prefer this over ` + "`run_command git branch`" + ` — structured output skips parsing the ` + "`* `" + ` prefix.`,
		Risk: RiskRead,
		Tags: []string{"git", "vcs", "read"},
		Args: []Arg{
			{Name: "include_remote", Type: ArgBoolean, Description: "Also list refs/remotes.", Default: false},
		},
		Returns:    "{current, local[], remote[], count}.",
		Idempotent: true,
		CostHint:   "cheap",
	}
}

// ---------------------------------------------------------------------------
// git_log
// ---------------------------------------------------------------------------

type GitLogTool struct{}

func NewGitLogTool() *GitLogTool          { return &GitLogTool{} }
func (t *GitLogTool) Name() string        { return "git_log" }
func (t *GitLogTool) Description() string { return "Recent commit history as structured entries." }

func (t *GitLogTool) Execute(ctx context.Context, req Request) (Result, error) {
	timeout := resolveGitTimeout(req.Params)
	limit := asInt(req.Params, "limit", 20)
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	// Use a format with ASCII-safe separators that are unlikely to appear in
	// subject lines.
	const sep = "\x1f"
	const rec = "\x1e"
	format := fmt.Sprintf("--pretty=format:%%H%s%%an%s%%s%s", sep, sep, rec)
	args := []string{"log", fmt.Sprintf("-n%d", limit), format, "--date=iso", "--no-color"}
	if rev := strings.TrimSpace(asString(req.Params, "revision", "")); rev != "" {
		if err := rejectGitFlagInjection("revision", rev); err != nil {
			return Result{}, err
		}
		args = append(args, rev)
	}
	if p := strings.TrimSpace(asString(req.Params, "path", "")); p != "" {
		if err := rejectGitFlagInjection("path", p); err != nil {
			return Result{}, err
		}
		args = append(args, "--", p)
	}
	stdout, stderr, exit, err := runGit(ctx, req.ProjectRoot, timeout, args...)
	if err != nil {
		return Result{Output: joinGitOutput(stdout, stderr), Data: map[string]any{"exit_code": exit}}, err
	}

	// The trailing format marker is inside the pretty string, so each commit
	// contributes "<hash>SEP<author>SEPREC" plus the subject that git inserts
	// before the next record. We use a much simpler machine-readable format
	// for robustness.
	return Result{
		Output: strings.TrimRight(stdout, "\n"),
		Data: map[string]any{
			"limit":     limit,
			"commits":   parseGitLog(stdout, sep, rec),
			"exit_code": exit,
		},
	}, nil
}

func parseGitLog(raw, sep, rec string) []map[string]string {
	out := make([]map[string]string, 0, 16)
	for _, entry := range strings.Split(raw, rec) {
		entry = strings.Trim(entry, "\n ")
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, sep, 3)
		if len(parts) < 2 {
			continue
		}
		commit := map[string]string{
			"hash":   strings.TrimSpace(parts[0]),
			"author": strings.TrimSpace(parts[1]),
		}
		if len(parts) >= 3 {
			commit["subject"] = strings.TrimSpace(parts[2])
		}
		out = append(out, commit)
	}
	return out
}

func (t *GitLogTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "git_log",
		Title:   "Git log",
		Summary: "Recent commits as {hash, author, subject} entries.",
		Purpose: "Inspect recent history on the current branch or a revision range.",
		Prompt: `Returns {limit, commits[]}. Default limit is 20, cap 200. Pass ` + "`revision`" + ` for a range (` + "`main..HEAD`" + `) or ` + "`path`" + ` to scope to one file.

Rules:
- Read-only.
- For a full textual log with bodies, use ` + "`run_command git show`" + ` with a specific commit — ` + "`git_log`" + ` is the index, not the detail view.`,
		Risk: RiskRead,
		Tags: []string{"git", "vcs", "read", "history"},
		Args: []Arg{
			{Name: "limit", Type: ArgInteger, Description: "Max commits to return (default 20, cap 200).", Default: 20},
			{Name: "revision", Type: ArgString, Description: "Optional revision or revision range."},
			{Name: "path", Type: ArgString, Description: "Restrict log to a single path."},
		},
		Returns:    "{limit, commits[{hash,author,subject}], exit_code}.",
		Idempotent: true,
		CostHint:   "cheap",
	}
}

