package theme

// types.go — shared types for the TUI theme subpackage.
//
// Keeps struct definitions and constants that are used across render.go
// and referenced by callers in the parent tui package. Type aliases live
// here so the parent tui package can use theme.StatsPanelInfo without
// importing render-specific details.

import "time"

// --- stats panel mode ---------------------------------------------------

type StatsPanelMode string

const (
	StatsPanelModeOverview  StatsPanelMode = "overview"
	StatsPanelModeTodos     StatsPanelMode = "todos"
	StatsPanelModeTasks     StatsPanelMode = "tasks"
	StatsPanelModeSubagents StatsPanelMode = "subagents"
	StatsPanelModeProviders StatsPanelMode = "providers"
)

// --- tool chip ----------------------------------------------------------

type ToolChip struct {
	Name            string
	Status          string // "ok", "failed", "running"
	DurationMs      int
	Preview         string
	Step            int
	OutputTokens    int // estimated tokens returned by the tool (0 when unknown)
	Truncated       bool
	Verb            string
	CompressedChars int
	SavedChars      int
	CompressionPct  int // 0–99, how much of the raw output was dropped
	// HardTruncated is true when the per-call char cap forced bytes
	// out of the model-bound payload (NOT just ANSI/spinner noise).
	// HardTruncatedRunes counts runes lost from output + data.
	// Distinct from Truncated above (which is the sandbox's own flag).
	// Renderers should show a "✂ N chars dropped" badge so the user
	// knows the model is missing real content, not just compressed.
	HardTruncated      bool
	HardTruncatedRunes int
	InnerLines         []string
	Reason             string
	Expanded           bool // Whether to show details
}

// --- todo strip ---------------------------------------------------------

type TodoStripItem struct {
	Content    string
	Status     string
	ActiveForm string
}

// --- runtime summary ----------------------------------------------------

type RuntimeSummary struct {
	Active       bool
	Phase        string
	Step         int
	MaxSteps     int
	ToolRounds   int
	LastTool     string
	LastStatus   string
	LastDuration int
	Provider     string
	Model        string
}

// --- message header -----------------------------------------------------

type MessageHeaderInfo struct {
	Role         string
	Timestamp    time.Time
	TokenCount   int
	DurationMs   int
	ToolCalls    int
	ToolFailures int
	Streaming    bool
	SpinnerFrame int
	CopyIndex    int
}

// --- chat header --------------------------------------------------------

type ChatHeaderInfo struct {
	Provider            string
	Model               string
	Configured          bool
	MaxContext          int
	ContextTokens       int
	ContextWindowTokens int
	Pinned              string
	ToolsEnabled        bool
	Streaming           bool
	AgentActive         bool
	AgentPhase          string
	AgentStep           int
	AgentMax            int
	QueuedCount         int
	Parked              bool
	// BannerActive is true while the resume banner is shown above the
	// composer. Used to suppress duplicate "parked" chips/rows in the
	// chat header and stats panel — the banner is the canonical "do
	// something" surface and the duplicates are noise. Esc dismisses
	// the banner, which clears this flag and lets the smaller surfaces
	// re-emerge so the user can rediscover the parked state.
	BannerActive    bool
	PendingNotes    int
	Slim            bool
	ActiveTools     int
	ActiveSubagents int
	SubagentSummary string
	PlanMode        bool
	ApprovalGated   bool
	ApprovalPending bool
	SpinnerFrame    int
	IntentLast      string
	DriveRunID      string
	DriveTodoID     string
	DriveDone       int
	DriveTotal      int
	DriveBlocked    int
}

// --- stats panel info ---------------------------------------------------

// ProviderPanelRow is one row in the F2 providers sub-panel.
type ProviderPanelRow struct {
	Name           string
	Active         bool // is current runtime provider
	Primary        bool // is primary in config
	Fallback       bool // is in the configured provider fallback chain
	Models         []string
	FallbackModels []string
	MaxContext     int
	Protocol       string
	HasAPIKey      bool
	Status         string // "ready" | "no-key" | "offline"
	IsPlaceholder  bool
}

