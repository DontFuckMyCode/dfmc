// changelog_generate_tool.go — Automated semantic changelog generator.
package tools

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// ChangelogGenerateTool generates semantic changelog from git commits.
type ChangelogGenerateTool struct{}

func NewChangelogGenerateTool() *ChangelogGenerateTool {
	return &ChangelogGenerateTool{}
}

func (t *ChangelogGenerateTool) Name() string { return "changelog_generate" }
func (t *ChangelogGenerateTool) Description() string {
	return "Generate CHANGELOG.md content from git commit messages using semantic versioning conventions."
}
func (t *ChangelogGenerateTool) Risk() Risk   { return RiskRead }
func (t *ChangelogGenerateTool) Timeout() int { return 30 }
func (t *ChangelogGenerateTool) Cache() bool  { return false }

func (t *ChangelogGenerateTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "changelog_generate",
		Title:   "Generate Changelog",
		Summary: "Generate CHANGELOG.md content from git commit messages.",
		Purpose: "Use to summarize conventional commits between tags, since a date, or across repository tags.",
		Risk:    RiskRead,
		Tags:    []string{"git", "changelog", "release-notes"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Default: ".", Description: "Git repository path."},
			{Name: "from_tag", Type: ArgString, Description: "Start tag, e.g. v1.0.0."},
			{Name: "to_tag", Type: ArgString, Default: "HEAD", Description: "End tag or revision."},
			{Name: "since", Type: ArgString, Description: "Start date, e.g. YYYY-MM-DD."},
			{Name: "all_tags", Type: ArgBoolean, Default: false, Description: "Generate changelog sections for all tags."},
			{Name: "unreleased", Type: ArgBoolean, Default: true, Description: "Reserved for compatibility; include unreleased changes."},
			{Name: "types", Type: ArgArray, Items: &Arg{Type: ArgString}, Description: "Commit types to include."},
		},
		Returns:    "Markdown changelog content.",
		Idempotent: true,
		CostHint:   "io-bound",
	}
}

// CommitEntry represents a parsed commit.
type CommitEntry struct {
	Hash     string
	Type     string
	Scope    string
	Message  string
	Breaking bool
	Author   string
	Date     string
	PRNumber string
}

// ChangelogSection groups commits by type.
type ChangelogSection struct {
	Type    string
	Title   string
	Commits []CommitEntry
}

// ChangelogResult holds the generated changelog.
type ChangelogResult struct {
	Version  string             `json:"version"`
	Date     string             `json:"date"`
	Sections []ChangelogSection `json:"sections"`
	Breaking []CommitEntry      `json:"breaking_changes"`
}

var (
	// Conventional commit patterns
	breakingPattern = regexp.MustCompile(`(?i)(BREAKING[- ]CHANGE|breaking change)`)
	typePattern     = regexp.MustCompile(`^(\w+)(?:\(([^)]+)\))?(!)?:\s*(.+)`)
	prPattern       = regexp.MustCompile(`#(\d+)`)
)

var typeTitles = map[string]string{
	"feat":     "Features",
	"fix":      "Bug Fixes",
	"perf":     "Performance Improvements",
	"refactor": "Refactoring",
	"docs":     "Documentation",
	"test":     "Tests",
	"chore":    "Chores",
	"style":    "Styles",
	"ci":       "CI/CD",
	"build":    "Build System",
	"breaking": "BREAKING CHANGES",
}

func (t *ChangelogGenerateTool) Execute(ctx context.Context, req Request) (Result, error) {
	path := strings.TrimSpace(asString(req.Params, "path", "."))
	fromTag := strings.TrimSpace(asString(req.Params, "from_tag", ""))
	toTag := strings.TrimSpace(asString(req.Params, "to_tag", "HEAD"))
	since := strings.TrimSpace(asString(req.Params, "since", ""))
	allTags := asBool(req.Params, "all_tags", false)
	if path == "" {
		path = "."
	}
	if toTag == "" {
		toTag = "HEAD"
	}

	var result string
	var err error

	if allTags {
		result, err = t.generateAllTagsChangelog(path)
	} else {
		result, err = t.generateChangelog(path, fromTag, toTag, since)
	}

	if err != nil {
		return Result{}, fmt.Errorf("failed to generate changelog: %w", err)
	}

	return Result{Output: result}, nil
}

