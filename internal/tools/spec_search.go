package tools

// spec_search.go — help renderers (ShortHelp, LongHelp) and the
// deterministic spec-search scorer (ScoreMatch + tokenize +
// tokenOverlap + SortSpecs) used by the tool_search / tool_help meta
// tools. Sibling of spec.go which keeps the canonical type model
// (Risk, ArgType, Arg, ToolSpec) + JSONSchema serializer + Specer
// interface + ReasonField/ExtractReason self-narration plumbing.
//
// Splitting the renderers/scorer out keeps the data model file
// scannable when adding a new ToolSpec field.

import (
	"encoding/json"
	"sort"
	"strings"
)

// ShortHelp renders a 1-2 line description suitable for tool_search results.
// Format: "name — summary [risk]".
func (s ToolSpec) ShortHelp() string {
	summary := strings.TrimSpace(s.Summary)
	if summary == "" {
		summary = strings.TrimSpace(s.Purpose)
	}
	if summary == "" {
		summary = "(no description)"
	}
	return s.Name + " — " + summary + " [" + string(s.Risk) + "]"
}

// LongHelp renders a full tool_help response with args, returns, examples.
func (s ToolSpec) LongHelp() string {
	var b strings.Builder
	b.WriteString(s.Name)
	if s.Title != "" {
		b.WriteString(" (" + s.Title + ")")
	}
	b.WriteString("\nRisk: " + string(s.Risk))
	if s.Idempotent {
		b.WriteString(" (idempotent)")
	}
	if s.CostHint != "" {
		b.WriteString("  Cost: " + s.CostHint)
	}
	b.WriteString("\n")
	if s.Summary != "" {
		b.WriteString("\nSummary: " + s.Summary + "\n")
	}
	if s.Purpose != "" {
		b.WriteString("\nPurpose: " + s.Purpose + "\n")
	}
	if strings.TrimSpace(s.Prompt) != "" {
		b.WriteString("\nGuide:\n" + strings.TrimSpace(s.Prompt) + "\n")
	}
	if len(s.Args) > 0 {
		b.WriteString("\nArgs:\n")
		for _, a := range s.Args {
			flag := ""
			if a.Required {
				flag = " (required)"
			}
			b.WriteString("  - " + a.Name + " <" + string(a.Type) + ">" + flag)
			if a.Description != "" {
				b.WriteString(": " + a.Description)
			}
			if a.Default != nil {
				def, _ := json.Marshal(a.Default)
				b.WriteString("  default=" + string(def))
			}
			if len(a.Enum) > 0 {
				en, _ := json.Marshal(a.Enum)
				b.WriteString("  enum=" + string(en))
			}
			b.WriteString("\n")
		}
	}
	if s.Returns != "" {
		b.WriteString("\nReturns: " + s.Returns + "\n")
	}
	if len(s.Examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, ex := range s.Examples {
			b.WriteString("  " + ex + "\n")
		}
	}
	if len(s.Tags) > 0 {
		b.WriteString("\nTags: " + strings.Join(s.Tags, ", ") + "\n")
	}
	if s.Deprecated != "" {
		b.WriteString("\nDEPRECATED: " + s.Deprecated + "\n")
	}
	return b.String()
}

// ScoreMatch ranks a spec against a search query. Higher is better; 0 means
// no match. Scoring is deterministic and cheap — no index, just substring +
// tag + word matches weighted by field.
//
//	name exact      : 100
//	name prefix     : 50
//	name substring  : 25
//	tag exact       : 20 per tag
//	summary token   : 5 per token match
//	purpose token   : 2 per token match
func ScoreMatch(spec ToolSpec, query string) int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return 1 // empty query matches everything weakly (stable sort by name)
	}
	qTokens := tokenize(q)
	name := strings.ToLower(spec.Name)
	score := 0
	switch {
	case name == q:
		score += 100
	case strings.HasPrefix(name, q):
		score += 50
	case strings.Contains(name, q):
		score += 25
	}
	for _, tag := range spec.Tags {
		t := strings.ToLower(tag)
		if t == q {
			score += 20
			continue
		}
		for _, qt := range qTokens {
			if t == qt {
				score += 15
			} else if strings.Contains(t, qt) {
				score += 5
			}
		}
	}
	score += tokenOverlap(strings.ToLower(spec.Summary), qTokens) * 5
	score += tokenOverlap(strings.ToLower(spec.Purpose), qTokens) * 2
	return score
}

func tokenize(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', ',', '.', ';', ':', '/', '\\', '-', '_':
			return true
		}
		return false
	})
	out := fields[:0]
	for _, f := range fields {
		t := strings.TrimSpace(f)
		if len(t) >= 2 {
			out = append(out, t)
		}
	}
	return out
}

func tokenOverlap(text string, qTokens []string) int {
	if text == "" || len(qTokens) == 0 {
		return 0
	}
	textTokens := tokenize(text)
	set := make(map[string]struct{}, len(textTokens))
	for _, t := range textTokens {
		set[t] = struct{}{}
	}
	hits := 0
	for _, qt := range qTokens {
		if _, ok := set[qt]; ok {
			hits++
		}
	}
	return hits
}

// SortSpecs sorts a []ToolSpec alphabetically by Name. Stable ordering helps
// list rendering and snapshot tests.
func SortSpecs(specs []ToolSpec) {
	sort.SliceStable(specs, func(i, j int) bool {
		return specs[i].Name < specs[j].Name
	})
}
