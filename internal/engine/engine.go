package engine

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
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
	"github.com/dontfuckmycode/dfmc/internal/memory"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/security"
	"github.com/dontfuckmycode/dfmc/internal/storage"
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

	providerOverride string
	modelOverride    string
	verbose          bool

	mu    sync.RWMutex
	state EngineState
}

type Status struct {
	State       EngineState `json:"state"`
	ProjectRoot string      `json:"project_root"`
	Provider    string      `json:"provider"`
	Model       string      `json:"model"`
}

type ContextBudgetInfo struct {
	Provider             string  `json:"provider"`
	Model                string  `json:"model"`
	ProviderMaxContext   int     `json:"provider_max_context"`
	Task                 string  `json:"task"`
	ExplicitFileMentions int     `json:"explicit_file_mentions"`
	TaskTotalScale       float64 `json:"task_total_scale"`
	TaskFileScale        float64 `json:"task_file_scale"`
	TaskPerFileScale     float64 `json:"task_per_file_scale"`

	ContextAvailableTokens int `json:"context_available_tokens"`
	ReserveTotalTokens     int `json:"reserve_total_tokens"`
	ReservePromptTokens    int `json:"reserve_prompt_tokens"`
	ReserveHistoryTokens   int `json:"reserve_history_tokens"`
	ReserveResponseTokens  int `json:"reserve_response_tokens"`
	ReserveToolTokens      int `json:"reserve_tool_tokens"`

	MaxFiles         int    `json:"max_files"`
	MaxTokensTotal   int    `json:"max_tokens_total"`
	MaxTokensPerFile int    `json:"max_tokens_per_file"`
	MaxHistoryTokens int    `json:"max_history_tokens"`
	Compression      string `json:"compression"`
	IncludeTests     bool   `json:"include_tests"`
	IncludeDocs      bool   `json:"include_docs"`
}

