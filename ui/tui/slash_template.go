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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const scopeMapUnavailable = "(scope map unavailable for this file type)"

// runTemplateSlash composes a natural-language prompt for one of the
// skill-style shortcuts (/review, /explain, /refactor, /test, /doc) and feeds
// it through the normal chat pipeline so it benefits from streaming, context
// injection, and agent-loop tooling without duplicating any of that code.
func (m Model) runTemplateSlash(verb string, args []string, raw string) (Model, tea.Cmd, bool) {
	verb = strings.ToLower(strings.TrimSpace(verb))
	if verb == "" {
		return m, nil, false
	}
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
	limit := min(len(targets), 2)
	outlines := make([]string, 0, limit)
	for _, target := range targets[:limit] {
		if outline := strings.TrimSpace(m.reviewScopeOutline(root, target)); outline != "" {
			outlines = append(outlines, outline)
		}
	}
	if len(outlines) == 0 && limit > 0 && len(targets) > 0 {
		if firstOutline := strings.TrimSpace(m.reviewScopeOutline(root, targets[0])); firstOutline != "" {
			outlines = append(outlines, firstOutline)
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
	if outline := reviewSymbolOutline(abs, target); outline != "" && outline != scopeMapUnavailable {
		return outline
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	return reviewSectionOutline(target, string(content))
}

func (m Model) defaultReviewTargets(explicit []string) []string {
	if len(explicit) > 0 {
		return explicit
	}
	if len(m.patchView.changed) > 0 {
		limit := min(len(m.patchView.changed), 4)
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
