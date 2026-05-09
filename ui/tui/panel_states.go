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
//
// Sibling state files (split out 2026-05-07 because this file used to
// host every Model sub-struct, panels and not):
//
//   chat_state.go    — chatState (composer/transcript/stream/queue),
//                      pasteBlock, assistantNextActionsState,
//                      composeInput method, intentState, slashMenuState,
//                      commandPickerState, inputHistoryState.
//   runtime_state.go — agentLoopState, sessionTelemetry,
//                      subagentRuntimeItem, statsPanelMode + consts,
//                      uiToggles.
//   view_state.go    — tasksPanelState, patchViewState, filesViewState,
//                      toolViewState, workflowPanelState,
//                      activityPanelState.

package tui

import (
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/planning"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/security"
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
	// provider CRUD state
	newProviderDraft string // name buffer when adding a new provider
	// profile field editor state
	profileEditMode  bool
	profileEditField int // 0=protocol, 1=base_url, 2=max_context, 3=max_tokens
	profileEditDraft string
	// sync state
	syncing      bool
	lastSyncedAt time.Time
	// action menu state — replaces single-key shortcuts with Enter-activated menus
	menuActive          bool
	menuLabels          []string
	menuActions         []string
	menuDisabled        []bool
	menuDisabledReasons []string
	menuIndex           int
	// search state — filters the provider list by name, model, or status
	query        string
	searchActive bool
	// confirm state — destructive actions ask for y/n before executing
	confirmAction string // e.g. "delete_provider", "delete_model", "delete_pipeline"
	confirmTarget string // name of the thing being deleted
	// loaded guard — refreshProvidersRows is idempotent so we gate on
	// first activation rather than re-reading on every ctrl+o press.
	loaded bool
	// probeResults caches the most recent test-connection result per
	// provider name (Phase I item 1). Lookup is lower-cased to match
	// router normalisation. Nil-safe — `T` allocates on first use.
	probeResults map[string]engine.ProviderProbeResult
	probing      map[string]bool // names with an in-flight probe
	// usageHistory is a bounded ring buffer of provider:complete events
	// per provider name (Phase I item 2). Newest at the end so the
	// detail panel can render last-N descending; capped at
	// providerUsageHistoryCap so steady-state memory stays flat across
	// long sessions.
	usageHistory map[string][]providerUsageEntry
}

// providerUsageEntry captures one provider:complete event so the
// Providers panel can render a per-provider history strip. Tokens come
// straight off the event payload; At lets the renderer show "12s ago".
type providerUsageEntry struct {
	At           time.Time
	Provider     string
	Model        string
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// providerUsageHistoryCap bounds the ring buffer size per provider so
// long sessions don't accumulate unbounded history. Tuned for a
// week-long session at one ask/minute (≈10k events) — far above the
// realistic edit-and-see-the-last-N use case.
const providerUsageHistoryCap = 50

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
	statusPanel   statusPanelState
	orchestrate   scrollOnlyPanelState
	shortcuts     scrollOnlyPanelState
	providerLog   scrollOnlyPanelState
	// helpOverlay scroll is shared by the Ctrl+H overlay rendered on
	// non-Chat tabs (the Chat-tab inline widget has its own scroll
	// behaviour). j/k/pgup/pgdn/g/G adjust it via handleHelpOverlayKey.
	// telegram panel state — accessed via Shift+F8 or Ctrl+B → Telegram.
	telegram telegramPanelState
	helpOverlay scrollOnlyPanelState
}

// scrollOnlyPanelState is the state shared by read-only reference
// overlays (Orchestrate, Shortcuts) that need nothing more than a
// vertical scroll offset. They have no selectable rows, no action menu,
// and no editing — just a long body the user pages through with j/k,
// pgup/pgdn, g/G. Kept tiny on purpose so adding another such panel is
// cheap.
type scrollOnlyPanelState struct {
	scroll int
}

// statusPanelState — arrow-key navigation state for the Status (F2)
// tab's card grid. Each card has an index; arrow keys move the
// selection, Enter on a selected card jumps to the related detail
// panel (Provider card → Providers tab, AST card → CodeMap tab, etc.)
// or runs an action. Reset implicitly when the panel re-renders.
type statusPanelState struct {
	selectedCard int
	// cardCount is updated by the renderer so navigation knows how
	// many cards are live (depends on optional sections like
	// Memory-degraded or Context-In appearing only when populated).
	cardCount int
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
		// Phase H item 2 — default to Working tier (the recent
		// scratchpad), not the merged All view. Rationale per Section
		// 2.8: users mostly care about "what was just remembered" on
		// open; episodic / semantic are reachable via `t` cycle (or
		// the action menu) when needed.
		d.memory.tier = string(types.MemoryWorking)
	}
	if d.codemap.view == "" {
		d.codemap.view = codemapViewOverview
	}
	if d.codemap.visualExpanded == nil {
		d.codemap.visualExpanded = make(map[string]bool)
	}
	if d.security.view == "" {
		d.security.view = securityViewSecrets
	}
	if d.security.ignored == nil {
		d.security.ignored = map[string]bool{}
	}
}

// contextPanelState — diagnostic view over Engine.ContextBudgetPreview /
// ContextRecommendations. Lets the user inspect a query's token budget
// without firing an Ask.
type contextPanelState struct {
	query       string
	preview     *engine.ContextBudgetInfo
	breakdown   *engine.ContextBreakdown
	hints       []engine.ContextRecommendation
	active      *engine.ContextDebugStatus
	showActive  bool
	scroll      int
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
	snap           codemapSnapshot
	view           string
	scroll         int
	loading        bool
	err            string
	loaded         bool
	visualExpanded map[string]bool // nodeID -> expanded
	visualCursor   int             // current line in the flattened visual tree
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
	// expandedID is the entry whose full multi-line value is rendered
	// inline below the row. Empty = collapsed (one-line per row, the
	// default). Phase H item 3 — per-entry detail expand. Enter on a
	// row sets/clears this field.
	expandedID string
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
	// ignored is the set of finding fingerprints the user has marked
	// "known acceptable risk" — those rows render with a muted IGN
	// chip and drop out of the unfiltered count. The fingerprint is a
	// stable hash of `kind|file|line|rule` so re-running the scan after
	// a code reshuffle correctly forgets ignores that no longer match.
	// Phase J item 1 — whitelist / ignore mechanism.
	ignored map[string]bool
}

// conversationsPanelState — read view over the JSONL-persisted conversation
// store. The preview pane holds the first few messages of the currently
// highlighted entry; it's lazy-loaded on enter.
//
// deepSearchQuery: when non-empty, `entries` are the results of an
// engine-side full-text search across message bodies (not the ID/
// provider/model substring filter that lives client-side as `query`).
// Phase G item 1 — Conversations full-text search.
type conversationsPanelState struct {
	entries          []conversation.Summary
	scroll           int
	query            string
	loading          bool
	err              string
	searchActive     bool
	loaded           bool
	preview          []types.Message
	previewID        string
	deepSearchQuery  string
	deepSearchActive bool
	// previewBranches is the branch list of the currently-previewed
	// conversation. Empty when the conversation only has the default
	// `main` branch. Phase G item 3 — branch tree visualization.
	previewBranches      []conversationBranchSummary
	previewActiveBranch  string
}
