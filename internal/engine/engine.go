package engine

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/hooks"
	"github.com/dontfuckmycode/dfmc/internal/memory"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/security"
	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type EngineState int

const (
	StateCreated EngineState = iota
	StateInitializing
	StateReady
	StateServing
	StateShuttingDown
	StateStopped
)

type Engine struct {
	Config       *config.Config
	Storage      *storage.Store
	EventBus     *EventBus
	ProjectRoot  string
	AST          *ast.Engine
	CodeMap      *codemap.Engine
	Context      *ctxmgr.Manager
	Providers    *provider.Router
	Tools        *tools.Engine
	Memory       *memory.Store
	Conversation *conversation.Manager
	Security     *security.Scanner
	// Hooks dispatches user-configured shell commands on lifecycle events
	// (user_prompt_submit, pre_tool, post_tool, session_start/end). A nil
	// value is safe — Fire is a no-op on nil.
	Hooks *hooks.Dispatcher

	providerOverride string
	modelOverride    string
	verbose          bool

	// Lock ordering (MUST be held in this order to avoid deadlocks):
	//   1. agentMu   — agent lifecycle + parked state
	//   2. mu        — general state (state, lastContextIn, background)
	// Never acquire agentMu while holding mu. Shutdown only touches mu;
	// the agent loop only touches agentMu and reads state via State()
	// which takes mu independently.
	mu    sync.RWMutex
	state EngineState

	lastContextIn ContextInStatus

	// memoryDegraded is set when Memory.Load() failed during Init. The
	// engine keeps running with an empty in-memory store so the user
	// isn't hard-blocked, but Status surfaces the flag so the TUI can
	// show a "memory disabled" notice and downstream code can decide
	// not to rely on historical recall.
	memoryDegraded bool
	memoryLoadErr  string

	// Background lifecycle. indexCancel cancels the initial codebase
	// indexer; indexWG waits for it (and any other engine-owned
	// goroutines) to finish before Shutdown tears down AST / CodeMap /
	// Storage. Without these, Shutdown could race with a still-running
	// indexer touching the CodeMap after it's been closed.
	indexCancel context.CancelFunc
	indexWG     sync.WaitGroup

	agentMu         sync.Mutex
	agentParked     *parkedAgentState
	agentNotesQueue []string
	// subagentInFlight counts active RunSubagent calls. The first subagent
	// to start stashes the parent's parked state under subagentStashed;
	// nested/concurrent subagents bump the counter without touching it.
	// The last subagent to finish restores it. Guarded by agentMu.
	subagentInFlight int
	subagentStashed  *parkedAgentState
}

// Status / report / option types live in status_types.go — keeping
// engine.go for the runtime (Engine struct, lifecycle methods, hot
// loop) instead of mixing JSON-shape data carriers in alongside.

func New(cfg *config.Config) (*Engine, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	return &Engine{
		Config:   cfg,
		EventBus: NewEventBus(),
		state:    StateCreated,
	}, nil
}

func (e *Engine) Init(ctx context.Context) error {
	e.setState(StateInitializing)
	e.EventBus.Publish(Event{Type: "engine:initializing", Source: "engine"})

	store, err := storage.Open(e.Config.DataDir())
	if err != nil {
		return fmt.Errorf("storage init failed: %w", err)
	}
	e.Storage = store
	e.AST = ast.New()
	e.CodeMap = codemap.New(e.AST)
	e.Context = ctxmgr.New(e.CodeMap)
	e.Tools = tools.New(*e.Config)
	e.Tools.SetSubagentRunner(e)
	e.Memory = memory.New(e.Storage)
	// Memory.Load failure is non-fatal — a corrupt or missing memory
	// db must not stop the user from running dfmc. But silently
	// proceeding with an empty store masks real data loss; flag
	// degradation so Status surfaces it and publish an event so the
	// TUI can render a notice next to the chat header.
	if err := e.Memory.Load(); err != nil {
		e.mu.Lock()
		e.memoryDegraded = true
		e.memoryLoadErr = err.Error()
		e.mu.Unlock()
		e.EventBus.Publish(Event{
			Type:   "memory:degraded",
			Source: "engine",
			Payload: map[string]any{
				"reason": err.Error(),
			},
		})
	}
	e.Conversation = conversation.New(e.Storage)
	e.Security = security.New()

	e.Providers, err = provider.NewRouter(e.Config.Providers)
	if err != nil {
		return fmt.Errorf("provider router init failed: %w", err)
	}

	// Hook dispatcher with observer that relays every hook outcome
	// through the engine's event bus, so the TUI / Web UI / remote
	// clients can render hook activity in the same timeline as tool
	// calls and engine lifecycle events.
	e.Hooks = hooks.New(e.Config.Hooks, func(r hooks.Report) {
		e.EventBus.Publish(Event{
			Type:   "hook:run",
			Source: "hooks",
			Payload: map[string]any{
				"event":       string(r.Event),
				"name":        r.Name,
				"command":     r.Command,
				"exit_code":   r.ExitCode,
				"duration_ms": r.Duration.Milliseconds(),
				"err":         errString(r.Err),
			},
		})
	})

	e.ProjectRoot = config.FindProjectRoot("")
	if e.ProjectRoot != "" {
		// Derive a cancellable child context so Shutdown can tell the
		// indexer to stop, then Wait for it before tearing down the
		// Storage / AST / CodeMap it's reading from. Without this we
		// could panic when the indexer writes to a closed store.
		indexCtx, cancel := context.WithCancel(ctx)
		e.mu.Lock()
		e.indexCancel = cancel
		e.mu.Unlock()
		e.indexWG.Add(1)
		go func() {
			defer e.indexWG.Done()
			e.indexCodebase(indexCtx)
		}()
	}

	// Fire session_start AFTER ProjectRoot is resolved so hooks have
	// access to the right cwd via os.Getwd() even if the user launched
	// dfmc from a subdirectory.
	e.Hooks.Fire(ctx, hooks.EventSessionStart, hooks.Payload{
		"project_root": e.ProjectRoot,
	})

	e.setState(StateReady)
	e.EventBus.Publish(Event{Type: "engine:ready", Source: "engine"})
	return nil
}

func (e *Engine) ListTools() []string {
	if e.Tools == nil {
		return nil
	}
	return e.Tools.List()
}

func (e *Engine) CallTool(ctx context.Context, name string, params map[string]any) (tools.Result, error) {
	res, err := e.executeToolWithLifecycle(ctx, name, params, "user")
	if err != nil {
		e.EventBus.Publish(Event{
			Type:    "tool:error",
			Source:  "engine",
			Payload: err.Error(),
		})
		return res, err
	}
	e.EventBus.Publish(Event{
		Type:   "tool:complete",
		Source: "engine",
		Payload: map[string]any{
			"name":       name,
			"durationMs": res.DurationMs,
		},
	})
	return res, nil
}

// executeToolWithLifecycle is the single point of entry for every tool
// invocation in the engine. It owns:
//   - approval gate (config.Tools.RequireApproval / Approver callback)
//   - pre_tool/post_tool hook dispatch with full payload
//   - raw tools.Engine.Execute call
//
// Both the user-initiated CallTool path and the agent-loop-initiated
// path (agent_loop_native, subagent) funnel through here so hooks and
// approval behave identically regardless of who decided to fire the
// tool.
//
// The `source` tag distinguishes user-initiated calls ("user") from
// agent calls ("agent", "subagent"). The approval gate currently only
// gates agent-initiated calls — user typing /tool is already explicit
// consent.
// executeToolWithPanicGuard converts any panic raised inside a tool's
// Execute into a regular error. Without this guard, a nil-pointer or
// out-of-bounds inside any tool implementation kills the entire DFMC
// process — taking down the agent loop, every connected web/SSE
// client, the TUI session, and every queued reply. Worse, the panic
// happens at an unpredictable point in the agent's reasoning so the
// failure looks like a hang from the user's side.
//
// Tools are first-party but they exec subprocesses, parse arbitrary
// AST shapes, walk filesystems with surprising layouts. The blast
// radius of "one tool bug crashes everything" is large enough to
// justify the cost of a defer/recover wrapper. The agent loop already
// knows how to surface tool errors back to the model (`isError=true`
// tool_result), so the recovered panic is just another error from
// the loop's perspective.
//
// We attach a stack trace to the error so a crash dump in the
// conversation log lets us file a real bug report instead of "the
// thing died."
func (e *Engine) executeToolWithPanicGuard(ctx context.Context, name string, params map[string]any) (res tools.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			err = fmt.Errorf("tool %s panicked: %v\n%s", name, r, truncateStackForError(stack))
			// Reset res so the caller sees an empty Result + the error,
			// not whatever partial state the tool may have populated
			// before panicking.
			res = tools.Result{}
			e.EventBus.Publish(Event{
				Type:   "tool:panicked",
				Source: "engine",
				Payload: map[string]any{
					"name":  name,
					"panic": fmt.Sprintf("%v", r),
				},
			})
		}
	}()
	return e.Tools.Execute(ctx, name, tools.Request{
		ProjectRoot: e.ProjectRoot,
		Params:      params,
	})
}

