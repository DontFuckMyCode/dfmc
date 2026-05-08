// slash_template_compose.go — per-verb prompt builders + arg-parsing
// helpers for the template-family slash commands. Sibling of
// slash_template.go which keeps the dispatcher (runTemplateSlash) and
// the Model-receiver scope helpers (composeReviewPrompt with scope-map
// attached, reviewScopeGuide, reviewScopeOutline, defaultReviewTargets).
//
// All functions here are pure (no Model receiver) so they're reusable
// from tests and from any future non-Model entry point that wants the
// same prompt shape.

package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	codeast "github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func composeReviewPrompt(targets []string, tail string) string {
	reviewTail := strings.TrimSpace(tail)
	if len(targets) == 0 {
		msg := "Review the current worktree diff only. Focus on correctness, risks, and missing tests. " +
			"Start from changed hunks, avoid broad codebase sweeps, and only read more context when the diff is insufficient."
		if reviewTail != "" {
			msg += " " + reviewTail
		}
		return strings.TrimSpace(msg)
	}
	return fmt.Sprintf(
		"Review the following file(s) for correctness, risks, readability, and missing tests: %s\n"+
			"Stay budget-aware: inspect the touched symbols or changed hunks first, avoid broad repo scans, and only open more files when directly justified.\n%s",
		joinFileMarkers(targets), reviewTail,
	)
}

// reviewSymbolOutline parses absPath via the AST engine (with a short
// 750ms timeout so a slow parse can't block the slash dispatcher) and
// renders up to 8 symbols as bullets. Returns scopeMapUnavailable when
// the file has no symbols or the parser failed — caller can then fall
// back to reviewSectionOutline.
func reviewSymbolOutline(absPath, target string) string {
	parser := codeast.New()
	defer func() { _ = parser.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()

	res, err := parser.ParseFile(ctx, absPath)
	if err != nil || res == nil || len(res.Symbols) == 0 {
		return scopeMapUnavailable
	}
	symbols := append([]types.Symbol(nil), res.Symbols...)
	sort.SliceStable(symbols, func(i, j int) bool {
		if symbols[i].Line == symbols[j].Line {
			return strings.ToLower(symbols[i].Name) < strings.ToLower(symbols[j].Name)
		}
		return symbols[i].Line < symbols[j].Line
	})

	limit := min(len(symbols), 8)
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

// reviewSectionOutline is the AST-less fallback: scans for markdown-style
// headings or bracketed section markers, otherwise emits 160-line chunk
// pointers (capped at 6) so the model still has a navigation map.
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
		end = min(end, totalLines)
		chunks = append(chunks, fmt.Sprintf("  - lines %d-%d", start, end))
	}
	return fmt.Sprintf("- %s:\n%s", fileMarker(target), strings.Join(chunks, "\n"))
}

func composeExplainPrompt(targets []string, tail string) string {
	if len(targets) == 0 {
		tail = strings.TrimSpace(tail)
		if tail == "" {
			return "Explain the recent changes or the listed topic."
		}
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
		return "" // triggers usage message in runTemplateSlash
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
		if strings.Contains(s, ":") {
			// Reject Windows drive letters (C:\, D:/, etc.) — single letter followed by :\ or :/
			if len(s) >= 2 && unicode.IsLetter(rune(s[0])) && (s[1] == '\\' || s[1] == '/') {
				return false
			}
			// Accept PATH:LINE or URL-style
			if !strings.HasPrefix(s, "http") {
				return true
			}
		}
		return true
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
