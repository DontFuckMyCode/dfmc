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
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/planning"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/security"
	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// providersPanelState — diagnostic view over the provider router. Rows
// are cached (refresh on first tab activation or menu action) because Hints()
// is cheap but there's no point redoing the walk on every keystroke.
type providersPanelState struct {
	rows          []providerRow
	scroll        int
	err           string
	selectedIndex int    // cursor position in the providers list
	editMode      string // "" | "model" | "fallback"
	modelEditIdx  int    // index into the selected profile's Models list when editMode == "model"
	fallbackIdx   int    // index into the selected profile's FallbackModels when editMode == "fallback"
	viewMode      string // "list" | "detail" | "pipelines"
	// detail state
	detailProvider string // which provider is being viewed in detail mode
	pipelineScroll int
	pipelineNames  []string
	activePipeline string
	// pipeline editor state
	pipelineEditMode   bool
	pipelineDraftName  string
	pipelineDraftSteps []config.PipelineStep
	pipelineEditStep   int // which step is selected (-1 = name field)
	pipelineEditField  int // 0=provider, 1=model (within selected step)
	pipelineDraftBuf   string
	// provider detail model picker state
	modelPickerActive bool
	modelPickerItems  []string
	modelPickerIndex  int
	modelPickerManual bool
	modelPickerDraft  string
	modelListScroll   int // scroll offset for long model lists in detail view
	// provider CRUD state
	confirmDelete    string // provider name awaiting delete confirmation
	newProviderDraft string // name buffer when adding a new provider
	// profile field editor state
	profileEditMode  bool
	profileEditField int    // 0=protocol, 1=base_url, 2=max_context, 3=max_tokens
	profileEditDraft string
	// sync state
	syncing bool
	// action menu state — replaces single-key shortcuts with Enter-activated menus
	menuActive   bool
	menuLabels   []string
	menuActions  []string
	menuDisabled []bool
	menuIndex    int
}

// diagnosticPanelsState groups the cold, mostly read-only diagnostic tabs.
// These views are loaded on demand and mutated far less often than the chat
// composer / stream path, so keeping them bundled behind one embedded struct
// makes the hot fields on Model easier to scan and reason about.
type diagnosticPanelsState struct {
	memory        memoryPanelState
	codemap       codemapPanelState
	conversations conversationsPanelState
	prompts       promptsPanelState
	security      securityPanelState
	plans         plansPanelState
	contextPanel  contextPanelState
	providers     providersPanelState
}

func newDiagnosticPanelsState() *diagnosticPanelsState {
	state := &diagnosticPanelsState{}
	state.applyDefaults()
	return state
}

func (d *diagnosticPanelsState) applyDefaults() {
	if d == nil {
		return
	}
	if d.memory.tier == "" {
		d.memory.tier = memoryTierAll
	}
	if d.codemap.view == "" {
		d.codemap.view = codemapViewOverview
	}
	if d.security.view == "" {
		d.security.view = securityViewSecrets
	}
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
//   - composer    — input, cursor, cursorManual, cursorInput
//   - stream      — sending, streamIndex/Messages/Cancel/StartedAt,
//     userCancelledStream, spinnerFrame/Ticking, scrollback
//   - queue/tools — pendingQueue, pendingNoteCount, toolPending, toolName
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
	// pasteBlocks holds multi-line paste segments. Each block stores the
	// original content and a compact display placeholder so the composer
	// shows "[pasted text #N +L lines]" instead of 500 raw lines.
	// Backspace at a block boundary deletes the whole block.
	// Enter submits all blocks + any regular text as one message.
	pasteBlocks    []pasteBlock
	pasteWindowEnd time.Time // if set, paste chunks arriving before this time accumulate into current block
}

// pasteBlock represents one multi-line paste operation.
type pasteBlock struct {
	content   string // original pasted text (newlines preserved)
	blockNum  int    // 1-based sequence number
	lineCount int    // number of lines in the content
}

