// Context-retrieval utility block. Pure helpers and small accessors
// that the build/preview pipeline in engine_context.go composes. Kept
// in one sibling so path normalization, query-term analysis, explicit
// [[file:...]] marker parsing, task profiles, provider/runtime
// resolution, compression level comparisons, and reserve breakdown
// accounting share one home instead of bloating the main file.

package engine

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
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

func (e *Engine) providerMaxContext() int {
	if e.Providers == nil {
		return 0
	}
	p, ok := e.Providers.Get(e.provider())
	if !ok || p == nil {
		return 0
	}
	return p.MaxContext()
}

func (e *Engine) providerMaxContextForRuntime(runtime ctxmgr.PromptRuntime) int {
	if runtime.MaxContext > 0 {
		return runtime.MaxContext
	}
	providerName := strings.TrimSpace(runtime.Provider)
	if providerName == "" || strings.EqualFold(providerName, e.provider()) {
		return e.providerMaxContext()
	}
	if e.Providers == nil {
		return 0
	}
	p, ok := e.Providers.Get(providerName)
	if !ok || p == nil {
		return 0
	}
	if max := p.MaxContext(); max > 0 {
		return max
	}
	return p.Hints().MaxContext
}

func normalizeContextCompression(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "none", "standard", "aggressive":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return "standard"
	}
}

func strongerContextCompression(current, desired string) string {
	cur := normalizeContextCompression(current)
	des := normalizeContextCompression(desired)
	if contextCompressionRank(des) > contextCompressionRank(cur) {
		return des
	}
	return cur
}

func contextCompressionRank(level string) int {
	switch normalizeContextCompression(level) {
	case "none":
		return 0
	case "standard":
		return 1
	case "aggressive":
		return 2
	default:
		return 1
	}
}

func (e *Engine) contextReserveBreakdownWithRuntime(question string, runtime ctxmgr.PromptRuntime) contextReserveBreakdown {
	promptReserve := maxInt(basePromptReserveTokens, tokens.Estimate(question)*3)
	responseReserve := defaultResponseReserveTokens
	providerName := strings.TrimSpace(runtime.Provider)
	if providerName == "" {
		providerName = e.provider()
	}
	if prof, ok := e.Config.Providers.Profiles[providerName]; ok && prof.MaxTokens > 0 {
		responseReserve = prof.MaxTokens
	}
	if responseReserve > maxResponseReserveTokens {
		responseReserve = maxResponseReserveTokens
	}
	if responseReserve < minContextPerFileTokens {
		responseReserve = minContextPerFileTokens
	}
	historyReserve := e.conversationHistoryBudget()
	toolReserve := baseToolReserveTokens
	providerLimit := e.providerMaxContextForRuntime(runtime)
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}

	// Tight context windows require proportionally smaller reserve buckets to avoid
	// starving the retrieval budget.
	if providerLimit <= 24000 {
		promptReserve = minInt(promptReserve, maxInt(minContextPerFileTokens*2, providerLimit/5))
		responseReserve = minInt(responseReserve, maxInt(minContextPerFileTokens, providerLimit/4))
		historyReserve = minInt(historyReserve, maxInt(minContextPerFileTokens, providerLimit/6))
		toolReserve = minInt(toolReserve, maxInt(minContextPerFileTokens/2, providerLimit/8))
	}

	// Keep reserve total bounded so context has meaningful headroom even on small windows.
	maxTotalReserve := providerLimit - minContextTotalBudgetTokens
	if maxTotalReserve < minContextPerFileTokens {
		maxTotalReserve = minContextPerFileTokens
	}
	total := promptReserve + responseReserve + toolReserve + historyReserve
	if total > maxTotalReserve {
		scale := float64(maxTotalReserve) / float64(total)
		promptReserve = maxInt(minContextPerFileTokens, int(math.Round(float64(promptReserve)*scale)))
		responseReserve = maxInt(minContextPerFileTokens, int(math.Round(float64(responseReserve)*scale)))
		historyReserve = maxInt(minContextPerFileTokens, int(math.Round(float64(historyReserve)*scale)))
		toolReserve = maxInt(minContextPerFileTokens/2, int(math.Round(float64(toolReserve)*scale)))

		total = promptReserve + responseReserve + toolReserve + historyReserve
		overflow := total - maxTotalReserve
		if overflow > 0 {
			cut := minInt(overflow, maxInt(0, responseReserve-minContextPerFileTokens))
			responseReserve -= cut
			overflow -= cut
		}
		if overflow > 0 {
			cut := minInt(overflow, maxInt(0, historyReserve-minContextPerFileTokens))
			historyReserve -= cut
			overflow -= cut
		}
		if overflow > 0 {
			cut := minInt(overflow, maxInt(0, toolReserve-(minContextPerFileTokens/2)))
			toolReserve -= cut
			overflow -= cut
		}
		if overflow > 0 {
			cut := minInt(overflow, maxInt(0, promptReserve-minContextPerFileTokens))
			promptReserve -= cut
		}
	}
	total = promptReserve + responseReserve + toolReserve + historyReserve
	return contextReserveBreakdown{
		Prompt:   promptReserve,
		History:  historyReserve,
		Response: responseReserve,
		Tool:     toolReserve,
		Total:    total,
	}
}
