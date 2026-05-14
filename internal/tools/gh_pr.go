// gh_pr.go — structured PR query and review-summary tool built on `gh pr`.
// Part of the Phase 7 structured git/PR tool surface expansion.
//
// Scope: read-only PR queries (view, list, diff, checks, status).
// Write operations (comment, close, merge) are intentionally omitted —
// use run_command with explicit approval for those.
//
// File layout: this file owns the tool surface (Spec, Execute, the
// per-action listPRs / viewPR / diffPR / checksPR / statusPR methods)
// and the public types. JSON parsers + auth check
// (parsePRListJSON / parsePRViewJSON / parsePRReviewsJSON /
// parseCheckStatus / checkGH_auth) and resolvePRAction live in
// gh_pr_parse.go.
package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type GHPullRequestTool struct{}

func NewGHPullRequestTool() *GHPullRequestTool { return &GHPullRequestTool{} }

func (t *GHPullRequestTool) Name() string { return "gh_pr" }
func (t *GHPullRequestTool) Description() string {
	return "Query GitHub pull requests: list, view, diff, checks, and status summaries."
}

// GHPullRequestSpec is the structured output shape for gh_pr.
// Stored in Result.Data["pr"] as a map[string]any.
type GHPullRequestSpec struct {
	Number       int                   `json:"number"`
	Title        string                `json:"title"`
	State        string                `json:"state"` // open, closed, merged, draft
	HeadRef      string                `json:"head_ref"`
	BaseRef      string                `json:"base_ref"`
	Author       string                `json:"author"`
	URL          string                `json:"url"`
	Body         string                `json:"body,omitempty"`
	Additions    int                   `json:"additions,omitempty"`
	Deletions    int                   `json:"deletions,omitempty"`
	ChangedFiles int                   `json:"changed_files,omitempty"`
	Reviews      []GHPullRequestReview `json:"reviews,omitempty"`
	CheckStatus  string                `json:"check_status"` // pending, pass, fail, unknown
	Comments     int                   `json:"comments"`
	Commits      int                   `json:"commits,omitempty"`
}

// GHPullRequestReview is the per-reviewer summary.
type GHPullRequestReview struct {
	Author    string `json:"author"`
	State     string `json:"state"` // APPROVED, CHANGES_REQUESTED, COMMENTED, DISMISSED, PENDING
	Submitted string `json:"submitted_at,omitempty"`
}

func (t *GHPullRequestTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "gh_pr",
		Title:   "GitHub pull request",
		Summary: "Structured PR summary with reviews, checks, and diff stats.",
		Purpose: `Use to understand the current state of a PR without opening a browser: who approved, what CI thinks, how many files changed, and what the diff summary looks like. Structured output means the model can reason over it rather than parsing prose.`,
		Prompt: `GitHub pull request query tool. Handles:
- "list": list open PRs (default 10, configurable via limit)
- "view": full PR summary with reviews and check status
- "diff": unified diff of the PR changes
- "checks": CI/CD pipeline status
- "status": one-line summary (title, state, review count, check count)

Requires gh CLI to be authenticated (run ` + "`gh auth status`" + ` first if you get an authentication error).

Uses ` + "`gh pr view`" + `, ` + "`gh pr list`" + `, ` + "`gh pr diff`" + `, ` + "`gh pr checks`" + `, and ` + "`gh api`" + ` under the hood.`,
		Risk: RiskRead,
		Tags: []string{"github", "pr", "review", "checks", "ci"},
		Args: []Arg{
			{Name: "action", Type: ArgString, Description: `Query action: "list" | "view" | "diff" | "checks" | "status". Defaults to "view" when number is set, "list" when not.`},
			{Name: "number", Type: ArgString, Description: `PR number (e.g. "123"). Required for "view", "diff", "checks".`},
			{Name: "repo", Type: ArgString, Description: `Owner/repo or full URL. Defaults to the detected repo from git remote.`},
			{Name: "state", Type: ArgString, Description: `Filter for "list": "open" | "closed" | "all". Default: "open".`},
			{Name: "limit", Type: ArgInteger, Description: `Max PRs to return for "list" (default 10, max 50).`},
			{Name: "include_diff", Type: ArgBoolean, Description: `Include full unified diff in the "view" output. Default false.`},
		},
		Returns: "Structured JSON: {pr: {number, title, state, author, reviews[], check_status, ...}} or {prs: [{number, title, state, author, url},...]} or {diff: string}.",
		Examples: []string{
			`{"action":"list","limit":5}`,
			`{"action":"view","number":"123"}`,
			`{"action":"checks","number":"123"}`,
			`{"action":"status","number":"123"}`,
		},
		Idempotent: true,
		CostHint:   "io-bound",
	}
}

