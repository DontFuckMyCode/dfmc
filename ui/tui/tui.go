package tui

// tui.go — bubbletea package entry: Options + the Model struct + the
// mouse/scroll/queue constants. NewModel constructor + ensureDiagnostics
// defaults + Init command batch + projectRoot accessor + the View
// renderer live in tui_lifecycle.go. Lifecycle (Run + panic guard) lives
// in tui_run.go; small types and helpers (chatRole, coachSeverity,
// paramStr, chatLine, patchSection, picker items, suggestion state,
// viewCacheState) live in tui_types.go.

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

type Options struct {
	AltScreen bool
}

// pendingQueueCap bounds how many "while-streaming" messages the user
// can stack up. Without a cap, holding Enter while a long reply is in
// flight grows []string memory unboundedly — a cheap DOS / oops vector
// when somebody walks away from the keyboard with a key held. 64 is
// enough for "ask three follow-ups in a row" without becoming a leak.
const pendingQueueCap = 64

// mouseWheelStep is the per-tick scroll distance for the chat transcript.
// 5 lines feels responsive on a standard mouse without overshooting on a
// high-DPI wheel; the input box stays pinned because fitChatBody clips
// only the head, never the tail. mouseWheelPageStep applies on shift+wheel
// for half-page jumps when traveling a long history.
const (
	mouseWheelStep     = 5
	mouseWheelPageStep = 15
)

// scrollPageStep is the number of transcript lines PgUp/PgDown scrolls.
// 8 is one "page" in a typical terminal after header+input are accounted
// for — enough to see one full screen of history without being jarring.
const scrollPageStep = 8

// scrollFineStep is the finer scroll for Shift+PgUp/PgDown or Shift+Up/Down.
// Matches the mouse wheel single-tick step so keyboard and wheel feel consistent.
const scrollFineStep = 3

type Model struct {
	ctx                context.Context
	eng                *engine.Engine
	eventRelayExternal bool

	width  int
	height int

	tabs      []string
	activeTab int

	status engine.Status

	// Chat tab hot path — composer + transcript + in-flight stream
	// lifecycle + the FIFO of queued submissions and /btw notes that
	// arrive while the engine is busy. See chatState in panel_states.go
	// for the field-by-field rationale.
	chat chatState

	// program is the live bubbletea program handle. Populated by Run()
	// so runtime commands (e.g. /mouse) can call EnableMouse /
	// DisableMouse without re-constructing the program.
	program *tea.Program

	// Workspace metadata painted into the status line. gitInfo is refreshed
	// asynchronously via loadGitInfoCmd so the UI never blocks on shell-out;
	// sessionStart is captured when NewModel runs and drives the session
	// timer chip.
	gitInfo      gitWorkspaceInfo
	sessionStart time.Time
	// Runtime UI flags driven by /slash commands and ctrl-key shortcuts —
	// overlays, panels, mode toggles, key-log debug, etc. See uiToggles in
	// panel_states.go for the per-flag commentary.
	ui uiToggles

	// Patch tab state + workspace-diff snapshot. See patchViewState in
	// panel_states.go. Workspace loader writes diff/changed; parser writes
	// latestPatch/set/files; the [/] keys move index/hunk.
	patchView patchViewState
	workflow  workflowPanelState
	// Files tab state (entries list, cursor, sticky pin, preview pane).
	// See filesViewState in panel_states.go.
	filesView filesViewState

	// actionMenu is the shared list-panel action picker. Any panel
	// that wants to expose secondary actions (pin/explain/reload/etc.)
	// without forcing the user to memorise single-letter shortcuts
	// opens it via openActionMenu(...) when Right/Enter is pressed on
	// a row. Arrows + enter + esc drive it. See panel_action_menu.go.
	actionMenu panelActionMenu

	// Tools-tab state (cursor position, current output, in-place editor
	// for param overrides). See toolViewState in panel_states.go.
	toolView toolViewState

	// Composer popup indices and chat history. See slashMenuState +
	// inputHistoryState in panel_states.go.
	slashMenu    slashMenuState
	inputHistory inputHistoryState

	// Modal command picker (provider/model/skill chooser invoked from the
	// chat composer when a slash command needs an interactive selection
	// instead of a positional arg). See commandPickerState in panel_states.go.
	commandPicker commandPickerState

	eventSub    chan engine.Event
	activityLog []string

	// Activity panel state — a timestamped firehose fed by every engine
	// event (not the filtered shouldLogActivity gate). See activityPanelState
	// in panel_states.go.
	activity activityPanelState

	// All cold, diagnostic panel state is grouped into one embedded bundle.
	// Chat / stream / activity remain top-level because they are the hot path;
	// diagnostic tabs load lazily and are touched far less often.
	*diagnosticPanelsState

	// Native tool-loop telemetry surfaced by the chat header chips, the
	// stats panel, and the per-step toolTimeline strip. See agentLoopState
	// in panel_states.go — engine events are the only writers.
	agentLoop   agentLoopState
	toolCallLog toolCallLogState
	toolStatus  toolStatusPanelState

	// Running counters surfaced by the chat header chips and the stats
	// panel — RTK-style compression aggregates plus in-flight fan-out
	// counts. See sessionTelemetry in panel_states.go.
	telemetry sessionTelemetry

	// Tasks panel state for /tasks overlay on the chat tab.
	tasksPanel tasksPanelState

	// Latest intent layer decision (engine pre-Ask normalizer). Engine
	// publishes "intent:decision" events on every user submit; this
	// struct caches the most recent so the header chip + /intent show
	// can surface what the engine actually saw. See intentState in
	// panel_states.go.
	intent intentState

	// Ctrl+B panel switcher overlay state. See panel_switcher.go.
	panelSwitcher panelSwitcherState

	// Latest assistant `[next: ...]` tail block parsed by the engine
	// (assistant_hints.go). The chat composer renders these as a
	// numbered starter strip below the most recent assistant turn so
	// the user can keep moving without retyping a follow-up. Cleared
	// the moment the user submits the next prompt — they were ideas
	// for *that* answer, not stable navigation.
	assistantNextActions assistantNextActionsState

	notice string

	// pendingApproval holds the current tool-approval prompt awaiting a
	// y/n keystroke. Non-nil value draws a modal over the chat tab and
	// captures y/n/Esc until resolved. Only one prompt is queued at a
	// time — the agent loop is sequential, and subagents are fed from
	// the same Approver, so there's no concurrent-approval scenario.
	pendingApproval *pendingApproval

	viewCache *viewCacheState
}

