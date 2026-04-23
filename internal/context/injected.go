// injected.go — [[file:path#Lstart-Lend]] and fenced-code extraction
// from the user's query. The user marks extra context to inject by
// either referencing a file with the [[file:...]] syntax or dropping a
// fenced code block into the query body; both are pulled out here and
// formatted for the system prompt.
//
//   - extractInjectedContext: the public entry point. Walks every
//     [[file:...]] match, reads the referenced range under a lines cap,
//     falls back to scanning the query for fenced blocks if budget
//     remains, and returns the concatenated rendered blocks.
//   - extractQueryCodeBlocks: pulls fenced code blocks out of the raw
//     query text, trims to maxLines, and returns pre-formatted fences.
//   - resolvePathWithinRoot: makes sure a [[file:...]] reference can't
//     escape the project root via `..` traversal.
//   - safeSub: bounded index into a regex capture slice.
//
// Extracted from manager.go.

package context

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

var (
	injectionMarker  = regexp.MustCompile(`\[\[file:([^\]#]+?)(?:#L(\d+)(?:-L?(\d+))?)?\]\]`)
	queryCodeBlockRe = regexp.MustCompile("(?s)```([a-zA-Z0-9_+-]*)\\r?\\n(.*?)\\r?\\n?```")
)

func extractInjectedContext(projectRoot, query string, maxBlocks, maxLines int) string {
	if strings.TrimSpace(query) == "" {
		return "(none)"
	}
	matches := injectionMarker.FindAllStringSubmatch(query, -1)
	if maxBlocks <= 0 {
		maxBlocks = 3
	}
	if maxLines <= 0 {
		maxLines = 120
	}

	blocks := make([]string, 0, maxBlocks)
	if strings.TrimSpace(projectRoot) != "" && len(matches) > 0 {
		seen := map[string]struct{}{}
		for _, m := range matches {
			if len(blocks) >= maxBlocks {
				break
			}
			rel := strings.TrimSpace(m[1])
			if rel == "" {
				continue
			}
			key := rel + "#" + safeSub(m, 2) + "#" + safeSub(m, 3)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}

			abs, err := resolvePathWithinRoot(projectRoot, rel)
			if err != nil {
				continue
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				continue
			}
			lines := strings.Split(string(data), "\n")
			lineStart := 1
			lineEnd := len(lines)
			if safeSub(m, 2) != "" {
				if n, err := strconv.Atoi(safeSub(m, 2)); err == nil && n > 0 {
					lineStart = n
				}
			}
			if safeSub(m, 3) != "" {
				if n, err := strconv.Atoi(safeSub(m, 3)); err == nil && n >= lineStart {
					lineEnd = n
				}
			}
			if lineStart > len(lines) {
				lineStart = len(lines)
			}
			if lineStart < 1 {
				lineStart = 1
			}
			if lineEnd > len(lines) {
				lineEnd = len(lines)
			}
			if lineEnd < lineStart {
				lineEnd = lineStart
			}
			if lineEnd-lineStart+1 > maxLines {
				lineEnd = min(len(lines), lineStart+maxLines-1)
			}

			snippet := strings.Join(lines[lineStart-1:lineEnd], "\n")
			lang := detectLanguageFromPath(rel)
			if lang == "" {
				lang = "text"
			}
			blocks = append(blocks,
				fmt.Sprintf("%s%s#L%d-L%d%s\n```%s\n%s\n```",
					types.FileMarkerPrefix, filepath.ToSlash(rel), lineStart, lineEnd, types.FileMarkerSuffix, lang, snippet))
		}
	}
	if len(blocks) < maxBlocks {
		for i, block := range extractQueryCodeBlocks(query, maxBlocks-len(blocks), maxLines) {
			if strings.TrimSpace(block) == "" {
				continue
			}
			blocks = append(blocks, fmt.Sprintf("[[query-code:%d]]\n%s", i+1, block))
			if len(blocks) >= maxBlocks {
				break
			}
		}
	}
	if len(blocks) == 0 {
		return "(none)"
	}
	return strings.Join(blocks, "\n\n")
}

func extractQueryCodeBlocks(query string, maxBlocks, maxLines int) []string {
	if maxBlocks <= 0 {
		return nil
	}
	matches := queryCodeBlockRe.FindAllStringSubmatch(query, -1)
	if len(matches) == 0 {
		return nil
	}
	if maxLines <= 0 {
		maxLines = 120
	}
	out := make([]string, 0, min(len(matches), maxBlocks))
	for _, m := range matches {
		if len(out) >= maxBlocks {
			break
		}
		lang := strings.TrimSpace(safeSub(m, 1))
		if lang == "" {
			lang = "text"
		}
		raw := strings.ReplaceAll(safeSub(m, 2), "\r\n", "\n")
		lines := strings.Split(raw, "\n")
		if len(lines) > maxLines {
			lines = append(lines[:maxLines], "... [query code truncated]")
		}
		snippet := strings.TrimSpace(strings.Join(lines, "\n"))
		if snippet == "" {
			continue
		}
		out = append(out, fmt.Sprintf("```%s\n%s\n```", lang, snippet))
	}
	return out
}

func resolvePathWithinRoot(root, rel string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := rel
	if !filepath.IsAbs(target) {
		target = filepath.Join(absRoot, rel)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes project root")
	}
	return absTarget, nil
}

func safeSub(parts []string, idx int) string {
	if idx >= 0 && idx < len(parts) {
		return strings.TrimSpace(parts[idx])
	}
	return ""
}
