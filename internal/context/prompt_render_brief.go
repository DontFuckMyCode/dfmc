package context

// prompt_render_brief.go — project brief loader for the system prompt.
// Companion siblings:
//
//   - prompt_render.go         profile/role/budget/injected-context core
//   - prompt_render_tools.go   tool-list summarization
//   - prompt_render_policy.go  tool-call + response policy paragraphs
//
// loadProjectBrief reads .dfmc/magic/MAGIC_DOC.md when present and
// scores its sections against the active task and query, picking the
// most relevant subset to inline into the system prompt within budget.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func loadProjectBrief(projectRoot, query, task string, maxTokens int) string {
	root := strings.TrimSpace(projectRoot)
	if root == "" || maxTokens <= 0 {
		return "(none)"
	}
	path := filepath.Join(root, ".dfmc", "magic", "MAGIC_DOC.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "(none)"
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "(none)"
	}
	sections := projectBriefSections(text)
	selected := selectProjectBriefSections(sections, query, task, 4)
	if len(selected) == 0 {
		selected = firstProjectBriefLines(text, 48)
	}
	if len(selected) == 0 {
		return "(none)"
	}
	return trimToTokenBudget(strings.Join(selected, "\n"), maxTokens)
}

type projectBriefSection struct {
	Index int
	Title string
	Lines []string
}

func projectBriefSections(text string) []projectBriefSection {
	rawLines := strings.Split(text, "\n")
	sections := make([]projectBriefSection, 0)
	current := projectBriefSection{Index: 0}
	flush := func() {
		if len(current.Lines) == 0 {
			return
		}
		sections = append(sections, current)
		current = projectBriefSection{Index: len(sections)}
	}
	for _, line := range rawLines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "```") {
			continue
		}
		if strings.HasPrefix(t, "#") {
			flush()
			current.Title = strings.TrimSpace(strings.TrimLeft(t, "#"))
			current.Lines = append(current.Lines, t)
			continue
		}
		current.Lines = append(current.Lines, t)
	}
	flush()
	return sections
}

func selectProjectBriefSections(sections []projectBriefSection, query, task string, limit int) []string {
	if len(sections) == 0 || limit <= 0 {
		return nil
	}
	terms := append(tokenizeQuery(query), projectBriefTaskTerms(task)...)
	type scored struct {
		section projectBriefSection
		score   int
	}
	scoredSections := make([]scored, 0, len(sections))
	for _, section := range sections {
		score := scoreProjectBriefSection(section, terms)
		if score <= 0 {
			continue
		}
		scoredSections = append(scoredSections, scored{section: section, score: score})
	}
	if len(scoredSections) == 0 {
		return nil
	}
	sort.SliceStable(scoredSections, func(i, j int) bool {
		if scoredSections[i].score != scoredSections[j].score {
			return scoredSections[i].score > scoredSections[j].score
		}
		return scoredSections[i].section.Index < scoredSections[j].section.Index
	})
	if len(scoredSections) > limit {
		scoredSections = scoredSections[:limit]
	}
	sort.SliceStable(scoredSections, func(i, j int) bool {
		return scoredSections[i].section.Index < scoredSections[j].section.Index
	})
	out := []string{"Project brief filtered for task=" + strings.TrimSpace(task) + ":"}
	for _, item := range scoredSections {
		out = append(out, item.section.Lines...)
	}
	return out
}

func scoreProjectBriefSection(section projectBriefSection, terms []string) int {
	if len(terms) == 0 {
		return 0
	}
	title := strings.ToLower(section.Title)
	body := strings.ToLower(strings.Join(section.Lines, "\n"))
	score := 0
	for _, term := range terms {
		term = strings.ToLower(strings.TrimSpace(term))
		if len(term) < 3 {
			continue
		}
		if strings.Contains(title, term) {
			score += 4
		}
		if strings.Contains(body, term) {
			score++
		}
	}
	return score
}

func projectBriefTaskTerms(task string) []string {
	switch strings.ToLower(strings.TrimSpace(task)) {
	case "security":
		return []string{"security", "auth", "secret", "credential", "vulnerab", "risk", "audit", "threat", "token"}
	case "review":
		return []string{"review", "bug", "risk", "hotspot", "todo", "quality", "regression"}
	case "refactor":
		return []string{"refactor", "architecture", "design", "cleanup", "debt", "module"}
	case "test":
		return []string{"test", "coverage", "fixture", "mock", "benchmark"}
	case "doc":
		return []string{"doc", "readme", "usage", "guide", "manual"}
	case "planning":
		return []string{"plan", "roadmap", "milestone", "priority", "next"}
	default:
		return nil
	}
}

func firstProjectBriefLines(text string, limit int) []string {
	lines := strings.Split(text, "\n")
	filtered := make([]string, 0, min(len(lines), max(0, limit)))
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "```") {
			continue
		}
		filtered = append(filtered, t)
		if len(filtered) >= limit {
			break
		}
	}
	return filtered
}
