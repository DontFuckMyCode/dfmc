package context

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// ContextInspector analyzes and visualizes context composition.
// Use Inspect() to get a detailed breakdown of what's in the context.
type ContextInspector struct {
	projectRoot string
	chunks      []types.ContextChunk
	maxTokens   int
}

// NewInspector creates a new context inspector.
func NewInspector(projectRoot string, chunks []types.ContextChunk) *ContextInspector {
	return &ContextInspector{
		projectRoot: projectRoot,
		chunks:      chunks,
		maxTokens:   16000, // default
	}
}

// NewInspectorWithBudget creates a new context inspector with token budget.
func NewInspectorWithBudget(projectRoot string, chunks []types.ContextChunk, maxTokens int) *ContextInspector {
	return &ContextInspector{
		projectRoot: projectRoot,
		chunks:      chunks,
		maxTokens:   maxTokens,
	}
}

// InspectionResult is the structured output of context inspection.
type InspectionResult struct {
	// Summary counts
	TotalFiles  int            `json:"total_files"`
	TotalTokens int            `json:"total_tokens"`
	TotalLines  int            `json:"total_lines"`

	// Breakdown by source type
	BySource map[string]SourceStats `json:"by_source"`

	// Breakdown by language
	ByLanguage map[string]LanguageStats `json:"by_language"`

	// Detailed file list
	Files []FileDetail `json:"files"`

	// Token budget status
	Budget BudgetStatus `json:"budget"`
}

// SourceStats aggregates stats for a chunk source type.
type SourceStats struct {
	Count  int `json:"count"`
	Tokens int `json:"tokens"`
	Lines  int `json:"lines"`
}

// LanguageStats aggregates stats for a programming language.
type LanguageStats struct {
	Count  int `json:"count"`
	Tokens int `json:"tokens"`
}

// FileDetail is per-file context information.
type FileDetail struct {
	Path        string  `json:"path"`
	RelPath     string  `json:"rel_path"`
	Language    string  `json:"language"`
	Lines       string  `json:"lines"`
	Tokens      int     `json:"tokens"`
	Source      string  `json:"source"`
	Score       float64 `json:"score"`
	Compression string  `json:"compression"`
	FirstLine   string  `json:"first_line"`
}

// BudgetStatus shows how context uses the available token budget.
type BudgetStatus struct {
	Total      int     `json:"total"`
	Used       int     `json:"used"`
	Remaining  int     `json:"remaining"`
	UsedPct    float64 `json:"used_pct"`
	AvgPerFile float64 `json:"avg_per_file"`
}

// Inspect analyzes the given context chunks and returns a detailed breakdown.
func (ci *ContextInspector) Inspect() InspectionResult {
	r := InspectionResult{
		BySource:   make(map[string]SourceStats),
		ByLanguage: make(map[string]LanguageStats),
		Files:      make([]FileDetail, 0, len(ci.chunks)),
	}

	if len(ci.chunks) == 0 {
		r.Budget = BudgetStatus{Total: ci.maxTokens, Used: 0, Remaining: ci.maxTokens, UsedPct: 0}
		return r
	}

	seenFiles := make(map[string]bool)

	for _, ch := range ci.chunks {
		r.TotalTokens += ch.TokenCount
		lines := ch.LineEnd - ch.LineStart + 1
		r.TotalLines += lines

		// Skip duplicate files (same path, keep first)
		if seenFiles[ch.Path] {
			continue
		}
		seenFiles[ch.Path] = true
		r.TotalFiles++

		// Source breakdown
		src := ch.Source
		if src == "" {
			src = "unknown"
		}
		s := r.BySource[src]
		s.Count++
		s.Tokens += ch.TokenCount
		s.Lines += lines
		r.BySource[src] = s

		// Language breakdown
		lang := ch.Language
		if lang == "" {
			lang = "unknown"
		}
		l := r.ByLanguage[lang]
		l.Count++
		l.Tokens += ch.TokenCount
		r.ByLanguage[lang] = l

		// File detail
		relPath := ch.Path
		if strings.HasPrefix(ch.Path, ci.projectRoot) {
			relPath = strings.TrimPrefix(ch.Path, ci.projectRoot)
			if len(relPath) > 0 && (relPath[0] == '/' || relPath[0] == '\\') {
				relPath = relPath[1:]
			}
		}

		firstLine := ""
		if len(ch.Content) > 0 {
			if nl := strings.Index(ch.Content, "\n"); nl > 0 {
				firstLine = ch.Content[:nl]
			} else {
				firstLine = ch.Content
			}
			if len(firstLine) > 60 {
				firstLine = firstLine[:60] + "..."
			}
		}

		r.Files = append(r.Files, FileDetail{
			Path:        ch.Path,
			RelPath:     relPath,
			Language:    lang,
			Lines:       fmt.Sprintf("%d-%d", ch.LineStart, ch.LineEnd),
			Tokens:      ch.TokenCount,
			Source:      src,
			Score:       ch.Score,
			Compression: ch.Compression,
			FirstLine:   firstLine,
		})
	}

	// Budget status
	r.Budget = BudgetStatus{
		Total:     ci.maxTokens,
		Used:      r.TotalTokens,
		Remaining: ci.maxTokens - r.TotalTokens,
		UsedPct:   float64(r.TotalTokens) / float64(ci.maxTokens) * 100,
		AvgPerFile: float64(r.TotalTokens) / float64(r.TotalFiles),
	}

	// Sort files by score descending
	sort.Slice(r.Files, func(i, j int) bool {
		return r.Files[i].Score > r.Files[j].Score
	})

	return r
}

