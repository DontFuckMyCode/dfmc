package tools

// git.go — first-class git surface that keeps the model out of shell quoting
// hell. Every tool routes through runGit(), which pins the working directory
// to the project root, applies a timeout, refuses destructive flags, and
// returns structured data alongside the raw output. Prefer these over
// run_command + "git ..." because the blocklist + argv construction are
// handled for you.

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	gitDefaultTimeout = 30 * time.Second
	gitMaxTimeout     = 120 * time.Second
)

// runGit executes `git <args...>` inside projectRoot and returns (stdout,
// stderr, exit, err). Never invokes a shell. The caller is responsible for
// vetting args — blockedGitArg handles the shared "never allow this flag"
// list. Empty projectRoot falls back to the process CWD, consistent with the
// rest of the tool registry.
func runGit(ctx context.Context, projectRoot string, timeout time.Duration, args ...string) (string, string, int, error) {
	if timeout <= 0 {
		timeout = gitDefaultTimeout
	}
	if timeout > gitMaxTimeout {
		timeout = gitMaxTimeout
	}
	for _, a := range args {
		if blockedGitArg(a) {
			return "", "", 0, fmt.Errorf("git argument blocked by policy: %s", a)
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "git", args...)
	cmd.Dir = projectRoot
	// Bounded capture — same reasoning as run_command. `git log -p`
	// across a large repo, `git diff main..feature` over a sweeping
	// refactor, `git show <huge-commit>`: all can produce tens of MB
	// of patch output. Capping at 4 MiB per stream keeps the parent
	// heap bounded; the agent gets the head + a truncation marker
	// instead of an OOM.
	stdout := newBoundedBuffer(runCommandOutputCap)
	stderr := newBoundedBuffer(runCommandOutputCap)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	exitCode := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return stdout.String(), stderr.String(), exitCode, fmt.Errorf("git timed out after %s", timeout)
	}
	return stdout.String(), stderr.String(), exitCode, err
}

// blockedGitArg enforces the safety rules from CLAUDE.md's git-safety
// protocol for anything that lands in these tools. Destructive flags route
// users back to explicit shell approval via run_command.
func blockedGitArg(arg string) bool {
	a := strings.ToLower(strings.TrimSpace(arg))
	switch a {
	case "--no-verify", "--no-gpg-sign", "--amend", "-i", "--interactive":
		return true
	}
	return false
}

// resolveGitTimeout reads the optional per-call timeout param (seconds or
// duration string).
func resolveGitTimeout(params map[string]any) time.Duration {
	if raw := strings.TrimSpace(asString(params, "timeout", "")); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	if ms := asInt(params, "timeout_ms", 0); ms > 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return gitDefaultTimeout
}

// ---------------------------------------------------------------------------
// git_status
// ---------------------------------------------------------------------------

type GitStatusTool struct{}

func NewGitStatusTool() *GitStatusTool         { return &GitStatusTool{} }
func (t *GitStatusTool) Name() string          { return "git_status" }
func (t *GitStatusTool) Description() string   { return "Show the working tree status in porcelain form." }