// truncateStackForError keeps the first ~2 KiB of a stack trace so a
// recovered tool panic doesn't bloat the conversation JSONL with a
// 10 KiB Go runtime dump for every retry. The head frames are the
// useful bit anyway — they point at the panic site.
func truncateStackForError(stack []byte) string {
	const cap = 2048
	if len(stack) <= cap {
		return string(stack)
	}
	return string(stack[:cap]) + "\n[stack truncated]"
}

func (e *Engine) executeToolWithLifecycle(ctx context.Context, name string, params map[string]any, source string) (tools.Result, error) {
	if e.Tools == nil {
		return tools.Result{}, fmt.Errorf("tool engine is not initialized")
	}
	// Approval gate — only engages for non-user sources and only when
	// the tool is on the approval list. Blocks until the Approver
	// responds or returns an implicit deny on timeout. See approver.go.
	if source != "user" && e.requiresApproval(name) {
		decision := e.askToolApproval(ctx, name, params, source)
		if !decision.Approved {
			reason := decision.Reason
			if reason == "" {
				reason = "user denied"
			}
			e.recordDenial(name, source, reason)
			e.EventBus.Publish(Event{
				Type:   "tool:denied",
				Source: "engine",
				Payload: map[string]any{
					"name":   name,
					"reason": reason,
					"source": source,
				},
			})
			return tools.Result{}, fmt.Errorf("tool %s denied: %s", name, reason)
		}
	}
	if e.Hooks != nil && e.Hooks.Count(hooks.EventPreTool) > 0 {
		e.Hooks.Fire(ctx, hooks.EventPreTool, hooks.Payload{
			"tool_name":    name,
			"tool_source":  source,
			"project_root": e.ProjectRoot,
		})
	}
	res, err := e.executeToolWithPanicGuard(ctx, name, params)
	if e.Hooks != nil && e.Hooks.Count(hooks.EventPostTool) > 0 {
		success := "true"
		if err != nil {
			success = "false"
		}
		e.Hooks.Fire(ctx, hooks.EventPostTool, hooks.Payload{
			"tool_name":        name,
			"tool_source":      source,
			"tool_success":     success,
			"tool_duration_ms": fmt.Sprintf("%d", res.DurationMs),
			"project_root":     e.ProjectRoot,
		})
	}
	return res, err
}

func (e *Engine) StartServing() {
	e.setState(StateServing)
	e.EventBus.Publish(Event{Type: "engine:serving", Source: "engine"})
}

func (e *Engine) Shutdown() {
	e.setState(StateShuttingDown)
	e.EventBus.Publish(Event{Type: "engine:shutdown", Source: "engine"})

	// Cancel and join background goroutines (initial codebase indexer,
	// anything else started via indexWG.Add) BEFORE tearing down the
	// stores they're reading from. The indexer writes into CodeMap
	// which in turn touches AST; closing Storage mid-write panics.
	e.mu.Lock()
	cancel := e.indexCancel
	e.indexCancel = nil
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	e.indexWG.Wait()

	// session_end fires after background goroutines have stopped so
	// hooks see a quiesced state, but before we close Storage — hooks
	// legitimately read conversation/memory here.
	if e.Hooks != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		e.Hooks.Fire(ctx, hooks.EventSessionEnd, hooks.Payload{
			"project_root": e.ProjectRoot,
		})
		cancel()
	}

	if e.Conversation != nil {
		_ = e.Conversation.SaveActive()
	}
	if e.Memory != nil {
		_ = e.Memory.Persist()
	}
	if e.Storage != nil {
		_ = e.Storage.Close()
	}

	e.setState(StateStopped)
	e.EventBus.Publish(Event{Type: "engine:stopped", Source: "engine"})
}

func (e *Engine) State() EngineState {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.state
}

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
	return Status{
		State:           e.state,
		ProjectRoot:     e.ProjectRoot,
		Provider:        e.provider(),
		Model:           e.model(),
		ProviderProfile: providerProfile,
		ModelsDevCache:  modelsDevCache,
		ContextIn:       contextIn,
		ASTBackend:      astBackend,
		ASTReason:       astReason,
		ASTLanguages:    astLanguages,
		ASTMetrics:      astMetrics,
		CodeMap:         codemapMetrics,
		MemoryDegraded:  e.memoryDegraded,
		MemoryLoadErr:   e.memoryLoadErr,
	}
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

func (e *Engine) PromptRecommendation(question string) PromptRecommendationInfo {
	return e.PromptRecommendationWithRuntime(question, ctxmgr.PromptRuntime{})
}

func (e *Engine) PromptRecommendationWithRuntime(question string, overrides ctxmgr.PromptRuntime) PromptRecommendationInfo {
	query := strings.TrimSpace(question)
	runtime := e.promptRuntimeWithOverrides(overrides)
	task := detectContextTask(query)
	language := promptlib.InferLanguage(query, nil)
	role := ctxmgr.ResolvePromptRole(query, task)
	profile := ctxmgr.ResolvePromptProfile(query, task, runtime)
	renderBudget := ctxmgr.ResolvePromptRenderBudget(task, profile, runtime)
	promptBudget := ctxmgr.PromptTokenBudget(task, profile, runtime)

	hints := make([]ContextRecommendation, 0, 4)
	add := func(severity, code, message string) {
		hints = append(hints, ContextRecommendation{
			Severity: strings.TrimSpace(strings.ToLower(severity)),
			Code:     strings.TrimSpace(strings.ToLower(code)),
			Message:  strings.TrimSpace(message),
		})
	}

	if runtime.MaxContext > 0 && promptBudget > runtime.MaxContext/4 {
		add("warn", "prompt_budget_high", "Prompt budget is high relative to runtime max_context. Use compact profile or narrower injected context.")
	}
	if runtime.MaxContext > 0 && runtime.MaxContext <= 12000 && profile == "deep" {
		add("warn", "runtime_context_tight", "Runtime context is tight for deep profile. Compact profile may reduce truncation risk.")
	}
	if countExplicitFileMentions(query) == 0 && !strings.Contains(query, "```") {
		add("info", "add_explicit_context", "No explicit file marker or fenced code detected. Add [[file:...]] or inline code blocks for higher precision.")
	}
	if runtime.ToolStyle == "" {
		add("info", "tool_style_unknown", "Provider tool style is unknown. Consider explicit runtime tool-style override when rendering prompts.")
	}
	if len(hints) == 0 {
		add("info", "prompt_budget_balanced", "Prompt profile and budget look balanced for this query.")
	}

	cacheable, dynamic := e.promptCacheTokens(query, overrides)
	percent := 0
	if total := cacheable + dynamic; total > 0 {
		percent = (cacheable * 100) / total
	}

	return PromptRecommendationInfo{
		Provider: runtime.Provider,
		Model:    runtime.Model,

		Task:     task,
		Language: language,
		Profile:  profile,
		Role:     role,

		ToolStyle:  runtime.ToolStyle,
		MaxContext: runtime.MaxContext,
		LowLatency: runtime.LowLatency,

		PromptBudgetTokens: promptBudget,

		ContextFiles:       renderBudget.ContextFiles,
		ToolList:           renderBudget.ToolList,
		InjectedBlocks:     renderBudget.InjectedBlocks,
		InjectedLines:      renderBudget.InjectedLines,
		InjectedTokens:     renderBudget.InjectedTokens,
		ProjectBriefTokens: renderBudget.ProjectBriefTokens,

		CacheableTokens:  cacheable,
		DynamicTokens:    dynamic,
		CacheablePercent: percent,

		Hints: hints,
	}
}

// promptCacheTokens renders the system prompt bundle for the given query
// and returns (cacheable_tokens, dynamic_tokens). No injected context is
// built beyond what BuildSystemPromptBundle already assembles, so the
// call is diagnostic-safe — callable from status endpoints without side
// effects. Returns zeros when the context manager isn't wired up.
func (e *Engine) promptCacheTokens(query string, overrides ctxmgr.PromptRuntime) (int, int) {
	if e == nil || e.Context == nil {
		return 0, 0
	}
	runtime := e.promptRuntimeWithOverrides(overrides)
	bundle := e.Context.BuildSystemPromptBundle(e.ProjectRoot, query, nil, e.ListTools(), runtime)
	if bundle == nil {
		return 0, 0
	}
	return tokens.Estimate(bundle.CacheableText()), tokens.Estimate(bundle.DynamicText())
}

func (e *Engine) SetProviderModel(provider, model string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.providerOverride = provider
	e.modelOverride = model
}

func (e *Engine) SetVerbose(v bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.verbose = v
}

