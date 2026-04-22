// Context-budgeting and context-in-status methods for the Engine.
// Extracted from engine.go. Owns the "what files does the LLM see
// for this query" pipeline: preview, recommendations, tuning,
// build-and-report, task-profile scaling, compression level, and
// reserve breakdown accounting.

package engine

import (
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (e *Engine) ContextBudgetPreview(question string) ContextBudgetInfo {
	return e.ContextBudgetPreviewWithRuntime(question, ctxmgr.PromptRuntime{})
}

func (e *Engine) ContextBudgetPreviewWithRuntime(question string, overrides ctxmgr.PromptRuntime) ContextBudgetInfo {
	runtime := e.promptRuntimeWithOverrides(overrides)
	opts := e.contextBuildOptionsWithRuntime(question, runtime)
	task := detectContextTask(question)
	profile := contextTaskProfile(task)
	explicitMentions := countExplicitFileMentions(question)
	providerLimit := e.providerMaxContextForRuntime(runtime)
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}
	reserve := e.contextReserveBreakdownWithRuntime(question, runtime)
	available := providerLimit - reserve.Total
	if available < minContextTotalBudgetTokens {
		available = minContextTotalBudgetTokens
	}
	providerName := strings.TrimSpace(runtime.Provider)
	if providerName == "" {
		providerName = e.provider()
	}
	modelName := strings.TrimSpace(runtime.Model)
	if modelName == "" {
		modelName = e.model()
	}
	return ContextBudgetInfo{
		Provider:               providerName,
		Model:                  modelName,
		ProviderMaxContext:     providerLimit,
		Task:                   task,
		ExplicitFileMentions:   explicitMentions,
		TaskTotalScale:         profile.TotalScale,
		TaskFileScale:          profile.FileScale,
		TaskPerFileScale:       profile.PerFileScale,
		ContextAvailableTokens: available,
		ReserveTotalTokens:     reserve.Total,
		ReservePromptTokens:    reserve.Prompt,
		ReserveHistoryTokens:   reserve.History,
		ReserveResponseTokens:  reserve.Response,
		ReserveToolTokens:      reserve.Tool,
		MaxFiles:               opts.MaxFiles,
		MaxTokensTotal:         opts.MaxTokensTotal,
		MaxTokensPerFile:       opts.MaxTokensPerFile,
		MaxHistoryTokens:       e.conversationHistoryBudget(),
		Compression:            opts.Compression,
		IncludeTests:           opts.IncludeTests,
		IncludeDocs:            opts.IncludeDocs,
	}
}

func (e *Engine) ContextRecommendations(question string) []ContextRecommendation {
	return e.ContextRecommendationsWithRuntime(question, ctxmgr.PromptRuntime{})
}

func (e *Engine) ContextRecommendationsWithRuntime(question string, overrides ctxmgr.PromptRuntime) []ContextRecommendation {
	preview := e.ContextBudgetPreviewWithRuntime(question, overrides)
	recs := make([]ContextRecommendation, 0, 6)
	add := func(severity, code, message string) {
		recs = append(recs, ContextRecommendation{
			Severity: strings.TrimSpace(strings.ToLower(severity)),
			Code:     strings.TrimSpace(strings.ToLower(code)),
			Message:  strings.TrimSpace(message),
		})
	}

	available := preview.ContextAvailableTokens
	if available <= 0 {
		available = minContextTotalBudgetTokens
	}
	utilization := float64(preview.MaxTokensTotal) / float64(available)

	if utilization >= 0.92 {
		add("warn", "near_context_cap", "Context budget is near provider limit. Reduce max_files, lower max_tokens_per_file, or use [[file:...]] markers.")
	}
	if preview.ReserveHistoryTokens > available/3 {
		add("warn", "history_reserve_high", "History reserve is large relative to available context. Lower context.max_history_tokens for deeper code context.")
	}
	if preview.ExplicitFileMentions == 0 {
		add("info", "use_file_markers", "No explicit file markers detected. Add [[file:path#Lx-Ly]] to focus retrieval and reduce token waste.")
	}
	if (preview.Task == "security" || preview.Task == "review" || preview.Task == "debug") && preview.MaxTokensPerFile < 320 {
		add("warn", "shallow_file_slices", "Per-file token budget is shallow for this task type. Consider increasing context.max_tokens_per_file.")
	}
	if (preview.Task == "security" || preview.Task == "review") && utilization < 0.55 {
		add("info", "headroom_available", "There is context headroom for deeper inspection. You can increase context.max_tokens_total for richer evidence.")
	}
	if len(recs) == 0 {
		add("info", "balanced_budget", "Current context budget looks balanced for this query.")
	}
	return recs
}

