// status_types.go — JSON-shaped data carriers exposed by Engine
// methods. Lifted out of engine.go (which was 174 symbols + 27
// imports + ~300 lines of pure type definitions before any runtime
// logic) so the file you read to understand "what the engine is"
// stops being weighed down by "what its replies look like".
//
// Nothing here has behaviour beyond field tags. If you're adding a
// new status field that ALSO needs a method, put the method in
// engine.go (or its analyze/status sibling files) and keep this
// file pure data — that boundary is the whole point of the split.

package engine

import (
	"time"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

type Status struct {
	State           EngineState                 `json:"state"`
	ProjectRoot     string                      `json:"project_root"`
	Provider        string                      `json:"provider"`
	Model           string                      `json:"model"`
	ProviderProfile ProviderProfileStatus       `json:"provider_profile,omitempty"`
	ModelsDevCache  ModelsDevCacheStatus        `json:"models_dev_cache,omitempty"`
	ContextIn       *ContextInStatus            `json:"context_in,omitempty"`
	ASTBackend      string                      `json:"ast_backend"`
	ASTReason       string                      `json:"ast_reason,omitempty"`
	ASTLanguages    []ast.BackendLanguageStatus `json:"ast_languages,omitempty"`
	ASTMetrics      ast.ParseMetrics            `json:"ast_metrics,omitempty"`
	CodeMap         codemap.BuildMetrics        `json:"codemap_metrics,omitempty"`
	// MemoryDegraded reports that the persistent memory store failed
	// to load at Init and the engine is running with an empty store.
	// TUI/web surfaces can render this next to the chat header.
	MemoryDegraded bool   `json:"memory_degraded,omitempty"`
	MemoryLoadErr  string `json:"memory_load_err,omitempty"`

	// ActiveDrives lists drive runs currently executing in this
	// process. Empty (omitted from JSON) when no run is in flight.
	// TUI/web/CLI status surfaces use this to render an "in flight"
	// badge alongside provider/model.
	ActiveDrives []ActiveDriveStatus `json:"active_drives,omitempty"`

	// EventsDropped is the cumulative number of bus events the engine
	// has had to discard because some subscriber's channel was full
	// when Publish ran. Non-zero means the TUI / web client may have
	// missed activity-feed entries — the agent itself is unaffected,
	// but operators should investigate before assuming the loop is
	// hung. Increments are monotonic for the lifetime of the process.
	EventsDropped uint64 `json:"events_dropped,omitempty"`

	// OpenCircuits lists provider names whose circuit breaker has
	// tripped and is currently in cooldown. The router skips these
	// providers entirely on Complete/Stream until cooldown elapses,
	// so a non-empty list explains why fallback providers may be
	// serving traffic the user expected from the primary. Empty (and
	// omitted from JSON) when every circuit is closed.
	OpenCircuits []string `json:"open_circuits,omitempty"`

	// SubagentRetries is the cumulative count of transient retries
	// fired by runSubagentRetrying since process start. Counts the
	// retries themselves, not the underlying calls — 0 means every
	// subagent invocation succeeded on first attempt (or no subagent
	// activity yet). Non-zero indicates flaky upstream behaviour the
	// retry layer is silently absorbing; if it climbs unbounded,
	// investigate the provider rather than just trusting the eventual
	// success.
	SubagentRetries int64 `json:"subagent_retries,omitempty"`

	// SubagentRetries5m is the count of retries fired in the last
	// 5 minutes — answers "is this happening NOW?" which the
	// cumulative counter alone can't. Long-running daemons can have
	// SubagentRetries climb steadily over weeks while
	// SubagentRetries5m stays at 0; the windowed view is the
	// page-the-oncall signal.
	SubagentRetries5m int `json:"subagent_retries_5m,omitempty"`

	// SubagentsActive is the number of RunSubagent calls currently inside
	// the engine. SubagentsLimit is the hard ceiling used by RunSubagent
	// before it starts a new worker. Both are snapshot values for status
	// surfaces; lifecycle events remain the timeline source of truth.
	SubagentsActive int `json:"subagents_active,omitempty"`
	SubagentsLimit  int `json:"subagents_limit,omitempty"`
}

// ActiveDriveStatus is the status-surface projection of a single
// in-flight drive run. Mirrors drive.ActiveRun but lives here so
// the engine package's Status() doesn't import internal/drive
// (it does — drive_adapter.go — but the type lives here so
// downstream JSON consumers don't have to chase imports either).
type ActiveDriveStatus struct {
	RunID string `json:"run_id"`
	Task  string `json:"task,omitempty"`
}

type ContextInStatus struct {
	Query                string                `json:"query,omitempty"`
	Task                 string                `json:"task,omitempty"`
	BuiltAt              time.Time             `json:"built_at,omitempty"`
	Provider             string                `json:"provider,omitempty"`
	Model                string                `json:"model,omitempty"`
	ProviderMaxContext   int                   `json:"provider_max_context,omitempty"`
	ContextAvailable     int                   `json:"context_available_tokens,omitempty"`
	ExplicitFileMentions int                   `json:"explicit_file_mentions,omitempty"`
	MaxFiles             int                   `json:"max_files,omitempty"`
	MaxTokensTotal       int                   `json:"max_tokens_total,omitempty"`
	MaxTokensPerFile     int                   `json:"max_tokens_per_file,omitempty"`
	Compression          string                `json:"compression,omitempty"`
	IncludeTests         bool                  `json:"include_tests"`
	IncludeDocs          bool                  `json:"include_docs"`
	FileCount            int                   `json:"file_count,omitempty"`
	TokenCount           int                   `json:"token_count,omitempty"`
	Reasons              []string              `json:"reasons,omitempty"`
	Files                []ContextInFileStatus `json:"files,omitempty"`
}

type ContextInFileStatus struct {
	Path        string  `json:"path"`
	LineStart   int     `json:"line_start,omitempty"`
	LineEnd     int     `json:"line_end,omitempty"`
	TokenCount  int     `json:"token_count,omitempty"`
	Score       float64 `json:"score,omitempty"`
	Compression string  `json:"compression,omitempty"`
	Reason      string  `json:"reason,omitempty"`
}

type ContextDebugStatus struct {
	Query              string                   `json:"query,omitempty"`
	Task               string                   `json:"task,omitempty"`
	BuiltAt            time.Time                `json:"built_at,omitempty"`
	Provider           string                   `json:"provider,omitempty"`
	Model              string                   `json:"model,omitempty"`
	ProviderMaxContext int                      `json:"provider_max_context,omitempty"`
	MaxTokensTotal     int                      `json:"max_tokens_total,omitempty"`
	TokenCount         int                      `json:"token_count,omitempty"`
	FileCount          int                      `json:"file_count,omitempty"`
	Reasons            []string                 `json:"reasons,omitempty"`
	Files              []ContextDebugFileStatus `json:"files,omitempty"`
}

type ContextDebugFileStatus struct {
	Path        string  `json:"path"`
	Language    string  `json:"language,omitempty"`
	LineStart   int     `json:"line_start,omitempty"`
	LineEnd     int     `json:"line_end,omitempty"`
	TokenCount  int     `json:"token_count,omitempty"`
	Score       float64 `json:"score,omitempty"`
	Compression string  `json:"compression,omitempty"`
	Source      string  `json:"source,omitempty"`
	Reason      string  `json:"reason,omitempty"`
	Content     string  `json:"content,omitempty"`
}

type ProviderProfileStatus struct {
	Name            string   `json:"name,omitempty"`
	Model           string   `json:"model,omitempty"`
	Protocol        string   `json:"protocol,omitempty"`
	BaseURL         string   `json:"base_url,omitempty"`
	MaxTokens       int      `json:"max_tokens,omitempty"`
	MaxContext      int      `json:"max_context,omitempty"`
	CostPer1kTokens float64  `json:"cost_per_1k_tokens,omitempty"`
	Configured      bool     `json:"configured"`
	Advisories      []string `json:"advisories,omitempty"`
}

type ModelsDevCacheStatus struct {
	Path      string    `json:"path,omitempty"`
	Exists    bool      `json:"exists"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	SizeBytes int64     `json:"size_bytes,omitempty"`
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

	MaxFiles         int `json:"max_files"`
	MaxTokensTotal   int `json:"max_tokens_total"`
	MaxTokensPerFile int `json:"max_tokens_per_file"`
	MaxHistoryTokens int `json:"max_history_tokens"`
	// MaxHistoryMessages is the resolved trim-window message ceiling
	// (config override or engine default). Surfaced so CLI/HTTP/remote
	// consumers can show "you're at N/M stored messages" without
	// duplicating the resolution logic.
	MaxHistoryMessages int    `json:"max_history_messages"`
	Compression        string `json:"compression"`
	AutoIncludeFiles   bool   `json:"auto_include_files"`
	IncludeTests       bool   `json:"include_tests"`
	IncludeDocs        bool   `json:"include_docs"`
}

type ContextRecommendation struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type ContextTuningSuggestion struct {
	Priority string `json:"priority"`
	Key      string `json:"key"`
	Value    any    `json:"value"`
	Reason   string `json:"reason"`
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

	// Cache boundary metrics — how the rendered bundle splits across
	// the stable/dynamic boundary declared by <<DFMC_CACHE_BREAK>>.
	// CacheablePercent is the cacheable share rounded to an integer
	// percentage so it fits a status line without losing meaning.
	CacheableTokens  int `json:"cacheable_tokens"`
	DynamicTokens    int `json:"dynamic_tokens"`
	CacheablePercent int `json:"cacheable_percent"`

	Hints []ContextRecommendation `json:"hints"`
}

// ContextBreakdown is the canonical real-time context budget snapshot.
// Gathered from contextReserveBreakdown, historyBudgetForRequest, and
// contextBuildOptionsWithRuntime so every surface (TUI panel, web API,
// dfmc status) consumes the same data shape.
type ContextBreakdown struct {
	// Provider identity
	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Budget boundaries
	MaxContext int `json:"max_context"`
	UsedTotal  int `json:"used_total"`
	// Reserve buckets
	SystemPrompt  int `json:"system_prompt_tokens"`
	History       int `json:"history_tokens"`
	ContextChunks int `json:"context_chunks_tokens"`
	Response      int `json:"response_reserve_tokens"`
	ToolReserve   int `json:"tool_reserve_tokens"`
	// Remaining
	Available int `json:"available"`
	// Percentages (0.0-1.0)
	SystemPromptPct  float64 `json:"system_prompt_pct"`
	HistoryPct       float64 `json:"history_pct"`
	ContextChunksPct float64 `json:"context_chunks_pct"`
	ResponsePct      float64 `json:"response_pct"`
	// Files in context
	FilesInContext []string `json:"files_in_context"`
	// Configuration
	Compression string `json:"compression"`
	Task        string `json:"task"`
}
