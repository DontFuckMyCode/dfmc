// Context-retrieval utility block. Pure helpers and small accessors
// that the build/preview pipeline in engine_context.go composes. Path
// normalization, query-term analysis, explicit [[file:...]] marker
// parsing, and task profiles live here. Provider/runtime context
// lookup, compression-level comparisons, and reserve breakdown
// accounting live in the sibling engine_context_helpers_reserve.go.

package engine

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

func normalizeContextPathForStatus(projectRoot, path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return path
	}
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		return path
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "../") {
		return path
	}
	return rel
}

func explainContextFileReason(path string, terms, explicitPaths []string, score float64, source string) string {
	if contextPathMatchesMention(path, explicitPaths) {
		return "explicit [[file:...]] marker"
	}
	// The source label (set by the context manager when ranking) is the
	// authoritative reason. Fall through to score-based heuristics only
	// if the chunk arrived without one — that preserves backward behavior
	// for older tests or providers that construct chunks directly.
	switch source {
	case ctxmgr.ChunkSourceSymbolMatch:
		return "query identifier resolved to a codemap symbol"
	case ctxmgr.ChunkSourceGraphNeighborhood:
		return "import-graph neighbor of a symbol-matched seed"
	case ctxmgr.ChunkSourceHotspot:
		return "high codemap centrality (hotspot)"
	case ctxmgr.ChunkSourceMarker:
		return "explicit [[file:...]] marker"
	}
	matchCount := 0
	lowerPath := strings.ToLower(filepath.ToSlash(path))
	for _, term := range terms {
		if strings.Contains(lowerPath, term) {
			matchCount++
		}
	}
	if matchCount > 0 {
		return fmt.Sprintf("matched %d query term(s)", matchCount)
	}
	if score >= 3 {
		return "high codemap relevance score"
	}
	if score > 0 {
		return "codemap ranking + hotspot weight"
	}
	return "fallback ranking to keep broad project coverage"
}

func contextPathMatchesMention(path string, mentions []string) bool {
	if len(mentions) == 0 {
		return false
	}
	lowerPath := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if lowerPath == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(lowerPath))
	for _, mention := range mentions {
		m := strings.ToLower(filepath.ToSlash(strings.TrimSpace(mention)))
		if m == "" {
			continue
		}
		if m == lowerPath || strings.HasSuffix(lowerPath, "/"+m) || strings.HasSuffix(lowerPath, m) {
			return true
		}
		if mb := strings.ToLower(filepath.Base(m)); mb != "" && mb == base {
			return true
		}
	}
	return false
}

func detectContextTask(question string) string {
	task := strings.TrimSpace(strings.ToLower(promptlib.DetectTask(question)))
	if task == "" {
		return "general"
	}
	return task
}

func contextTaskProfile(task string) contextTaskBudgetProfile {
	switch task {
	case "security":
		return contextTaskBudgetProfile{TotalScale: 1.25, FileScale: 1.20, PerFileScale: 1.15}
	case "review":
		return contextTaskBudgetProfile{TotalScale: 1.18, FileScale: 1.12, PerFileScale: 1.10}
	case "debug":
		return contextTaskBudgetProfile{TotalScale: 1.15, FileScale: 1.10, PerFileScale: 1.08}
	case "test":
		return contextTaskBudgetProfile{TotalScale: 1.05, FileScale: 1.08, PerFileScale: 1.00}
	case "planning":
		return contextTaskBudgetProfile{TotalScale: 0.82, FileScale: 0.85, PerFileScale: 0.90}
	case "doc":
		return contextTaskBudgetProfile{TotalScale: 0.78, FileScale: 0.82, PerFileScale: 0.88}
	default:
		return contextTaskBudgetProfile{TotalScale: 1.00, FileScale: 1.00, PerFileScale: 1.00}
	}
}

var (
	explicitFileMentionRe = regexp.MustCompile(`\[\[file:[^\]]+\]\]`)
	explicitFilePathRe    = regexp.MustCompile(`\[\[file:([^\]#]+)(?:#[^\]]*)?\]\]`)
	fileMentionRe         = regexp.MustCompile(`(?i)\b[\w./-]+\.(go|ts|tsx|js|jsx|py|rs|java|cs|php|yaml|yml|json|md)\b`)
)

func countExplicitFileMentions(question string) int {
	if strings.TrimSpace(question) == "" {
		return 0
	}
	return len(explicitFileMentionRe.FindAllStringIndex(question, -1))
}

func tokenizeContextQueryTerms(question string) []string {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(question)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '.' && r != '/' && r != '-'
	})
	if len(parts) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if len(part) < 3 {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		out = append(out, part)
	}
	return out
}

func explicitFileMentionPaths(question string) []string {
	matches := explicitFilePathRe.FindAllStringSubmatch(strings.TrimSpace(question), -1)
	if len(matches) == 0 {
		return nil
	}
	paths := make([]string, 0, len(matches))
	seen := map[string]struct{}{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		path := strings.TrimSpace(filepath.ToSlash(match[1]))
		if path == "" {
			continue
		}
		key := strings.ToLower(path)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// providerMaxContext + providerMaxContextForRuntime +
// normalizeContextCompression + strongerContextCompression +
// contextCompressionRank + contextReserveBreakdownWithRuntime live in
// engine_context_helpers_reserve.go.
