// git_review_tool.go — Automated git diff and code review tool.
package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// GitReviewTool analyzes git changes for code review.
type GitReviewTool struct{}

func NewGitReviewTool() *GitReviewTool {
	return &GitReviewTool{}
}

func (t *GitReviewTool) Name() string { return "git_review" }
func (t *GitReviewTool) Description() string {
	return "Analyze git changes for code review: modified files, churn, and commit history."
}
func (t *GitReviewTool) Risk() Risk   { return RiskRead }
func (t *GitReviewTool) Timeout() int { return 30 }
func (t *GitReviewTool) Cache() bool  { return false }

func (t *GitReviewTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "git_review",
		Title:   "Git Review",
		Summary: "Analyze git changes for code review.",
		Purpose: "Use before reviewing a branch or commit range to summarize changed files, churn, commits, and contributors.",
		Risk:    RiskRead,
		Tags:    []string{"git", "review", "diff"},
		Args: []Arg{
			{Name: "target", Type: ArgString, Default: ".", Description: "Git target: directory, commit hash, branch name, or HEAD~N."},
			{Name: "since", Type: ArgString, Description: "Start date for commits, e.g. YYYY-MM-DD."},
			{Name: "limit", Type: ArgInteger, Default: 50, Description: "Max number of commits to analyze."},
			{Name: "include_stats", Type: ArgBoolean, Default: true, Description: "Include diff statistics."},
			{Name: "file_pattern", Type: ArgString, Description: "Filter files by substring or pattern text."},
		},
		Returns:    "Markdown review summary with embedded JSON metadata.",
		Idempotent: true,
		CostHint:   "io-bound",
	}
}

// CommitInfo represents a single commit.
type CommitInfo struct {
	Hash      string `json:"hash"`
	ShortHash string `json:"short_hash"`
	Author    string `json:"author"`
	Date      string `json:"date"`
	Subject   string `json:"subject"`
	Files     int    `json:"files_changed"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// FileChange represents a file modification.
type FileChange struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status"` // added, modified, deleted
}

// ReviewSummary contains the analysis results.
type ReviewSummary struct {
	TotalCommits   int           `json:"total_commits"`
	TotalFiles     int           `json:"total_files"`
	TotalAdditions int           `json:"total_additions"`
	TotalDeletions int           `json:"total_deletions"`
	TopFiles       []FileChange  `json:"top_files"` // Most changed
	Commits        []CommitInfo  `json:"commits"`
	Authors        []AuthorStats `json:"top_authors"`
}

