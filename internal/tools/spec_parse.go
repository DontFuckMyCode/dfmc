package tools

// spec_parse.go — parse a markdown specification document into a
// flat section + task index. Designed as the "see what's in this
// spec" primitive that downstream tools (spec_to_todo, spec_validate)
// can build on without re-implementing the markdown walk.
//
// Why a custom walker rather than a third-party markdown library:
// the consumer surface is tiny (headings 1-6, GFM-style task list
// items, fenced code blocks to skip) and pulling in goldmark/cmark
// would be a 50× weight increase for one tool. The walker is
// permissive — malformed markdown still yields a best-effort
// section list rather than a parse error, because a half-written
// spec is the common case the model needs to inspect.
//
// Naming caveat: this file is about a project SPECIFICATION document
// (e.g. .project/SPECIFICATION.md), not the tools.ToolSpec metadata
// in spec.go / spec_search.go. The two share a word but live in
// different domains.

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"unicode"
)

// SpecParseTool implements the spec_parse tool.
type SpecParseTool struct{}

func NewSpecParseTool() *SpecParseTool { return &SpecParseTool{} }
func (t *SpecParseTool) Name() string  { return "spec_parse" }
func (t *SpecParseTool) Description() string {
	return "Parse a markdown specification into headings + task-list index."
}

// specSection is one heading block in the document.
type specSection struct {
	Heading      string `json:"heading"`
	Level        int    `json:"level"`
	Anchor       string `json:"anchor"`
	ParentAnchor string `json:"parent_anchor,omitempty"`
	LineStart    int    `json:"line_start"` // 1-based; the heading line itself
	LineEnd      int    `json:"line_end"`   // 1-based; last line before next heading at <= level
	TaskCount    int    `json:"task_count"`
}

// specTask is one GFM-style checklist item. Anchored to its enclosing
// section so spec_to_todo can group tasks by section.
type specTask struct {
	SectionAnchor string `json:"section_anchor"`
	Line          int    `json:"line"`
	Done          bool   `json:"done"`
	Indent        int    `json:"indent"`
	Text          string `json:"text"`
}

// headingRE matches ATX headings only. Setext underline (=== / ---)
// is NOT supported — every spec we ship uses ATX exclusively, and
// adding setext means a two-line lookahead that would mostly fire
// false positives on horizontal rules.
var headingRE = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)

// taskRE matches GFM task list items. The `[ ]` and `[x]/[X]` are
// the canonical forms; we do NOT accept `[-]` (in-progress, custom
// extension) because it is not portable across renderers.
var taskRE = regexp.MustCompile(`^(\s*)-\s+\[([ xX])\]\s+(.+?)\s*$`)

// fenceRE matches the start/end of a fenced code block. Anything
// inside is skipped — heading-shaped lines inside code samples are a
// common false positive without this.
var fenceRE = regexp.MustCompile("^(```|~~~)")

func (t *SpecParseTool) Execute(ctx context.Context, req Request) (Result, error) {
	path := strings.TrimSpace(asString(req.Params, "path", ""))
	if path == "" {
		return Result{}, missingParamError("spec_parse", "path", req.Params,
			`{"path":".project/SPECIFICATION.md"}`,
			`path is the markdown spec file relative to the project root. Optional: max_body_chars (cap on body excerpt; default 0 = no body), include_tasks (default true).`)
	}
	abs, err := EnsureWithinRoot(req.ProjectRoot, path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{}, fmt.Errorf("read spec: %w", err)
	}
	includeTasks := true
	if v, ok := req.Params["include_tasks"].(bool); ok {
		includeTasks = v
	}

	sections, tasks := parseSpecMarkdown(string(data), includeTasks)
	out := map[string]any{
		"path":          path,
		"section_count": len(sections),
		"task_count":    len(tasks),
		"sections":      sections,
	}
	if len(sections) > 0 {
		out["title"] = sections[0].Heading
	}
	if includeTasks {
		out["tasks"] = tasks
	}
	return Result{Success: true, Data: out}, nil
}

