package tools

// git_log.go — git_branch + git_log read-only tools. Sibling of
// git.go which keeps git_status + git_diff and the small parsing
// helpers they share. The shared runner (runGit, blockedGitArg,
// rejectGitFlagInjection, the timeout/allowlist helpers, the
// output/argv utilities) lives in git_runner.go.
//
// These two tools live together because they're both
// branch/commit-shaped read tools that consume the same runGit
// surface and produce structured per-entry data.

import (
	"context"
	"fmt"
	"strings"
)

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
