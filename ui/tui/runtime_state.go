package tui

// runtime_state.go — agent loop / session telemetry / sub-agent
// runtime / UI toggles. All Model state that isn't a chat-tab field
// (chat_state.go) and isn't a diagnostic-panel field (panel_states.go)
// and isn't a side-tab view-state field (view_state.go) lives here.
//
// agentLoopState is the largest member — its job is to mirror engine
// agent:loop:* / tool:* events so the runtime card, runtime strip, and
// stats panel can read aggregated turn-level state without rewalking
// the activity timeline.

import (
	"time"
)

// uiToggles — runtime UI flags driven by /slash commands and ctrl-key
// shortcuts. These are independent on/off knobs that don't share state,
// but grouping them keeps the Model declaration from being half-flooded
// with bools whose only role is "is this overlay visible?".
type uiToggles struct {
	showHelpOverlay       bool // ctrl+h: keybinding card overlay
	showStatsPanel        bool // ctrl+s: right-side stats panel on chat tab
	statsPanelMode        statsPanelMode
	statsPanelScroll      int // scroll offset for the right-hand stats panel
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
	// strip renders as a one-line summary table (when false) or as
	// the full per-call chip block (default, true). Default is expanded
	// because seeing which tools fired and their outcomes is essential
	// for trust and transparency — the user can /tools (or ctrl+y) to
	// flip it when they want a quieter view.
	toolStripExpanded bool
	showTasksPanel    bool // /tasks: floating tasks panel on chat tab
	// panelOverlayKind names a demoted-panel overlay covering the active
	// tab body. Empty = no overlay. One of: status, tools, codemap,
	// prompts, security, plans, context, orchestrate, shortcuts. The
	// 17-tab strip was reduced to 8 first-class tabs; the rest are now
	// reachable through slash commands and F-keys that flip this flag.
	// Esc clears it. The active tab itself is unchanged so a chat-tab
	// user gets back to chat after closing the overlay.
	panelOverlayKind string
}

type statsPanelMode string

const (
	statsPanelModeOverview  statsPanelMode = "overview"
	statsPanelModeTodos     statsPanelMode = "todos"
	statsPanelModeTasks     statsPanelMode = "tasks"
	statsPanelModeSubagents statsPanelMode = "subagents"
	statsPanelModeProviders statsPanelMode = "providers"
)

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
	lastInputTokens       int
	lastOutputTokens      int
	lastTotalTokens       int
	sessionInputTokens    int
	sessionOutputTokens   int
	sessionTotalTokens    int
	subagents             map[string]subagentRuntimeItem
	subagentOrder         []string

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