func (e *Engine) ReloadConfig(cwd string) error {
	cfg, err := config.LoadWithOptions(config.LoadOptions{CWD: cwd})
	if err != nil {
		return err
	}
	providers, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		return err
	}
	newTools := tools.New(*cfg)

	e.mu.Lock()
	e.Config = cfg
	e.Providers = providers
	e.Tools = newTools
	e.mu.Unlock()
	return nil
}

func (e *Engine) provider() string {
	if e.providerOverride != "" {
		return e.providerOverride
	}
	return e.Config.Providers.Primary
}

func (e *Engine) model() string {
	if e.modelOverride != "" {
		return e.modelOverride
	}
	profile, ok := e.Config.Providers.Profiles[e.provider()]
	if !ok {
		return ""
	}
	return profile.Model
}

func (e *Engine) providerProfileStatusLocked() ProviderProfileStatus {
	status := ProviderProfileStatus{
		Name: strings.TrimSpace(e.provider()),
	}
	if e.Config == nil {
		status.Model = strings.TrimSpace(e.model())
		return status
	}
	if status.Name == "" {
		status.Name = strings.TrimSpace(e.Config.Providers.Primary)
	}
	if profile, ok := e.Config.Providers.Profiles[status.Name]; ok {
		status.Model = strings.TrimSpace(profile.Model)
		status.Protocol = strings.TrimSpace(profile.Protocol)
		status.BaseURL = strings.TrimSpace(profile.BaseURL)
		status.MaxTokens = profile.MaxTokens
		status.MaxContext = profile.MaxContext
		status.Configured = providerProfileConfigured(status.Name, profile)
	}
	if status.Model == "" {
		status.Model = strings.TrimSpace(e.model())
	}
	if override := strings.TrimSpace(e.modelOverride); override != "" {
		status.Model = override
	}
	return status
}

func modelsDevCacheStatus() ModelsDevCacheStatus {
	path := config.ModelsDevCachePath()
	status := ModelsDevCacheStatus{
		Path: strings.TrimSpace(path),
	}
	if status.Path == "" {
		return status
	}
	info, err := os.Stat(status.Path)
	if err != nil {
		return status
	}
	status.Exists = true
	status.UpdatedAt = info.ModTime()
	status.SizeBytes = info.Size()
	return status
}

func providerProfileConfigured(name string, profile config.ModelConfig) bool {
	apiKey := strings.TrimSpace(profile.APIKey)
	baseURL := strings.TrimSpace(profile.BaseURL)

	switch strings.ToLower(strings.TrimSpace(name)) {
	case "generic":
		return baseURL != ""
	default:
		return apiKey != "" || baseURL != ""
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

func (e *Engine) promptRuntime() ctxmgr.PromptRuntime {
	rt := ctxmgr.PromptRuntime{
		Provider: strings.TrimSpace(e.provider()),
		Model:    strings.TrimSpace(e.model()),
	}
	if e.Providers == nil {
		return rt
	}
	p, ok := e.Providers.Get(rt.Provider)
	if !ok || p == nil {
		return rt
	}
	hints := p.Hints()
	if rt.Model == "" {
		rt.Model = strings.TrimSpace(p.Model())
	}
	rt.ToolStyle = strings.TrimSpace(hints.ToolStyle)
	rt.DefaultMode = strings.TrimSpace(hints.DefaultMode)
	rt.Cache = hints.Cache
	rt.LowLatency = hints.LowLatency
	rt.MaxContext = hints.MaxContext
	if rt.MaxContext <= 0 {
		rt.MaxContext = p.MaxContext()
	}
	if len(hints.BestFor) > 0 {
		rt.BestFor = append([]string(nil), hints.BestFor...)
	}
	return rt
}

func (e *Engine) PromptRuntime() ctxmgr.PromptRuntime {
	return e.promptRuntime()
}

func (e *Engine) promptRuntimeWithOverrides(overrides ctxmgr.PromptRuntime) ctxmgr.PromptRuntime {
	runtime := e.promptRuntime()

	overrideProvider := strings.TrimSpace(overrides.Provider)
	if overrideProvider != "" && !strings.EqualFold(overrideProvider, runtime.Provider) {
		runtime = e.promptRuntimeForProvider(overrideProvider, strings.TrimSpace(overrides.Model))
	}

	if provider := strings.TrimSpace(overrides.Provider); provider != "" {
		runtime.Provider = provider
	}
	if model := strings.TrimSpace(overrides.Model); model != "" {
		runtime.Model = model
	}
	if style := strings.TrimSpace(overrides.ToolStyle); style != "" {
		runtime.ToolStyle = style
	}
	if mode := strings.TrimSpace(overrides.DefaultMode); mode != "" {
		runtime.DefaultMode = mode
	}
	if overrides.Cache {
		runtime.Cache = true
	}
	if overrides.LowLatency {
		runtime.LowLatency = true
	}
	if overrides.MaxContext > 0 {
		runtime.MaxContext = overrides.MaxContext
	}
	if len(overrides.BestFor) > 0 {
		runtime.BestFor = append([]string(nil), overrides.BestFor...)
	}

	return runtime
}

func (e *Engine) promptRuntimeForProvider(providerName, modelOverride string) ctxmgr.PromptRuntime {
	rt := ctxmgr.PromptRuntime{
		Provider: strings.TrimSpace(providerName),
		Model:    strings.TrimSpace(modelOverride),
	}
	if e.Providers == nil {
		return rt
	}
	p, ok := e.Providers.Get(rt.Provider)
	if !ok || p == nil {
		return rt
	}
	hints := p.Hints()
	if rt.Model == "" {
		rt.Model = strings.TrimSpace(p.Model())
	}
	rt.ToolStyle = strings.TrimSpace(hints.ToolStyle)
	rt.DefaultMode = strings.TrimSpace(hints.DefaultMode)
	rt.Cache = hints.Cache
	rt.LowLatency = hints.LowLatency
	rt.MaxContext = hints.MaxContext
	if rt.MaxContext <= 0 {
		rt.MaxContext = p.MaxContext()
	}
	if len(hints.BestFor) > 0 {
		rt.BestFor = append([]string(nil), hints.BestFor...)
	}
	return rt
}

func (e *Engine) contextReserveBreakdown(question string) contextReserveBreakdown {
	return e.contextReserveBreakdownWithRuntime(question, e.promptRuntime())
}

func (e *Engine) contextReserveBreakdownWithRuntime(question string, runtime ctxmgr.PromptRuntime) contextReserveBreakdown {
	promptReserve := maxInt(basePromptReserveTokens, estimateTokens(question)*3)
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

func (e *Engine) buildRequestMessages(question string, chunks []types.ContextChunk, systemPrompt string) []provider.Message {
	historyBudget := e.historyBudgetForRequest(question, chunks, systemPrompt)
	summaryBudget := 0
	if historyBudget >= 64 {
		summaryBudget = clampInt(historyBudget/6, minHistorySummaryTokens, maxHistorySummaryTokens)
	}
	mainBudget := historyBudget - summaryBudget
	if mainBudget < minHistorySummaryTokens {
		mainBudget = historyBudget
		summaryBudget = 0
	}

	msgs, omitted := e.trimmedConversationMessages(mainBudget)
	if summaryBudget > 0 && len(omitted) > 0 {
		summary := buildHistorySummary(omitted, summaryBudget)
		if strings.TrimSpace(summary) != "" {
			msgs = append([]provider.Message{
				{Role: types.RoleAssistant, Content: summary},
			}, msgs...)
		}
	}
	msgs = append(msgs, provider.Message{
		Role:    types.RoleUser,
		Content: question,
	})
	return msgs
}

func (e *Engine) conversationHistoryBudget() int {
	budget := e.Config.Context.MaxHistoryTokens
	if budget <= 0 {
		limit := e.providerMaxContext()
		if limit <= 0 {
			limit = defaultProviderContextTokens
		}
		budget = limit / 16
		if budget <= 0 {
			budget = defaultHistoryBudgetTokens
		}
	}
	if budget < minContextPerFileTokens {
		budget = minContextPerFileTokens
	}
	if budget > maxHistoryBudgetTokens {
		budget = maxHistoryBudgetTokens
	}
	return budget
}

func (e *Engine) trimmedConversationMessages(budget int) ([]provider.Message, []types.Message) {
	if e.Conversation == nil {
		return nil, nil
	}
	active := e.Conversation.Active()
	if active == nil {
		return nil, nil
	}
	rawHistory := active.Messages()
	if len(rawHistory) == 0 {
		return nil, nil
	}
	if budget <= 0 {
		return nil, nil
	}

	history := make([]types.Message, 0, len(rawHistory))
	for _, msg := range rawHistory {
		if msg.Role != types.RoleUser && msg.Role != types.RoleAssistant {
			continue
		}
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		history = append(history, msg)
	}
	if len(history) == 0 {
		return nil, nil
	}

	out := make([]provider.Message, 0, minInt(maxHistoryMessages, len(history)))
	used := 0
	cutoff := -1

	for i := len(history) - 1; i >= 0; i-- {
		if len(out) >= maxHistoryMessages || used >= budget {
			cutoff = i
			break
		}
		msg := history[i]
		content := strings.TrimSpace(msg.Content)
		tok := estimateTokens(content)
		if tok <= 0 {
			continue
		}
		if used+tok > budget {
			remaining := budget - used
			if remaining < minHistorySummaryTokens {
				cutoff = i
				break
			}
			content = trimToTokenBudget(content, remaining)
			tok = estimateTokens(content)
			if tok <= 0 {
				cutoff = i
				break
			}
		}
		out = append(out, provider.Message{
			Role:    msg.Role,
			Content: content,
		})
		used += tok
	}

	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if cutoff < 0 {
		return out, nil
	}
	omitted := make([]types.Message, cutoff+1)
	copy(omitted, history[:cutoff+1])
	return out, omitted
}

func (e *Engine) historyBudgetForRequest(question string, chunks []types.ContextChunk, systemPrompt string) int {
	providerLimit := e.providerMaxContext()
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}
	responseReserve := defaultResponseReserveTokens
	if prof, ok := e.Config.Providers.Profiles[e.provider()]; ok && prof.MaxTokens > 0 {
		responseReserve = prof.MaxTokens
	}
	if responseReserve > maxResponseReserveTokens {
		responseReserve = maxResponseReserveTokens
	}
	if responseReserve < minContextPerFileTokens {
		responseReserve = minContextPerFileTokens
	}

	usedByRequest := estimateTokens(question) + estimateTokens(systemPrompt) + baseToolReserveTokens
	for _, ch := range chunks {
		usedByRequest += ch.TokenCount
	}
	available := providerLimit - responseReserve - usedByRequest
	if available <= 0 {
		return 0
	}

	maxHistory := e.conversationHistoryBudget()
	return minInt(maxHistory, available)
}

func trimToTokenBudget(content string, maxTokens int) string {
	return tokens.TrimToBudget(content, maxTokens, "")
}

func buildHistorySummary(omitted []types.Message, maxTokens int) string {
	if maxTokens <= 0 || len(omitted) == 0 {
		return ""
	}
	userN := 0
	assistantN := 0
	for _, m := range omitted {
		if m.Role == types.RoleUser {
			userN++
		}
		if m.Role == types.RoleAssistant {
			assistantN++
		}
	}
	terms := topTermsFromMessages(omitted, 3)
	files := topFileMentions(omitted, 2)
	primary := latestOmittedByRole(omitted, types.RoleUser, 12)
	progress := latestOmittedByRole(omitted, types.RoleAssistant, 12)
	openItems := recentUserQuestions(omitted, 1, 10)

	var b strings.Builder
	fmt.Fprintf(&b, "[History summary] Scope=%d msgs (%dU/%dA).", len(omitted), userN, assistantN)
	if primary != "" {
		b.WriteString(" Primary=")
		b.WriteString(primary)
		b.WriteString(".")
	}
	if progress != "" {
		b.WriteString(" Progress=")
		b.WriteString(progress)
		b.WriteString(".")
	}
	if len(terms) > 0 {
		b.WriteString(" Topics=")
		b.WriteString(strings.Join(terms, ", "))
		b.WriteString(".")
	}
	if len(files) > 0 {
		b.WriteString(" Files=")
		b.WriteString(strings.Join(files, ", "))
		b.WriteString(".")
	}
	if len(openItems) > 0 {
		b.WriteString(" Open=")
		b.WriteString(strings.Join(openItems, " | "))
		b.WriteString(".")
	}
	return trimToTokenBudget(b.String(), maxTokens)
}

func latestOmittedByRole(messages []types.Message, role types.MessageRole, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != role {
			continue
		}
		s := trimToTokenBudget(strings.TrimSpace(messages[i].Content), maxTokens)
		if s != "" {
			return s
		}
	}
	return ""
}

func recentUserQuestions(messages []types.Message, maxItems, maxTokensPerItem int) []string {
	if maxItems <= 0 {
		return nil
	}
	out := make([]string, 0, maxItems)
	for i := len(messages) - 1; i >= 0 && len(out) < maxItems; i-- {
		msg := messages[i]
		if msg.Role != types.RoleUser {
			continue
		}
		text := strings.TrimSpace(msg.Content)
		if !strings.Contains(text, "?") {
			continue
		}
		s := trimToTokenBudget(text, maxTokensPerItem)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func topTermsFromMessages(messages []types.Message, limit int) []string {
	if limit <= 0 {
		return nil
	}
	stop := map[string]struct{}{
		"the": {}, "and": {}, "for": {}, "with": {}, "this": {}, "that": {}, "from": {}, "into": {}, "your": {}, "you": {},
		"about": {}, "also": {}, "just": {}, "when": {}, "then": {}, "than": {}, "what": {}, "which": {}, "where": {}, "while": {},
		"code": {}, "file": {}, "line": {}, "tool": {}, "message": {}, "messages": {}, "user": {}, "assistant": {},
	}
	counts := map[string]int{}
	for _, msg := range messages {
		for _, tok := range tokenizeForSummary(msg.Content) {
			if _, blocked := stop[tok]; blocked {
				continue
			}
			counts[tok]++
		}
	}
	type kv struct {
		Key   string
		Count int
	}
	ranked := make([]kv, 0, len(counts))
	for k, c := range counts {
		ranked = append(ranked, kv{Key: k, Count: c})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Count == ranked[j].Count {
			return ranked[i].Key < ranked[j].Key
		}
		return ranked[i].Count > ranked[j].Count
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]string, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.Key)
	}
	return out
}

func tokenizeForSummary(text string) []string {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(text)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) < 3 {
			continue
		}
		out = append(out, p)
	}
	return out
}

