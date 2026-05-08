package tools

// spec_validate.go — surface common authoring problems in a markdown
// spec without pulling in a full markdown linter. Companion to
// spec_parse.go and spec_to_todo.go.
//
// Checks (intentionally narrow — every rule fired here must be one
// the user can act on within the same file):
//   - malformed task syntax: `- []`, `- [Y]`, `- [ ]text` (no space)
//     → warn; the model is constantly tempted to drift into these
//     when hand-editing a checklist.
//   - broken internal anchor links: `[txt](#anchor)` where anchor
//     does not match any heading in this file → error.
//   - broken relative paths: `[txt](rel/path)` (no protocol) where
//     the target does not exist on disk → warn.
//   - heading skips: h1 → h3 with no h2 in between → info.
//
// Anything beyond this list belongs in a separate, deeper linter.
// The point of spec_validate is "30-second sanity check before
// handing this spec to spec_to_todo".

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type SpecValidateTool struct{}

func NewSpecValidateTool() *SpecValidateTool { return &SpecValidateTool{} }
func (t *SpecValidateTool) Name() string     { return "spec_validate" }
func (t *SpecValidateTool) Description() string {
	return "Lint a markdown spec for malformed tasks, broken links, and heading skips."
}

type specIssue struct {
	Severity string `json:"severity"` // "error" | "warn" | "info"
	Line     int    `json:"line"`
	Rule     string `json:"rule"`
	Message  string `json:"message"`
}

// linkRE matches inline markdown links [text](target). Capturing
// group 1 = link text, 2 = target. Greedy on text is fine here since
// we only care about the target.
var linkRE = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

// malformedTaskRE catches the three high-frequency typos: missing
// closing bracket, weird marker char, and missing space after `]`.
// We deliberately don't try to recover the intended task text —
// surfacing the line number is enough for the author to fix.
var malformedTaskRE = regexp.MustCompile(`^\s*-\s+\[(?:[^ xX\]]|[ xX][^\]\s])`)

// taskMissingSpaceRE catches `- [ ]text` (closed-bracket-then-text
// with no space). The good form is `- [ ] text`.
var taskMissingSpaceRE = regexp.MustCompile(`^\s*-\s+\[[ xX]\]\S`)

// fenceRE2 lives in spec_parse.go as fenceRE; we reuse it without
// re-exporting to keep both files self-contained.