// composeInput reconstructs the full submission text from all paste blocks
// and the visible composer text. Paste block placeholders are replaced
// with the original content.
func (m Model) composeInput() string {
	var full strings.Builder
	// Reconstruct from stored blocks + visible input
	blocks := m.chat.pasteBlocks
	if len(blocks) == 0 {
		return m.chat.input
	}
	// The visible m.chat.input contains placeholders like
	// "[pasted text #1 +3 lines]" interleaved with regular typed text.
	// We reconstruct by scanning the input left-to-right and substituting.
	rest := m.chat.input
	for len(rest) > 0 {
		matched := false
		for _, b := range blocks {
			placeholder := b.placeholder()
			if strings.HasPrefix(rest, placeholder) {
				full.WriteString(b.content)
				rest = rest[len(placeholder):]
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		// No placeholder match — take one character
		full.WriteByte(rest[0])
		rest = rest[1:]
	}
	return full.String()
}

// placeholder returns the compact display string for this block.
func (b pasteBlock) placeholder() string {
	return fmt.Sprintf("[pasted text #%d +%d lines]", b.blockNum, b.lineCount)
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
	showHelpOverlay       bool // ctrl+h: keybinding card overlay
	showStatsPanel        bool // ctrl+s: right-side stats panel on chat tab
	statsPanelMode        statsPanelMode
	statsPanelBoostUntil  time.Time
	statsPanelFocusLocked bool
	keyLogEnabled         bool // /keylog or DFMC_KEYLOG=1: dump KeyMsg into notice
	planMode              bool // /plan: investigate-only agent loop
	resumePromptActive    bool // agent:loop:parked: show "press enter to resume"
	coachMuted            bool // /coach: hide coach:note transcript lines
	hintsVerbose          bool // /hints: surface model-facing trajectory hints
	mouseCaptureEnabled   bool // /mouse: cell-motion mouse tracking on/off
	selectionModeActive   bool // /select or alt+x: chat-only selection layout
	selectionRestoreStats bool // previous showStatsPanel before selection mode
	selectionRestoreMouse bool // previous mouseCaptureEnabled before selection mode
	// toolStripExpanded controls whether the per-message tool-call
	// strip renders as a one-line summary table (default, false) or as
	// the full per-call chip block (when true). Default is collapsed
	// because long sessions otherwise drown the actual answer in tool
	// noise — the user can /tools (or ctrl+y) to flip it for a session
	// when they want the breakdown.
	toolStripExpanded bool
}

type statsPanelMode string

const (
	statsPanelModeOverview  statsPanelMode = "overview"
	statsPanelModeTodos     statsPanelMode = "todos"
	statsPanelModeTasks     statsPanelMode = "tasks"
	statsPanelModeSubagents statsPanelMode = "subagents"
	statsPanelModeProviders statsPanelMode = "providers"
)

// activityPanelState — Activity tab state. `entries` is the timestamped
// firehose fed by every engine event; `follow` pins the view to the tail
// (any manual scroll unpins it).
type activityPanelState struct {
	entries      []activityEntry
	scroll       int
	follow       bool
	mode         activityViewMode
	query        string
	searchActive bool
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

	// drive* fields track the most recently active drive run so the
	// chat header can show "▸ drive 3/12 · T5" while the run is in
	// flight. Reset on drive:run:done/stopped/failed so the chip
	// disappears when the run is over. Concurrent drive runs are
	// uncommon; the last one to publish "wins" the chip — tracking
	// only the latest matches what users actually want to see.
	driveRunID   string
	driveTodoID  string
	driveDone    int
	driveTotal   int
	driveBlocked int
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
	active       bool
	step         int
	maxToolStep  int
	toolRounds   int
	phase        string
	provider     string
	model        string
	lastTool     string
	lastStatus   string
	lastDuration int
	lastOutput   string
	contextScope string
	toolTimeline []toolChip
}

// workflowPanelState — Drive TODO tree panel state for the Workflow tab.
// Tracks the list of drive runs, which run is selected, scroll position,
// and which TODO nodes are expanded to show their detail.
type workflowPanelState struct {
	runs               []*drive.Run // from drive store List(), refreshed on events
	selectedRunID      string        // empty = show run selector; set = show TODO tree
	scrollY            int           // vertical scroll offset in the TODO tree
	expandedTodo       map[string]bool
	selectedIndex      int // index in run selector list when no run selected
	selectedTodoID     string        // ID of the TODO whose detail is shown
	// routingEditor controls the drive.Config.Routing editor overlay.
	showRoutingEditor  bool            // true = overlay open
	routingEditTag     string          // tag being edited (empty = new entry)
	routingEditProfile string          // profile name being edited
	routingEditIndex   int             // which row is selected in the routing list
	routingEditMode    bool            // true = currently editing the profile field
	routingDraft       map[string]string // routing entries in the editor (tag -> profile)
}
