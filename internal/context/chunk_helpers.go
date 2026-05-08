package context

// chunk_helpers.go — small stateless helpers used by
// BuildWithOptions and BuildSystemPromptBundle:
//
//   tokenizeQuery        — lowercase + punctuation split, dedupes, drops
//                          tokens shorter than 3 chars.
//   extractSnippet       — center the snippet around the first matched
//                          term, capped at maxLines.
//   summarizeContextFiles — bullet list shown in the system prompt
//                          listing each chunk's path:start-end + source
//                          tag.
//   compactPath          — project-relative + slash-normalized + middle-
//                          truncated for surfacing long paths in the
//                          summary lines.

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func tokenizeQuery(query string) []string {
	parts := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.'
	})
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, p := range parts {
		if len(p) < 3 {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func extractSnippet(content string, terms []string, maxLines int) (string, int, int) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return "", 1, 1
	}
	if maxLines <= 0 {
		maxLines = 60
	}

	needleIdx := -1
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, t := range terms {
			if strings.Contains(lower, t) {
				needleIdx = i
				break
			}
		}
		if needleIdx >= 0 {
			break
		}
	}

	start := 0
	end := len(lines)
	if needleIdx >= 0 {
		start = needleIdx - maxLines/2
		start = max(0, start)
		end = start + maxLines
		end = min(len(lines), end)
	} else if len(lines) > maxLines {
		end = maxLines
	}

	snippet := strings.Join(lines[start:end], "\n")
	return snippet, start + 1, end
}

func summarizeContextFiles(projectRoot string, chunks []types.ContextChunk, limit int) string {
	if len(chunks) == 0 || limit <= 0 {
		return "(none)"
	}
	overflow := 0
	if len(chunks) > limit {
		overflow = len(chunks) - limit
		chunks = chunks[:limit]
	}
	lines := make([]string, 0, len(chunks))
	for _, ch := range chunks {
		path := compactPath(projectRoot, ch.Path)
		if path == "" {
			path = "(unknown)"
		}
		tag := ""
		switch ch.Source {
		case ChunkSourceSymbolMatch:
			tag = " (symbol)"
		case ChunkSourceGraphNeighborhood:
			tag = " (neighbor)"
		case ChunkSourceMarker:
			tag = " (pinned)"
		case ChunkSourceHotspot:
			tag = " (hotspot)"
		}
		lines = append(lines, fmt.Sprintf("- %s:%d-%d%s", path, ch.LineStart, ch.LineEnd, tag))
	}
	if overflow > 0 {
		lines = append(lines, fmt.Sprintf("- ... +%d more files", overflow))
	}
	return strings.Join(lines, "\n")
}

func compactPath(projectRoot, path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	absPath, errPath := filepath.Abs(p)
	absRoot, errRoot := filepath.Abs(strings.TrimSpace(projectRoot))
	if errPath == nil && errRoot == nil && strings.TrimSpace(absRoot) != "" {
		if rel, err := filepath.Rel(absRoot, absPath); err == nil {
			if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				p = rel
			}
		}
	}
	p = filepath.ToSlash(p)
	if len(p) <= 88 {
		return p
	}
	return p[:42] + ".../" + p[len(p)-40:]
}