// Text returns a human-readable context inspection.
func (r InspectionResult) Text() string {
	var b strings.Builder

	b.WriteString("Context Inspection\n")
	b.WriteString("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Summary
	b.WriteString(fmt.Sprintf("Files:   %d\n", r.TotalFiles))
	b.WriteString(fmt.Sprintf("Tokens:  %d / %d (%.1f%%)\n", r.Budget.Used, r.Budget.Total, r.Budget.UsedPct))
	b.WriteString(fmt.Sprintf("Lines:   %d\n", r.TotalLines))
	b.WriteString(fmt.Sprintf("Avg/file: %.0f tokens\n\n", r.Budget.AvgPerFile))

	// By source
	if len(r.BySource) > 0 {
		b.WriteString("Sources:\n")
		sources := make([]string, 0, len(r.BySource))
		for src := range r.BySource {
			sources = append(sources, src)
		}
		sort.Strings(sources)
		for _, src := range sources {
			s := r.BySource[src]
			b.WriteString(fmt.Sprintf("  %-20s %2d files  %5d tokens  %4d lines\n",
				src, s.Count, s.Tokens, s.Lines))
		}
		b.WriteString("\n")
	}

	// By language
	if len(r.ByLanguage) > 0 {
		b.WriteString("Languages:\n")
		languages := make([]string, 0, len(r.ByLanguage))
		for lang := range r.ByLanguage {
			languages = append(languages, lang)
		}
		sort.Strings(languages)
		for _, lang := range languages {
			l := r.ByLanguage[lang]
			b.WriteString(fmt.Sprintf("  %-15s %2d files  %5d tokens\n", lang, l.Count, l.Tokens))
		}
		b.WriteString("\n")
	}

	// Files (sorted by score)
	b.WriteString("Files (by relevance):\n")
	for i, f := range r.Files {
		if i >= 15 {
			b.WriteString(fmt.Sprintf("  ... and %d more file(s)\n", len(r.Files)-i))
			break
		}
		source := f.Source
		if source == "" {
			source = "-"
		}
		b.WriteString(fmt.Sprintf("  %-40s %s %s %4d tokens\n",
			f.RelPath, f.Language, source, f.Tokens))
	}

	return b.String()
}

// JSON returns a JSON-encoded context inspection.
func (r InspectionResult) JSON() string {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return `{"error": "json encode failed: ` + err.Error() + `"}`
	}
	return string(b)
}

// RawJSON is an alias for JSON.
func (r InspectionResult) RawJSON() string {
	return r.JSON()
}

// Inspect is a package-level convenience function for backward compatibility.
// Creates an inspector and runs inspection in one step.
func Inspect(projectRoot string, chunks []types.ContextChunk, maxTokens int) InspectionResult {
	ci := &ContextInspector{
		projectRoot: projectRoot,
		chunks:      chunks,
		maxTokens:   maxTokens,
	}
	return ci.Inspect()
}