func topFileMentions(messages []types.Message, limit int) []string {
	if limit <= 0 {
		return nil
	}
	counts := map[string]int{}
	for _, msg := range messages {
		matches := fileMentionRe.FindAllString(strings.TrimSpace(msg.Content), -1)
		for _, m := range matches {
			key := strings.ToLower(strings.TrimSpace(strings.Trim(m, ".,;:()[]{}\"'`")))
			if key == "" {
				continue
			}
			counts[key]++
		}
	}
	type kv struct {
		Key   string
		Count int
	}
	ranked := make([]kv, 0, len(counts))
	for k, c := range counts {
		ranked = append(ranked, kv{Key: k, Count: c})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Count == ranked[j].Count {
			return ranked[i].Key < ranked[j].Key
		}
		return ranked[i].Count > ranked[j].Count
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]string, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.Key)
	}
	return out
}

func (e *Engine) setState(state EngineState) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = state
}

func (e *Engine) indexCodebase(ctx context.Context) {
	start := time.Now()
	e.EventBus.Publish(Event{Type: "index:start", Source: "engine", Payload: e.ProjectRoot})
	paths, err := e.collectSourceFiles(e.ProjectRoot)
	if err != nil {
		e.EventBus.Publish(Event{Type: "index:error", Source: "engine", Payload: err.Error()})
		return
	}

	if e.CodeMap != nil {
		if err := e.CodeMap.BuildFromFiles(ctx, paths); err != nil {
			e.EventBus.Publish(Event{Type: "index:error", Source: "engine", Payload: err.Error()})
			return
		}
	}

	select {
	case <-ctx.Done():
		e.EventBus.Publish(Event{Type: "index:cancelled", Source: "engine"})
		return
	default:
	}
	e.EventBus.Publish(Event{
		Type:   "index:done",
		Source: "engine",
		Payload: map[string]any{
			"duration_ms": time.Since(start).Milliseconds(),
			"files":       len(paths),
		},
	})
}

func (e *Engine) Ask(ctx context.Context, question string) (string, error) {
	return e.AskWithMetadata(ctx, question)
}

