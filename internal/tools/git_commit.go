// git_commit.go — the single write-path tool in the git surface. Keeps
// its own file because commit has more policy than the read-only tools:
// explicit paths only (no `-A` / `.` / `*`), --amend / --no-verify /
// --no-gpg-sign are blocklisted upstream in runGit(), and the hash is
// parsed out of the porcelain "[branch abcdef1] subject" header.

package tools

import (
	"context"
	"fmt"
	"strings"
)

type GitCommitTool struct{}

func NewGitCommitTool() *GitCommitTool { return &GitCommitTool{} }
func (t *GitCommitTool) Name() string  { return "git_commit" }
func (t *GitCommitTool) Description() string {
	return "Stage explicit paths and create a commit with the given message."
}

func (t *GitCommitTool) Execute(ctx context.Context, req Request) (Result, error) {
	timeout := resolveGitTimeout(req.Params)
	message := strings.TrimSpace(asString(req.Params, "message", ""))
	if message == "" {
		return Result{}, missingParamError("git_commit", "message", req.Params,
			`{"message":"feat(engine): add park-and-resume","paths":["internal/engine/agent_loop.go","internal/engine/agent_parking.go"]}`,
			`message is the commit subject (and body, multi-line is fine). Optional paths is a string[] of files to stage; without it, only previously-staged changes are committed. To stage everything use {"paths":["."]}.`)
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
		if err := rejectGitFlagInjection("path", p); err != nil {
			return Result{}, err
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
