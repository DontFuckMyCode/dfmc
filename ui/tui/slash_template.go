// slash_template.go — the template-family slash commands (/review,
// /explain, /refactor, /test, /doc). Each verb composes a natural-
// language prompt and feeds it through the regular chat pipeline so
// streaming, context injection, and the agent loop come for free. No
// provider call happens here directly — runTemplateSlash just shapes
// the prompt and hands it to submitChatQuestion.
//
//   - runTemplateSlash: the dispatcher.
//   - compose{Review,Explain,Refactor,Test,Doc}Prompt: per-verb
//     prompt builders.
//   - reviewScopeGuide / reviewScopeOutline / reviewSymbolOutline /
//     reviewSectionOutline: the "scope map" attached to /review so the
//     model starts from touched symbols rather than sweeping the repo.
//   - splitTargetsAndTail / looksLikePath / joinFileMarkers /
//     defaultReviewTargets: small arg-parsing helpers shared by the
//     verbs.
//
// Shared helpers (truncateSingleLine, fileMarker, blankFallback,
// appendSystemMessage, submitChatQuestion, etc.) live in other files
// in the tui package and stay accessible through same-package
// visibility.

package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	codeast "github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// runTemplateSlash composes a natural-language prompt for one of the
// skill-style shortcuts (/review, /explain, /refactor, /test, /doc) and feeds
// it through the normal chat pipeline so it benefits from streaming, context
// injection, and agent-loop tooling without duplicating any of that code.
func (m Model) runTemplateSlash(verb string, args []string, raw string) (Model, tea.Cmd, bool) {
	verb = strings.ToLower(strings.TrimSpace(verb))
	if verb == "" {
		return m, nil, false
	}
	_ = raw
	payload := strings.TrimSpace(strings.Join(args, " "))
	targets, tail := splitTargetsAndTail(args)
	if verb == "review" {
		targets = m.defaultReviewTargets(targets)
		if len(targets) == 0 && len(tail) > 0 {
			words := strings.Fields(tail)
			if len(words) == 1 && len(words[0]) > 2 {
				targets = []string{words[0]}
				tail = ""
			}
		}
	} else if len(targets) == 0 {
		if target := strings.TrimSpace(m.toolTargetFile()); target != "" {
			targets = []string{target}
		}
	}

	var prompt string
	switch verb {
	case "review":
		prompt = m.composeReviewPrompt(targets, tail)
	case "explain":
		prompt = composeExplainPrompt(targets, tail)
	case "refactor":
		prompt = composeRefactorPrompt(targets, tail)
	case "test":
		prompt = composeTestPrompt(targets, tail)
	case "doc":
		prompt = composeDocPrompt(targets, tail)
	default:
		return m, nil, false
	}
	if prompt == "" {
		prompt = payload
	}
	if strings.TrimSpace(prompt) == "" {
		m.notice = "/" + verb + " needs a file or topic."
		return m.appendSystemMessage("Usage: /" + verb + " <path|topic>"), nil, true
	}
	m.chat.input = ""
	m = m.appendSystemMessage(fmt.Sprintf("/%s → submitting as chat: %s", verb, truncateSingleLine(prompt, 120)))
	next, cmdOut := m.submitChatQuestion(prompt, nil)
	return next, cmdOut, true
}

func composeReviewPrompt(targets []string, tail string) string {
	reviewTail := strings.TrimSpace(tail)
	if len(targets) == 0 {
		return strings.TrimSpace(
			"Review the current worktree diff only. Focus on correctness, risks, and missing tests. " +
				"Start from changed hunks, avoid broad codebase sweeps, and only read more context when the diff is insufficient. " +
				reviewTail,
		)
	}
	return fmt.Sprintf(
		"Review the following file(s) for correctness, risks, readability, and missing tests: %s\n"+
			"Stay budget-aware: inspect the touched symbols or changed hunks first, avoid broad repo scans, and only open more files when directly justified.\n%s",
		joinFileMarkers(targets), reviewTail,
	)
}

