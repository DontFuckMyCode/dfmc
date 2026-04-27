package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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

// chatLineRole canonicalises the strings that go into chatLine.Role.
// The field stays a plain string for backwards compatibility with
// ~100 existing call sites and tests, but new code should reference
// these constants so typos like "asistant" surface at compile time
// (or via grep) instead of silently mis-routing a render branch.
//
// Mirrors pkg/types.MessageRole values exactly. "coach" is TUI-only
// (a system-style hint addressed to the user, separate from the
// LLM's "system" role). Typed (chatRole, not plain string) so the
// compiler catches misspellings at every call site that branches on
// role — the M1 review flagged this as a footgun because the comments
// were the only "type safety" before.
type chatRole string

const (
	chatRoleUser      chatRole = "user"
	chatRoleAssistant chatRole = "assistant"
	chatRoleSystem    chatRole = "system"
	chatRoleTool      chatRole = "tool"
	chatRoleCoach     chatRole = "coach"
)

// Eq compares two roles case-insensitively. Used everywhere the renderer
// branches on role since the wire format (LLM responses, JSONL replays)
// can deliver "Assistant", "ASSISTANT", etc. Replaces the dozen
// strings.EqualFold(item.Role, "literal") sites.
func (c chatRole) Eq(other chatRole) bool {
	return strings.EqualFold(string(c), string(other))
}

// coachSeverity tags a coach note's tone — drives the leading marker the
// renderer puts on the transcript line. Pre-fix the parameter was a bare
// `string` and the dispatcher did `strings.ToLower(...) == "warn"` — a
// caller typo ("warning" instead of "warn") silently fell through to the
// no-marker path. Typing it makes every call site name a constant the
// compiler validates.
type coachSeverity string

const (
	coachSeverityInfo      coachSeverity = "info"
	coachSeverityWarn      coachSeverity = "warn"
	coachSeverityCelebrate coachSeverity = "celebrate"
)

// coachSeverityFromWire normalises a severity string arriving over an
// engine event payload (where it's still typeless). Unknown values
// degrade to coachSeverityInfo rather than erroring — the wire is
// untrusted input and a future engine adding "fyi" shouldn't crash old
// TUIs.
func coachSeverityFromWire(s string) coachSeverity {
	switch coachSeverity(strings.ToLower(strings.TrimSpace(s))) {
	case coachSeverityWarn:
		return coachSeverityWarn
	case coachSeverityCelebrate:
		return coachSeverityCelebrate
	default:
		return coachSeverityInfo
	}
}

