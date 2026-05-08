package engine

// engine_context_options.go — composes the per-question
// ctxmgr.BuildOptions: budgets are scaled by the detected task profile,
// compressed harder for tight provider windows, narrowed when explicit
// file markers are present, and deepened for low-confidence retrievals.
// Companion siblings:
//
//   - engine_context.go        ContextBudgetPreview / buildContextChunks
//                              / shouldBuildWorkspaceContext / Inspect
//                              / setLast* / ActiveContextDebug /
//                              buildContextSnapshot + constants/types
//   - engine_context_status.go buildWorkspaceContextSkippedStatus +
//                              buildContextInStatus +
//                              buildContextDebugStatus +
//                              cloneContextDebugStatus

import (
	"math"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
)

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
	e.mu.RLock()
	prevSnapshot := e.lastContextSnapshot
	e.mu.RUnlock()
	if prev := prevSnapshot; prev != nil && prev.Confidence < 0.5 {
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