func (m Model) composeReviewPrompt(targets []string, tail string) string {
	base := composeReviewPrompt(targets, tail)
	if len(targets) == 0 {
		return base
	}
	scopeGuide := strings.TrimSpace(m.reviewScopeGuide(targets))
	if scopeGuide == "" {
		return base
	}
	return strings.TrimSpace(base + "\n\n" + scopeGuide)
}

func (m Model) reviewScopeGuide(targets []string) string {
	root := strings.TrimSpace(m.projectRoot())
	if root == "" {
		return ""
	}
	limit := len(targets)
	if limit > 2 {
		limit = 2
	}
	outlines := make([]string, 0, limit)
	for _, target := range targets[:limit] {
		if outline := strings.TrimSpace(m.reviewScopeOutline(root, target)); outline != "" {
			outlines = append(outlines, outline)
		}
	}
	if len(outlines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Scope map:\n")
	b.WriteString("Use this map before opening more files. Start with 1-2 high-risk scopes, inspect only those symbols or line ranges first, and widen only if the scoped read leaves uncertainty.\n")
	b.WriteString(strings.Join(outlines, "\n"))
	return strings.TrimSpace(b.String())
}

func (m Model) reviewScopeOutline(root, target string) string {
	target = filepath.ToSlash(strings.TrimSpace(target))
	if target == "" {
		return ""
	}
	abs := filepath.Join(root, filepath.FromSlash(target))
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return ""
	}
	if info.Size() > 512*1024 {
		return fmt.Sprintf("- %s: large file (%d bytes). Start from changed hunks or the most suspicious symbol names before widening.", fileMarker(target), info.Size())
	}
	if outline := reviewSymbolOutline(abs, target); outline != "" {
		return outline
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	return reviewSectionOutline(target, string(content))
}

func reviewSymbolOutline(absPath, target string) string {
	parser := codeast.New()
	defer func() { _ = parser.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()

	res, err := parser.ParseFile(ctx, absPath)
	if err != nil || res == nil || len(res.Symbols) == 0 {
		return ""
	}
	symbols := append([]types.Symbol(nil), res.Symbols...)
	sort.SliceStable(symbols, func(i, j int) bool {
		if symbols[i].Line == symbols[j].Line {
			return strings.ToLower(symbols[i].Name) < strings.ToLower(symbols[j].Name)
		}
		return symbols[i].Line < symbols[j].Line
	})

	limit := len(symbols)
	if limit > 8 {
		limit = 8
	}
	lines := make([]string, 0, limit+2)
	lines = append(lines, fmt.Sprintf("- %s (%s):", fileMarker(target), blankFallback(res.Language, "source")))
	for _, sym := range symbols[:limit] {
		label := strings.TrimSpace(sym.Name)
		if label == "" {
			continue
		}
		kind := strings.TrimSpace(string(sym.Kind))
		if kind == "" {
			kind = "symbol"
		}
		lines = append(lines, fmt.Sprintf("  - %s %s (line %d)", kind, label, sym.Line))
	}
	if extra := len(symbols) - limit; extra > 0 {
		lines = append(lines, fmt.Sprintf("  - ... plus %d more symbols", extra))
	}
	return strings.Join(lines, "\n")
}

func reviewSectionOutline(target, content string) string {
	lines := strings.Split(content, "\n")
	headings := make([]string, 0, 8)
	for idx, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "#"):
			headings = append(headings, fmt.Sprintf("  - section %s (line %d)", strings.TrimSpace(strings.TrimLeft(trimmed, "#")), idx+1))
		case strings.HasPrefix(trimmed, "[") && strings.Contains(trimmed, "]"):
			headings = append(headings, fmt.Sprintf("  - section %s (line %d)", trimmed, idx+1))
		}
		if len(headings) >= 8 {
			break
		}
	}
	if len(headings) > 0 {
		return fmt.Sprintf("- %s:\n%s", fileMarker(target), strings.Join(headings, "\n"))
	}

	totalLines := len(lines)
	if totalLines == 0 {
		return ""
	}
	const chunkSize = 160
	chunks := make([]string, 0, 6)
	for start := 1; start <= totalLines && len(chunks) < 6; start += chunkSize {
		end := start + chunkSize - 1
		if end > totalLines {
			end = totalLines
		}
		chunks = append(chunks, fmt.Sprintf("  - lines %d-%d", start, end))
	}
	return fmt.Sprintf("- %s:\n%s", fileMarker(target), strings.Join(chunks, "\n"))
}