// paramStr extracts a tool-param value as a trimmed string, handling the
// JSON-decoded type fan-out (string / int / int64 / float64 / bool) that
// would otherwise force every caller into `fmt.Sprint(params[k])` plus a
// `strings.EqualFold(s, "<nil>")` workaround for the way fmt prints nil
// interfaces. Pre-fix that pattern was duplicated 16× across tui.go,
// command_picker.go, slash_picker.go and missed typed-nil edge cases —
// the H1 review item. Returns "" for missing keys, nil values, or
// whitespace-only strings.
func paramStr(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	v, ok := params[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case int:
		return strconv.Itoa(t)
	case int32:
		return strconv.Itoa(int(t))
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

type chatLine struct {
	Role          chatRole
	Content       string
	Preview       string
	PatchFiles    []string
	PatchHunks    int
	IsLatestPatch bool
	ToolNames     []string
	ToolCalls     int
	ToolFailures  int
	ToolChips     []toolChip
	Timestamp     time.Time
	TokenCount    int
	DurationMs    int
}

type slashCommandItem struct {
	Command     string
	Template    string
	Description string
}

type patchSection struct {
	Path      string
	Content   string
	HunkCount int
	Hunks     []patchHunk
}

type patchHunk struct {
	Header  string
	Content string
}

type commandPickerItem struct {
	Value       string
	Description string
	Meta        string
}

type chatSuggestionState struct {
	slashMenuActive     bool
	slashCommands       []slashCommandItem
	slashArgSuggestions []string
	// mentionActive is true when the trailing token begins with `@`, even
	// if no files match yet. The render path keys off this so the picker
	// always shows feedback (loading, empty-state, match list) instead of
	// going silent and leaving the user unsure whether @ is wired up.
	mentionActive      bool
	mentionQuery       string
	mentionRange       string
	mentionSuggestions []mentionRow
	quickActions       []quickActionSuggestion
}

type quickActionSuggestion struct {
	Tool          string
	Params        map[string]any
	Reason        string
	PreparedInput string
}

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
	workflow workflowPanelState
	// Files tab state (entries list, cursor, sticky pin, preview pane).
	// See filesViewState in panel_states.go.
	filesView filesViewState

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
	agentLoop agentLoopState

	// Running counters surfaced by the chat header chips and the stats
	// panel — RTK-style compression aggregates plus in-flight fan-out
	// counts. See sessionTelemetry in panel_states.go.
	telemetry sessionTelemetry

	// Latest intent layer decision (engine pre-Ask normalizer). Engine
	// publishes "intent:decision" events on every user submit; this
	// struct caches the most recent so the header chip + /intent show
	// can surface what the engine actually saw. See intentState in
	// panel_states.go.
	intent intentState

	notice string

	// pendingApproval holds the current tool-approval prompt awaiting a
	// y/n keystroke. Non-nil value draws a modal over the chat tab and
	// captures y/n/Esc until resolved. Only one prompt is queued at a
	// time — the agent loop is sequential, and subagents are fed from
	// the same Approver, so there's no concurrent-approval scenario.
	pendingApproval *pendingApproval
}


// mouseWheelStep is the per-tick scroll distance for the chat transcript.
// 5 lines feels responsive on a standard mouse without overshooting on a
// high-DPI wheel; the input box stays pinned because fitChatBody clips
// only the head, never the tail. mouseWheelPageStep applies on shift+wheel
// for half-page jumps when traveling a long history.
const (
	mouseWheelStep     = 5
	mouseWheelPageStep = 15
)

func NewModel(ctx context.Context, eng *engine.Engine) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	m := Model{
		ctx:                   ctx,
		eng:                   eng,
		tabs:                  []string{"Chat", "Status", "Files", "Patch", "Workflow", "Tools", "Activity", "Memory", "CodeMap", "Conversations", "Prompts", "Security", "Plans", "Context", "Providers"},
		activity:              activityPanelState{follow: true},
		diagnosticPanelsState: newDiagnosticPanelsState(),
		chat:                  chatState{streamIndex: -1},
		inputHistory:          inputHistoryState{index: -1},
		toolView:              toolViewState{overrides: map[string]string{}},
		// The chat body shows the welcome + starters on first paint; don't
		// park a duplicate banner in the footer notice slot (signal density).
		sessionStart: time.Now(),
		ui: uiToggles{
			showStatsPanel: true,
			statsPanelMode: statsPanelModeOverview,
			keyLogEnabled:  os.Getenv("DFMC_KEYLOG") == "1",
		},
	}
	// Seed status synchronously so the chat header renders with real
	// provider info on the first paint, before the async loadStatusCmd
	// delivers. Without this the header shows "⚠ no provider" until the
	// message loop processes statusLoadedMsg.
	if eng != nil {
		m.status = eng.Status()
	}
	return m
}

func (m *Model) ensureDiagnostics() {
	if m == nil {
		return
	}
	if m.diagnosticPanelsState == nil {
		m.diagnosticPanelsState = newDiagnosticPanelsState()
		return
	}
	m.diagnosticPanelsState.applyDefaults()
}