func (t *ChangelogGenerateTool) generateChangelog(path, fromTag, toTag, since string) (string, error) {
	args := []string{"log", "--pretty=format:%H|%s|%an|%ad|%D"}
	args = append(args, "--date=short")

	// Validate tag inputs to prevent malformed git arguments
	if fromTag != "" {
		tagRegex := regexp.MustCompile(`^[a-zA-Z0-9._/-]+$`)
		if !tagRegex.MatchString(fromTag) || !tagRegex.MatchString(toTag) {
			return "", fmt.Errorf("invalid tag format (allowed: alphanumeric, ., _, -, /)")
		}
	}

	if fromTag != "" {
		args = append(args, fromTag+".."+toTag)
	} else if since != "" {
		args = append(args, "--since="+since)
	} else {
		args = append(args, "-n", "100")
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = path
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git log failed: %w", err)
	}

	commits := t.parseCommits(string(output))
	if len(commits) == 0 {
		return "# Changelog\n\n*No changes found.*\n", nil
	}

	return t.formatChangelog(commits, fromTag), nil
}

func (t *ChangelogGenerateTool) generateAllTagsChangelog(path string) (string, error) {
	args := []string{"tag", "--sort", "-version:refname"}
	cmd := exec.Command("git", args...)
	cmd.Dir = path
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git tag failed: %w", err)
	}

	var tags []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		if tag := strings.TrimSpace(scanner.Text()); tag != "" {
			tags = append(tags, tag)
		}
	}

	var sb strings.Builder
	sb.WriteString("# Changelog\n\n")

	for i := 0; i < len(tags)-1; i++ {
		changelog, err := t.generateChangelog(path, tags[i+1], tags[i], "")
		if err != nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("## [%s] - %s\n\n", tags[i], t.getTagDate(path, tags[i])))
		sb.WriteString(changelog)
		sb.WriteString("\n---\n\n")
	}

	return sb.String(), nil
}

func (t *ChangelogGenerateTool) parseCommits(output string) []CommitEntry {
	var commits []CommitEntry
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.SplitN(line, "|", 5)
		if len(parts) < 4 {
			continue
		}

		commit := CommitEntry{
			Hash:    parts[0],
			Message: parts[1],
			Author:  parts[2],
			Date:    parts[3],
		}

		// Skip merges
		if strings.HasPrefix(commit.Message, "Merge") {
			continue
		}

		// Parse conventional commit
		if matches := typePattern.FindStringSubmatch(commit.Message); matches != nil {
			commit.Type = matches[1]
			commit.Scope = matches[2]
			commit.Breaking = matches[3] != "" || breakingPattern.MatchString(commit.Message)
			commit.Message = matches[4]
		}

		// Extract PR number
		if matches := prPattern.FindStringSubmatch(commit.Message); matches != nil {
			commit.PRNumber = matches[1]
		}

		if commit.Type != "" {
			commits = append(commits, commit)
		}
	}

	return commits
}

func (t *ChangelogGenerateTool) formatChangelog(commits []CommitEntry, version string) string {
	var sb strings.Builder

	// Group by type
	sections := make(map[string][]CommitEntry)
	var breaking []CommitEntry

	for _, c := range commits {
		if c.Breaking {
			c.Type = "breaking"
			breaking = append(breaking, c)
		}
		sections[c.Type] = append(sections[c.Type], c)
	}

	// Sort types by conventional order
	typeOrder := []string{"breaking", "feat", "fix", "perf", "refactor", "docs", "test", "chore", "style", "ci", "build"}

	for _, typeName := range typeOrder {
		commits := sections[typeName]
		if len(commits) == 0 {
			continue
		}

		title := typeTitles[typeName]
		if title == "" {
			title = strings.Title(typeName)
		}

		sb.WriteString(fmt.Sprintf("### %s\n\n", title))

		for _, c := range commits {
			scope := ""
			if c.Scope != "" {
				scope = fmt.Sprintf("**(%s)** ", c.Scope)
			}

			prLink := ""
			if c.PRNumber != "" {
				prLink = fmt.Sprintf(" ([#%s](https://github.com/owner/repo/pull/%s))", c.PRNumber, c.PRNumber)
			}

			sb.WriteString(fmt.Sprintf("- %s%s%s\n", scope, c.Message, prLink))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func (t *ChangelogGenerateTool) getTagDate(path, tag string) string {
	cmd := exec.Command("git", "log", "-1", "--format=%ad", "--date=short", tag)
	cmd.Dir = path
	output, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}
