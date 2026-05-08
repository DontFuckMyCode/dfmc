package engine

// engine_context_status.go — produces the ContextInStatus and the
// debug-mirror ContextDebugStatus that the TUI / web /api/v1/context
// surfaces consume. Skipped-build, successful-build, and clone helpers
// live here so the math in engine_context.go stays focused on the
// build pipeline. Companion siblings:
//
//   - engine_context.go         lifecycle: ContextBudgetPreview /
//                               buildContextChunks / shouldBuild
//                               WorkspaceContext / InspectLastContext
//                               / setLastContextInStatus / setLast
//                               ContextDebugStatus / ActiveContextDebug
//                               / buildContextSnapshot + constants/types
//   - engine_context_options.go contextBuildOptions(WithRuntime)
//                               compose the per-question BuildOptions

import (
	"fmt"
	"strings"
	"time"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (e *Engine) buildWorkspaceContextSkippedStatus(question string, runtime ctxmgr.PromptRuntime, opts ctxmgr.BuildOptions) ContextInStatus {
	query := strings.TrimSpace(question)
	task := detectContextTask(query)
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
	explicitMentions := countExplicitFileMentions(query)
	reasons := []string{
		"conversation history only; workspace evidence auto-attach is off",
		"code enters context through @file/[[file:...]], pasted blocks, or read/search tools",
		"set context.auto_include_files=true or add [[workspace-context]] to opt into broad retrieval",
	}
	if explicitMentions > 0 {
		reasons = append(reasons, fmt.Sprintf("%d explicit file marker(s) will be injected separately", explicitMentions))
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
		Reasons:              reasons,
	}
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

func buildContextDebugStatus(report ContextInStatus, chunks []types.ContextChunk) ContextDebugStatus {
	files := make([]ContextDebugFileStatus, 0, len(chunks))
	for i, chunk := range chunks {
		reason := ""
		path := chunk.Path
		if i < len(report.Files) {
			reason = report.Files[i].Reason
			if report.Files[i].Path != "" {
				path = report.Files[i].Path
			}
		}
		files = append(files, ContextDebugFileStatus{
			Path:        path,
			Language:    chunk.Language,
			LineStart:   chunk.LineStart,
			LineEnd:     chunk.LineEnd,
			TokenCount:  chunk.TokenCount,
			Score:       chunk.Score,
			Compression: chunk.Compression,
			Source:      chunk.Source,
			Reason:      reason,
			Content:     chunk.Content,
		})
	}
	return ContextDebugStatus{
		Query:              report.Query,
		Task:               report.Task,
		BuiltAt:            report.BuiltAt,
		Provider:           report.Provider,
		Model:              report.Model,
		ProviderMaxContext: report.ProviderMaxContext,
		MaxTokensTotal:     report.MaxTokensTotal,
		TokenCount:         report.TokenCount,
		FileCount:          report.FileCount,
		Reasons:            append([]string(nil), report.Reasons...),
		Files:              files,
	}
}

func cloneContextDebugStatus(src ContextDebugStatus) ContextDebugStatus {
	copyStatus := src
	if len(src.Reasons) > 0 {
		copyStatus.Reasons = append([]string(nil), src.Reasons...)
	}
	if len(src.Files) > 0 {
		copyStatus.Files = append([]ContextDebugFileStatus(nil), src.Files...)
	}
	return copyStatus
}
