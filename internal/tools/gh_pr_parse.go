package tools

// gh_pr_parse.go — JSON-extract helpers for the gh_pr tool. Pre-fix
// these wrapped a real json.Unmarshal, but the gh CLI's --json output
// is line-oriented and the regex extractor reads "good enough" without
// taking on a structural dependency. Each parser is conservative: when
// the gh shape diverges (new field name, missing field) the parser
// degrades to a partial spec rather than failing, so the caller still
// sees usable output. Sibling to gh_pr.go (tool surface + per-action
// dispatch).

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)


func resolvePRAction(action string, params map[string]any) string {
	if action != "" {
		return action
	}
	number := strings.TrimSpace(asString(params, "number", ""))
	if number != "" {
		return "view"
	}
	return "list"
}

// JSON parsers — gh outputs JSON arrays for list and JSON objects for view.

func parsePRListJSON(raw string) ([]map[string]any, error) {
	// Try to parse as a JSON array of PR objects.
	// gh --json outputs valid JSON; we do a quick field extraction without
	// a full unmarshal to avoid adding a dependency.
	results := make([]map[string]any, 0, 20)
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		// Extract key fields via regex.
		get := func(name string) string {
			re := regexp.MustCompile(`"` + name + `"\s*:\s*("([^"]*)"|([0-9]+)|(\[[^\]]*\]))`)
			m := re.FindStringSubmatch(line)
			if len(m) < 2 {
				return ""
			}
			if m[2] != "" {
				return m[2]
			}
			return m[3]
		}
		num := get("number")
		if num == "" {
			continue
		}
		entry := map[string]any{
			"number":   num,
			"title":    get("title"),
			"state":    get("state"),
			"head_ref": get("headRefName"),
			"base_ref": get("baseRefName"),
			"author":   get("author"),
			"url":      get("url"),
		}
		results = append(results, entry)
	}
	if len(results) == 0 && strings.TrimSpace(raw) != "" {
		// Fallback: just return the raw output
		return []map[string]any{{"raw": raw}}, nil
	}
	return results, nil
}

func parsePRViewJSON(raw string) (GHPullRequestSpec, error) {
	var pr GHPullRequestSpec
	get := func(name string) string {
		re := regexp.MustCompile(`"` + name + `"\s*:\s*"([^"]*)"`)
		m := re.FindStringSubmatch(raw)
		if len(m) > 1 {
			return m[1]
		}
		// Also try number fields
		re2 := regexp.MustCompile(`"` + name + `"\s*:\s*([0-9]+)`)
		m2 := re2.FindStringSubmatch(raw)
		if len(m2) > 1 {
			return m2[1]
		}
		return ""
	}
	if n := get("number"); n != "" {
		_, _ = fmt.Sscanf(n, "%d", &pr.Number)
	}
	pr.Title = get("title")
	pr.State = get("state")
	pr.HeadRef = get("headRefName")
	pr.BaseRef = get("baseRefName")
	pr.Author = get("author")
	pr.URL = get("url")
	pr.Body = get("body")

	// changedFiles, additions, deletions
	if ai := strings.Index(raw, `"additions":`); ai >= 0 {
		snippet := raw[ai:]
		re := regexp.MustCompile(`([0-9]+)`)
		m := re.FindStringSubmatch(snippet)
		if len(m) > 0 {
			_, _ = fmt.Sscanf(m[1], "%d", &pr.Additions)
		}
	}
	if di := strings.Index(raw, `"deletions":`); di >= 0 {
		snippet := raw[di:]
		re := regexp.MustCompile(`([0-9]+)`)
		m := re.FindStringSubmatch(snippet)
		if len(m) > 0 {
			_, _ = fmt.Sscanf(m[1], "%d", &pr.Deletions)
		}
	}
	if ci := strings.Index(raw, `"comments":`); ci >= 0 {
		snippet := raw[ci:]
		re := regexp.MustCompile(`([0-9]+)`)
		m := re.FindStringSubmatch(snippet)
		if len(m) > 0 {
			_, _ = fmt.Sscanf(m[1], "%d", &pr.Comments)
		}
	}
	if co := strings.Index(raw, `"commits":`); co >= 0 {
		snippet := raw[co:]
		re := regexp.MustCompile(`([0-9]+)`)
		m := re.FindStringSubmatch(snippet)
		if len(m) > 0 {
			_, _ = fmt.Sscanf(m[1], "%d", &pr.Commits)
		}
	}
	return pr, nil
}

func parsePRReviewsJSON(raw string) []GHPullRequestReview {
	var reviews []GHPullRequestReview
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		get := func(name string) string {
			re := regexp.MustCompile(`"` + name + `"\s*:\s*"([^"]*)"`)
			m := re.FindStringSubmatch(line)
			if len(m) > 1 {
				return m[1]
			}
			return ""
		}
		state := get("state")
		if state == "" {
			continue
		}
		reviews = append(reviews, GHPullRequestReview{
			Author:    get("author"),
			State:     state,
			Submitted: get("submittedAt"),
		})
	}
	return reviews
}

func parseCheckStatus(raw string) string {
	if strings.Contains(raw, "FAIL") || strings.Contains(raw, "ERROR") {
		return "fail"
	}
	if strings.Contains(raw, "PASS") || strings.Contains(raw, "SUCCESS") {
		return "pass"
	}
	if strings.Contains(raw, "PENDING") || strings.Contains(raw, "IN PROGRESS") {
		return "pending"
	}
	return "unknown"
}

// checkGH_auth verifies the gh CLI is authenticated.
func checkGH_auth() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stdout, stderr, exit, _ := runGH(ctx, ".", 5*time.Second, "auth", "status", "--json", "user")
	if exit != 0 || strings.Contains(stdout+stderr, "not logged in") || strings.Contains(stdout+stderr, "authenticate") {
		return fmt.Errorf("gh is not authenticated — run `gh auth login` first")
	}
	return nil
}