func (e *Engine) ContextTuningSuggestions(question string) []ContextTuningSuggestion {
	return e.ContextTuningSuggestionsWithRuntime(question, ctxmgr.PromptRuntime{})
}

func (e *Engine) ContextTuningSuggestionsWithRuntime(question string, overrides ctxmgr.PromptRuntime) []ContextTuningSuggestion {
	preview := e.ContextBudgetPreviewWithRuntime(question, overrides)
	suggestions := make([]ContextTuningSuggestion, 0, 6)
	add := func(priority, key string, value any, reason string) {
		suggestions = append(suggestions, ContextTuningSuggestion{
			Priority: strings.TrimSpace(strings.ToLower(priority)),
			Key:      strings.TrimSpace(key),
			Value:    value,
			Reason:   strings.TrimSpace(reason),
		})
	}

	available := preview.ContextAvailableTokens
	if available <= 0 {
		available = minContextTotalBudgetTokens
	}
	utilization := float64(preview.MaxTokensTotal) / float64(available)

	if utilization >= 0.92 {
		targetTotal := int(math.Round(float64(available) * 0.78))
		if targetTotal < minContextTotalBudgetTokens {
			targetTotal = minContextTotalBudgetTokens
		}
		add("high", "context.max_tokens_total", targetTotal, "Current budget is near context cap; lowering total budget reduces truncation risk.")
	}
	if preview.ReserveHistoryTokens > available/3 {
		targetHistory := available / 4
		if targetHistory < minContextPerFileTokens {
			targetHistory = minContextPerFileTokens
		}
		if targetHistory > maxHistoryBudgetTokens {
			targetHistory = maxHistoryBudgetTokens
		}
		add("high", "context.max_history_tokens", targetHistory, "History reserve is large relative to available context; reducing it increases code context headroom.")
	}
	if (preview.Task == "security" || preview.Task == "review" || preview.Task == "debug") && preview.MaxTokensPerFile < 320 {
		perFile := 320
		if capPerFile := preview.MaxTokensTotal / maxInt(1, preview.MaxFiles); capPerFile > 0 && capPerFile < perFile {
			perFile = capPerFile
		}
		if perFile < minContextPerFileTokens {
			perFile = minContextPerFileTokens
		}
		add("medium", "context.max_tokens_per_file", perFile, "Task type benefits from deeper per-file slices for evidence quality.")
	}
	if preview.ProviderMaxContext <= 12000 && preview.Compression != "aggressive" {
		add("medium", "context.compression", "aggressive", "Tight runtime context benefits from aggressive compression to preserve critical context.")
	}
	if preview.ProviderMaxContext <= 8000 && preview.IncludeDocs {
		add("low", "context.include_docs", false, "Disabling docs frees tokens for code context in very tight windows.")
	}
	if len(suggestions) == 0 {
		add("low", "context.profile", "balanced", "No urgent tuning required for current query/runtime profile.")
	}
	return suggestions
}

const (
	defaultContextTotalCapTokens = 16000
	minContextTotalBudgetTokens  = 512
	minContextPerFileTokens      = 96
	minContextFiles              = 2
	maxContextFiles              = 64
	defaultProviderContextTokens = 32000
	defaultResponseReserveTokens = 2048
	defaultHistoryBudgetTokens   = 1200
	maxHistoryBudgetTokens       = 2048
	maxHistoryMessages           = 12
	minHistorySummaryTokens      = 24
	maxHistorySummaryTokens      = 96
	maxResponseReserveTokens     = 16384
	basePromptReserveTokens      = 900
	baseToolReserveTokens        = 512
)

type contextTaskBudgetProfile struct {
	TotalScale   float64
	FileScale    float64
	PerFileScale float64
}

type contextReserveBreakdown struct {
	Prompt   int
	History  int
	Response int
	Tool     int
	Total    int
}

func (e *Engine) buildContextChunks(question string) []types.ContextChunk {
	if e.Context == nil {
		return nil
	}
	runtime := e.promptRuntime()
	opts := e.contextBuildOptionsWithRuntime(question, runtime)
	chunks, err := e.Context.BuildWithOptions(question, opts)
	if err != nil {
		e.setLastContextInStatus(ContextInStatus{
			Query:   strings.TrimSpace(question),
			Task:    detectContextTask(question),
			BuiltAt: time.Now(),
			Reasons: []string{"Context build failed: " + strings.TrimSpace(err.Error())},
		})
		e.EventBus.Publish(Event{
			Type:    "context:error",
			Source:  "engine",
			Payload: err.Error(),
		})
		return nil
	}
	report := e.buildContextInStatus(question, runtime, opts, chunks)
	e.setLastContextInStatus(report)
	total := 0
	for _, c := range chunks {
		total += c.TokenCount
	}
	task := detectContextTask(question)
	e.EventBus.Publish(Event{
		Type:   "context:built",
		Source: "engine",
		Payload: map[string]any{
			"files":       len(chunks),
			"tokens":      total,
			"budget":      opts.MaxTokensTotal,
			"per_file":    opts.MaxTokensPerFile,
			"compression": opts.Compression,
			"task":        task,
			"reasons":     report.Reasons,
		},
	})
	// Capture snapshot for task-attached context reuse.
	e.lastContextSnapshot = e.buildContextSnapshot(question, task, total, chunks)
	return chunks
}

