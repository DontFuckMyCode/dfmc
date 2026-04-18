// Sub-struct definitions for the read-only diagnostic panels on the TUI
// model. The model used to carry every panel's state as flat m.<panel>*
// fields — fine when there were two panels, painful at eight. Each panel
// owns its own little state machine (entries / scroll / query / loading
// flags / err / loaded sentinel), so grouping them into per-panel structs
// keeps Model declarations scannable and stops auto-complete from drowning
// the unrelated panels in noise.
//
// Naming convention: lowercase struct fields (panel-internal), one named
// field on Model per panel (m.providers, m.codemap, etc.). The rename is
// purely structural — value semantics are preserved, every field still
// belongs to one Model copy, no pointer aliasing.

package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/planning"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/security"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// providersPanelState — diagnostic view over the provider router. Rows
// are cached (refresh on 'r' or first tab activation) because Hints() is
// cheap but there's no point redoing the walk on every keystroke.
type providersPanelState struct {
	rows   []providerRow
	scroll int
	err    string
}

// contextPanelState — diagnostic view over Engine.ContextBudgetPreview /
// ContextRecommendations. Lets the user inspect a query's token budget
// without firing an Ask.
type contextPanelState struct {
	query       string
	preview     *engine.ContextBudgetInfo
	hints       []engine.ContextRecommendation
	err         string
	inputActive bool
}

// plansPanelState — diagnostic view over internal/planning.SplitTask.
// Decomposition runs locally on enter; no engine round-trip.
type plansPanelState struct {
	query       string
	plan        *planning.Plan
	scroll      int
	err         string
	inputActive bool
}

// codemapPanelState — snapshot of the symbol/dep graph from internal/codemap.
// View rotates overview/hotspots/orphans/cycles.
type codemapPanelState struct {
	snap    codemapSnapshot
	view    string
	scroll  int
	loading bool
	err     string
	loaded  bool
}

// memoryPanelState — read view over internal/memory. Tier is a string
// (not MemoryTier) so "all" can park alongside real values.
type memoryPanelState struct {
	entries      []types.MemoryEntry
	scroll       int
	tier         string
	query        string
	loading      bool
	err          string
	searchActive bool
}

// promptsPanelState — read view over the merged promptlib catalog (defaults
// + ~/.dfmc/prompts + .dfmc/prompts). Preview is rendered inline when the
// user hits enter on a row.
type promptsPanelState struct {
	templates    []promptlib.Template
	scroll       int
	query        string
	loading      bool
	err          string
	searchActive bool
	loaded       bool
	previewID    string
}

// securityPanelState — findings from internal/security.Scanner. Scans are
// manual (r) because I/O is noticeable on large trees; the view toggle
// flips between secrets and vulnerabilities.
type securityPanelState struct {
	report       *security.Report
	view         string
	scroll       int
	query        string
	loading      bool
	err          string
	searchActive bool
	loaded       bool
}

// conversationsPanelState — read view over the JSONL-persisted conversation
// store. The preview pane holds the first few messages of the currently
// highlighted entry; it's lazy-loaded on enter.
type conversationsPanelState struct {
	entries      []conversation.Summary
	scroll       int
	query        string
	loading      bool
	err          string
	searchActive bool
	loaded       bool
	preview      []types.Message
	previewID    string
}

// chatState — the chat tab's hot path: composer state, transcript,
// in-flight stream lifecycle, and the FIFO of queued submissions /btw
// notes that arrive while the engine is busy. The fields cluster into
// three loose groups that all live here because they're touched together
// on every keystroke and stream event:
//
//   • composer    — input, cursor, cursorManual, cursorInput
//   • stream      — sending, streamIndex/Messages/Cancel/StartedAt,
//                   userCancelledStream, spinnerFrame/Ticking, scrollback
//   • queue/tools — pendingQueue, pendingNoteCount, toolPending, toolName
//
// `transcript` is the rendered history; `scrollback` is how far PageUp
// has scrolled us back from the tail (0 = pinned to latest).
type chatState struct {
	transcript          []chatLine
	input               string
	cursor              int
	cursorManual        bool
	cursorInput         string
	sending             bool
	streamIndex         int
	streamMessages      <-chan tea.Msg
	streamCancel        context.CancelFunc
	userCancelledStream bool
	pendingQueue        []string
	pendingNoteCount    int
	streamStartedAt     time.Time
	spinnerFrame        int
	spinnerTicking      bool
	scrollback          int
	toolPending         bool
	toolName            string
}