// parseSpecMarkdown walks the document line by line, emitting a flat
// section list (parent links via anchor) and a flat task list. Pure
// function — exported-internally so tests can drive it without disk.
func parseSpecMarkdown(body string, includeTasks bool) ([]specSection, []specTask) {
	lines := strings.Split(body, "\n")
	sections := make([]specSection, 0, 16)
	tasks := make([]specTask, 0, 16)
	usedAnchors := make(map[string]int)
	// parentStack tracks active heading anchors by level so a level-3
	// heading correctly attributes its parent_anchor to the most
	// recent level-2.
	parentStack := make([]string, 7) // indices 1..6 used; 0 unused
	inFence := false

	flushSection := func(idx int, endLine int) {
		if idx >= 0 && idx < len(sections) {
			sections[idx].LineEnd = endLine
		}
	}
	currentIdx := -1
	for i, line := range lines {
		lineNum := i + 1
		// Toggle fenced-code state before any heading/task match so
		// content inside ```...``` is treated as opaque text.
		if fenceRE.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		if m := headingRE.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			heading := strings.TrimSpace(m[2])
			anchor := dedupedAnchor(slugifyHeading(heading), usedAnchors)
			// Close the previous section's range at the line BEFORE
			// this heading; if there was no prior section the call is
			// a no-op.
			flushSection(currentIdx, lineNum-1)
			parent := ""
			for lvl := level - 1; lvl >= 1; lvl-- {
				if parentStack[lvl] != "" {
					parent = parentStack[lvl]
					break
				}
			}
			sec := specSection{
				Heading:      heading,
				Level:        level,
				Anchor:       anchor,
				ParentAnchor: parent,
				LineStart:    lineNum,
				LineEnd:      lineNum, // refined when next heading or EOF closes
			}
			sections = append(sections, sec)
			currentIdx = len(sections) - 1
			parentStack[level] = anchor
			// Reset deeper levels — a fresh level-2 invalidates any
			// level-3+ context we were tracking.
			for lvl := level + 1; lvl < len(parentStack); lvl++ {
				parentStack[lvl] = ""
			}
			continue
		}

		if includeTasks {
			if m := taskRE.FindStringSubmatch(line); m != nil {
				indent := len(m[1])
				done := m[2] == "x" || m[2] == "X"
				text := strings.TrimSpace(m[3])
				sectionAnchor := ""
				if currentIdx >= 0 {
					sectionAnchor = sections[currentIdx].Anchor
					sections[currentIdx].TaskCount++
				}
				tasks = append(tasks, specTask{
					SectionAnchor: sectionAnchor,
					Line:          lineNum,
					Done:          done,
					Indent:        indent,
					Text:          text,
				})
			}
		}
	}
	// Close the final section at EOF.
	flushSection(currentIdx, len(lines))
	return sections, tasks
}

// slugifyHeading produces a GitHub-flavoured anchor: lowercase, ASCII
// letters/digits/dashes/underscores; runs of other chars collapse into
// a single dash; leading/trailing dashes trimmed. The exact GitHub
// rule (preserve unicode letters, drop punctuation classes) is more
// elaborate; we approximate to the common case because we only need
// stable, dedupeable anchors — not byte-identical-to-GitHub output.
func slugifyHeading(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToLower(r))
			prevDash = false
		case r == '-' || r == '_':
			b.WriteRune(r)
			prevDash = false
		case unicode.IsSpace(r):
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		default:
			// Punctuation, symbols → collapse to dash. Avoid emitting
			// double dashes; readers don't care, but it makes the
			// anchor cleaner.
			if !prevDash {
				b.WriteRune('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		// All-symbols heading → fall back to a stable placeholder so
		// dedupe still works.
		return "section"
	}
	return out
}

// dedupedAnchor appends a -N suffix when the slug has already been
// emitted, mirroring the GitHub anchor uniqueness rule. Mutates the
// counter map.
func dedupedAnchor(base string, used map[string]int) string {
	n := used[base]
	used[base] = n + 1
	if n == 0 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, n)
}

func (t *SpecParseTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "spec_parse",
		Title:   "Parse markdown spec",
		Summary: "Read a markdown spec file and return a flat heading + task-list index.",
		Purpose: "Use when you need a structural view of SPECIFICATION.md / IMPLEMENTATION.md / TASKS.md without slurping the whole document into context.",
		Prompt: `Returns the document's heading tree (as a flat list with parent_anchor links) and every GFM task-list item (` + "`- [ ]`" + ` / ` + "`- [x]`" + `) anchored to its enclosing section. Bodies are NOT included — pair with read_file using each section's line range when you need the prose.

Args:
- path (required): markdown file relative to project root. ` + "`.project/SPECIFICATION.md`" + ` is the canonical target.
- include_tasks (optional, default true): when false, skip task extraction (sections only).

Output: {path, title, section_count, task_count, sections:[{heading, level, anchor, parent_anchor, line_start, line_end, task_count}], tasks:[{section_anchor, line, done, indent, text}]}.

When to use:
- "What sections does the spec have?" — one call, much cheaper than read_file on a 3k-line spec.
- "Which TODOs are still unchecked?" — filter tasks where done=false.
- Pre-step before spec_to_todo (which converts the same task index into Drive TODOs).

When NOT to use:
- Body-level inspection (use read_file with the section's line range).
- Non-markdown specs (XML, YAML — neither headings nor task lists exist there).`,
		Risk: RiskRead,
		Tags: []string{"read", "spec", "markdown", "planning"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Markdown spec file path relative to project root."},
			{Name: "include_tasks", Type: ArgBoolean, Default: true, Description: "Include the task-list index (set false when you only want the heading tree)."},
		},
		Returns:    "{path, title, section_count, task_count, sections:[...], tasks:[...]}.",
		Examples:   []string{`{"path":".project/SPECIFICATION.md"}`, `{"path":"docs/PLAN.md","include_tasks":false}`},
		Idempotent: true,
		CostHint:   "io-bound",
	}
}