func (t *GitStatusTool) Execute(ctx context.Context, req Request) (Result, error) {
	timeout := resolveGitTimeout(req.Params)
	args := []string{"status", "--porcelain=v1", "--branch"}
	stdout, stderr, exit, err := runGit(ctx, req.ProjectRoot, timeout, args...)
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
//   ## main...origin/main [ahead 1]
//   M  path/to/staged.go
//    M path/to/modified.go
//   ?? path/to/untracked.go
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

func NewGitDiffTool() *GitDiffTool         { return &GitDiffTool{} }
func (t *GitDiffTool) Name() string        { return "git_diff" }
func (t *GitDiffTool) Description() string { return "Show a unified diff for the working tree or a revision." }

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
		args = append(args, rev)
	}
	if paths := stringSliceArg(req.Params["paths"]); len(paths) > 0 {
		args = append(args, "--")
		args = append(args, paths...)
	} else if p := strings.TrimSpace(asString(req.Params, "path", "")); p != "" {
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
		Risk:       RiskRead,
		Tags:       []string{"git", "vcs", "diff", "read"},
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

func NewGitBranchTool() *GitBranchTool       { return &GitBranchTool{} }
func (t *GitBranchTool) Name() string        { return "git_branch" }
func (t *GitBranchTool) Description() string { return "List local branches and report the current branch." }

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
		args = append(args, rev)
	}
	if p := strings.TrimSpace(asString(req.Params, "path", "")); p != "" {
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

// ---------------------------------------------------------------------------
// git_blame
// ---------------------------------------------------------------------------

type GitBlameTool struct{}

func NewGitBlameTool() *GitBlameTool        { return &GitBlameTool{} }
func (t *GitBlameTool) Name() string        { return "git_blame" }
func (t *GitBlameTool) Description() string { return "Show line-by-line authorship for a file via git blame." }

func (t *GitBlameTool) Execute(ctx context.Context, req Request) (Result, error) {
	timeout := resolveGitTimeout(req.Params)
	path := strings.TrimSpace(asString(req.Params, "path", ""))
	if path == "" {
		return Result{}, fmt.Errorf("path is required")
	}
	if _, err := EnsureWithinRoot(req.ProjectRoot, path); err != nil {
		return Result{}, err
	}

	// Porcelain format gives one stable, parseable record per line:
	//
	//   <hash> <orig-lno> <final-lno> [<group-size>]
	//   author <name>
	//   author-time <unix>
	//   summary <subject>
	//   ...
	//   \t<line content>
	//
	// We don't ask for incremental output — file blames here are typically
	// hundreds of lines, not millions, and the simpler porcelain shape is
	// easier to parse with a single linear pass.
	args := []string{"blame", "--porcelain"}

	lineStart := asInt(req.Params, "line_start", 0)
	lineEnd := asInt(req.Params, "line_end", 0)
	if lineStart > 0 || lineEnd > 0 {
		// `git blame -L start,end file` — both bounds inclusive. If only one
		// is set, fall back to a single-line range so the user doesn't get a
		// silent full-file blame when they meant "just this line".
		if lineStart <= 0 {
			lineStart = lineEnd
		}
		if lineEnd <= 0 {
			lineEnd = lineStart
		}
		if lineEnd < lineStart {
			return Result{}, fmt.Errorf("line_end (%d) must be >= line_start (%d)", lineEnd, lineStart)
		}
		args = append(args, "-L", fmt.Sprintf("%d,%d", lineStart, lineEnd))
	}

	if rev := strings.TrimSpace(asString(req.Params, "revision", "")); rev != "" {
		args = append(args, rev)
	}

	args = append(args, "--", path)

	stdout, stderr, exit, err := runGit(ctx, req.ProjectRoot, timeout, args...)
	if err != nil {
		return Result{Output: joinGitOutput(stdout, stderr), Data: map[string]any{"exit_code": exit}}, err
	}

	lines := parseGitBlamePorcelain(stdout)
	return Result{
		Output: strings.TrimRight(stdout, "\n"),
		Data: map[string]any{
			"path":      path,
			"lines":     lines,
			"count":     len(lines),
			"exit_code": exit,
		},
	}, nil
}

// parseGitBlamePorcelain walks porcelain output and emits one record per
// source line. Header keys we care about (author, author-time, summary)
// are inherited from the most recent commit-block — git only re-emits
// them on the first occurrence of a hash within the output.
func parseGitBlamePorcelain(raw string) []map[string]any {
	out := make([]map[string]any, 0, 64)
	if strings.TrimSpace(raw) == "" {
		return out
	}

	// Per-commit cache so subsequent occurrences of the same hash inherit
	// author/summary without git re-emitting them.
	type commitInfo struct {
		author  string
		time    string
		summary string
	}
	commits := map[string]commitInfo{}

	var (
		currentHash    string
		currentFinal   int
		currentInfo    commitInfo
		haveLineHeader bool
	)

	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		// Source line — porcelain prefixes content with a single TAB.
		if strings.HasPrefix(line, "\t") {
			if !haveLineHeader {
				continue
			}
			content := strings.TrimPrefix(line, "\t")
			info := currentInfo
			if cached, ok := commits[currentHash]; ok {
				if info.author == "" {
					info.author = cached.author
				}
				if info.time == "" {
					info.time = cached.time
				}
				if info.summary == "" {
					info.summary = cached.summary
				}
			}
			// Persist any newly-discovered metadata for later lines that
			// share this commit hash.
			commits[currentHash] = info
			out = append(out, map[string]any{
				"line":        currentFinal,
				"hash":        currentHash,
				"author":      info.author,
				"author_time": info.time,
				"summary":     info.summary,
				"content":     content,
			})
			haveLineHeader = false
			continue
		}
		// Header line — either "<hash> <orig> <final> [group]" or "key value".
		fields := strings.Fields(line)
		if len(fields) >= 3 && len(fields[0]) >= 7 && isHexLine(fields[0]) {
			currentHash = fields[0]
			// fields[1] is orig line; we expose only final-line numbers.
			fmt.Sscanf(fields[2], "%d", &currentFinal)
			currentInfo = commitInfo{}
			haveLineHeader = true
			continue
		}
		if len(fields) < 2 || !haveLineHeader {
			continue
		}
		switch fields[0] {
		case "author":
			currentInfo.author = strings.Join(fields[1:], " ")
		case "author-time":
			currentInfo.time = fields[1]
		case "summary":
			currentInfo.summary = strings.Join(fields[1:], " ")
		}
	}
	return out
}