// intentState — most recent decision from the engine's intent router,
// plus a verbose flag that controls whether enrichments surface in the
// chat transcript as gray "you said X / agent saw Y" pairs. The chip
// in the chat header reads from active+source; the slash command
// /intent show prints the full last decision via Recent.
//
// Engine publishes "intent:decision" events via EventBus; the TUI
// handler pulls fields out of the payload and assigns into this
// struct. Empty struct = "intent layer hasn't fired yet this session".
type intentState struct {
	verbose          bool   // /intent verbose toggles transcript pairs
	lastIntent       string // "resume" | "new" | "clarify" | ""
	lastSource       string // "llm" | "fallback" | ""
	lastRaw          string
	lastEnriched     string
	lastReasoning    string
	lastFollowUp     string
	lastLatencyMs    int64
	lastDecisionAtMs int64 // Unix millis; 0 when never fired
}

// uiToggles — runtime UI flags driven by /slash commands and ctrl-key
// shortcuts. These are independent on/off knobs that don't share state,
// but grouping them keeps the Model declaration from being half-flooded
// with bools whose only role is "is this overlay visible?".
type uiToggles struct {
	showHelpOverlay     bool // ctrl+h: keybinding card overlay
	showStatsPanel      bool // ctrl+s: right-side stats panel on chat tab
	keyLogEnabled       bool // /keylog or DFMC_KEYLOG=1: dump KeyMsg into notice
	planMode            bool // /plan: investigate-only agent loop
	resumePromptActive  bool // agent:loop:parked: show "press enter to resume"
	coachMuted          bool // /coach: hide coach:note transcript lines
	hintsVerbose        bool // /hints: surface model-facing trajectory hints
	mouseCaptureEnabled bool // /mouse: cell-motion mouse tracking on/off
}

// activityPanelState — Activity tab state. `entries` is the timestamped
// firehose fed by every engine event; `follow` pins the view to the tail
// (any manual scroll unpins it).
type activityPanelState struct {
	entries []activityEntry
	scroll  int
	follow  bool
}

// sessionTelemetry — running counters surfaced by the chat header chips
// and the stats panel. Compression* aggregate every tool:result event so
// we can show "rtk saved N chars (M%)" without rewalking the timeline;
// active*Count track in-flight fan-out (incremented on tool:call /
// agent:subagent:start, decremented on the matching done event).
type sessionTelemetry struct {
	compressionSavedChars int
	compressionRawChars   int
	activeToolCount       int
	activeSubagentCount   int
}

// patchViewState — Patch tab state plus the workspace-diff snapshot that
// feeds it. `diff`/`changed` mirror `git diff` output (refreshed by the
// workspace loader); `latestPatch` is the most recent patch the assistant
// emitted; `set`/`files`/`index`/`hunk` are the parsed view we paginate
// through with [/]-keys.
type patchViewState struct {
	diff        string
	changed     []string
	latestPatch string
	set         []patchSection
	files       []string
	index       int
	hunk        int
}

// filesViewState — Files tab state. `entries` is the directory listing,
// `index` the cursor row, `pinned` a sticky selection that survives
// re-loads, and `path/preview/size` the currently shown file.
type filesViewState struct {
	entries []string
	index   int
	pinned  string
	preview string
	path    string
	size    int
}

// toolViewState — Tools tab cursor position, current output for the
// selected tool, and the in-place editor (editing flag, draft buffer,
// per-key overrides) used to tweak parameters before re-running.
type toolViewState struct {
	index     int
	output    string
	editing   bool
	draft     string
	overrides map[string]string
}

// inputHistoryState — chat composer command history (up/down recall),
// plus the in-progress draft we stash before navigating into history so
// pressing down past the newest entry restores what the user was typing.
type inputHistoryState struct {
	history []string
	index   int
	draft   string
}

// setupWizardState — first-run configuration wizard cursor + draft buffer.
// Editing flips on while the user is typing into a field.
type setupWizardState struct {
	index   int
	editing bool
	draft   string
}

// slashMenuState — composer popup indices for the four completion menus
// (slash command, slash argument, file mention, quick action). Each is
// the highlighted-row index inside the corresponding rendered list.
type slashMenuState struct {
	command     int
	commandArg  int
	mention     int
	quickAction int
}

// commandPickerState — modal chooser state for slash commands that need
// an interactive selection (provider/model/skill). Active flips on while
// the picker is open and pins keyboard focus to the picker handler.
type commandPickerState struct {
	active  bool
	kind    string
	query   string
	index   int
	persist bool
	all     []commandPickerItem
}

// agentLoopState aggregates everything the TUI knows about the currently
// running native tool loop — surface used by the chat header chips, the
// stats panel, and the per-step toolTimeline chip strip. Engine events
// (agent:loop:*, tool:*) are the only writers; renderers read it.
//
// Lives off Model so the 13 separate flat fields don't drown unrelated
// state in the Model declaration.
type agentLoopState struct {
	active        bool
	step          int
	maxToolStep   int
	toolRounds    int
	phase         string
	provider      string
	model         string
	lastTool      string
	lastStatus    string
	lastDuration  int
	lastOutput    string
	contextScope  string
	toolTimeline  []toolChip
}