func (t *SpecValidateTool) Execute(ctx context.Context, req Request) (Result, error) {
	path := strings.TrimSpace(asString(req.Params, "path", ""))
	if path == "" {
		return Result{}, missingParamError("spec_validate", "path", req.Params,
			`{"path":".project/SPECIFICATION.md"}`,
			`path is the markdown spec to lint. No other args today.`)
	}
	abs, err := EnsureWithinRoot(req.ProjectRoot, path)
	if err != nil {
		return Result{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Result{}, fmt.Errorf("read spec: %w", err)
	}

	sections, _ := parseSpecMarkdown(string(data), false)
	anchors := make(map[string]struct{}, len(sections))
	for _, s := range sections {
		anchors[s.Anchor] = struct{}{}
	}

	issues := lintSpecLines(string(data), anchors, abs)
	bySeverity := map[string]int{"error": 0, "warn": 0, "info": 0}
	for _, iss := range issues {
		bySeverity[iss.Severity]++
	}
	out := map[string]any{
		"path":        path,
		"issue_count": len(issues),
		"by_severity": bySeverity,
		"valid":       bySeverity["error"] == 0,
		"issues":      issues,
	}
	return Result{Success: true, Data: out}, nil
}

// lintSpecLines runs every per-line check. Pure function so tests
// drive it without disk I/O.
//
// fileAbs is the absolute path to the spec being linted; it is used
// only to resolve relative link targets via filepath.Dir(fileAbs).
// Pass "" to skip the path-existence check (link targets that look
// relative will then be reported as info-level "could not verify").
func lintSpecLines(body string, anchors map[string]struct{}, fileAbs string) []specIssue {
	lines := strings.Split(body, "\n")
	issues := make([]specIssue, 0)
	inFence := false
	prevHeadingLevel := 0
	specDir := ""
	if fileAbs != "" {
		specDir = filepath.Dir(fileAbs)
	}

	for i, line := range lines {
		lineNum := i + 1
		if fenceRE.MatchString(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		// Heading skip rule.
		if m := headingRE.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			if prevHeadingLevel != 0 && level > prevHeadingLevel+1 {
				issues = append(issues, specIssue{
					Severity: "info",
					Line:     lineNum,
					Rule:     "heading_skip",
					Message:  fmt.Sprintf("heading jumps from level %d to level %d", prevHeadingLevel, level),
				})
			}
			prevHeadingLevel = level
			continue
		}

		// Malformed task syntax — two distinct rules so the message
		// can name what's wrong, not just "this line is weird".
		if malformedTaskRE.MatchString(line) {
			issues = append(issues, specIssue{
				Severity: "warn",
				Line:     lineNum,
				Rule:     "task_syntax",
				Message:  "checkbox marker should be `[ ]` or `[x]`/`[X]`",
			})
		} else if taskMissingSpaceRE.MatchString(line) {
			issues = append(issues, specIssue{
				Severity: "warn",
				Line:     lineNum,
				Rule:     "task_syntax",
				Message:  "missing space after `]`; write `- [ ] text` not `- [ ]text`",
			})
		}

		// Link-target validation.
		for _, m := range linkRE.FindAllStringSubmatchIndex(line, -1) {
			// m = [whole_lo, whole_hi, text_lo, text_hi, target_lo, target_hi]
			target := line[m[4]:m[5]]
			validateLinkTarget(&issues, lineNum, target, anchors, specDir)
		}
	}
	return issues
}

func validateLinkTarget(issues *[]specIssue, line int, target string, anchors map[string]struct{}, specDir string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return
	}
	// Internal anchor: starts with `#`.
	if anchor, ok := strings.CutPrefix(target, "#"); ok {
		if _, ok := anchors[anchor]; !ok {
			*issues = append(*issues, specIssue{
				Severity: "error",
				Line:     line,
				Rule:     "broken_anchor",
				Message:  fmt.Sprintf("link points to unknown anchor #%s", anchor),
			})
		}
		return
	}
	// External / protocol-prefixed: skip.
	if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") || strings.HasPrefix(target, "tel:") {
		return
	}
	// Strip a fragment so foo.md#bar is checked as foo.md.
	pathPart := target
	if idx := strings.Index(pathPart, "#"); idx >= 0 {
		pathPart = pathPart[:idx]
	}
	if pathPart == "" {
		return
	}
	if specDir == "" {
		// Cannot verify without an anchor on disk; report as info so
		// the call is honest about what it didn't check.
		*issues = append(*issues, specIssue{
			Severity: "info",
			Line:     line,
			Rule:     "link_unverified",
			Message:  fmt.Sprintf("could not verify relative path %q (no on-disk root)", pathPart),
		})
		return
	}
	resolved := pathPart
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(specDir, pathPart)
	}
	if _, err := os.Stat(resolved); err != nil {
		*issues = append(*issues, specIssue{
			Severity: "warn",
			Line:     line,
			Rule:     "broken_link",
			Message:  fmt.Sprintf("relative link target not found: %s", pathPart),
		})
	}
}

func (t *SpecValidateTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "spec_validate",
		Title:   "Lint markdown spec",
		Summary: "Surface malformed tasks, broken anchor/path links, and heading skips in a markdown spec.",
		Purpose: "Run before spec_to_todo or before handing a spec to a human reviewer to catch authoring slips that would otherwise turn into surprising plan failures downstream.",
		Prompt: `Reads a markdown spec and reports four issue classes:

- ` + "`task_syntax`" + ` (warn): malformed GFM checkbox like ` + "`- []`" + `, ` + "`- [Y]`" + `, or ` + "`- [ ]text`" + ` with no space.
- ` + "`broken_anchor`" + ` (error): inline link ` + "`[txt](#anchor)`" + ` whose anchor doesn't match any heading slug in this file.
- ` + "`broken_link`" + ` (warn): relative path target ` + "`[txt](rel/path)`" + ` that doesn't exist on disk. Protocols (http/https/mailto/tel) and absolute paths are skipped.
- ` + "`heading_skip`" + ` (info): heading jumps more than one level (e.g. h1 → h3 with no h2).

Args:
- path (required): markdown file relative to project root.

Output: {path, valid, issue_count, by_severity:{error,warn,info}, issues:[{severity, line, rule, message}]}.

When to use:
- Pre-flight before spec_to_todo so you don't materialize TODOs from a spec full of typos.
- After hand-editing a spec to confirm anchor refs still resolve.

When NOT to use:
- Prose quality, grammar, or style — this tool only sees structure.`,
		Risk: RiskRead,
		Tags: []string{"read", "spec", "lint"},
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "Markdown spec file relative to project root."},
		},
		Returns:    "{path, valid, issue_count, by_severity, issues:[...]}.",
		Examples:   []string{`{"path":".project/SPECIFICATION.md"}`},
		Idempotent: true,
		CostHint:   "io-bound",
	}
}
