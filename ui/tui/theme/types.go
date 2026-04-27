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
	StatsPanelModeOverview   StatsPanelMode = "overview"
	StatsPanelModeTodos      StatsPanelMode = "todos"
	StatsPanelModeTasks      StatsPanelMode = "tasks"
	StatsPanelModeSubagents  StatsPanelMode = "subagents"
	StatsPanelModeProviders  StatsPanelMode = "providers"
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
	InnerLines      []string
	Reason          string
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
	Provider        string
	Model           string
	Configured      bool
	MaxContext      int
	ContextTokens   int
	Pinned          string
	ToolsEnabled    bool
	Streaming       bool
	AgentActive     bool
	AgentPhase      string
	AgentStep       int
	AgentMax        int
	QueuedCount     int
	Parked          bool
	PendingNotes    int
	Slim            bool
	ActiveTools     int
	ActiveSubagents int
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
	Models         []string
	FallbackModels []string
	MaxContext     int
	Protocol       string
	HasAPIKey      bool
	Status         string // "ready" | "no-key" | "offline"
	IsPlaceholder  bool
}

type StatsPanelInfo struct {
	Mode                  StatsPanelMode
	Provider              string
	Model                 string
	Configured            bool
	ContextTokens         int
	MaxContext            int
	Streaming             bool
	AgentActive           bool
	AgentPhase            string
	AgentStep             int
	AgentMaxSteps         int
	ToolRounds            int
	LastTool              string
	LastStatus            string
	LastDurationMs        int
	Parked                bool
	QueuedCount           int
	PendingNotes          int
	ToolsEnabled          bool
	ToolCount             int
	Branch                string
	Dirty                 bool
	Detached              bool
	Inserted              int
	Deleted               int
	SessionElapsed        time.Duration
	MessageCount          int
	Pinned                string
	CompressionSavedChars int
	CompressionRawChars   int
	TodoTotal             int
	TodoPending           int
	TodoDoing             int
	TodoDone              int
	TodoActive            string
	TodoLines             []string
	TaskLines             []string
	TaskTreeLines         []string
	WorkflowStatus        string
	WorkflowMeter         string
	WorkflowExecution     string
	WorkflowTimeline      []string
	WorkflowRecent        []string
	Boosted               bool
	BoostSeconds          int
	FocusLocked           bool
	SubagentLines         []string
	ActiveSubagents       int
	DriveRunID            string
	DriveDone             int
	DriveTotal            int
	DriveBlocked          int
	PlanSubtasks          int
	PlanParallel          bool
	PlanConfidence        float64
	SpinnerFrame          int
	Providers             []ProviderPanelRow
	ProvidersSelectedIndex int // cursor position in the providers list
}