type subagentRuntimeItem struct {
	Key        string
	Task       string
	Role       string
	Status     string
	Provider   string
	Model      string
	Candidates []string
	Tried      []string
	Attempt    int
	Attempts   int
	Rounds     int
	DurationMs int
	Fallback   bool
	Error      string
	LastReason string
	StartedAt  time.Time
	UpdatedAt  time.Time
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
	// lastToolReason is the model's `_reason` field on the most recent
	// tool call — its self-narration of WHY this call. Surfaced in the
	// runtime "now" strip as "→ thinking: <reason>" so a user watching
	// a long autonomous run can see the model's current intent at a
	// glance without scrolling through chips. Cleared on each new
	// tool:call (previous intent is now stale); refreshed on each
	// tool:reasoning event.
	lastToolReason string
	toolTimeline   []toolChip
	// sessionCoachNotes accumulates coach:note text during the current round for
	// runtime visibility and test assertions.
	sessionCoachNotes []string
	// stuck* — last seen `agent:coach:stuck` payload. Powers the warn
	// badge in the runtime "now" strip so a multi-hour autonomous run
	// surfaces "stalled: <tool> ×N" at a glance until the next
	// successful tool clears it. Empty stuckTool means no current stall.
	stuckTool      string
	stuckCount     int
	stuckErrClass  string
	stuckClearedAt int // step number where the stall was last cleared (0=never)
	stuckNoticeKey string
	stuckNoticeAt  int
	// cumulative* — running totals across auto-resume cycles within a
	// single root ask. The autonomous wrapper accumulates these on
	// every park→compact→resume transition, and the engine refuses
	// further resumes when cumulativeSteps >= stepCeiling (or tokens
	// hit tokenCeiling). Surfacing them in the runtime strip lets the
	// user see "I'm 240/600 cumulative steps in" at a glance during a
	// multi-hour run instead of having to read auto-resume chips that
	// scroll out of view. Reset on agent:loop:start (fresh ask).
	cumulativeSteps  int
	stepCeiling      int
	cumulativeTokens int
	tokenCeiling     int

	// unvalidatedEdits tracks files mutated since the last successful
	// build/test/vet command. The trajectory layer already nudges the
	// model with a "validate this" hint per turn, but never escalates
	// when the unvalidated count keeps climbing across rounds. The TUI
	// surface compensates: a warn badge "unverified: N edits" lights up
	// from the third edit onward so a multi-hour run that has been
	// happily editing for 20 rounds without a single test pass becomes
	// visually obvious. Cleared by successful build/test/vet, reset on
	// agent:loop:start.
	unvalidatedEdits []string
	// unvalidatedSinceStep records the step where the first edit in the
	// current unvalidated batch landed; useful for "edited 5 files
	// across the last 12 steps" signals if we ever surface duration.
	unvalidatedSinceStep int

	// Turn-scoped accumulators powering the on-final summary card.
	// `unvalidated*` above is a LIVE state that clears on validation;
	// these survive validation passes and only reset on agent:loop:start
	// because their job is "what did this whole turn touch?", not
	// "what's still unverified right now". Together they answer the
	// "what did it actually do for the last 2 hours?" question that the
	// chip ribbon can't because chips scroll out of view.
	turnStartedAt          time.Time
	turnEditedFiles        []string
	turnValidationPasses   int
	turnCoachInterventions int
	// Live in-loop conversation footprint as reported by the engine on
	// agent:loop:thinking. The CONTEXT panel only refreshes once per
	// Ask (on context:built) so it shows "what WOULD be sent if you
	// asked now" — the static value, not the actively-growing one.
	// During a long autonomous loop the user wants to see how close
	// the working context is to the budget; this field powers a
	// "loop ~47k/250k" indicator in the runtime strip that keeps
	// updating round-by-round.
	liveLoopTokens    int
	liveLoopBudgetCap int // engine's max_tool_tokens (live working-set ceiling)

	// headroomThresholdsHit tracks WHICH context-fill % bands the
	// current turn has already crossed (70/85/95). Used to dedupe the
	// chat-event "context X% full · auto-compact pending" notifications
	// so a long loop ticking over 70% repeatedly only fires the warning
	// ONCE per turn per band. Reset on agent:loop:start.
	// Bit 0 = 70%, bit 1 = 85%, bit 2 = 95%.
	headroomThresholdsHit uint8

	// compactsThisTurn counts how many times the engine has run an
	// auto-compact cycle during the CURRENT turn (reactive + proactive
	// combined). A turn that needed multiple compacts is fighting hard
	// for budget — the runtime card shows a "compacts ×N" badge so
	// the user can spot a loop that's barely keeping its head above
	// water without watching the activity feed event-by-event. Reset
	// on agent:loop:start.
	compactsThisTurn     int
	compactReclaimedTurn int // cumulative tokens reclaimed by compacts this turn

	// cacheHitsThisTurn counts how many sub-agent / parallel-tool cache
	// hits the current turn served. A cache hit is a tool call that the
	// engine answered from a prior result without re-running the tool —
	// silent token savings the user wouldn't otherwise see (no
	// tool:call → no chip → no signal). Surfaced as "cache ×N" in the
	// runtime strip so a savings-heavy turn registers visibly. Reset
	// on agent:loop:start.
	cacheHitsThisTurn int

	// toolErrorsThisTurn counts how many tool:result events arrived
	// with success=false during the current turn. Recovery happens
	// silently (the model retries or pivots), so a turn that failed 8
	// tool calls and limped to a final answer leaves no trace once
	// the chips scroll out. The end-of-turn summary card surfaces this
	// as "errors: N recovered" so retry-heavy turns are visible in
	// scrollback. Counts every failed result including timeouts and
	// denials — the goal is to capture turn-level fragility, not to
	// taxonomize causes. Reset on agent:loop:start.
	toolErrorsThisTurn int

	// Transcript-noise guards. Activity still receives every raw event, but
	// chat history should not fill with the same operational warning every
	// round during a long run.
	toolForceStopNotified    bool
	unverifiedCoachLastCount int
}