func (t *GHPullRequestTool) Execute(ctx context.Context, req Request) (Result, error) {
	timeout := resolveGHTimeout(req.Params)
	action := strings.TrimSpace(asString(req.Params, "action", ""))
	repo := strings.TrimSpace(asString(req.Params, "repo", ""))

	if err := checkGH_auth(); err != nil {
		return Result{}, err
	}

	action = resolvePRAction(action, req.Params)
	switch action {
	case "list":
		return t.listPRs(ctx, repo, req.ProjectRoot, timeout, req.Params)
	case "view":
		return t.viewPR(ctx, repo, req.ProjectRoot, timeout, req.Params)
	case "diff":
		return t.diffPR(ctx, repo, req.ProjectRoot, timeout, req.Params)
	case "checks":
		return t.checksPR(ctx, repo, req.ProjectRoot, timeout, req.Params)
	case "status":
		return t.statusPR(ctx, repo, req.ProjectRoot, timeout, req.Params)
	default:
		return Result{}, fmt.Errorf("unknown pr action %q", action)
	}
}

func (t *GHPullRequestTool) listPRs(ctx context.Context, repo, projectRoot string, timeout time.Duration, params map[string]any) (Result, error) {
	state := strings.TrimSpace(asString(params, "state", "open"))
	limit := asInt(params, "limit", 10)
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	args := []string{"pr", "list"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	args = append(args, "--state", state, "--limit", fmt.Sprintf("%d", limit), "--json", "number,title,state,headRefName,baseRefName,author,url,changedFiles,additions,deletions,comments")
	stdout, stderr, exit, err := runGH(ctx, projectRoot, timeout, args...)
	if err != nil {
		return Result{Output: joinGitOutput(stdout, stderr), Data: map[string]any{"exit_code": exit}},
			fmt.Errorf("gh pr list: %w (stderr: %s)", err, stderr)
	}

	prs, parseErr := parsePRListJSON(stdout)
	if parseErr != nil {
		return Result{Output: stdout}, fmt.Errorf("parse gh pr list json: %w", parseErr)
	}

	return Result{
		Output: fmt.Sprintf("PR list (%s, %d):", state, len(prs)),
		Data: map[string]any{
			"prs":   prs,
			"count": len(prs),
		},
	}, nil
}

func (t *GHPullRequestTool) viewPR(ctx context.Context, repo, projectRoot string, timeout time.Duration, params map[string]any) (Result, error) {
	number := strings.TrimSpace(asString(params, "number", ""))
	if number == "" {
		return Result{}, missingParamError("gh_pr", "number", params,
			`{"action":"view","number":"123"}`,
			`number is required for "view" action.`)
	}
	includeDiff := asBool(params, "include_diff", false)

	args := []string{"pr", "view", number}
	if repo != "" {
		args = append(args, "--repo", repo)
	}

	// Fetch base info + reviews + checks via separate JSON exports
	infoArgs := append([]string{}, args...)
	infoArgs = append(infoArgs, "--json", "number,title,state,headRefName,baseRefName,author,url,body,additions,deletions,changedFiles,comments,commits")
	infoOut, _, exit, err := runGH(ctx, projectRoot, timeout, infoArgs...)
	if err != nil {
		return Result{}, fmt.Errorf("gh pr view: %w (exit %d)", err, exit)
	}
	pr, err := parsePRViewJSON(infoOut)
	if err != nil {
		return Result{Output: infoOut}, fmt.Errorf("parse pr view json: %w", err)
	}

	// Reviews
	revArgs := append([]string{}, args...)
	revArgs = append(revArgs, "--json", "author,state,submittedAt")
	revOut, _, _, _ := runGH(ctx, projectRoot, timeout, revArgs...)
	reviews := parsePRReviewsJSON(revOut)
	pr.Reviews = reviews

	// Check status
	checkArgs := append([]string{}, args...)
	checkArgs = append(checkArgs, "checks", "--json", "status,conclusion")
	checkOut, _, _, _ := runGH(ctx, projectRoot, timeout, checkArgs...)
	pr.CheckStatus = parseCheckStatus(checkOut)

	var diff string
	if includeDiff {
		diffArgs := append([]string{}, args...)
		diffArgs = append(diffArgs, "--repo", repo)
		diffOut, _, _, _ := runGH(ctx, projectRoot, timeout, diffArgs...)
		diff = diffOut
	}

	data := map[string]any{
		"pr": pr,
	}
	if diff != "" {
		data["diff"] = diff
	}

	return Result{
		Output: fmt.Sprintf("PR #%d: %s (%s)", pr.Number, pr.Title, pr.State),
		Data:   data,
	}, nil
}

func (t *GHPullRequestTool) diffPR(ctx context.Context, repo, projectRoot string, timeout time.Duration, params map[string]any) (Result, error) {
	number := strings.TrimSpace(asString(params, "number", ""))
	if number == "" {
		return Result{}, missingParamError("gh_pr", "number", params,
			`{"action":"diff","number":"123"}`,
			`number is required for "diff" action.`)
	}
	args := []string{"pr", "diff", number}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	stdout, stderr, exit, err := runGH(ctx, projectRoot, timeout, args...)
	if err != nil {
		return Result{Output: joinGitOutput(stdout, stderr), Data: map[string]any{"exit_code": exit}},
			fmt.Errorf("gh pr diff: %w (stderr: %s)", err, stderr)
	}
	truncated := ""
	if len(stdout) > 50000 {
		truncated = stdout[:50000] + "\n... (diff truncated at 50K chars)"
	} else {
		truncated = stdout
	}
	return Result{
		Output: fmt.Sprintf("PR #%s diff (%d chars)", number, len(stdout)),
		Data: map[string]any{
			"diff":      truncated,
			"full_len":  len(stdout),
			"truncated": len(stdout) > 50000,
		},
	}, nil
}

func (t *GHPullRequestTool) checksPR(ctx context.Context, repo, projectRoot string, timeout time.Duration, params map[string]any) (Result, error) {
	number := strings.TrimSpace(asString(params, "number", ""))
	if number == "" {
		return Result{}, missingParamError("gh_pr", "checks", params,
			`{"action":"checks","number":"123"}`,
			`number is required for "checks" action.`)
	}
	args := []string{"pr", "checks", number}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	stdout, stderr, exit, err := runGH(ctx, projectRoot, timeout, args...)
	if err != nil {
		return Result{Output: joinGitOutput(stdout, stderr), Data: map[string]any{"exit_code": exit}},
			fmt.Errorf("gh pr checks: %w (stderr: %s)", err, stderr)
	}
	return Result{
		Output: fmt.Sprintf("PR #%s checks:", number),
		Data: map[string]any{
			"checks":    stdout,
			"exit_code": exit,
		},
	}, nil
}

func (t *GHPullRequestTool) statusPR(ctx context.Context, repo, projectRoot string, timeout time.Duration, params map[string]any) (Result, error) {
	number := strings.TrimSpace(asString(params, "number", ""))
	if number == "" {
		return Result{}, missingParamError("gh_pr", "status", params,
			`{"action":"status","number":"123"}`,
			`number is required for "status" action.`)
	}
	args := []string{"pr", "view", number, "--json", "number,title,state,author,url,comments,reviews"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	stdout, _, exit, err := runGH(ctx, projectRoot, timeout, args...)
	if err != nil {
		return Result{}, fmt.Errorf("gh pr view: %w (exit %d)", err, exit)
	}
	pr, err := parsePRViewJSON(stdout)
	if err != nil {
		return Result{Output: stdout}, fmt.Errorf("parse pr json: %w", err)
	}
	summary := fmt.Sprintf("PR #%d | %s | %s | by %s | %d comments | %d reviews",
		pr.Number, pr.Title, pr.State, pr.Author, pr.Comments, len(pr.Reviews))
	return Result{
		Output: summary,
		Data: map[string]any{
			"summary": summary,
			"pr":      pr,
		},
	}, nil
}