type ContextRecommendation struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type PromptRecommendationInfo struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`

	Task     string `json:"task"`
	Language string `json:"language"`
	Profile  string `json:"profile"`
	Role     string `json:"role"`

	ToolStyle  string `json:"tool_style"`
	MaxContext int    `json:"max_context"`
	LowLatency bool   `json:"low_latency"`

	PromptBudgetTokens int `json:"prompt_budget_tokens"`

	ContextFiles       int `json:"context_files"`
	ToolList           int `json:"tool_list"`
	InjectedBlocks     int `json:"injected_blocks"`
	InjectedLines      int `json:"injected_lines"`
	InjectedTokens     int `json:"injected_tokens"`
	ProjectBriefTokens int `json:"project_brief_tokens"`

	Hints []ContextRecommendation `json:"hints"`
}

type AnalyzeReport struct {
	ProjectRoot string            `json:"project_root"`
	Files       int               `json:"files"`
	Nodes       int               `json:"nodes"`
	Edges       int               `json:"edges"`
	Cycles      int               `json:"cycles"`
	HotSpots    []codemap.Node    `json:"hotspots"`
	Security    *security.Report  `json:"security,omitempty"`
	DeadCode    []DeadCodeItem    `json:"dead_code,omitempty"`
	Complexity  *ComplexityReport `json:"complexity,omitempty"`
}

type AnalyzeOptions struct {
	Path       string
	Full       bool
	Security   bool
	DeadCode   bool
	Complexity bool
}

type DeadCodeItem struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	Occurrences int    `json:"occurrences"`
}

type FunctionComplexity struct {
	Name  string `json:"name"`
	File  string `json:"file"`
	Line  int    `json:"line"`
	Score int    `json:"score"`
}

type ComplexityReport struct {
	Files         int                  `json:"files"`
	Average       float64              `json:"average"`
	Max           int                  `json:"max"`
	TopFunctions  []FunctionComplexity `json:"top_functions,omitempty"`
	TopFiles      []FunctionComplexity `json:"top_files,omitempty"`
	TotalSymbols  int                  `json:"total_symbols"`
	ScannedSymbol int                  `json:"scanned_symbols"`
}

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
	e.Memory = memory.New(e.Storage)
	_ = e.Memory.Load()
	e.Conversation = conversation.New(e.Storage)
	e.Security = security.New()

	e.Providers, err = provider.NewRouter(e.Config.Providers)
	if err != nil {
		return fmt.Errorf("provider router init failed: %w", err)
	}

	e.ProjectRoot = config.FindProjectRoot("")
	if e.ProjectRoot != "" {
		go e.indexCodebase(ctx)
	}

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
	if e.Tools == nil {
		return tools.Result{}, fmt.Errorf("tool engine is not initialized")
	}
	res, err := e.Tools.Execute(ctx, name, tools.Request{
		ProjectRoot: e.ProjectRoot,
		Params:      params,
	})
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

func (e *Engine) StartServing() {
	e.setState(StateServing)
	e.EventBus.Publish(Event{Type: "engine:serving", Source: "engine"})
}

func (e *Engine) Shutdown() {
	e.setState(StateShuttingDown)
	e.EventBus.Publish(Event{Type: "engine:shutdown", Source: "engine"})

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
	return Status{
		State:       e.state,
		ProjectRoot: e.ProjectRoot,
		Provider:    e.provider(),
		Model:       e.model(),
	}
}

func (e *Engine) ContextBudgetPreview(question string) ContextBudgetInfo {
	opts := e.contextBuildOptions(question)
	task := detectContextTask(question)
	profile := contextTaskProfile(task)
	explicitMentions := countExplicitFileMentions(question)
	providerLimit := e.providerMaxContext()
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}
	reserve := e.contextReserveBreakdown(question)
	available := providerLimit - reserve.Total
	if available < minContextTotalBudgetTokens {
		available = minContextTotalBudgetTokens
	}
	return ContextBudgetInfo{
		Provider:               e.provider(),
		Model:                  e.model(),
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
	preview := e.ContextBudgetPreview(question)
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

func (e *Engine) PromptRecommendation(question string) PromptRecommendationInfo {
	query := strings.TrimSpace(question)
	runtime := e.promptRuntime()
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

		Hints: hints,
	}
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
	opts := e.contextBuildOptions(question)
	chunks, err := e.Context.BuildWithOptions(question, opts)
	if err != nil {
		e.EventBus.Publish(Event{
			Type:    "context:error",
			Source:  "engine",
			Payload: err.Error(),
		})
		return nil
	}
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
		},
	})
	return chunks
}

func (e *Engine) contextBuildOptions(question string) ctxmgr.BuildOptions {
	cfg := e.Config.Context
	task := detectContextTask(question)
	profile := contextTaskProfile(task)
	explicitFileRefs := countExplicitFileMentions(question)
	opts := ctxmgr.BuildOptions{
		MaxFiles:         cfg.MaxFiles,
		MaxTokensTotal:   cfg.MaxTokensTotal,
		MaxTokensPerFile: cfg.MaxTokensPerFile,
		Compression:      cfg.Compression,
		IncludeTests:     cfg.IncludeTests,
		IncludeDocs:      cfg.IncludeDocs,
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

	providerLimit := e.providerMaxContext()
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}
	reserve := e.contextReserveBreakdown(question)
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
	fileMentionRe         = regexp.MustCompile(`(?i)\b[\w./-]+\.(go|ts|tsx|js|jsx|py|rs|java|cs|php|yaml|yml|json|md)\b`)
)

func countExplicitFileMentions(question string) int {
	if strings.TrimSpace(question) == "" {
		return 0
	}
	return len(explicitFileMentionRe.FindAllStringIndex(question, -1))
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

func (e *Engine) contextReserveBreakdown(question string) contextReserveBreakdown {
	promptReserve := maxInt(basePromptReserveTokens, estimateTokens(question)*3)
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
	historyReserve := e.conversationHistoryBudget()
	toolReserve := baseToolReserveTokens
	total := promptReserve + responseReserve + toolReserve + historyReserve
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
	if maxTokens <= 0 {
		return ""
	}
	words := strings.Fields(strings.TrimSpace(content))
	if len(words) <= maxTokens {
		return strings.TrimSpace(content)
	}
	return strings.Join(words[:maxTokens], " ")
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

func recentOmittedUserIntents(messages []types.Message, maxItems, maxTokensPerItem int) []string {
	if maxItems <= 0 {
		return nil
	}
	out := make([]string, 0, maxItems)
	for i := len(messages) - 1; i >= 0 && len(out) < maxItems; i-- {
		if messages[i].Role != types.RoleUser {
			continue
		}
		s := trimToTokenBudget(strings.TrimSpace(messages[i].Content), maxTokensPerItem)
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
		"bir": {}, "ve": {}, "ile": {}, "icin": {}, "için": {}, "ama": {}, "gibi": {}, "daha": {}, "bunu": {}, "sunu": {},
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
	e.ensureIndexed(ctx)

	chunks := e.buildContextChunks(question)

	systemPrompt := ""
	if e.Context != nil {
		systemPrompt = e.Context.BuildSystemPromptWithRuntime(
			e.ProjectRoot,
			question,
			chunks,
			e.ListTools(),
			e.promptRuntime(),
		)
	}
	req := provider.CompletionRequest{
		Provider: e.provider(),
		Model:    e.model(),
		Messages: e.buildRequestMessages(question, chunks, systemPrompt),
		Context:  chunks,
		System:   systemPrompt,
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
	e.ensureIndexed(ctx)

	chunks := e.buildContextChunks(question)

	systemPrompt := ""
	if e.Context != nil {
		systemPrompt = e.Context.BuildSystemPromptWithRuntime(
			e.ProjectRoot,
			question,
			chunks,
			e.ListTools(),
			e.promptRuntime(),
		)
	}
	req := provider.CompletionRequest{
		Provider: e.provider(),
		Model:    e.model(),
		Messages: e.buildRequestMessages(question, chunks, systemPrompt),
		Context:  chunks,
		System:   systemPrompt,
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
	type symbolRef struct {
		File string
		Line int
		Kind string
	}
	symbols := map[string]symbolRef{}
	contents := map[string]string{}
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(content)
		contents[path] = text
		if e.AST == nil {
			continue
		}
		res, err := e.AST.ParseContent(ctx, path, content)
		if err != nil {
			continue
		}
		for _, sym := range res.Symbols {
			key := sym.Name
			if key == "" {
				continue
			}
			if _, exists := symbols[key]; !exists {
				symbols[key] = symbolRef{
					File: path,
					Line: sym.Line,
					Kind: string(sym.Kind),
				}
			}
		}
	}

	wordCount := map[string]int{}
	for name := range symbols {
		re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
		total := 0
		for _, content := range contents {
			total += len(re.FindAllStringIndex(content, -1))
		}
		wordCount[name] = total
	}

	out := make([]DeadCodeItem, 0)
	for name, meta := range symbols {
		n := wordCount[name]
		if n <= 1 && !looksEntrypoint(name, meta.File) {
			out = append(out, DeadCodeItem{
				Name:        name,
				Kind:        meta.Kind,
				File:        filepath.ToSlash(meta.File),
				Line:        meta.Line,
				Occurrences: n,
			})
		}
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
		for i, sym := range res.Symbols {
			kind := strings.ToLower(string(sym.Kind))
			if kind != "function" && kind != "method" {
				continue
			}
			scannedSymbols++
			start := sym.Line - 1
			if start < 0 || start >= len(lines) {
				continue
			}
			end := len(lines)
			for j := i + 1; j < len(res.Symbols); j++ {
				if res.Symbols[j].Line > sym.Line {
					end = res.Symbols[j].Line - 1
					break
				}
			}
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

func complexityScore(text string) int {
	score := 1
	score += strings.Count(text, " if ")
	score += strings.Count(text, " for ")
	score += strings.Count(text, " switch ")
	score += strings.Count(text, " case ")
	score += strings.Count(text, " && ")
	score += strings.Count(text, " || ")
	score += strings.Count(text, " else if ")
	score += strings.Count(text, " catch ")
	score += strings.Count(text, " except ")
	score += strings.Count(text, " ? ")
	return score
}

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
	return len(strings.Fields(text))
}