func (e *Engine) contextBuildOptions(question string) ctxmgr.BuildOptions {
	return e.contextBuildOptionsWithRuntime(question, e.promptRuntime())
}

func (e *Engine) contextBuildOptionsWithRuntime(question string, runtime ctxmgr.PromptRuntime) ctxmgr.BuildOptions {
	cfg := e.Config.Context
	task := detectContextTask(question)
	profile := contextTaskProfile(task)
	explicitFileRefs := countExplicitFileMentions(question)
	providerLimit := e.providerMaxContextForRuntime(runtime)
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}
	opts := ctxmgr.BuildOptions{
		MaxFiles:         cfg.MaxFiles,
		MaxTokensTotal:   cfg.MaxTokensTotal,
		MaxTokensPerFile: cfg.MaxTokensPerFile,
		Compression:      cfg.Compression,
		IncludeTests:     cfg.IncludeTests,
		IncludeDocs:      cfg.IncludeDocs,
		SymbolAware:      cfg.SymbolAware,
		GraphDepth:       cfg.GraphDepth,
	}
	opts.Compression = normalizeContextCompression(opts.Compression)
	if runtime.LowLatency || providerLimit <= 12000 {
		opts.Compression = strongerContextCompression(opts.Compression, "aggressive")
	} else if providerLimit <= 32000 {
		opts.Compression = strongerContextCompression(opts.Compression, "standard")
	}
	if providerLimit <= 8000 && task != "doc" {
		opts.IncludeDocs = false
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 8
	}
	opts.MaxFiles = clampInt(int(math.Round(float64(opts.MaxFiles)*profile.FileScale)), minContextFiles, maxContextFiles)
	if explicitFileRefs > 0 {
		// Explicit file markers imply targeted retrieval: fewer files, deeper slices.
		opts.MaxFiles = minInt(opts.MaxFiles, explicitFileRefs+4)
		opts.MaxFiles = maxInt(opts.MaxFiles, minContextFiles)
	}

	if opts.MaxTokensPerFile <= 0 {
		opts.MaxTokensPerFile = 1200
	}
	opts.MaxTokensPerFile = maxInt(minContextPerFileTokens, int(math.Round(float64(opts.MaxTokensPerFile)*profile.PerFileScale)))

	configuredTotal := opts.MaxTokensTotal
	if configuredTotal <= 0 {
		configuredTotal = opts.MaxFiles * opts.MaxTokensPerFile
		configuredTotal = minInt(configuredTotal, defaultContextTotalCapTokens)
	}
	configuredTotal = maxInt(minContextTotalBudgetTokens, int(math.Round(float64(configuredTotal)*profile.TotalScale)))

	reserve := e.contextReserveBreakdownWithRuntime(question, runtime)
	availableForContext := providerLimit - reserve.Total
	if availableForContext < minContextTotalBudgetTokens {
		availableForContext = minContextTotalBudgetTokens
	}

	opts.MaxTokensTotal = minInt(configuredTotal, availableForContext)
	if opts.MaxTokensTotal < minContextTotalBudgetTokens {
		opts.MaxTokensTotal = minContextTotalBudgetTokens
	}

	perFileByTotal := opts.MaxTokensTotal / opts.MaxFiles
	if perFileByTotal < minContextPerFileTokens {
		perFileByTotal = minContextPerFileTokens
	}
	opts.MaxTokensPerFile = minInt(opts.MaxTokensPerFile, perFileByTotal)
	if opts.MaxTokensPerFile > opts.MaxTokensTotal {
		opts.MaxTokensPerFile = opts.MaxTokensTotal
	}

	// Phase 4: If the previous retrieval had low confidence, expand the
	// next retrieval with deeper graph traversal and fewer but more thoroughly
	// explored files. This triggers "uncertainty-aware retrieval": when
	// confidence is low, the next round does expanded graph exploration.
	if prev := e.lastContextSnapshot; prev != nil && prev.Confidence < 0.5 {
		opts.GraphDepth += 1
		if opts.MaxFiles > 1 {
			opts.MaxFiles = maxInt(1, opts.MaxFiles/2)
		}
	}

	return opts
}
func (e *Engine) setLastContextInStatus(status ContextInStatus) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastContextIn = status
}