func Run(ctx context.Context, eng *engine.Engine, opts Options) error {
	if eng == nil {
		return fmt.Errorf("tui engine is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	model := NewModel(ctx, eng)
	model.eventRelayExternal = true
	programOpts := []tea.ProgramOption{}
	// Mouse capture is ON by default — wheel scrolls the transcript, which
	// is what people reach for in a full-screen TUI. Drag-to-select still
	// works via Shift+drag in most terminals, and /mouse flips capture at
	// runtime (or set tui.mouse_capture: false in .dfmc/config.yaml to
	// make the "off" behavior the default).
	if eng.Config != nil && eng.Config.TUI.MouseCapture {
		model.ui.mouseCaptureEnabled = true
		programOpts = append(programOpts, tea.WithMouseCellMotion())
	}
	if opts.AltScreen {
		programOpts = append(programOpts, tea.WithAltScreen())
	}
	p := tea.NewProgram(model, programOpts...)
	model.program = p

	// Wire the tool-approval gate. SetApprover is a no-op when the engine
	// has tools.require_approval empty, but registering it here is cheap
	// and means flipping the config flag at runtime doesn't need a restart.
	approver := newTeaApprover()
	approver.bindProgram(p)
	eng.SetApprover(approver)
	defer eng.SetApprover(nil)
	unsubscribeEvents := func() {}
	if eng.EventBus != nil {
		unsubscribeEvents = eng.EventBus.SubscribeFunc("*", func(ev engine.Event) {
			p.Send(engineEventMsg{event: ev})
		})
	}
	defer unsubscribeEvents()

	return runProgramSafely(p)
}

// runProgramSafely wraps tea.Program.Run with a panic guard that
// restores the terminal to a usable state on crash. Without this, a
// panic inside any panel's Update/View leaves the terminal stuck in
// alt-screen + mouse-capture + hidden-cursor mode — the user gets a
// blank screen that looks like a hang until they blindly type `reset`.
func runProgramSafely(p *tea.Program) error {
	return runWithPanicGuard(os.Stderr, func() error {
		_, err := p.Run()
		return err
	})
}

// runWithPanicGuard is the testable core: it runs `fn` and, on panic,
// emits ANSI reset sequences to `out`, prints the panic + stack, and
// returns a wrapped error so the caller can exit cleanly. Split out
// from runProgramSafely so tests don't need a real tea.Program.
//
// ANSI sequences emitted on panic:
//   - CSI ?1049l — exit alt screen
//   - CSI ?1000l / ?1002l / ?1006l — disable mouse reporting variants
//   - CSI ?25h — show cursor
//
// Terminals ignore sequences that aren't currently active, so sending
// all of them is safe regardless of which modes were enabled.
func runWithPanicGuard(out io.Writer, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			_, _ = fmt.Fprint(out,
				"\x1b[?1049l\x1b[?1000l\x1b[?1002l\x1b[?1006l\x1b[?25h")
			_, _ = fmt.Fprintf(out, "\nDFMC TUI crashed: %v\n\n", r)
			_, _ = fmt.Fprintf(out, "%s\n", debug.Stack())
			err = fmt.Errorf("tui panic: %v", r)
		}
	}()
	return fn()
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.EnableBracketedPaste,
		loadStatusCmd(m.eng),
		loadWorkspaceCmd(m.eng),
		loadLatestPatchCmd(m.eng),
		loadFilesCmd(m.eng),
		loadGitInfoCmd(m.projectRoot()),
		heartbeatTickCmd(),
	}
	if !m.eventRelayExternal {
		cmds = append(cmds, subscribeEventsCmd(m.eng))
	}
	return tea.Batch(cmds...)
}

func (m Model) projectRoot() string {
	if m.eng == nil {
		return ""
	}
	return m.eng.ProjectRoot
}

// Update (the bubbletea reducer / message dispatcher) lives in update.go.
// handleChatKey (chat panel keyboard router) lives in chat_key.go.



// executeChatCommand (the slash-command dispatcher) lives in chat_commands.go.


// submitChatQuestion is the single send path used by both the raw Enter key
// and slash-command shortcuts that compose a prompt (/review, /explain, ...).
// It drains agent state, picks the best execution mode (quick-action tool,
// auto-tool intent, or streamed LLM answer), and returns the model + cmd.
// Callers are responsible for clearing input before calling.

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


func (m Model) View() string {
	m.ensureDiagnostics()
	width := m.width
	if width <= 0 {
		width = 100
	}
	height := m.height
	if height <= 0 {
		height = 30
	}
	bodyWidth := width - 4
	if bodyWidth < 20 {
		bodyWidth = width
	}

	// New header: a single dense strip with brand on the left, the
	// active tab badge centered between its prev/next neighbours, and
	// a navigation hint on the right. Replaces the old two-line
	// banner + 15-tab row that wrapped on narrow terminals and made
	// the active tab hard to spot. The "DFMC WORKBENCH" text is kept
	// for tests and for users who grep their terminal scrollback.
	planMode := m.ui.planMode
	tabName := ""
	if m.activeTab >= 0 && m.activeTab < len(m.tabs) {
		tabName = m.tabs[m.activeTab]
	}
	pal := paletteForTab(tabName, planMode)
	strip := renderTopTabStrip(m.tabs, m.activeTab, planMode, width)
	// Keep the canonical brand string in the rendered output so
	// downstream tests / scrollback grep continue to work.
	brandTag := subtleStyle.Render("DFMC WORKBENCH · " + tabName)
	header := strip + "\n" + brandTag
	footer := statusBarStyle.Width(width).Render(m.renderFooter(width))
	bodyHeight := height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyHeight < 6 {
		bodyHeight = 6
	}
	body := m.renderActiveView(bodyWidth, bodyHeight, pal)

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}



// Patch parsing & apply (patchSectionPaths, totalPatchHunks,
// patchLineCounts, extractPatchedFiles, parseUnifiedDiffSections,
// normalizePatchPath, extractPatchHunks, gitWorkingDiff,
// latestAssistantUnifiedDiff, extractUnifiedDiff,
// looksLikeUnifiedDiff, applyUnifiedDiff) lives in patch_parse.go.


// renderTUIHelp builds the /help body: the registry-backed catalog of TUI
// verbs followed by a short section of TUI-only slash shortcuts and panel
// hotkeys that don't exist as standalone CLI commands.
