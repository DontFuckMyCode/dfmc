package promptlib

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type StatsTemplate struct {
	ID                  string   `json:"id"`
	Type                string   `json:"type"`
	Task                string   `json:"task"`
	Language            string   `json:"language,omitempty"`
	Profile             string   `json:"profile,omitempty"`
	Compose             string   `json:"compose,omitempty"`
	Priority            int      `json:"priority,omitempty"`
	Tokens              int      `json:"tokens"`
	Placeholders        []string `json:"placeholders,omitempty"`
	UnknownPlaceholders []string `json:"unknown_placeholders,omitempty"`
	Warnings            []string `json:"warnings,omitempty"`
}

type StatsReport struct {
	TemplateCount     int             `json:"template_count"`
	MaxTemplateTokens int             `json:"max_template_tokens"`
	TotalTokens       int             `json:"total_tokens"`
	AvgTokens         float64         `json:"avg_tokens"`
	MaxTokens         int             `json:"max_tokens"`
	WarningCount      int             `json:"warning_count"`
	Templates         []StatsTemplate `json:"templates"`
}

type StatsOptions struct {
	MaxTemplateTokens int
	AllowVars         []string
}

func BuildStatsReport(templates []Template, opts StatsOptions) StatsReport {
	known := DefaultKnownVars()
	for _, raw := range opts.AllowVars {
		k := strings.TrimSpace(raw)
		if k == "" {
			continue
		}
		known[k] = struct{}{}
	}

	report := StatsReport{
		TemplateCount:     len(templates),
		MaxTemplateTokens: opts.MaxTemplateTokens,
		Templates:         make([]StatsTemplate, 0, len(templates)),
	}
	for _, tpl := range templates {
		ph := extractPlaceholders(tpl.Body)
		unknown := make([]string, 0, len(ph))
		for _, name := range ph {
			if _, ok := known[name]; !ok {
				unknown = append(unknown, name)
			}
		}
		sort.Strings(unknown)

		tokens := EstimateTokens(tpl.Body)
		item := StatsTemplate{
			ID:                  strings.TrimSpace(tpl.ID),
			Type:                strings.TrimSpace(tpl.Type),
			Task:                strings.TrimSpace(tpl.Task),
			Language:            strings.TrimSpace(tpl.Language),
			Profile:             strings.TrimSpace(tpl.Profile),
			Compose:             strings.TrimSpace(tpl.Compose),
			Priority:            tpl.Priority,
			Tokens:              tokens,
			Placeholders:        ph,
			UnknownPlaceholders: unknown,
			Warnings:            []string{},
		}
		if opts.MaxTemplateTokens > 0 && tokens > opts.MaxTemplateTokens {
			item.Warnings = append(item.Warnings, fmt.Sprintf("token_estimate=%d exceeds threshold=%d", tokens, opts.MaxTemplateTokens))
		}
		if len(unknown) > 0 {
			item.Warnings = append(item.Warnings, "unknown placeholders: "+strings.Join(unknown, ", "))
		}
		if len(item.Warnings) > 0 {
			report.WarningCount += len(item.Warnings)
		}

		report.TotalTokens += tokens
		if tokens > report.MaxTokens {
			report.MaxTokens = tokens
		}
		report.Templates = append(report.Templates, item)
	}
	if report.TemplateCount > 0 {
		report.AvgTokens = float64(report.TotalTokens) / float64(report.TemplateCount)
	}
	return report
}

func DefaultKnownVars() map[string]struct{} {
	return map[string]struct{}{
		"project_root":     {},
		"task":             {},
		"language":         {},
		"profile":          {},
		"project_brief":    {},
		"user_query":       {},
		"context_files":    {},
		"injected_context": {},
		"tools_overview":   {},
		"tool_call_policy": {},
		"response_policy":  {},
	}
}

var statsPlaceholderRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

func extractPlaceholders(body string) []string {
	matches := statsPlaceholderRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		k := strings.TrimSpace(m[1])
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func EstimateTokens(text string) int {
	return len(strings.Fields(strings.TrimSpace(text)))
}
