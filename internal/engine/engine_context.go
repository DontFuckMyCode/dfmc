// Context-budgeting and context-in-status methods for the Engine.
// Extracted from engine.go. Owns the "what files does the LLM see
// for this query" pipeline: budget preview, build-and-report,
// last-context bookkeeping, and the post-build snapshot used for
// uncertainty-aware retrieval. The per-question BuildOptions math
// lives in engine_context_options.go and the ContextInStatus /
// ContextDebugStatus formatting lives in engine_context_status.go.

package engine

import (
	"strings"
	"time"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

const (
	defaultContextTotalCapTokens = 16000
	minContextTotalBudgetTokens  = 512
	minContextPerFileTokens      = 96
	minContextFiles              = 2
	maxContextFiles              = 64
	defaultProviderContextTokens = 32000
	defaultResponseReserveTokens = 2048
	// Conversation-history retention floors. The previous values
	// (defaultHistoryBudgetTokens=1200, maxHistoryBudgetTokens=2048,
	// maxHistoryMessages=12) were sized for the 8k-window era and made
	// the assistant feel amnesiac after 3-4 substantive turns on
	// 200k+/1M-window models. New floors target "remember the framing
	// across a 30-turn working session" by default while staying well
	// under any provider's hard limit. Both can be raised further via
	// agent.history_budget_tokens / agent.history_max_messages config
	// overrides for users on big-context models.
	defaultHistoryBudgetTokens = 4096
	maxHistoryBudgetTokens     = 32768
	maxHistoryMessages         = 60
	minHistorySummaryTokens    = 64
	maxHistorySummaryTokens    = 1024
	maxResponseReserveTokens   = 16384
	basePromptReserveTokens    = 900
	baseToolReserveTokens      = 512
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
		MaxHistoryMessages:     e.conversationHistoryMaxMessages(),
		Compression:            opts.Compression,
		AutoIncludeFiles:       e.Config != nil && e.Config.Context.AutoIncludeFiles,
		IncludeTests:           opts.IncludeTests,
		IncludeDocs:            opts.IncludeDocs,
	}
}

func (e *Engine) buildContextChunks(question string) []types.ContextChunk {
	runtime := e.promptRuntime()
	opts := e.contextBuildOptionsWithRuntime(question, runtime)
	if !e.shouldBuildWorkspaceContext(question) {
		status := e.buildWorkspaceContextSkippedStatus(question, runtime, opts)
		e.setLastContextInStatus(status)
		e.setLastContextDebugStatus(ContextDebugStatus{
			Query:              status.Query,
			Task:               status.Task,
			BuiltAt:            status.BuiltAt,
			Provider:           status.Provider,
			Model:              status.Model,
			ProviderMaxContext: status.ProviderMaxContext,
			MaxTokensTotal:     status.MaxTokensTotal,
			TokenCount:         status.TokenCount,
			FileCount:          status.FileCount,
			Reasons:            append([]string(nil), status.Reasons...),
		})
		e.clearLastContextSnapshot()
		return nil
	}
	if e.Context == nil {
		status := e.buildWorkspaceContextSkippedStatus(question, runtime, opts)
		status.Reasons = []string{"workspace evidence requested, but context index is unavailable"}
		e.setLastContextInStatus(status)
		e.setLastContextDebugStatus(ContextDebugStatus{
			Query:              status.Query,
			Task:               status.Task,
			BuiltAt:            status.BuiltAt,
			Provider:           status.Provider,
			Model:              status.Model,
			ProviderMaxContext: status.ProviderMaxContext,
			MaxTokensTotal:     status.MaxTokensTotal,
			Reasons:            append([]string(nil), status.Reasons...),
		})
		e.clearLastContextSnapshot()
		return nil
	}
	chunks, err := e.Context.BuildWithOptions(question, opts)
	if err != nil {
		status := ContextInStatus{
			Query:   strings.TrimSpace(question),
			Task:    detectContextTask(question),
			BuiltAt: time.Now(),
			Reasons: []string{"Context build failed: " + strings.TrimSpace(err.Error())},
		}
		e.setLastContextInStatus(status)
		e.setLastContextDebugStatus(ContextDebugStatus{
			Query:   status.Query,
			Task:    status.Task,
			BuiltAt: status.BuiltAt,
			Reasons: append([]string(nil), status.Reasons...),
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
	e.setLastContextDebugStatus(buildContextDebugStatus(report, chunks))
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
	snapshot := e.buildContextSnapshot(question, task, total, chunks)
	e.mu.Lock()
	e.lastContextSnapshot = snapshot
	e.mu.Unlock()
	return chunks
}

func (e *Engine) shouldBuildWorkspaceContext(question string) bool {
	if e == nil || e.Config == nil {
		return false
	}
	if e.Config.Context.AutoIncludeFiles {
		return true
	}
	// Opt-in markers for the AutoIncludeFiles=false default. The model
	// pulls files via tools (grep_codebase / find_symbol / read_file)
	// for any bare query; these markers are how the *user* says "for
	// THIS turn, dump file evidence in too."
	q := strings.ToLower(strings.TrimSpace(question))
	return strings.Contains(q, "[[workspace-context]]") ||
		strings.Contains(q, "[[file:") ||
		strings.Contains(q, "#context:on") ||
		strings.Contains(q, "#ctx-files")
}

func (e *Engine) clearLastContextSnapshot() {
	e.mu.Lock()
	e.lastContextSnapshot = nil
	e.mu.Unlock()
}

// ClearContextSnapshot exposes the unexported clearLastContextSnapshot
// for use by the TUI when a drive run terminates — the snapshot from
// the previous Ask is stale after an autonomous drive session.
func (e *Engine) ClearContextSnapshot() {
	e.clearLastContextSnapshot()
}

// InspectLastContext returns a detailed breakdown of the most recently
// built context chunks. Use this to show context composition in TUI or CLI.
func (e *Engine) InspectLastContext() ctxmgr.InspectionResult {
	runtime := e.promptRuntime()
	opts := e.contextBuildOptionsWithRuntime("inspect context", runtime)
	if e.Context == nil {
		return ctxmgr.InspectionResult{}
	}
	chunks, err := e.Context.BuildWithOptions("inspect context", opts)
	if err != nil || len(chunks) == 0 {
		// Fall back to last status info if no chunks available
		e.mu.RLock()
		status := e.lastContextIn
		e.mu.RUnlock()
		if len(status.Files) == 0 {
			return ctxmgr.InspectionResult{Budget: ctxmgr.BudgetStatus{Total: opts.MaxTokensTotal}}
		}
		// Reconstruct minimal chunks from status
		typesChunks := make([]types.ContextChunk, 0, len(status.Files))
		for _, f := range status.Files {
			typesChunks = append(typesChunks, types.ContextChunk{
				Path:       f.Path,
				LineStart:  f.LineStart,
				LineEnd:    f.LineEnd,
				TokenCount: f.TokenCount,
				Score:      f.Score,
				Source:     f.Reason,
			})
		}
		return ctxmgr.Inspect(e.ProjectRoot, typesChunks, opts.MaxTokensTotal)
	}
	return ctxmgr.Inspect(e.ProjectRoot, chunks, opts.MaxTokensTotal)
}

func (e *Engine) setLastContextInStatus(status ContextInStatus) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastContextIn = status
}

func (e *Engine) setLastContextDebugStatus(status ContextDebugStatus) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastContextDebug = cloneContextDebugStatus(status)
}

func (e *Engine) ActiveContextDebug() ContextDebugStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return cloneContextDebugStatus(e.lastContextDebug)
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
