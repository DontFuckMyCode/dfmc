package context

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type Manager struct {
	codemap *codemap.Engine
}

func New(cm *codemap.Engine) *Manager {
	return &Manager{codemap: cm}
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

func (m *Manager) BuildSystemPrompt(projectRoot string) string {
	if strings.TrimSpace(projectRoot) == "" {
		return "You are DFMC, a code intelligence assistant. Be concise, practical, and safe."
	}
	return fmt.Sprintf(
		"You are DFMC, a code intelligence assistant for project at %s. Prioritize correctness, explicit file references, and safe suggestions.",
		projectRoot,
	)
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