func composeExplainPrompt(targets []string, tail string) string {
	if len(targets) == 0 {
		return strings.TrimSpace("Explain the recent changes or the listed topic: " + tail)
	}
	return fmt.Sprintf("Explain what this code does, its structure, and any non-obvious invariants: %s\n%s",
		joinFileMarkers(targets), strings.TrimSpace(tail))
}

func composeRefactorPrompt(targets []string, tail string) string {
	goal := strings.TrimSpace(tail)
	if goal == "" {
		goal = "propose a scoped, reversible refactor plan"
	}
	if len(targets) == 0 {
		return "Refactor target unspecified — " + goal
	}
	return fmt.Sprintf("Refactor %s. Goal: %s. Produce a scoped, reversible plan with file-level edits.",
		joinFileMarkers(targets), goal)
}

func composeTestPrompt(targets []string, tail string) string {
	if len(targets) == 0 {
		return strings.TrimSpace("Draft tests for the recent changes. " + tail)
	}
	return fmt.Sprintf("Draft tests for %s. Cover happy path, edge cases, and one regression. %s",
		joinFileMarkers(targets), strings.TrimSpace(tail))
}

func composeDocPrompt(targets []string, tail string) string {
	if len(targets) == 0 {
		return strings.TrimSpace("Draft or update documentation. " + tail)
	}
	return fmt.Sprintf("Draft or update documentation for %s. Keep it concise and reference-style. %s",
		joinFileMarkers(targets), strings.TrimSpace(tail))
}

// splitTargetsAndTail separates path-looking args from the free-form tail
// (used for `--goal <text>`, `--framework pytest`, etc.). An arg is treated as
// a target if it contains a path separator, a file extension, or is a bare
// identifier that would plausibly be a filename.
func splitTargetsAndTail(args []string) ([]string, string) {
	targets := make([]string, 0, len(args))
	tail := make([]string, 0, len(args))
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if looksLikePath(a) {
			targets = append(targets, a)
		} else {
			tail = append(tail, a)
		}
	}
	return targets, strings.Join(tail, " ")
}

func looksLikePath(s string) bool {
	if strings.HasPrefix(s, "-") {
		return false
	}
	if strings.ContainsAny(s, "/\\") {
		return true
	}
	if strings.Contains(s, ":") && !strings.HasPrefix(s, "http") {
		return true // PATH:LINE form
	}
	if ext := filepath.Ext(s); ext != "" {
		return true
	}
	return false
}

func joinFileMarkers(targets []string) string {
	out := make([]string, 0, len(targets))
	for _, t := range targets {
		out = append(out, fileMarker(t))
	}
	return strings.Join(out, " ")
}

func (m Model) defaultReviewTargets(explicit []string) []string {
	if len(explicit) > 0 {
		return explicit
	}
	if len(m.patchView.changed) > 0 {
		limit := len(m.patchView.changed)
		if limit > 4 {
			limit = 4
		}
		out := make([]string, 0, limit)
		for _, path := range m.patchView.changed[:limit] {
			if path = strings.TrimSpace(path); path != "" {
				out = append(out, path)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if target := strings.TrimSpace(m.toolTargetFile()); target != "" {
		return []string{target}
	}
	return nil
}
