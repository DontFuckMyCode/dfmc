// compress.go — content compression helpers for the context manager.
// Pure functions, no Manager state:
//
//   - normalizeCompression / compressionFallbackOrder: resolve the
//     "none|standard|aggressive" knob into the walk order the chunk
//     builder uses when the requested level produces an empty snippet.
//   - buildChunkForBudget / downshiftChunkForRemaining: turn raw file
//     bytes into a ContextChunk under a per-file token budget, with a
//     graceful fallback + final trim when remaining budget shrinks.
//   - compressContent: the dispatcher — "none" trims to budget, "standard"
//     extracts a term-anchored snippet with comments stripped, and
//     "aggressive" falls back to a signatures-only view.
//   - extractSignatures / stripComments / trimToTokenBudget: primitives
//     the dispatcher composes. signatureLineRe stays next to its sole
//     caller so the heuristic is obvious.
//   - shouldIncludePath: the tests/docs filter Build respects when the
//     caller has opted out of them.
//
// Extracted from manager.go to keep the main file focused on the Build
// hot path and prompt assembly.

package context

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func normalizeCompression(v string) string {
	c := strings.ToLower(strings.TrimSpace(v))
	switch c {
	case "none", "standard", "aggressive":
		return c
	default:
		return "standard"
	}
}

func buildChunkForBudget(path, raw string, terms []string, score float64, compression string, maxTokensPerFile int) types.ContextChunk {
	levels := compressionFallbackOrder(compression)
	lang := detectLanguageFromPath(path)
	for _, lvl := range levels {
		content, lineStart, lineEnd := compressContent(raw, terms, lang, lvl, maxTokensPerFile)
		tc := tokens.Estimate(content)
		if tc <= 0 || strings.TrimSpace(content) == "" {
			continue
		}
		return types.ContextChunk{
			Path:        path,
			Language:    lang,
			Content:     content,
			LineStart:   lineStart,
			LineEnd:     lineEnd,
			TokenCount:  tc,
			Score:       score,
			Compression: lvl,
		}
	}
	return types.ContextChunk{}
}

func downshiftChunkForRemaining(chunk types.ContextChunk, remaining, maxTokensPerFile int) types.ContextChunk {
	if remaining <= 0 {
		return types.ContextChunk{}
	}
	budget := remaining
	if maxTokensPerFile > 0 && budget > maxTokensPerFile {
		budget = maxTokensPerFile
	}
	if chunk.TokenCount <= budget {
		return chunk
	}
	trimmed := ""
	tokenCount := 0
	for budget > 0 {
		trimmed = trimToTokenBudget(chunk.Content, budget)
		if strings.TrimSpace(trimmed) == "" {
			return types.ContextChunk{}
		}
		tokenCount = tokens.Estimate(trimmed)
		if tokenCount <= budget {
			break
		}
		over := tokenCount - budget
		over = max(1, over)
		budget -= over
	}
	if strings.TrimSpace(trimmed) == "" || budget <= 0 {
		return types.ContextChunk{}
	}
	chunk.Content = trimmed
	chunk.TokenCount = tokenCount
	chunk.Compression = chunk.Compression + "+trim"
	return chunk
}

func compressionFallbackOrder(primary string) []string {
	switch normalizeCompression(primary) {
	case "none":
		return []string{"none", "standard", "aggressive"}
	case "aggressive":
		return []string{"aggressive"}
	default:
		return []string{"standard", "aggressive"}
	}
}

func compressContent(raw string, terms []string, lang, level string, maxTokens int) (string, int, int) {
	switch level {
	case "none":
		lineStart, lineEnd := 1, len(strings.Split(raw, "\n"))
		return trimToTokenBudget(raw, maxTokens), lineStart, lineEnd
	case "aggressive":
		sig := extractSignatures(raw, lang, 160)
		if strings.TrimSpace(sig) == "" {
			snippet, lineStart, lineEnd := extractSnippet(raw, terms, 30)
			return trimToTokenBudget(stripComments(snippet), maxTokens), lineStart, lineEnd
		}
		return trimToTokenBudget(sig, maxTokens), 1, min(160, len(strings.Split(raw, "\n")))
	default:
		snippet, lineStart, lineEnd := extractSnippet(raw, terms, 60)
		return trimToTokenBudget(stripComments(snippet), maxTokens), lineStart, lineEnd
	}
}

func trimToTokenBudget(content string, maxTokens int) string {
	return tokens.TrimToBudget(content, maxTokens, "... [truncated for token budget]")
}

func stripComments(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	inBlock := false
	for _, line := range lines {
		l := line
		trim := strings.TrimSpace(l)
		if inBlock {
			if strings.Contains(trim, "*/") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(trim, "/*") {
			if !strings.Contains(trim, "*/") {
				inBlock = true
			}
			continue
		}
		if strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "#") {
			continue
		}
		if idx := strings.Index(l, "//"); idx >= 0 {
			l = l[:idx]
		}
		l = strings.TrimRight(l, " \t")
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

var signatureLineRe = regexp.MustCompile(`^\s*(func|type|class|interface|def|fn|pub|const|var|let|struct|enum|impl|export\s+(function|class|const|let|type|interface)|async\s+function)\b`)

func extractSignatures(content, lang string, maxLines int) string {
	if maxLines <= 0 {
		maxLines = 120
	}
	lines := strings.Split(content, "\n")
	out := make([]string, 0, min(maxLines, len(lines)))
	for _, line := range lines {
		if len(out) >= maxLines {
			break
		}
		trim := strings.TrimSpace(line)
		if trim == "" {
			continue
		}
		if signatureLineRe.MatchString(trim) {
			out = append(out, trim)
			continue
		}
		if (lang == "go" || lang == "rust" || lang == "java" || lang == "csharp") && strings.HasPrefix(trim, "package ") {
			out = append(out, trim)
			continue
		}
		if strings.HasPrefix(trim, "import ") || strings.HasPrefix(trim, "from ") {
			out = append(out, trim)
		}
	}
	return strings.Join(out, "\n")
}

func shouldIncludePath(path string, includeTests, includeDocs bool) bool {
	p := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if p == "" {
		return false
	}
	if !includeTests {
		if strings.Contains(p, "/test/") || strings.Contains(p, "/tests/") || strings.HasSuffix(p, "_test.go") ||
			strings.HasSuffix(p, ".spec.ts") || strings.HasSuffix(p, ".test.ts") || strings.HasSuffix(p, ".spec.js") || strings.HasSuffix(p, ".test.js") {
			return false
		}
	}
	if !includeDocs {
		if strings.HasSuffix(p, ".md") || strings.HasSuffix(p, ".rst") || strings.HasSuffix(p, ".txt") || strings.Contains(p, "/docs/") {
			return false
		}
	}
	return true
}