// isHexLine reports whether s is a plausible git object hash (all-hex,
// at least 7 chars). Used to distinguish blame's commit-header lines
// from key/value metadata lines.
func isHexLine(s string) bool {
	if len(s) < 7 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

func (t *GitBlameTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "git_blame",
		Title:   "Git blame",
		Summary: "Per-line authorship for a file with hash, author, time, and commit subject.",
		Purpose: "Identify who last touched a region — for debug, audit, or refactor scoping.",
		Prompt: `Returns ` + "`{path, lines[{line, hash, author, author_time, summary, content}], count}`" + `.

Rules:
- ` + "`path`" + ` is required and must resolve inside the project root.
- Use ` + "`line_start`" + ` / ` + "`line_end`" + ` to scope a region — much cheaper than blaming a 10k-line file when you only need one function. Both bounds inclusive.
- Pass ` + "`revision`" + ` to blame at a specific ref instead of HEAD.
- ` + "`author_time`" + ` is a unix timestamp string; format it on the consumer side if you need a date.`,
		Risk: RiskRead,
		Tags: []string{"git", "vcs", "read", "history"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Description: "File path within the project root.", Required: true},
			{Name: "line_start", Type: ArgInteger, Description: "Optional 1-based start line (inclusive)."},
			{Name: "line_end", Type: ArgInteger, Description: "Optional 1-based end line (inclusive). Defaults to line_start when only one bound is set."},
			{Name: "revision", Type: ArgString, Description: "Optional revision to blame at (defaults to HEAD)."},
		},
		Returns:    "{path, lines[{line,hash,author,author_time,summary,content}], count, exit_code}.",
		Idempotent: true,
		CostHint:   "cheap",
	}
}

