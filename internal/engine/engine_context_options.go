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
	"time"

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
	// Read engine-owned state under e.mu and CLONE the file maps. These
	// maps are written by invalidateContextForTool (under e.mu) on every
	// read_file/edit_file/write_file/apply_patch, while this function runs
	// concurrently from the web/TUI context-budget handlers AND the agent
	// loop. Aliasing the live maps (the old behaviour, despite the comment
	// claiming a copy) raced — `fatal error: concurrent map read and map
	// write`, unrecoverable by the panic guard — and BuildWithOptions even
	// deletes stale entries from ExcludeStaleFilters, mutating the engine's
	// map. opts must own independent copies.
	e.mu.RLock()
	prevSnapshot := e.lastContextSnapshot
	if len(e.modifiedFiles) > 0 {
		cloned := make(map[string]time.Time, len(e.modifiedFiles))
		for k, v := range e.modifiedFiles {
			cloned[k] = v
		}
		opts.ExcludeStaleFilters = cloned
	}
	if len(e.seenFiles) > 0 {
		cloned := make(map[string]struct{}, len(e.seenFiles))
		for k := range e.seenFiles {
			cloned[k] = struct{}{}
		}
		opts.SeenFiles = cloned
	}
	e.mu.RUnlock()
	if prev := prevSnapshot; prev != nil && prev.Confidence < 0.5 {
		opts.GraphDepth += 1
		if opts.MaxFiles > 1 {
			opts.MaxFiles = maxInt(1, opts.MaxFiles/2)
		}
	}

	return opts
}
