// Passthrough / delegation methods for the Engine.
//
// The passthrough surface is split across siblings to keep this file
// focused on the Status() aggregator and ContextBreakdown projection
// — both are read-side composites that touch every subsystem. Domain
// passthrough lives in:
//
//   engine_passthrough_provider.go     — provider/model selection,
//                                         pipelines, profile resolution
//   engine_passthrough_config.go       — SetVerbose, ReloadConfig,
//                                         project-config auto-reload
//   engine_passthrough_tasks.go        — UnifiedTaskView + ListAllTasks
//   engine_passthrough_memory.go       — Memory* wrappers
//   engine_passthrough_conversation.go — Conversation* wrappers +
//                                         RecentConversation projection

package engine

import (
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func (e *Engine) Status() Status {
	e.mu.RLock()
	defer e.mu.RUnlock()
	astBackend := ""
	astReason := ""
	var astLanguages []ast.BackendLanguageStatus
	var astMetrics ast.ParseMetrics
	var codemapMetrics codemap.BuildMetrics
	providerProfile := e.providerProfileStatusLocked()
	modelsDevCache := modelsDevCacheStatus()
	if e.AST != nil {
		bs := e.AST.BackendStatus()
		astBackend = bs.Active
		astReason = bs.Reason
		astLanguages = bs.Languages
		astMetrics = e.AST.ParseMetrics()
	}
	if e.CodeMap != nil {
		codemapMetrics = e.CodeMap.Metrics()
	}
	contextIn := cloneContextInStatus(e.lastContextIn)
	var openCircuits []string
	if e.Providers != nil {
		openCircuits = e.Providers.CircuitState()
	}
	subagentsActive, subagentsLimit := e.subagentRuntimeStatus()
	return Status{
		State:             e.state,
		ProjectRoot:       e.ProjectRoot,
		Provider:          e.provider(),
		Model:             e.model(),
		ProviderProfile:   providerProfile,
		ModelsDevCache:    modelsDevCache,
		ContextIn:         contextIn,
		ASTBackend:        astBackend,
		ASTReason:         astReason,
		ASTLanguages:      astLanguages,
		ASTMetrics:        astMetrics,
		CodeMap:           codemapMetrics,
		MemoryDegraded:    e.memoryDegraded,
		MemoryLoadErr:     e.memoryLoadErr,
		ActiveDrives:      activeDriveStatuses(),
		EventsDropped:     e.EventBus.DroppedCount(),
		OpenCircuits:      openCircuits,
		SubagentRetries:   tools.SubagentRetriesTotal(),
		SubagentRetries5m: tools.SubagentRetriesInWindow(5 * time.Minute),
		SubagentsActive:   subagentsActive,
		SubagentsLimit:    subagentsLimit,
	}
}

// activeDriveStatuses asks the drive package for currently-running
// runs and projects them into the status type. Lives here (not in
// drive_adapter.go) because Status() is the canonical aggregator
// and keeping the lookup inline avoids a per-field method indirection.
func activeDriveStatuses() []ActiveDriveStatus {
	active := drive.ListActive()
	if len(active) == 0 {
		return nil
	}
	out := make([]ActiveDriveStatus, 0, len(active))
	for _, a := range active {
		out = append(out, ActiveDriveStatus{RunID: a.RunID, Task: a.Task})
	}
	return out
}

func cloneContextInStatus(src ContextInStatus) *ContextInStatus {
	if strings.TrimSpace(src.Query) == "" && src.FileCount == 0 && src.TokenCount == 0 && len(src.Reasons) == 0 && len(src.Files) == 0 {
		return nil
	}
	copyStatus := src
	if len(src.Reasons) > 0 {
		copyStatus.Reasons = append([]string(nil), src.Reasons...)
	}
	if len(src.Files) > 0 {
		copyStatus.Files = append([]ContextInFileStatus(nil), src.Files...)
	}
	return &copyStatus
}

// toolReasoningEnabled reports whether the per-tool-call self-narration
// surface (tool:reasoning events + the virtual `_reason` field on every
// tool's JSON schema) is active. Mirrors the AutonomousResume parser:
// "off"/"false"/"no"/"0" disable; any other value (including "" and
// "auto") enables. Centralised here so the publisher wiring at Init
// and any future schema gate read the same source of truth.
func (e *Engine) toolReasoningEnabled() bool {
	if e == nil {
		return true
	}
	return toolReasoningEnabledForConfig(e.Config)
}

func toolReasoningEnabledForConfig(cfg *config.Config) bool {
	if cfg == nil {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Agent.ToolReasoning)) {
	case "off", "false", "no", "0", "disabled":
		return false
	default:
		return true
	}
}

