package context

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type Manager struct {
	codemap *codemap.Engine
	prompts *promptlib.Library
}

func New(cm *codemap.Engine) *Manager {
	return &Manager{
		codemap: cm,
		prompts: promptlib.New(),
	}
}

func (m *Manager) Build(query string, maxFiles int) ([]types.ContextChunk, error) {
	if m == nil || m.codemap == nil || m.codemap.Graph() == nil {
		return nil, nil
	}
	if maxFiles <= 0 {
		maxFiles = 6
	}

	terms := tokenizeQuery(query)
	scores := map[string]float64{}

	graph := m.codemap.Graph()
	for _, n := range graph.Nodes() {
		switch n.Kind {
		case "file":
			pathLower := strings.ToLower(n.Path)
			nameLower := strings.ToLower(n.Name)
			for _, t := range terms {
				if strings.Contains(pathLower, t) || strings.Contains(nameLower, t) {
					scores[n.Path] += 2.0
				}
			}
			if _, ok := scores[n.Path]; !ok {
				scores[n.Path] = 0.15
			}
		default:
			if n.Path == "" {
				continue
			}
			nameLower := strings.ToLower(n.Name)
			for _, t := range terms {
				if strings.Contains(nameLower, t) {
					scores[n.Path] += 3.0
				}
			}
		}
	}

	for _, hs := range graph.HotSpots(maxFiles * 2) {
		if hs.Path != "" {
			scores[hs.Path] += 1.0
		}
	}

	type ranked struct {
		Path  string
		Score float64
	}
	rankedPaths := make([]ranked, 0, len(scores))
	for path, score := range scores {
		rankedPaths = append(rankedPaths, ranked{Path: path, Score: score})
	}
	sort.Slice(rankedPaths, func(i, j int) bool {
		if rankedPaths[i].Score == rankedPaths[j].Score {
			return rankedPaths[i].Path < rankedPaths[j].Path
		}
		return rankedPaths[i].Score > rankedPaths[j].Score
	})

	chunks := make([]types.ContextChunk, 0, maxFiles)
	for _, r := range rankedPaths {
		if len(chunks) >= maxFiles {
			break
		}
		content, err := os.ReadFile(r.Path)
		if err != nil {
			continue
		}
		snippet, lineStart, lineEnd := extractSnippet(string(content), terms, 60)
		chunks = append(chunks, types.ContextChunk{
			Path:        r.Path,
			Language:    detectLanguageFromPath(r.Path),
			Content:     snippet,
			LineStart:   lineStart,
			LineEnd:     lineEnd,
			TokenCount:  estimateTokens(snippet),
			Score:       r.Score,
			Compression: "standard",
		})
	}

	return chunks, nil
}

func (m *Manager) BuildSystemPrompt(projectRoot, query string, chunks []types.ContextChunk) string {
	if m == nil || m.prompts == nil {
		return "You are DFMC, a code intelligence assistant. Be concise, practical, and safe."
	}
	_ = m.prompts.LoadOverrides(projectRoot)

	task := promptlib.DetectTask(query)
	language := promptlib.InferLanguage(query, chunks)
	injected := extractInjectedContext(projectRoot, query, 3, 120)
	return m.prompts.Render(promptlib.RenderRequest{
		Type:     "system",
		Task:     task,
		Language: language,
		Vars: map[string]string{
			"project_root":     projectRoot,
			"task":             task,
			"language":         language,
			"user_query":       strings.TrimSpace(query),
			"context_files":    summarizeContextFiles(chunks, 12),
			"injected_context": injected,
		},
	})
}

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
		if start < 0 {
			start = 0
		}
		end = start + maxLines
		if end > len(lines) {
			end = len(lines)
		}
	} else if len(lines) > maxLines {
		end = maxLines
	}

	snippet := strings.Join(lines[start:end], "\n")
	return snippet, start + 1, end
}

func estimateTokens(content string) int {
	return len(strings.Fields(content))
}

func summarizeContextFiles(chunks []types.ContextChunk, limit int) string {
	if len(chunks) == 0 || limit <= 0 {
		return "(none)"
	}
	if len(chunks) > limit {
		chunks = chunks[:limit]
	}
	lines := make([]string, 0, len(chunks))
	for _, ch := range chunks {
		path := filepath.ToSlash(ch.Path)
		if path == "" {
			path = "(unknown)"
		}
		lines = append(lines, fmt.Sprintf("- %s:%d-%d", path, ch.LineStart, ch.LineEnd))
	}
	return strings.Join(lines, "\n")
}

func detectLanguageFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".java":
		return "java"
	case ".cs":
		return "csharp"
	case ".php":
		return "php"
	case ".kt", ".kts":
		return "kotlin"
	case ".swift":
		return "swift"
	default:
		return ""
	}
}

var injectionMarker = regexp.MustCompile(`\[\[file:([^\]#]+?)(?:#L(\d+)(?:-L?(\d+))?)?\]\]`)

func extractInjectedContext(projectRoot, query string, maxBlocks, maxLines int) string {
	if strings.TrimSpace(projectRoot) == "" || strings.TrimSpace(query) == "" {
		return "(none)"
	}
	matches := injectionMarker.FindAllStringSubmatch(query, -1)
	if len(matches) == 0 {
		return "(none)"
	}
	if maxBlocks <= 0 {
		maxBlocks = 3
	}
	if maxLines <= 0 {
		maxLines = 120
	}

	blocks := make([]string, 0, min(len(matches), maxBlocks))
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
			lineEnd = lineStart + maxLines - 1
			if lineEnd > len(lines) {
				lineEnd = len(lines)
			}
		}

		snippet := strings.Join(lines[lineStart-1:lineEnd], "\n")
		lang := detectLanguageFromPath(rel)
		if lang == "" {
			lang = "text"
		}
		blocks = append(blocks,
			fmt.Sprintf("[[file:%s#L%d-L%d]]\n```%s\n%s\n```",
				filepath.ToSlash(rel), lineStart, lineEnd, lang, snippet))
	}
	if len(blocks) == 0 {
		return "(none)"
	}
	return strings.Join(blocks, "\n\n")
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