// AuthorStats holds commit statistics per author.
type AuthorStats struct {
	Name      string `json:"name"`
	Commits   int    `json:"commits"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

func (t *GitReviewTool) Execute(ctx context.Context, req Request) (Result, error) {
	target := strings.TrimSpace(asString(req.Params, "target", "."))
	since := strings.TrimSpace(asString(req.Params, "since", ""))
	limit := asInt(req.Params, "limit", 50)
	includeStats := asBool(req.Params, "include_stats", true)
	filePattern := strings.TrimSpace(asString(req.Params, "file_pattern", ""))
	if target == "" {
		target = "."
	}
	if limit <= 0 {
		limit = 50
	}

	// Get commit log
	commits, err := t.getCommits(target, since, limit)
	if err != nil {
		return Result{}, fmt.Errorf("failed to get commits: %w", err)
	}

	// Get file changes
	files, err := t.getFileChanges(target, filePattern)
	if err != nil {
		return Result{}, fmt.Errorf("failed to get file changes: %w", err)
	}

	// Calculate summary
	summary := t.calculateSummary(commits, files)

	// Format output
	output := t.formatOutput(summary, includeStats)

	return Result{Output: output}, nil
}

func (t *GitReviewTool) getCommits(target, since string, limit int) ([]CommitInfo, error) {
	args := []string{"log", "--pretty=format:%H|%h|%an|%ad|%s", "--date=short"}
	if since != "" {
		args = append(args, "--since="+since)
	}
	args = append(args, "-n", strconv.Itoa(limit), "--")

	if target != "." {
		args = append(args, target)
	}

	cmd := exec.Command("git", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	var commits []CommitInfo
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 5)
		if len(parts) >= 5 {
			commits = append(commits, CommitInfo{
				Hash:      parts[0],
				ShortHash: parts[1],
				Author:    parts[2],
				Date:      parts[3],
				Subject:   parts[4],
			})
		}
	}

	return commits, nil
}

func (t *GitReviewTool) getFileChanges(target, pattern string) ([]FileChange, error) {
	args := []string{"diff", "--numstat", "HEAD"}
	if target != "." {
		args[len(args)-1] = target
	}

	cmd := exec.Command("git", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff failed: %w", err)
	}

	var files []FileChange
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) >= 2 {
			additions := 0
			deletions := 0
			if parts[0] != "-" && parts[0] != "" {
				additions, _ = strconv.Atoi(parts[0])
			}
			if parts[1] != "-" && parts[1] != "" {
				deletions, _ = strconv.Atoi(parts[1])
			}

			status := "modified"
			if additions > 0 && deletions == 0 {
				status = "added"
			} else if additions == 0 && deletions > 0 {
				status = "deleted"
			}

			files = append(files, FileChange{
				Path:      parts[2],
				Additions: additions,
				Deletions: deletions,
				Status:    status,
			})
		}
	}

	return files, nil
}

func (t *GitReviewTool) calculateSummary(commits []CommitInfo, files []FileChange) ReviewSummary {
	summary := ReviewSummary{
		TotalCommits: len(commits),
		TotalFiles:   len(files),
		Commits:      commits,
		TopFiles:     files,
	}

	// Sort files by total changes
	sort.Slice(summary.TopFiles, func(i, j int) bool {
		return (summary.TopFiles[i].Additions + summary.TopFiles[i].Deletions) >
			(summary.TopFiles[j].Additions + summary.TopFiles[j].Deletions)
	})

	// Limit to top 20 files
	if len(summary.TopFiles) > 20 {
		summary.TopFiles = summary.TopFiles[:20]
	}

	// Calculate totals
	authorMap := make(map[string]*AuthorStats)
	for _, f := range files {
		summary.TotalAdditions += f.Additions
		summary.TotalDeletions += f.Deletions
	}

	for _, c := range commits {
		summary.TotalAdditions += c.Additions
		summary.TotalDeletions += c.Deletions

		if authorMap[c.Author] == nil {
			authorMap[c.Author] = &AuthorStats{Name: c.Author}
		}
		authorMap[c.Author].Commits++
		authorMap[c.Author].Additions += c.Additions
		authorMap[c.Author].Deletions += c.Deletions
	}

	// Convert to slice and sort
	for _, a := range authorMap {
		summary.Authors = append(summary.Authors, *a)
	}
	sort.Slice(summary.Authors, func(i, j int) bool {
		return summary.Authors[i].Commits > summary.Authors[j].Commits
	})
	if len(summary.Authors) > 10 {
		summary.Authors = summary.Authors[:10]
	}

	return summary
}

func (t *GitReviewTool) formatOutput(summary ReviewSummary, includeStats bool) string {
	var sb strings.Builder

	sb.WriteString("## Git Code Review\n\n")
	sb.WriteString(fmt.Sprintf("**%d commits** · **%d files** · **+%d/-%d lines**\n\n",
		summary.TotalCommits, summary.TotalFiles,
		summary.TotalAdditions, summary.TotalDeletions))

	// Top changed files
	if len(summary.TopFiles) > 0 {
		sb.WriteString("### Most Changed Files\n\n")
		sb.WriteString("| File | + | - | Status |\n")
		sb.WriteString("|------|---|---|-|-------|\n")
		for _, f := range summary.TopFiles {
			sb.WriteString(fmt.Sprintf("| %s | %d | %d | %s |\n",
				f.Path, f.Additions, f.Deletions, f.Status))
		}
		sb.WriteString("\n")
	}

	// Top authors
	if len(summary.Authors) > 0 {
		sb.WriteString("### Top Contributors\n\n")
		sb.WriteString("| Author | Commits | + | - |\n")
		sb.WriteString("|--------|---------|---|---|")
		for _, a := range summary.Authors {
			sb.WriteString(fmt.Sprintf("| %s | %d | %d | %d |\n",
				a.Name, a.Commits, a.Additions, a.Deletions))
		}
		sb.WriteString("\n")
	}

	// Recent commits
	if len(summary.Commits) > 0 {
		sb.WriteString("### Recent Commits\n\n")
		limit := 10
		if len(summary.Commits) < limit {
			limit = len(summary.Commits)
		}
		for _, c := range summary.Commits[:limit] {
			sb.WriteString(fmt.Sprintf("- `%s` %s — *%s* (%s)\n",
				c.ShortHash, c.Subject, c.Author, c.Date))
		}
	}

	// Metadata
	sb.WriteString(fmt.Sprintf("\n<!-- METADATA: %s -->\n",
		mustMarshal(summary)))

	return sb.String()
}

func mustMarshal(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