// ContextBreakdown returns a real-time snapshot of the context budget,
// combining reserve breakdown, history budget, and built context chunks
// into a single unified view consumed by TUI, web API, and dfmc status.
func (e *Engine) ContextBreakdown(question string) ContextBreakdown {
	runtime := e.promptRuntime()
	providerName := strings.TrimSpace(runtime.Provider)
	if providerName == "" {
		providerName = e.provider()
	}
	modelName := strings.TrimSpace(runtime.Model)
	if modelName == "" {
		modelName = e.model()
	}

	providerLimit := e.providerMaxContextForRuntime(runtime)
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}

	reserve := e.contextReserveBreakdownWithRuntime(question, runtime)
	opts := e.contextBuildOptionsWithRuntime(question, runtime)
	historyTokens := e.historyBudgetForRequest(question, nil, "")

	// Collect file paths from the last context build.
	var filesInContext []string
	var contextChunksTokens int
	e.mu.RLock()
	lastContextIn := cloneContextInStatus(e.lastContextIn)
	e.mu.RUnlock()
	if lastContextIn != nil && lastContextIn.FileCount > 0 && lastContextIn.Files != nil {
		filesInContext = make([]string, 0, len(lastContextIn.Files))
		for _, f := range lastContextIn.Files {
			filesInContext = append(filesInContext, f.Path)
			contextChunksTokens += f.TokenCount
		}
	}

	usedTotal := reserve.Total
	available := providerLimit - usedTotal
	if available < 0 {
		available = 0
	}

	// Compute percentages as 0.0-1.0 so callers can render without re-scaling.
	systemPromptPct := 0.0
	historyPct := 0.0
	contextChunksPct := 0.0
	responsePct := 0.0
	if providerLimit > 0 {
		systemPromptPct = float64(reserve.Prompt) / float64(providerLimit)
		historyPct = float64(historyTokens) / float64(providerLimit)
		contextChunksPct = float64(contextChunksTokens) / float64(providerLimit)
		responsePct = float64(reserve.Response) / float64(providerLimit)
	}

	// InputFootprint = real input-token spend, NOT reserve sum. Excludes
	// response/tool reserves (those are output headroom). Approximates
	// tools[] at 700 tokens for the four meta tools — close enough for
	// the "context full" gauge; the precise count is provider-internal.
	historyActual := e.CurrentConversationTokens()
	const metaToolsApproxTokens = 700
	inputFootprint := reserve.Prompt + historyActual + contextChunksTokens + metaToolsApproxTokens

	return ContextBreakdown{
		Provider:         providerName,
		Model:            modelName,
		MaxContext:       providerLimit,
		UsedTotal:        usedTotal,
		InputFootprint:   inputFootprint,
		HistoryActual:    historyActual,
		SystemPrompt:     reserve.Prompt,
		History:          historyTokens,
		ContextChunks:    contextChunksTokens,
		Response:         reserve.Response,
		ToolReserve:      reserve.Tool,
		Available:        available,
		SystemPromptPct:  systemPromptPct,
		HistoryPct:       historyPct,
		ContextChunksPct: contextChunksPct,
		ResponsePct:      responsePct,
		FilesInContext:   filesInContext,
		Compression:      opts.Compression,
		Task:             detectContextTask(question),
	}
}
