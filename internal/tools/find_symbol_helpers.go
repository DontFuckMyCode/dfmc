package tools

// find_symbol_helpers.go — pure helpers used by FindSymbolTool.Execute:
// the per-symbol filters (name/kind matchers), the scope-aware body
// builder + slicer with elision marker, the result list compactor, and
// the human-readable renderer that turns matches into the Output
// string. The driving Execute method, the Spec, and the symbolMatch
// type live in find_symbol.go.

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func appendCapped(dst, src []symbolMatch, cap int) []symbolMatch {
	for _, m := range src {
		if len(dst) >= cap {
			break
		}
		dst = append(dst, m)
	}
	return dst
}

// filterSymbols applies the name/kind/match filters to an AST symbol
// list. Kind aliases ("function"↔"method", "class"↔"struct"↔"type")
// are accepted so the model doesn't need to know the exact AST term
// per language.
func filterSymbols(symbols []types.Symbol, name, kind, mode string) []types.Symbol {
	out := []types.Symbol{}
	for _, s := range symbols {
		if !nameMatches(s.Name, name, mode) {
			continue
		}
		if kind != "" && !kindMatches(string(s.Kind), kind) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func nameMatches(have, want, mode string) bool {
	if want == "" {
		return false
	}
	switch mode {
	case "prefix":
		return strings.HasPrefix(have, want)
	case "contains":
		return strings.Contains(strings.ToLower(have), strings.ToLower(want))
	default:
		return have == want
	}
}

func kindMatches(have, want string) bool {
	have = strings.ToLower(have)
	want = strings.ToLower(want)
	if have == want {
		return true
	}
	// Common aliases — the model often guesses one term per language.
	aliases := map[string][]string{
		"function":  {"method", "func"},
		"method":    {"function", "func"},
		"class":     {"type", "struct", "interface"},
		"type":      {"class", "struct", "interface"},
		"struct":    {"type", "class"},
		"interface": {"type", "class"},
		"variable":  {"var", "field"},
		"constant":  {"const", "enum"},
	}
	return slices.Contains(aliases[want], have)
}

// buildScopeMatch turns one symbol hit into a symbolMatch with its
// extracted body. Picks the scope strategy from the language.
func buildScopeMatch(path, language string, sym types.Symbol, lines []string, bodyMax int, includeBody bool) symbolMatch {
	startLine := min(max(sym.Line, 1), len(lines))
	endLine := extractScopeEnd(language, lines, startLine)
	endLine = max(endLine, startLine)
	m := symbolMatch{
		Path:      path,
		Language:  language,
		Name:      sym.Name,
		Kind:      string(sym.Kind),
		StartLine: startLine,
		EndLine:   endLine,
		Signature: sym.Signature,
	}
	if includeBody {
		m.Body, m.Truncated = sliceBody(lines, startLine, endLine, bodyMax)
	} else if endLine-startLine+1 > bodyMax {
		m.Truncated = true
	}
	return m
}

// sliceBody returns lines[start-1:end] joined, clamped to maxLines.
// When clamped, leaves a "// … (NN lines elided)" marker at the cut so
// the model knows the body was bigger.
func sliceBody(lines []string, start, end, maxLines int) (string, bool) {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if end < start {
		return "", false
	}
	span := lines[start-1 : end]
	if len(span) <= maxLines {
		return strings.Join(span, "\n"), false
	}
	// Keep the head — the function/class signature + opening — and trim
	// the tail. The model usually wants to see HOW it begins more than
	// HOW it ends; a tail elision marker tells it how much was cut.
	keep := maxLines - 1
	elided := len(span) - keep
	head := span[:keep]
	return strings.Join(head, "\n") + fmt.Sprintf("\n// … (%d lines elided — raise body_max_lines to see the rest)", elided), true
}

// renderSymbolMatches produces the human-readable Output. Each match
// gets a header "N. PATH:START-END  KIND  NAME" then (when bodies are
// included) a fenced code block. Without bodies it's a compact one-line
// list.
func renderSymbolMatches(matches []symbolMatch, includeBody bool) string {
	// Stable sort by path then line so repeated calls render the same
	// shape, even though the walker order is filesystem-dependent.
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].StartLine < matches[j].StartLine
	})

	var b strings.Builder
	for i, m := range matches {
		if i > 0 {
			b.WriteString("\n\n")
		}
		display := m.Name
		if m.Parent != "" {
			display = m.Parent + "." + m.Name
		}
		header := fmt.Sprintf("%d. %s:%d-%d  %s  %s", i+1, m.Path, m.StartLine, m.EndLine, m.Kind, display)
		if m.Signature != "" && m.Signature != m.Name {
			header += "  " + m.Signature
		}
		if m.Truncated {
			header += "  [truncated]"
		}
		if m.Fallback {
			header += "  [regex-fallback]"
		}
		b.WriteString(header)
		if includeBody && m.Body != "" {
			b.WriteString("\n```")
			if m.Language != "" {
				b.WriteString(m.Language)
			}
			b.WriteString("\n")
			b.WriteString(m.Body)
			b.WriteString("\n```")
		}
	}
	return b.String()
}
