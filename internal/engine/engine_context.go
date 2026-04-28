// Context-budgeting and context-in-status methods for the Engine.
// Extracted from engine.go. Owns the "what files does the LLM see
// for this query" pipeline: preview, recommendations, tuning,
// build-and-report, task-profile scaling, compression level, and
// reserve breakdown accounting.

package engine

import (
	"fmt"
	"math"
	"strings"
	"time"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
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
	minHistorySummaryTokens      = 64
	maxHistorySummaryTokens      = 512
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

	// Propagate recently modified files so BuildWithOptions excludes them
	// from context retrieval. Files written/edited by tools in the last
	// few minutes must be re-read via read_file, not served from a stale
	// context chunk. The map is safely copied — ExcludeStaleFilters is
	// read-only during the BuildOptions pass.
	if len(e.modifiedFiles) > 0 {
		opts.ExcludeStaleFilters = e.modifiedFiles
	}
	// Propagate files already read via read_file this session so they are
	// excluded from context deduplication. The model already has the content
	// via conversation, so sending it again via context is redundant.
	if len(e.seenFiles) > 0 {
		opts.SeenFiles = e.seenFiles
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