// Update (the bubbletea reducer / message dispatcher) lives in update.go.
// handleChatKey (chat panel keyboard router) lives in chat_key.go.
// executeChatCommand (the slash-command dispatcher) lives in chat_commands.go.
// submitChatQuestion is the single send path used by both the raw Enter key
// and slash-command shortcuts that compose a prompt (/review, /explain, ...).

// Intent extraction & slash arg parsers (parseListDirChatArgs,
// parseReadFileChatArgs, parseGrepChatArgs, parseRunCommandChatArgs,
// looksLikeActionRequest, enforceToolUseForActionRequests,
// hasToolCapableProvider, autoToolIntentFromQuestion,
// hasReadIntentPrefix, extractRunIntentCommand,
// extractSearchIntentPattern, extractListIntent, extractBacktickBlock,
// splitExecutableAndArgs, detectReferencedFile, extractReadLineRange)
// live in intent.go.

// Provider/model selection & project config persistence
// (availableProviders, currentProvider, currentModel,
// defaultModelForProvider, loadDriveRoutingFromProjectConfig,
// parseModelPersistArgs, parseArgsWithPersist,
// applyProviderModelSelection, formatProviderSwitchNotice,
// projectConfigPath, reloadEngineConfig,
// persistProviderModelProjectConfig, ensureStringAnyMap,
// toStringAnyMap, providerProfile) lives in provider.go.

// Patch Lab (renderPatchView, patchCommandSummary, loadLatestPatchCmd,
// applyPatchCmd, focusPatchFile, shiftPatchTarget/Hunk, the patch*
// Model accessors, annotateAssistant{Patch,ToolUsage},
// matchAssistantConversationMessage, markLatestPatchInTranscript)
// lives in patch_view.go.

// Patch parsing & apply (patchSectionPaths, totalPatchHunks,
// patchLineCounts, extractPatchedFiles, parseUnifiedDiffSections,
// normalizePatchPath, extractPatchHunks, gitWorkingDiff,
// latestAssistantUnifiedDiff, extractUnifiedDiff,
// looksLikeUnifiedDiff, applyUnifiedDiff) lives in patch_parse.go.

// renderTUIHelp builds the /help body: the registry-backed catalog of TUI
// verbs followed by a short section of TUI-only slash shortcuts and panel
// hotkeys that don't exist as standalone CLI commands.