func (e *Engine) buildContextInStatus(question string, runtime ctxmgr.PromptRuntime, opts ctxmgr.BuildOptions, chunks []types.ContextChunk) ContextInStatus {
	query := strings.TrimSpace(question)
	task := detectContextTask(query)
	profile := contextTaskProfile(task)
	providerLimit := e.providerMaxContextForRuntime(runtime)
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}
	reserve := e.contextReserveBreakdownWithRuntime(query, runtime)
	available := providerLimit - reserve.Total
	if available < minContextTotalBudgetTokens {
		available = minContextTotalBudgetTokens
	}
	providerName := strings.TrimSpace(runtime.Provider)
	if providerName == "" {
		providerName = e.provider()
	}
	modelName := strings.TrimSpace(runtime.Model)
	if modelName == "" {
		modelName = e.model()
	}

	terms := tokenizeContextQueryTerms(query)
	explicitMentions := countExplicitFileMentions(query)
	explicitPaths := explicitFileMentionPaths(query)
	totalTokens := 0
	files := make([]ContextInFileStatus, 0, len(chunks))
	for _, chunk := range chunks {
		totalTokens += chunk.TokenCount
		path := normalizeContextPathForStatus(e.ProjectRoot, chunk.Path)
		files = append(files, ContextInFileStatus{
			Path:        path,
			LineStart:   chunk.LineStart,
			LineEnd:     chunk.LineEnd,
			TokenCount:  chunk.TokenCount,
			Score:       chunk.Score,
			Compression: chunk.Compression,
			Reason:      explainContextFileReason(path, terms, explicitPaths, chunk.Score, chunk.Source),
		})
	}

	reasons := make([]string, 0, 8)
	reasons = append(reasons, fmt.Sprintf("task=%s profile(total x%.2f, files x%.2f, per-file x%.2f)", task, profile.TotalScale, profile.FileScale, profile.PerFileScale))
	if explicitMentions > 0 {
		reasons = append(reasons, fmt.Sprintf("explicit file markers detected (%d), retrieval was narrowed", explicitMentions))
	}
	if providerLimit <= 12000 {
		reasons = append(reasons, "provider max context is tight, compression and budget were constrained")
	}
	configCompression := "standard"
	if e.Config != nil {
		configCompression = normalizeContextCompression(e.Config.Context.Compression)
	}
	if opts.Compression != configCompression {
		reasons = append(reasons, fmt.Sprintf("compression adjusted from %s to %s for runtime budget", configCompression, opts.Compression))
	}
	if !opts.IncludeDocs {
		reasons = append(reasons, "docs were excluded to preserve code-context tokens")
	}
	if !opts.IncludeTests {
		reasons = append(reasons, "tests were excluded by context settings")
	}
	if available > 0 && opts.MaxTokensTotal >= int(float64(available)*0.9) {
		reasons = append(reasons, "context budget is near runtime cap; deeper retrieval may require tighter query/file markers")
	}

	return ContextInStatus{
		Query:                query,
		Task:                 task,
		BuiltAt:              time.Now(),
		Provider:             providerName,
		Model:                modelName,
		ProviderMaxContext:   providerLimit,
		ContextAvailable:     available,
		ExplicitFileMentions: explicitMentions,
		MaxFiles:             opts.MaxFiles,
		MaxTokensTotal:       opts.MaxTokensTotal,
		MaxTokensPerFile:     opts.MaxTokensPerFile,
		Compression:          opts.Compression,
		IncludeTests:         opts.IncludeTests,
		IncludeDocs:          opts.IncludeDocs,
		FileCount:            len(chunks),
		TokenCount:           totalTokens,
		Reasons:              reasons,
		Files:                files,
	}
}

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

// buildContextSnapshot creates a ContextSnapshot from the retrieval outcome.
func (e *Engine) buildContextSnapshot(query, task string, budgetUsed int, chunks []types.ContextChunk) *ctxmgr.ContextSnapshot {
	if chunks == nil {
		return nil
	}
	avgScore := 0.0
	if len(chunks) > 0 {
		for _, c := range chunks {
			avgScore += c.Score
		}
		avgScore /= float64(len(chunks))
	}
	refs := make([]ctxmgr.ContextChunkRef, len(chunks))
	for i, c := range chunks {
		refs[i] = ctxmgr.ContextChunkRef{
			Path:      c.Path,
			Language:  c.Language,
			LineStart: c.LineStart,
			LineEnd:   c.LineEnd,
			Score:     c.Score,
			Source:    c.Source,
		}
	}
	return &ctxmgr.ContextSnapshot{
		Query:       query,
		Task:        task,
		BudgetUsed:  budgetUsed,
		Confidence:  avgScore,
		RetrievedAt: time.Now(),
		Chunks:      refs,
	}
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