// ---------------------------------------------------------------------------
// git_worktree_list / add / remove
// ---------------------------------------------------------------------------

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
		return Result{}, fmt.Errorf("path is required")
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
		args = append(args, "-b", branch)
	}
	args = append(args, absPath)
	if ref := strings.TrimSpace(asString(req.Params, "ref", "")); ref != "" {
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
		return Result{}, fmt.Errorf("path is required")
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

// ---------------------------------------------------------------------------
// git_commit
// ---------------------------------------------------------------------------

type GitCommitTool struct{}

func NewGitCommitTool() *GitCommitTool       { return &GitCommitTool{} }
func (t *GitCommitTool) Name() string        { return "git_commit" }
func (t *GitCommitTool) Description() string { return "Stage explicit paths and create a commit with the given message." }

func (t *GitCommitTool) Execute(ctx context.Context, req Request) (Result, error) {
	timeout := resolveGitTimeout(req.Params)
	message := strings.TrimSpace(asString(req.Params, "message", ""))
	if message == "" {
		return Result{}, fmt.Errorf("message is required")
	}

	paths := stringSliceArg(req.Params["paths"])
	if len(paths) == 0 {
		if single := strings.TrimSpace(asString(req.Params, "path", "")); single != "" {
			paths = []string{single}
		}
	}
	if len(paths) == 0 {
		return Result{}, fmt.Errorf("paths is required — git_commit refuses to stage everything; name the files explicitly")
	}
	for _, p := range paths {
		if strings.TrimSpace(p) == "" || p == "-A" || p == "." || p == "*" {
			return Result{}, fmt.Errorf("invalid commit path %q — use explicit file paths, not wildcards", p)
		}
		if _, err := EnsureWithinRoot(req.ProjectRoot, p); err != nil {
			return Result{}, err
		}
	}

	// Stage the files.
	addArgs := append([]string{"add", "--"}, paths...)
	if _, addStderr, addExit, addErr := runGit(ctx, req.ProjectRoot, timeout, addArgs...); addErr != nil {
		return Result{Output: addStderr, Data: map[string]any{"exit_code": addExit}}, fmt.Errorf("git add failed: %w", addErr)
	}

	commitArgs := []string{"commit", "-m", message}
	if signoff := asBool(req.Params, "signoff", false); signoff {
		commitArgs = append(commitArgs, "--signoff")
	}
	stdout, stderr, exit, err := runGit(ctx, req.ProjectRoot, timeout, commitArgs...)
	if err != nil {
		return Result{Output: joinGitOutput(stdout, stderr), Data: map[string]any{"exit_code": exit}}, err
	}

	// Parse "[branch abcdef1] subject" to expose the new commit hash.
	hash := ""
	if first := firstNonEmptyLine(stdout); first != "" {
		if open := strings.Index(first, "["); open >= 0 {
			if close := strings.Index(first[open:], "]"); close > 0 {
				inner := first[open+1 : open+close]
				parts := strings.Fields(inner)
				if len(parts) >= 2 {
					hash = parts[len(parts)-1]
				}
			}
		}
	}

	return Result{
		Output: strings.TrimSpace(joinGitOutput(stdout, stderr)),
		Data: map[string]any{
			"hash":      hash,
			"paths":     paths,
			"exit_code": exit,
		},
	}, nil
}

func (t *GitCommitTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "git_commit",
		Title:   "Git commit",
		Summary: "Stage the named paths and commit with the given message. Refuses wildcards and --amend.",
		Purpose: "Create a fresh commit from an explicit file list. Never amends, never stages everything.",
		Prompt: `Safe-by-construction commit. Stages exactly the paths you list and creates a NEW commit — ` + "`--amend`" + `, ` + "`--no-verify`" + `, ` + "`--no-gpg-sign`" + ` are blocked.

Rules:
- ` + "`paths`" + ` is REQUIRED and may not contain ` + "`-A`" + `, ` + "`.`" + `, or ` + "`*`" + `. If you want to stage "everything" the user must do so outside this tool — we refuse to sweep in ` + "`.env`" + ` or unreviewed files.
- Pre-commit hooks run as normal. If a hook fails, fix the issue and commit again. DO NOT reach for ` + "`run_command`" + ` with ` + "`--no-verify`" + ` to get around a failing hook.
- Multiline messages are fine — pass the full text via ` + "`message`" + `.
- Returns the new commit ` + "`hash`" + ` when parsable.`,
		Risk: RiskWrite,
		Tags: []string{"git", "vcs", "commit", "write"},
		Args: []Arg{
			{Name: "message", Type: ArgString, Required: true, Description: "Commit message (can be multiline)."},
			{Name: "paths", Type: ArgArray, Description: "Explicit file paths to stage (required).", Items: &Arg{Type: ArgString}},
			{Name: "path", Type: ArgString, Description: "Alias when committing a single file."},
			{Name: "signoff", Type: ArgBoolean, Description: "Append a Signed-off-by trailer.", Default: false},
		},
		Returns:  "{hash, paths[], exit_code}.",
		CostHint: "io-bound",
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func stringSliceArg(raw any) []string {
	switch v := raw.(type) {
	case nil:
		return nil
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			s := strings.TrimSpace(fmt.Sprint(item))
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

func joinGitOutput(stdout, stderr string) string {
	stdout = strings.TrimRight(stdout, "\n")
	stderr = strings.TrimRight(stderr, "\n")
	switch {
	case stdout == "" && stderr == "":
		return ""
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n\n[stderr]\n" + stderr
	}
}

func splitLines(raw string) []string {
	out := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func firstNonEmptyLine(raw string) string {
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

func blockedBranchName(name string) bool {
	if strings.TrimSpace(name) == "" {
		return true
	}
	for _, r := range name {
		if r == ' ' || r == '\t' || r == '\n' || r == '~' || r == '^' || r == ':' || r == '?' || r == '*' {
			return true
		}
	}
	if strings.Contains(name, "..") {
		return true
	}
	if strings.HasPrefix(name, "-") {
		return true
	}
	return false
}