// AskRaced issues the same completion request against every candidate
// provider concurrently and returns the first successful response. When
// candidates is nil/empty the router derives candidates from ResolveOrder
// (stripping the offline stub).
//
// Race mode always goes through the non-tool-loop path: racing N provider-
// native tool loops would have them trying to edit files concurrently with
// no coordination. For multi-turn tool work, use Ask/Chat normally; race is
// for single-shot Q&A where latency or reliability matters more than cost.
func (e *Engine) AskRaced(ctx context.Context, question string, candidates []string) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(question) == "" {
		return "", "", fmt.Errorf("question cannot be empty")
	}
	if e.Providers == nil {
		return "", "", fmt.Errorf("provider router is not initialized")
	}
	e.maybeAutoHandoff(question)
	e.ensureIndexed(ctx)

	chunks := e.buildContextChunks(question)
	systemPrompt, systemBlocks := e.buildSystemPrompt(question, chunks)
	req := provider.CompletionRequest{
		Provider:     e.provider(),
		Model:        e.model(),
		Messages:     e.buildRequestMessages(question, chunks, systemPrompt),
		Context:      chunks,
		System:       systemPrompt,
		SystemBlocks: systemBlocks,
	}
	start := time.Now()
	resp, winner, err := e.Providers.CompleteRaced(ctx, req, candidates)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		e.EventBus.Publish(Event{
			Type:   "provider:race:failed",
			Source: "engine",
			Payload: map[string]any{
				"candidates":  candidates,
				"duration_ms": durMs,
				"error":       err.Error(),
			},
		})
		return "", "", err
	}
	e.recordInteraction(question, resp.Text, winner, resp.Model, resp.Usage.TotalTokens, chunks)
	e.EventBus.Publish(Event{
		Type:   "provider:race:complete",
		Source: "engine",
		Payload: map[string]any{
			"winner":      winner,
			"candidates":  candidates,
			"model":       resp.Model,
			"tokens":      resp.Usage.TotalTokens,
			"duration_ms": durMs,
		},
	})
	return resp.Text, winner, nil
}

// buildSystemPrompt renders the system prompt bundle via the context manager
// and returns both the flat text form (for providers that ignore caching)
// and the structured SystemBlocks (for Anthropic's prompt caching). Returns
// empty values when the context manager is unavailable.
func (e *Engine) buildSystemPrompt(question string, chunks []types.ContextChunk) (string, []provider.SystemBlock) {
	if e.Context == nil {
		return "", nil
	}
	bundle := e.Context.BuildSystemPromptBundle(
		e.ProjectRoot,
		question,
		chunks,
		e.ListTools(),
		e.promptRuntime(),
	)
	return bundleToSystemBlocks(bundle)
}

// bundleToSystemBlocks converts a PromptBundle into the paired (flat text,
// SystemBlocks) form consumed by providers. When the bundle has no cacheable
// sections the blocks slice is nil so non-cache-aware providers keep the
// flat-string fast path.
func bundleToSystemBlocks(bundle *promptlib.PromptBundle) (string, []provider.SystemBlock) {
	if bundle == nil {
		return "", nil
	}
	text := bundle.Text()
	if !bundle.HasCacheable() {
		return text, nil
	}
	blocks := make([]provider.SystemBlock, 0, len(bundle.Sections))
	for _, s := range bundle.Sections {
		if strings.TrimSpace(s.Text) == "" {
			continue
		}
		blocks = append(blocks, provider.SystemBlock{
			Label:     s.Label,
			Text:      s.Text,
			Cacheable: s.Cacheable,
		})
	}
	return text, blocks
}

func (e *Engine) AskWithMetadata(ctx context.Context, question string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if question == "" {
		return "", fmt.Errorf("question cannot be empty")
	}
	if e.Providers == nil {
		return "", fmt.Errorf("provider router is not initialized")
	}
	e.maybeAutoHandoff(question)
	if e.shouldUseNativeToolLoop() {
		completion, err := e.askWithNativeTools(ctx, question)
		if err != nil {
			return "", err
		}
		return completion.Answer, nil
	}
	e.ensureIndexed(ctx)

	chunks := e.buildContextChunks(question)

	systemPrompt, systemBlocks := e.buildSystemPrompt(question, chunks)
	req := provider.CompletionRequest{
		Provider:     e.provider(),
		Model:        e.model(),
		Messages:     e.buildRequestMessages(question, chunks, systemPrompt),
		Context:      chunks,
		System:       systemPrompt,
		SystemBlocks: systemBlocks,
	}

	resp, usedProvider, err := e.Providers.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	e.recordInteraction(question, resp.Text, usedProvider, resp.Model, resp.Usage.TotalTokens, chunks)
	e.EventBus.Publish(Event{
		Type:   "provider:complete",
		Source: "engine",
		Payload: map[string]any{
			"provider": usedProvider,
			"model":    resp.Model,
			"tokens":   resp.Usage.TotalTokens,
		},
	})
	return resp.Text, nil
}

func (e *Engine) StreamAsk(ctx context.Context, question string) (<-chan provider.StreamEvent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(question) == "" {
		return nil, fmt.Errorf("question cannot be empty")
	}
	if e.Providers == nil {
		return nil, fmt.Errorf("provider router is not initialized")
	}
	// user_prompt_submit fires before we commit to a provider round-trip
	// so hooks can observe every turn that leaves the UI, regardless of
	// whether the engine routes through the native-tool loop or plain
	// streaming. Hooks are best-effort — we don't block the ask if a
	// hook fails or times out.
	if e.Hooks != nil && e.Hooks.Count(hooks.EventUserPromptSubmit) > 0 {
		e.Hooks.Fire(ctx, hooks.EventUserPromptSubmit, hooks.Payload{
			"prompt":       question,
			"provider":     e.provider(),
			"model":        e.model(),
			"project_root": e.ProjectRoot,
		})
	}
	e.maybeAutoHandoff(question)
	if e.shouldUseNativeToolLoop() {
		completion, err := e.askWithNativeTools(ctx, question)
		if err != nil {
			return nil, err
		}
		return streamAnswerText(ctx, completion.Answer), nil
	}
	e.ensureIndexed(ctx)

	chunks := e.buildContextChunks(question)

	systemPrompt, systemBlocks := e.buildSystemPrompt(question, chunks)
	req := provider.CompletionRequest{
		Provider:     e.provider(),
		Model:        e.model(),
		Messages:     e.buildRequestMessages(question, chunks, systemPrompt),
		Context:      chunks,
		System:       systemPrompt,
		SystemBlocks: systemBlocks,
	}

	stream, usedProvider, err := e.Providers.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	out := make(chan provider.StreamEvent, 32)
	go func() {
		defer close(out)
		var acc strings.Builder
		for ev := range stream {
			if ev.Type == provider.StreamDelta {
				acc.WriteString(ev.Delta)
			}
			out <- ev
			if ev.Type == provider.StreamError {
				return
			}
			if ev.Type == provider.StreamDone {
				answer := acc.String()
				if strings.TrimSpace(answer) != "" {
					tokenEstimate := estimateTokens(question) + estimateTokens(answer)
					e.recordInteraction(question, answer, usedProvider, req.Model, tokenEstimate, chunks)
					e.EventBus.Publish(Event{
						Type:   "provider:complete",
						Source: "engine",
						Payload: map[string]any{
							"provider": usedProvider,
							"model":    req.Model,
							"tokens":   tokenEstimate,
						},
					})
				}
				return
			}
		}
	}()
	return out, nil
}

func (e *Engine) recordInteraction(question, answer, providerName, model string, tokenCount int, chunks []types.ContextChunk) {
	if e.Conversation != nil {
		e.Conversation.AddMessage(providerName, model, types.Message{
			Role:      types.RoleUser,
			Content:   question,
			Timestamp: time.Now(),
		})
		e.Conversation.AddMessage(providerName, model, types.Message{
			Role:      types.RoleAssistant,
			Content:   answer,
			Timestamp: time.Now(),
			TokenCnt:  tokenCount,
			Metadata: map[string]string{
				"provider": providerName,
				"model":    model,
			},
		})
	}
	if e.Memory != nil {
		e.Memory.SetWorkingQuestionAnswer(question, answer)
		for _, ch := range chunks {
			e.Memory.TouchFile(ch.Path)
		}
		_ = e.Memory.AddEpisodicInteraction(e.ProjectRoot, question, answer, 0.7)
	}
}

func (e *Engine) MemoryWorking() memory.WorkingMemory {
	if e.Memory == nil {
		return memory.WorkingMemory{}
	}
	return e.Memory.Working()
}