// ContextPayloadSnapshot is the canonical "what the next provider request
// would look like" budget snapshot. Stats panel, runtime strip, and future
// context cockpit views should read this instead of recomputing window/free
// arithmetic in their own renderers.
type ContextPayloadSnapshot struct {
	Provider             string
	Model                string
	LimitSource          string
	MaxContext           int
	WindowTokens         int
	FreeTokens           int
	EvidenceTokens       int
	EvidenceBudgetTokens int
	SystemTokens         int
	MessageTokens        int
	ResponseReserve      int
	ToolReserve          int
	HistoryReserve       int
	MessageCount         int
	ToolCallCount        int
	WorkspaceEvidenceOff bool
}

type StatsPanelInfo struct {
	Mode                    StatsPanelMode
	Provider                string
	Model                   string
	Configured              bool
	CostPer1kTokens         float64
	ContextTokens           int
	ContextWindowTokens     int
	MaxContext              int
	ContextProvider         string
	ContextModel            string
	ContextLimitSource      string
	ContextTask             string
	ContextFileCount        int
	ContextMaxFiles         int
	ContextBudgetTokens     int
	ContextAvailableTokens  int
	ContextMaxTokensPerFile int
	ContextCompression      string
	ContextReasons          []string
	ContextTopFiles         []string
	ContextSystemTokens     int
	ContextHistoryTokens    int
	ContextHistoryReserve   int
	ContextResponseTokens   int
	ContextToolTokens       int
	ContextMessageCount     int
	ContextToolCallCount    int
	ContextPayload          ContextPayloadSnapshot
	ComposerTokens          int
	TranscriptInputTokens   int
	TranscriptOutputTokens  int
	LiveInputTokens         int
	LiveOutputTokens        int
	LiveTotalTokens         int
	LastInputTokens         int
	LastOutputTokens        int
	LastTotalTokens         int
	SessionInputTokens      int
	SessionOutputTokens     int
	SessionTotalTokens      int
	Streaming               bool
	AgentActive             bool
	AgentPhase              string
	AgentStep               int
	AgentMaxSteps           int
	ToolRounds              int
	LastTool                string
	LastStatus              string
	LastDurationMs          int
	Parked                  bool
	// BannerActive — same meaning as ChatHeaderInfo.BannerActive: when
	// the resume banner is up, the stats panel suppresses its duplicate
	// "parked" row + "/continue resumes parked agent" hint so the panel
	// doesn't echo the banner's primary message.
	BannerActive           bool
	QueuedCount            int
	PendingNotes           int
	ToolsEnabled           bool
	ToolCount              int
	Branch                 string
	Dirty                  bool
	Detached               bool
	Inserted               int
	Deleted                int
	SessionElapsed         time.Duration
	MessageCount           int
	Pinned                 string
	CompressionSavedChars  int
	CompressionRawChars    int
	TodoTotal              int
	TodoPending            int
	TodoDoing              int
	TodoDone               int
	TodoActive             string
	TodoLines              []string
	TaskLines              []string
	TaskTreeLines          []string
	WorkflowStatus         string
	WorkflowMeter          string
	WorkflowExecution      string
	WorkflowTimeline       []string
	WorkflowRecent         []string
	Boosted                bool
	BoostSeconds           int
	FocusLocked            bool
	StatsPanelScroll       int
	SubagentLines          []string
	SubagentSummary        string
	ActiveSubagents        int
	SubagentLimit          int
	ActiveTools            int
	DriveRunID             string
	DriveDone              int
	DriveTotal             int
	DriveBlocked           int
	PlanSubtasks           int
	PlanParallel           bool
	PlanConfidence         float64
	SpinnerFrame           int
	Providers              []ProviderPanelRow
	ProvidersSelectedIndex int // cursor position in the providers list
}