func (e *Engine) MemoryList(tier types.MemoryTier, limit int) ([]types.MemoryEntry, error) {
	if e.Memory == nil {
		return nil, fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.List(tier, limit)
}

func (e *Engine) MemorySearch(query string, tier types.MemoryTier, limit int) ([]types.MemoryEntry, error) {
	if e.Memory == nil {
		return nil, fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.Search(query, tier, limit)
}

func (e *Engine) MemoryAdd(entry types.MemoryEntry) error {
	if e.Memory == nil {
		return fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.Add(entry)
}

func (e *Engine) MemoryClear(tier types.MemoryTier) error {
	if e.Memory == nil {
		return fmt.Errorf("memory store is not initialized")
	}
	return e.Memory.Clear(tier)
}

func (e *Engine) ConversationActive() *conversation.Conversation {
	if e.Conversation == nil {
		return nil
	}
	return e.Conversation.Active()
}

func (e *Engine) ConversationSave() error {
	if e.Conversation == nil {
		return nil
	}
	return e.Conversation.SaveActive()
}

func (e *Engine) ConversationStart() *conversation.Conversation {
	if e.Conversation == nil {
		return nil
	}
	return e.Conversation.Start(e.provider(), e.model())
}

func (e *Engine) ConversationLoad(id string) (*conversation.Conversation, error) {
	if e.Conversation == nil {
		return nil, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.Load(id)
}

func (e *Engine) ConversationList() ([]conversation.Summary, error) {
	if e.Conversation == nil {
		return nil, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.List()
}

func (e *Engine) ConversationSearch(query string, limit int) ([]conversation.Summary, error) {
	if e.Conversation == nil {
		return nil, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.Search(query, limit)
}

func (e *Engine) ConversationBranchCreate(name string) error {
	if e.Conversation == nil {
		return fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.BranchCreate(name)
}

func (e *Engine) ConversationBranchSwitch(name string) error {
	if e.Conversation == nil {
		return fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.BranchSwitch(name)
}

func (e *Engine) ConversationBranchList() []string {
	if e.Conversation == nil {
		return nil
	}
	return e.Conversation.BranchList()
}

func (e *Engine) ConversationBranchCompare(a, b string) (conversation.BranchComparison, error) {
	if e.Conversation == nil {
		return conversation.BranchComparison{}, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.BranchCompare(a, b)
}

func (e *Engine) ConversationUndoLast() (int, error) {
	if e.Conversation == nil {
		return 0, fmt.Errorf("conversation manager is not initialized")
	}
	return e.Conversation.UndoLast()
}

func (e *Engine) ensureIndexed(ctx context.Context) {
	if e.CodeMap == nil || e.CodeMap.Graph() == nil {
		return
	}
	if len(e.CodeMap.Graph().Nodes()) > 0 {
		return
	}
	paths, err := e.collectSourceFiles(e.ProjectRoot)
	if err != nil || len(paths) == 0 {
		return
	}
	_ = e.CodeMap.BuildFromFiles(ctx, paths)
}

func (e *Engine) Analyze(ctx context.Context, path string) (AnalyzeReport, error) {
	return e.AnalyzeWithOptions(ctx, AnalyzeOptions{Path: path})
}

func (e *Engine) AnalyzeWithOptions(ctx context.Context, opts AnalyzeOptions) (AnalyzeReport, error) {
	root := e.ProjectRoot
	if strings.TrimSpace(opts.Path) != "" {
		root = opts.Path
	}
	paths, err := e.collectSourceFiles(root)
	if err != nil {
		return AnalyzeReport{}, err
	}
	if e.CodeMap != nil {
		_ = e.CodeMap.BuildFromFiles(ctx, paths)
	}
	report := AnalyzeReport{
		ProjectRoot: root,
		Files:       len(paths),
	}
	if e.CodeMap != nil && e.CodeMap.Graph() != nil {
		graph := e.CodeMap.Graph()
		report.Nodes = len(graph.Nodes())
		report.Edges = len(graph.Edges())
		report.Cycles = len(graph.Cycles())
		report.HotSpots = graph.HotSpots(10)
	}

	runSecurity := opts.Full || opts.Security
	runDeadCode := opts.Full || opts.DeadCode
	runComplexity := opts.Full || opts.Complexity
	runDuplication := opts.Full || opts.Duplication

	if runSecurity && e.Security != nil {
		secReport, err := e.Security.ScanPaths(paths)
		if err != nil {
			return report, err
		}
		report.Security = &secReport
	}
	if runDeadCode {
		items, err := e.detectDeadCode(ctx, paths)
		if err != nil {
			return report, err
		}
		report.DeadCode = items
	}
	if runComplexity {
		cx, err := e.computeComplexity(ctx, paths)
		if err != nil {
			return report, err
		}
		report.Complexity = &cx
	}
	if runDuplication {
		dup := detectDuplication(paths, duplicationMinLines)
		report.Duplication = &dup
	}
	if opts.Full || opts.Todos {
		td := collectTodoMarkers(paths)
		report.Todos = &td
	}

	return report, nil
}

func (e *Engine) collectSourceFiles(root string) ([]string, error) {
	var out []string
	if strings.TrimSpace(root) == "" {
		return out, nil
	}

	skipDirs := map[string]struct{}{
		".git":         {},
		".dfmc":        {},
		"vendor":       {},
		"node_modules": {},
		"dist":         {},
		"build":        {},
		"bin":          {},
	}
	allowed := map[string]struct{}{
		".go": {}, ".ts": {}, ".tsx": {}, ".js": {}, ".jsx": {},
		".py": {}, ".rs": {}, ".java": {}, ".cs": {}, ".php": {},
		".rb": {}, ".c": {}, ".h": {}, ".cpp": {}, ".cc": {}, ".hpp": {},
		".swift": {}, ".kt": {}, ".kts": {}, ".scala": {}, ".sql": {}, ".lua": {},
	}

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if _, ok := skipDirs[d.Name()]; ok {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(d.Name()))
		if _, ok := allowed[ext]; ok || d.Name() == "Dockerfile" {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (e *Engine) detectDeadCode(ctx context.Context, paths []string) ([]DeadCodeItem, error) {
	// Each symbol is keyed by (path, name, line) so two packages that
	// happen to export the same identifier don't collide — the old
	// `map[name]` version silently dropped duplicates, losing one of
	// the two from the final report.
	type symbolRef struct {
		Name string
		File string
		Line int
		Kind string
	}
	var symbols []symbolRef
	codeContents := map[string]string{} // comments + string literals stripped
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(content)
		// Strip strings + comments before counting occurrences. Without
		// this a symbol merely mentioned in `// TODO: replace foo` looks
		// "used" and the detector gives it a pass — a real source of
		// false negatives noted in audits.
		stripped := stripStringsAndComments(text, filepath.Ext(path))
		codeContents[path] = stripped
		if e.AST == nil {
			continue
		}
		res, err := e.AST.ParseContent(ctx, path, content)
		if err != nil {
			continue
		}
		// Use the STRIPPED content for declaration-line verification.
		// A declaration inside a backtick raw string (e.g. JS embedded
		// in the web server's HTML bundle) will have its line blanked
		// by the stripper, so isGoDeclarationLine will correctly say
		// "not a real Go decl" and the symbol gets skipped.
		strippedLines := strings.Split(stripped, "\n")
		ext := strings.ToLower(filepath.Ext(path))
		for _, sym := range res.Symbols {
			if strings.TrimSpace(sym.Name) == "" {
				continue
			}
			if !declarationLineLooksReal(strippedLines, sym.Line, ext) {
				// AST matched a `const`/`let`/`function` inside a
				// raw string literal (e.g. embedded JS/CSS) — not a
				// real symbol of THIS file's language.
				continue
			}
			symbols = append(symbols, symbolRef{
				Name: sym.Name,
				File: path,
				Line: sym.Line,
				Kind: string(sym.Kind),
			})
		}
	}

	// Compile one regex per distinct name (names often repeat across
	// packages; dedupe to save on compile cost).
	nameRegexes := map[string]*regexp.Regexp{}
	for _, s := range symbols {
		if _, ok := nameRegexes[s.Name]; ok {
			continue
		}
		nameRegexes[s.Name] = regexp.MustCompile(`\b` + regexp.QuoteMeta(s.Name) + `\b`)
	}

	out := make([]DeadCodeItem, 0)
	for _, s := range symbols {
		if looksEntrypoint(s.Name, s.File) {
			continue
		}
		if goExportedEntrypoint(s.Name, s.File) {
			continue
		}
		if isTestingIdentifier(s.Name) {
			continue
		}
		re := nameRegexes[s.Name]
		total := 0
		for _, c := range codeContents {
			total += len(re.FindAllStringIndex(c, -1))
		}
		// n counts ALL occurrences in the stripped code (including
		// the definition line itself). <= 1 means "defined but
		// nothing else references it."
		if total > 1 {
			continue
		}
		out = append(out, DeadCodeItem{
			Name:        s.Name,
			Kind:        s.Kind,
			File:        filepath.ToSlash(s.File),
			Line:        s.Line,
			Occurrences: total,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Occurrences == out[j].Occurrences {
			if out[i].File == out[j].File {
				return out[i].Line < out[j].Line
			}
			return out[i].File < out[j].File
		}
		return out[i].Occurrences < out[j].Occurrences
	})
	if len(out) > 100 {
		out = out[:100]
	}
	return out, nil
}

// declarationLineLooksReal answers whether the AST-reported symbol
// at `line` (1-indexed) points at a line that actually looks like a
// declaration in the host language. Needed because the regex-based
// AST fallback happily extracts `const wrapper = ...` from inside a
// Go raw-string literal that embeds JavaScript for a served HTML
// page — those aren't real Go symbols, just text the AST scanned.
//
// Passing lines = stripped source (strings + comments blanked out).
// A symbol inside a string literal will have its line blanked, so
// the check for a real declaration keyword (const, var, func, type,
// class, let, def, ...) correctly returns false.
func declarationLineLooksReal(lines []string, line int, ext string) bool {
	if line <= 0 || line > len(lines) {
		// Out-of-range — can happen when the AST and stripped
		// content diverge by a few lines. Be permissive; dropping a
		// real symbol is worse than including a false positive here.
		return true
	}
	t := strings.TrimSpace(lines[line-1])
	if t == "" {
		return false
	}
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "go":
		return lineStartsWithAny(t, "func", "var", "const", "type", "package")
	case "ts", "tsx", "js", "jsx", "mjs", "cjs":
		return lineStartsWithAny(t, "function", "const", "let", "var",
			"class", "interface", "type", "enum", "export", "import",
			"async function", "abstract class")
	case "py", "pyw":
		return lineStartsWithAny(t, "def", "async def", "class")
	case "rs":
		return lineStartsWithAny(t, "fn", "pub fn", "struct", "enum",
			"trait", "impl", "const", "static", "type", "mod",
			"use")
	case "java", "cs", "kt", "kts", "scala", "swift":
		// Broad: these languages have many valid decl prefixes; require
		// at least something that looks alphanumeric before a name. A
		// stripped-to-spaces string literal line will be empty (already
		// rejected above).
		return isLetterByte(t[0]) || t[0] == '@'
	case "c", "h", "cpp", "cc", "hpp":
		return isLetterByte(t[0]) || t[0] == '#'
	}
	// Unknown language — default to permissive to avoid dropping real
	// dead-code findings.
	return true
}

func lineStartsWithAny(line string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(line, p) {
			// Must be followed by whitespace, punctuation, or end of
			// line — otherwise `function` matches inside `functional`.
			rest := line[len(p):]
			if rest == "" {
				return true
			}
			c := rest[0]
			if c == ' ' || c == '\t' || c == '(' || c == '{' || c == ':' || c == '<' {
				return true
			}
		}
	}
	return false
}

func isLetterByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_'
}

// goExportedEntrypoint reports whether a Go symbol is potentially
// consumed by another package (uppercase first letter is Go's
// export marker). A package-private lowercase symbol with no uses
// is a strong dead-code candidate; an exported Public one could
// legitimately be called by a downstream importer we can't see, so
// we skip it. Language check is path-based because types.Symbol
// carries language only via the ParseResult, not the symbol itself.
func goExportedEntrypoint(name, file string) bool {
	if !strings.HasSuffix(strings.ToLower(file), ".go") {
		return false
	}
	if name == "" {
		return false
	}
	first := name[0]
	return first >= 'A' && first <= 'Z'
}

// isTestingIdentifier recognises Go / Python / JS testing entrypoints
// that the runtime discovers by name. TestX, BenchmarkX, ExampleX in
// Go; test_X / Test class methods in Python; describe / it / test in
// JS. These are called by the test runner, not by other code, so
// zero-references isn't dead code.
func isTestingIdentifier(name string) bool {
	switch {
	case strings.HasPrefix(name, "Test"),
		strings.HasPrefix(name, "Benchmark"),
		strings.HasPrefix(name, "Example"),
		strings.HasPrefix(name, "Fuzz"):
		return true
	case strings.HasPrefix(name, "test_"),
		strings.HasPrefix(name, "setUp"),
		strings.HasPrefix(name, "tearDown"):
		return true
	}
	return false
}

// stripStringsAndComments removes string literals and comments from
// source so a symbol's name occurring inside them does not inflate
// the "looks used" count. Extension-keyed language heuristics:
//
//   - .go / .js / .jsx / .ts / .tsx / .java / .c / .cpp / .cs: `//`
//     line comments, `/* ... */` block comments (cross-line),
//     double-quoted strings with `\` escapes, single-quoted runes,
//     backtick raw strings.
//   - .py: `#` line comments, `"""..."""` / `'''...'''` triple-quoted
//     docstrings (cross-line), single-line "..." and '...' strings.
//   - others: no stripping — a conservative choice that may leave
//     false-usage mentions for unknown languages.
//
// Replaced characters become spaces so line numbers and whitespace
// alignment stay intact for any downstream line-oriented analysis.
func stripStringsAndComments(text, ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "go", "js", "jsx", "mjs", "cjs", "ts", "tsx",
		"java", "c", "cpp", "cc", "h", "hpp", "cs", "rs":
		return stripCFamily(text)
	case "py", "pyw":
		return stripPython(text)
	}
	return text
}

// stripCommentsOnly removes comments but preserves string literals.
// Used by callers (duplication detector) that need to distinguish
// struct-literal tables with different data from real copy-paste —
// without string content, `Name: "review"` and `Name: "explain"`
// collapse to the same normalised line and report as a clone even
// though the semantic content differs. Dead-code and similar
// occurrence-counting passes still use stripStringsAndComments
// because a symbol merely mentioned in a help-text string ISN'T a
// real usage.
func stripCommentsOnly(text, ext string) string {
	switch strings.ToLower(strings.TrimPrefix(ext, ".")) {
	case "go", "js", "jsx", "mjs", "cjs", "ts", "tsx",
		"java", "c", "cpp", "cc", "h", "hpp", "cs", "rs":
		return stripCFamilyComments(text)
	case "py", "pyw":
		return stripPythonComments(text)
	}
	return text
}

// stripCFamilyComments blanks out line (`//`) and block (`/* ... */`)
// comments while leaving string / rune / backtick literals intact.
// Mirrors stripCFamily's structure so the behaviour is easy to
// reason about.
func stripCFamilyComments(text string) string {
	b := []byte(text)
	out := make([]byte, len(b))
	copy(out, b)
	i := 0
	for i < len(out) {
		c := out[i]
		if c == '/' && i+1 < len(out) && out[i+1] == '/' {
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		if c == '/' && i+1 < len(out) && out[i+1] == '*' {
			for i < len(out) {
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i] = ' '
					out[i+1] = ' '
					i += 2
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			quote := c
			i++
			for i < len(out) {
				if quote != '`' && out[i] == '\\' && i+1 < len(out) {
					i += 2
					continue
				}
				if out[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		i++
	}
	return string(out)
}

// stripPythonComments blanks out `#` line comments and `"""..."""` /
// `'''...'''` docstrings while leaving ordinary string literals
// intact. Single-quoted / double-quoted single-line strings are
// preserved — they carry the data we need to distinguish struct /
// dict entries.
func stripPythonComments(text string) string {
	b := []byte(text)
	out := make([]byte, len(b))
	copy(out, b)
	i := 0
	for i < len(out) {
		c := out[i]
		if c == '#' {
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		if (c == '"' || c == '\'') && i+2 < len(out) && out[i+1] == c && out[i+2] == c {
			quote := c
			out[i] = ' '
			out[i+1] = ' '
			out[i+2] = ' '
			i += 3
			for i < len(out) {
				if i+2 < len(out) && out[i] == quote && out[i+1] == quote && out[i+2] == quote {
					out[i] = ' '
					out[i+1] = ' '
					out[i+2] = ' '
					i += 3
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		// Leave single-line strings alone.
		if c == '"' || c == '\'' {
			quote := c
			i++
			for i < len(out) && out[i] != '\n' {
				if out[i] == '\\' && i+1 < len(out) {
					i += 2
					continue
				}
				if out[i] == quote {
					i++
					break
				}
				i++
			}
			continue
		}
		i++
	}
	return string(out)
}

func stripCFamily(text string) string {
	b := []byte(text)
	out := make([]byte, len(b))
	copy(out, b)
	i := 0
	for i < len(out) {
		c := out[i]
		// Line comment
		if c == '/' && i+1 < len(out) && out[i+1] == '/' {
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		// Block comment
		if c == '/' && i+1 < len(out) && out[i+1] == '*' {
			for i < len(out) {
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i] = ' '
					out[i+1] = ' '
					i += 2
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		// String / rune / raw-string literal
		if c == '"' || c == '\'' || c == '`' {
			quote := c
			out[i] = ' '
			i++
			for i < len(out) {
				if quote != '`' && out[i] == '\\' && i+1 < len(out) {
					out[i] = ' '
					out[i+1] = ' '
					i += 2
					continue
				}
				if out[i] == quote {
					out[i] = ' '
					i++
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		i++
	}
	return string(out)
}

func stripPython(text string) string {
	b := []byte(text)
	out := make([]byte, len(b))
	copy(out, b)
	i := 0
	for i < len(out) {
		c := out[i]
		if c == '#' {
			for i < len(out) && out[i] != '\n' {
				out[i] = ' '
				i++
			}
			continue
		}
		// Triple-quoted docstrings.
		if (c == '"' || c == '\'') && i+2 < len(out) && out[i+1] == c && out[i+2] == c {
			quote := c
			out[i] = ' '
			out[i+1] = ' '
			out[i+2] = ' '
			i += 3
			for i < len(out) {
				if i+2 < len(out) && out[i] == quote && out[i+1] == quote && out[i+2] == quote {
					out[i] = ' '
					out[i+1] = ' '
					out[i+2] = ' '
					i += 3
					break
				}
				if out[i] != '\n' {
					out[i] = ' '
				}
				i++
			}
			continue
		}
		// Single-quoted / double-quoted strings (single-line).
		if c == '"' || c == '\'' {
			quote := c
			out[i] = ' '
			i++
			for i < len(out) && out[i] != '\n' {
				if out[i] == '\\' && i+1 < len(out) {
					out[i] = ' '
					out[i+1] = ' '
					i += 2
					continue
				}
				if out[i] == quote {
					out[i] = ' '
					i++
					break
				}
				out[i] = ' '
				i++
			}
			continue
		}
		i++
	}
	return string(out)
}

func (e *Engine) computeComplexity(ctx context.Context, paths []string) (ComplexityReport, error) {
	report := ComplexityReport{Files: len(paths)}
	functions := make([]FunctionComplexity, 0, 128)
	fileScores := make([]FunctionComplexity, 0, len(paths))
	totalScore := 0
	maxScore := 0
	totalSymbols := 0
	scannedSymbols := 0

	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(content)
		fileScore := complexityScore(text)
		fileScores = append(fileScores, FunctionComplexity{
			Name:  filepath.Base(path),
			File:  filepath.ToSlash(path),
			Line:  1,
			Score: fileScore,
		})
		totalScore += fileScore
		if fileScore > maxScore {
			maxScore = fileScore
		}

		if e.AST == nil {
			continue
		}
		res, err := e.AST.ParseContent(ctx, path, content)
		if err != nil {
			continue
		}
		totalSymbols += len(res.Symbols)
		lines := strings.Split(text, "\n")
		for _, sym := range res.Symbols {
			kind := strings.ToLower(string(sym.Kind))
			if kind != "function" && kind != "method" {
				continue
			}
			scannedSymbols++
			start := sym.Line - 1
			if start < 0 || start >= len(lines) {
				continue
			}
			// Slice the function body by tracking brace depth from the
			// declaration line. Works for Go, C, JS/TS, Java. Python
			// (indent-based) falls back to the next-symbol heuristic
			// via endByNextSymbol because Python has no '{'.
			end := endOfFunctionBody(lines, start, res.Language)
			if end <= start {
				end = start + 1
			}
			segment := strings.Join(lines[start:minInt(end, len(lines))], "\n")
			score := complexityScore(segment)
			functions = append(functions, FunctionComplexity{
				Name:  sym.Name,
				File:  filepath.ToSlash(path),
				Line:  sym.Line,
				Score: score,
			})
		}
	}

	report.Max = maxScore
	if len(fileScores) > 0 {
		report.Average = math.Round((float64(totalScore)/float64(len(fileScores)))*100) / 100
	}
	report.TotalSymbols = totalSymbols
	report.ScannedSymbol = scannedSymbols

	sort.Slice(functions, func(i, j int) bool { return functions[i].Score > functions[j].Score })
	sort.Slice(fileScores, func(i, j int) bool { return fileScores[i].Score > fileScores[j].Score })
	if len(functions) > 20 {
		functions = functions[:20]
	}
	if len(fileScores) > 10 {
		fileScores = fileScores[:10]
	}
	report.TopFunctions = functions
	report.TopFiles = fileScores
	return report, nil
}

// complexityScore approximates McCabe cyclomatic complexity. It counts
// decision points using word-boundary regex so the scorer catches
// `if(x)` (no trailing space), tab-indented `\tif`, `}else if{`, etc.
// — all of which the previous space-padded substring variant missed.
// False positives from identifiers containing keyword substrings (e.g.
// `verifyUser`) are avoided by anchoring on `\b`.
//
// The score is language-agnostic: any branch/loop/jump keyword in any
// of the supported languages contributes +1. A function with zero
// branches returns 1 (the single entry path).
func complexityScore(text string) int {
	if text == "" {
		return 1
	}
	score := 1
	for _, re := range complexityBranchRegexes {
		score += len(re.FindAllStringIndex(text, -1))
	}
	return score
}

// endOfFunctionBody returns the (0-indexed) line AFTER the closing
// delimiter of the function that STARTS at `start`. For brace-based
// languages it tracks `{` / `}` depth while respecting strings, runes,
// and line/block comments. For Python (indent-based) it walks until a
// non-blank line's indentation drops to or below the function's own
// indent. If neither strategy finds a clean end, returns `len(lines)`
// so the caller still gets a sensible segment (whole rest of file).
func endOfFunctionBody(lines []string, start int, language string) int {
	if start < 0 || start >= len(lines) {
		return len(lines)
	}
	lang := strings.ToLower(strings.TrimSpace(language))
	if lang == "python" {
		return endOfPythonBody(lines, start)
	}
	return endOfBraceBody(lines, start)
}

// endOfBraceBody walks lines from `start`, counting balanced braces
// outside strings/comments. Stops one line past the line where depth
// returns to zero AFTER having been positive at least once. This is
// resilient to nested closures — the body of an outer function
// legitimately contains many `{}` pairs and only the outermost match
// closes it.
func endOfBraceBody(lines []string, start int) int {
	depth := 0
	opened := false
	inBlockComment := false
	for i := start; i < len(lines); i++ {
		line := lines[i]
		j := 0
		for j < len(line) {
			if inBlockComment {
				if j+1 < len(line) && line[j] == '*' && line[j+1] == '/' {
					inBlockComment = false
					j += 2
					continue
				}
				j++
				continue
			}
			// Line comment — rest of the line is not code.
			if j+1 < len(line) && line[j] == '/' && line[j+1] == '/' {
				break
			}
			// Block comment start.
			if j+1 < len(line) && line[j] == '/' && line[j+1] == '*' {
				inBlockComment = true
				j += 2
				continue
			}
			// Skip string / rune literals so their braces don't count.
			if c := line[j]; c == '"' || c == '\'' || c == '`' {
				j = skipStringLiteral(line, j)
				continue
			}
			if line[j] == '{' {
				depth++
				opened = true
			} else if line[j] == '}' {
				depth--
				if opened && depth <= 0 {
					return i + 1
				}
			}
			j++
		}
	}
	return len(lines)
}

// skipStringLiteral returns the index of the character AFTER the
// closing quote of a string/rune/backtick literal starting at
// `line[start]`. Respects escape sequences for "" and ''. Backtick
// (raw) strings don't honour escapes in Go. If the literal doesn't
// close on this line (multi-line raw strings), returns len(line).
func skipStringLiteral(line string, start int) int {
	if start >= len(line) {
		return start
	}
	quote := line[start]
	j := start + 1
	for j < len(line) {
		c := line[j]
		if quote != '`' && c == '\\' {
			j += 2
			continue
		}
		if c == quote {
			return j + 1
		}
		j++
	}
	return len(line)
}

// endOfPythonBody walks until a non-blank line whose indentation is
// ≤ the def's indentation. That line belongs to the enclosing scope,
// so the function ends the line before.
func endOfPythonBody(lines []string, start int) int {
	defIndent := leadingWhitespaceLen(lines[start])
	for i := start + 1; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if leadingWhitespaceLen(line) <= defIndent {
			return i
		}
	}
	return len(lines)
}

func leadingWhitespaceLen(s string) int {
	n := 0
	for n < len(s) && (s[n] == ' ' || s[n] == '\t') {
		n++
	}
	return n
}

// complexityBranchRegexes is compiled once and reused; compiling per
// call is expensive and shows up in profiles for big codebases. Each
// regex is word-boundary-anchored on the keyword side and loose on
// the delimiter side (accepts space / paren / brace / line end).
var complexityBranchRegexes = func() []*regexp.Regexp {
	keywords := []string{
		"if", "else if", "elif",
		"for", "while", "do",
		"switch", "case",
		"catch", "except", "rescue", "finally",
		"goto",
	}
	out := make([]*regexp.Regexp, 0, len(keywords)+3)
	for _, kw := range keywords {
		// Keyword must be preceded by non-word OR start-of-string,
		// and followed by a space/paren/brace/colon. `\b...\b` alone
		// would match inside identifiers when followed by whitespace
		// only, which is why we also require the trailing-char class.
		out = append(out, regexp.MustCompile(`(^|\W)`+regexp.QuoteMeta(kw)+`[\s(:{]`))
	}
	// Short-circuit boolean operators — one decision per && / ||.
	out = append(out,
		regexp.MustCompile(`&&`),
		regexp.MustCompile(`\|\|`),
	)
	// Ternary: match `?` followed by non-punct so we don't count
	// `foo?.bar` (JS optional chaining) or `type?` annotations.
	out = append(out, regexp.MustCompile(`\?\s`))
	return out
}()

func looksEntrypoint(name, file string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "main" || n == "init" {
		return true
	}
	if strings.HasPrefix(n, "test") {
		return true
	}
	base := strings.ToLower(filepath.Base(file))
	return strings.HasSuffix(base, "_test.go")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func estimateTokens(text string) int {
	return tokens.Estimate(text)
}
