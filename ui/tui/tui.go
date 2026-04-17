package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"gopkg.in/yaml.v3"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/commands"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/hooks"
	"github.com/dontfuckmycode/dfmc/internal/planning"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/security"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
	toolruntime "github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
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
// LLM's "system" role).
const (
	chatRoleUser      = "user"
	chatRoleAssistant = "assistant"
	chatRoleSystem    = "system"
	chatRoleTool      = "tool"
	chatRoleCoach     = "coach"
)

type chatLine struct {
	Role          string
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
	ctx context.Context
	eng *engine.Engine

	width  int
	height int

	tabs      []string
	activeTab int

	status engine.Status

	transcript       []chatLine
	input            string
	chatCursor       int
	chatCursorManual bool
	chatCursorInput  string
	sending          bool
	streamIndex      int
	streamMessages   <-chan tea.Msg
	// streamCancel aborts the currently-streaming chat turn when set. The
	// submit path stores a per-stream context.CancelFunc here so Esc can
	// stop the provider call and surface a friendly cancellation notice
	// without tearing down the whole TUI context.
	streamCancel context.CancelFunc
	// userCancelledStream tracks whether the most recent stream
	// termination was initiated by the user (Esc) rather than a
	// network error or provider fault. The chatErrMsg handler reads
	// this to render a friendly notice + transcript marker instead of
	// the raw "context canceled" string.
	userCancelledStream bool
	// mouseCaptureEnabled mirrors the program's current mouse-capture
	// mode so /mouse and status copy can report "on" vs. "off". We
	// start from tui.mouse_capture config and flip on /mouse toggle.
	mouseCaptureEnabled bool
	// program is the live bubbletea program handle. Populated by Run()
	// so runtime commands (e.g. /mouse) can call EnableMouse /
	// DisableMouse without re-constructing the program.
	program *tea.Program
	// pendingQueue holds chat questions the user submitted while the engine
	// was still busy. When the current stream finishes we dequeue the oldest
	// entry and submit it — draining FIFO until the queue empties. The
	// composer stays typable while sending, so Enter keeps queueing.
	pendingQueue []string
	// pendingNoteCount tracks how many /btw notes the user submitted for the
	// current agent loop. The engine drains its own queue at step boundaries;
	// this field is only for surfacing the badge in the header until the
	// loop ends.
	pendingNoteCount int

	// Workspace metadata painted into the status line. gitInfo is refreshed
	// asynchronously via loadGitInfoCmd so the UI never blocks on shell-out;
	// sessionStart is captured when NewModel runs and drives the session
	// timer chip.
	gitInfo      gitWorkspaceInfo
	sessionStart time.Time
	// showHelpOverlay toggles the compact keybinding reference card.
	// Kept off by default so the footer stays quiet; ctrl+h flips it.
	showHelpOverlay bool
	// showStatsPanel toggles the right-side stats panel on the chat tab.
	// Default on when the terminal is wide enough; ctrl+s flips it so the
	// user can reclaim the width for chat on narrow screens.
	showStatsPanel bool
	// keyLogEnabled dumps every incoming KeyMsg into m.notice so users can
	// report back what bubbletea actually delivered on their terminal.
	// Turned on via DFMC_KEYLOG=1 at startup or toggled at runtime with the
	// /keylog slash command. The dump is the only practical way to debug
	// keyboard-layout / MinTTY / AltGr issues remotely.
	keyLogEnabled bool
	// planMode makes the agent loop investigate-only: every turn is
	// prepended with a directive forbidding mutations, and the header
	// badges the mode so the user never sends destructive intent by
	// accident. Toggled with /plan (enter) and /code (exit). Mirrors the
	// "plan mode" Claude Code users expect — a safe think-aloud pass
	// before touching files.
	planMode bool
	// resumePromptActive turns on when the engine emits agent:loop:parked and
	// controls whether the yellow "press enter to resume" banner is drawn
	// above the composer. Esc dismisses it; a fresh submit or a successful
	// resume clears it automatically.
	resumePromptActive bool
	// coachMuted silences the user-facing coach:note transcript lines for
	// this session (engine still publishes them; the TUI just drops them).
	// Toggled by /coach.
	coachMuted bool
	// hintsVerbose surfaces the model-facing `[trajectory coach]` hints as
	// subtle transcript lines so the user can see what DFMC is nudging the
	// model with between rounds. Off by default — the hints are meant for
	// the model. Toggled by /hints.
	hintsVerbose bool
	// streamStartedAt records the wall-clock moment a stream was kicked off
	// (fresh submit or /continue). Used to stamp the assistant line's
	// DurationMs on chatDoneMsg so the reader can see how long a turn took.
	streamStartedAt time.Time
	// spinnerFrame advances on tea.Tick while sending/agent is active so the
	// streaming indicator has live motion instead of a static glyph. Cheap —
	// a single int bump per frame, rendered by renderStreamingIndicator.
	spinnerFrame int
	// spinnerTicking is true while a tea.Tick cmd is in flight so we don't
	// schedule overlapping ticks.
	spinnerTicking bool
	// chatScrollback is the number of transcript entries hidden below the
	// visible window, i.e. how far PageUp has scrolled us back from the
	// tail. Zero means "pinned to latest" — any new message snaps us back
	// to the bottom so the user never misses live output.
	chatScrollback int

	diff         string
	changed      []string
	latestPatch  string
	patchFiles   []string
	patchSet     []patchSection
	patchIndex   int
	patchHunk    int
	setupIndex   int
	setupEditing bool
	setupDraft   string
	files        []string
	fileIndex    int
	pinnedFile   string
	filePreview  string
	filePath     string
	fileSize     int

	toolIndex     int
	toolOutput    string
	toolEditing   bool
	toolDraft     string
	toolOverrides map[string]string

	slashIndex       int
	slashArgIndex    int
	mentionIndex     int
	quickActionIndex int

	inputHistory      []string
	inputHistoryIndex int
	inputHistoryDraft string

	commandPickerActive  bool
	commandPickerKind    string
	commandPickerQuery   string
	commandPickerIndex   int
	commandPickerPersist bool
	commandPickerAll     []commandPickerItem

	chatToolPending bool
	chatToolName    string

	eventSub    chan engine.Event
	activityLog []string

	// Activity panel state — a timestamped firehose fed by every engine
	// event (not the filtered shouldLogActivity gate). activityFollow=true
	// pins the view to the tail; any manual scroll unpins it.
	activityEntries []activityEntry
	activityScroll  int
	activityFollow  bool

	// Memory panel state — read view over internal/memory. Tier is a
	// string (not MemoryTier) so "all" can park alongside real values.
	memoryEntries      []types.MemoryEntry
	memoryScroll       int
	memoryTier         string
	memoryQuery        string
	memoryLoading      bool
	memoryErr          string
	memorySearchActive bool

	// CodeMap panel state — snapshot of the symbol/dep graph from
	// internal/codemap. View rotates overview/hotspots/orphans/cycles.
	codemapSnap    codemapSnapshot
	codemapView    string
	codemapScroll  int
	codemapLoading bool
	codemapErr     string
	codemapLoaded  bool

	// Conversations panel state — read view over the JSONL-persisted
	// conversation store. The preview pane holds the first few messages
	// of the currently highlighted entry; it's lazy-loaded on enter.
	conversationsEntries      []conversation.Summary
	conversationsScroll       int
	conversationsQuery        string
	conversationsLoading      bool
	conversationsErr          string
	conversationsSearchActive bool
	conversationsLoaded       bool
	conversationsPreview      []types.Message
	conversationsPreviewID    string

	// Prompts panel state — read view over the merged promptlib catalog
	// (defaults + ~/.dfmc/prompts + .dfmc/prompts). Preview is rendered
	// inline when the user hits enter on a row.
	promptsTemplates     []promptlib.Template
	promptsScroll        int
	promptsQuery         string
	promptsLoading       bool
	promptsErr           string
	promptsSearchActive  bool
	promptsLoaded        bool
	promptsPreviewID     string

	// Security panel state — findings from internal/security.Scanner.
	// Scans are manual (r) because I/O is noticeable on large trees; the
	// view toggle flips between secrets and vulnerabilities.
	securityReport       *security.Report
	securityView         string
	securityScroll       int
	securityQuery        string
	securityLoading      bool
	securityErr          string
	securitySearchActive bool
	securityLoaded       bool

	// Plans panel state — diagnostic view over internal/planning.SplitTask.
	// Decomposition runs locally on enter; no engine round-trip.
	plansQuery       string
	plansPlan        *planning.Plan
	plansScroll      int
	plansErr         string
	plansInputActive bool

	// Context panel state — diagnostic view over Engine.ContextBudgetPreview
	// and ContextRecommendations. Lets the user see the per-query token
	// budget before an Ask is actually sent.
	contextQuery       string
	contextPreview     *engine.ContextBudgetInfo
	contextHints       []engine.ContextRecommendation
	contextErr         string
	contextInputActive bool

	// Providers panel state — diagnostic view over the provider router.
	// Rows are cached (refresh on 'r' or first tab activation) because
	// Hints() is cheap but there's no point redoing the walk on every
	// keystroke.
	providersRows   []providerRow
	providersScroll int
	providersErr    string

	agentLoopActive        bool
	agentLoopStep          int
	agentLoopMaxToolStep   int
	agentLoopToolRounds    int
	agentLoopPhase         string
	agentLoopProvider      string
	agentLoopModel         string
	agentLoopLastTool      string
	agentLoopLastStatus    string
	agentLoopLastDuration  int
	agentLoopLastOutput    string
	agentLoopContextScope  string
	toolTimeline           []toolChip

	// Cumulative RTK-style tool-output compression stats for the session —
	// aggregated across every tool:result event so the stats panel can show
	// "rtk saved N chars (M%)" without re-walking the timeline.
	compressionSavedChars int
	compressionRawChars   int

	// Live counters of in-flight fan-out. activeToolCount is incremented on
	// tool:call and decremented on tool:result/tool:error; activeSubagentCount
	// tracks delegate_task lifecycles via agent:subagent:start|done. Both
	// feed the chat-header badges so the user can see parallel work unfold.
	activeToolCount      int
	activeSubagentCount  int

	notice string

	// pendingApproval holds the current tool-approval prompt awaiting a
	// y/n keystroke. Non-nil value draws a modal over the chat tab and
	// captures y/n/Esc until resolved. Only one prompt is queued at a
	// time — the agent loop is sequential, and subagents are fed from
	// the same Approver, so there's no concurrent-approval scenario.
	pendingApproval *pendingApproval
}

type statusLoadedMsg struct {
	status engine.Status
}

type workspaceLoadedMsg struct {
	diff    string
	changed []string
	err     error
}

type latestPatchLoadedMsg struct {
	patch string
}

type filesLoadedMsg struct {
	files []string
	err   error
}

type filePreviewLoadedMsg struct {
	path    string
	content string
	size    int
	err     error
}

type patchApplyMsg struct {
	checkOnly bool
	changed   []string
	err       error
}

type conversationUndoMsg struct {
	removed int
	err     error
}

type toolRunMsg struct {
	name   string
	params map[string]any
	result toolruntime.Result
	err    error
}

type chatDeltaMsg struct {
	delta string
}

type chatDoneMsg struct{}

type chatErrMsg struct {
	err error
}

type streamClosedMsg struct{}

type eventSubscribedMsg struct {
	ch chan engine.Event
}

type engineEventMsg struct {
	event engine.Event
}

// spinnerTickMsg fires on a short interval while something is streaming or the
// agent loop is alive. Each tick bumps m.spinnerFrame so the streaming
// indicator, stats panel, and any other animated surface can paint motion
// instead of a static glyph.
type spinnerTickMsg struct{}

// spinnerInterval is the frame cadence. ~125ms lands at ~8fps, which reads as
// continuous motion without chewing CPU.
const spinnerInterval = 125 * time.Millisecond

// spinnerTickCmd schedules the next spinner frame. The caller is responsible
// for only scheduling one at a time (see Model.spinnerTicking).
func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// heartbeatTickMsg fires once per second, forever. It keeps the session timer,
// elapsed-duration chips, and any other wall-clock-driven widget alive when
// nothing else is happening — without it, the UI would freeze to whatever was
// last painted until the next event arrived.
type heartbeatTickMsg struct{}

const heartbeatInterval = 1 * time.Second

func heartbeatTickCmd() tea.Cmd {
	return tea.Tick(heartbeatInterval, func(time.Time) tea.Msg { return heartbeatTickMsg{} })
}

func NewModel(ctx context.Context, eng *engine.Engine) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	return Model{
		ctx:               ctx,
		eng:               eng,
		tabs:              []string{"Chat", "Status", "Files", "Patch", "Setup", "Tools", "Activity", "Memory", "CodeMap", "Conversations", "Prompts", "Security", "Plans", "Context", "Providers"},
		activityFollow:    true,
		memoryTier:        memoryTierAll,
		codemapView:       codemapViewOverview,
		securityView:      securityViewSecrets,
		streamIndex:       -1,
		inputHistoryIndex: -1,
		toolOverrides: map[string]string{},
		// The chat body shows the welcome + starters on first paint; don't
		// park a duplicate banner in the footer notice slot (signal density).
		sessionStart:   time.Now(),
		showStatsPanel: true,
		keyLogEnabled:  os.Getenv("DFMC_KEYLOG") == "1",
	}
}

func Run(ctx context.Context, eng *engine.Engine, opts Options) error {
	model := NewModel(ctx, eng)
	programOpts := []tea.ProgramOption{}
	// Mouse capture is OFF by default so terminal drag-to-select / copy
	// just works. Users who prefer wheel-scroll can flip tui.mouse_capture
	// in their config — the TUI will read it below and enable cell-motion
	// tracking. A runtime toggle (/mouse) lets you switch mid-session
	// without restarting.
	if eng != nil && eng.Config != nil && eng.Config.TUI.MouseCapture {
		model.mouseCaptureEnabled = true
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
	return tea.Batch(
		loadStatusCmd(m.eng),
		loadWorkspaceCmd(m.eng),
		loadLatestPatchCmd(m.eng),
		loadFilesCmd(m.eng),
		subscribeEventsCmd(m.eng),
		loadGitInfoCmd(m.projectRoot()),
		heartbeatTickCmd(),
	)
}

func (m Model) projectRoot() string {
	if m.eng == nil {
		return ""
	}
	return m.eng.ProjectRoot
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.MouseMsg:
		// Mouse wheel scrolls the chat transcript on the Chat tab. We
		// deliberately only react on press/release edges — bubbletea emits
		// a press+release pair per wheel tick, so handling both would
		// double-scroll. Ignore the other tabs (their content is static
		// enough to fit in-panel).
		if m.tabs[m.activeTab] != "Chat" {
			return m, nil
		}
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scrollTranscript(-3)
		case tea.MouseButtonWheelDown:
			m.scrollTranscript(3)
		}
		return m, nil

	case eventSubscribedMsg:
		if msg.ch == nil {
			return m, nil
		}
		m.eventSub = msg.ch
		return m, waitForEventMsg(msg.ch)

	case engineEventMsg:
		m = m.handleEngineEvent(msg.event)
		if m.eventSub == nil {
			return m, nil
		}
		next := waitForEventMsg(m.eventSub)
		if strings.EqualFold(strings.TrimSpace(msg.event.Type), "context:built") {
			return m, tea.Batch(next, loadStatusCmd(m.eng))
		}
		return m, next

	case statusLoadedMsg:
		m.status = msg.status
		return m, nil

	case workspaceLoadedMsg:
		if msg.err != nil {
			m.notice = "workspace: " + msg.err.Error()
			return m, nil
		}
		m.diff = msg.diff
		m.changed = msg.changed
		if strings.TrimSpace(msg.diff) == "" {
			m.notice = "Working tree is clean."
		} else if len(msg.changed) > 0 {
			m.notice = "Changed files: " + strings.Join(msg.changed, ", ")
		}
		return m, nil

	case latestPatchLoadedMsg:
		m.latestPatch = msg.patch
		m.patchSet = parseUnifiedDiffSections(msg.patch)
		m.patchFiles = patchSectionPaths(m.patchSet)
		if len(m.patchFiles) == 0 {
			m.patchFiles = extractPatchedFiles(msg.patch)
		}
		m.patchIndex = m.bestPatchIndex()
		m.patchHunk = 0
		m.markLatestPatchInTranscript(msg.patch)
		if strings.TrimSpace(msg.patch) == "" {
			m.notice = "No assistant patch found yet."
		} else {
			m.notice = "Loaded latest assistant patch."
		}
		return m, nil

	case filesLoadedMsg:
		if msg.err != nil {
			m.notice = "files: " + msg.err.Error()
			return m, nil
		}
		m.files = msg.files
		if len(m.files) == 0 {
			m.fileIndex = 0
			m.filePath = ""
			m.filePreview = ""
			m.fileSize = 0
			m.notice = "No project files found."
			return m, nil
		}
		selected := m.selectedFile()
		nextIndex := 0
		if selected != "" {
			for i, path := range m.files {
				if path == selected {
					nextIndex = i
					break
				}
			}
		}
		m.fileIndex = nextIndex
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())

	case filePreviewLoadedMsg:
		if msg.err != nil {
			m.notice = "preview: " + msg.err.Error()
			return m, nil
		}
		m.filePath = msg.path
		m.filePreview = msg.content
		m.fileSize = msg.size
		if strings.TrimSpace(msg.path) != "" {
			m.notice = fmt.Sprintf("Previewing %s (%d bytes)", msg.path, msg.size)
		}
		return m, nil

	case memoryLoadedMsg:
		m.memoryLoading = false
		if msg.err != nil {
			m.memoryErr = msg.err.Error()
			return m, nil
		}
		m.memoryErr = ""
		m.memoryEntries = msg.entries
		if msg.tier != "" {
			m.memoryTier = msg.tier
		}
		if m.memoryScroll >= len(m.memoryEntries) {
			m.memoryScroll = 0
		}
		return m, nil

	case codemapLoadedMsg:
		m.codemapLoading = false
		m.codemapLoaded = true
		if msg.err != nil {
			m.codemapErr = msg.err.Error()
			return m, nil
		}
		m.codemapErr = ""
		m.codemapSnap = msg.snap
		if m.codemapScroll >= codemapViewRowCount(m.codemapView, m.codemapSnap) {
			m.codemapScroll = 0
		}
		return m, nil

	case conversationsLoadedMsg:
		m.conversationsLoading = false
		m.conversationsLoaded = true
		if msg.err != nil {
			m.conversationsErr = msg.err.Error()
			return m, nil
		}
		m.conversationsErr = ""
		m.conversationsEntries = msg.entries
		if m.conversationsScroll >= len(m.conversationsEntries) {
			m.conversationsScroll = 0
		}
		return m, nil

	case conversationPreviewMsg:
		if msg.err != nil {
			m.notice = "conversations: " + msg.err.Error()
			return m, nil
		}
		m.conversationsPreviewID = msg.id
		m.conversationsPreview = msg.msgs
		// Manager.Load sets the conversation as active as a side-effect,
		// so pressing enter here is effectively "load + preview". Surface
		// that so users aren't surprised when Chat history rolls over.
		m.notice = fmt.Sprintf("Loaded conversation %s (%d messages) — switch to Chat (f1/alt+1) to resume.", msg.id, len(msg.msgs))
		return m, nil

	case promptsLoadedMsg:
		m.promptsLoading = false
		m.promptsLoaded = true
		if msg.err != nil {
			m.promptsErr = msg.err.Error()
			return m, nil
		}
		m.promptsErr = ""
		m.promptsTemplates = msg.templates
		if m.promptsScroll >= len(m.promptsTemplates) {
			m.promptsScroll = 0
		}
		return m, nil

	case securityLoadedMsg:
		m.securityLoading = false
		m.securityLoaded = true
		if msg.err != nil {
			m.securityErr = msg.err.Error()
			return m, nil
		}
		m.securityErr = ""
		m.securityReport = msg.report
		m.securityScroll = 0
		return m, nil

	case patchApplyMsg:
		if msg.err != nil {
			m.notice = "patch: " + msg.err.Error()
			return m, nil
		}
		if msg.checkOnly {
			m.notice = "Patch check passed."
			return m, nil
		}
		m = m.focusChangedFiles(msg.changed)
		if len(msg.changed) > 0 {
			m.notice = "Patch applied: " + strings.Join(msg.changed, ", ")
		} else {
			m.notice = "Patch applied."
		}
		cmds := []tea.Cmd{loadWorkspaceCmd(m.eng)}
		if target := m.selectedFile(); target != "" {
			cmds = append(cmds, loadFilePreviewCmd(m.eng, target))
		}
		return m, tea.Batch(cmds...)

	case conversationUndoMsg:
		if msg.err != nil {
			m.notice = "undo: " + msg.err.Error()
			return m, nil
		}
		m.notice = fmt.Sprintf("Undone messages: %d", msg.removed)
		return m, loadLatestPatchCmd(m.eng)

	case toolRunMsg:
		if msg.err != nil {
			m.notice = "tool: " + msg.err.Error()
			m.toolOutput = formatToolErrorForPanel(msg.name, msg.params, msg.result, msg.err)
			if m.chatToolPending && strings.EqualFold(strings.TrimSpace(msg.name), strings.TrimSpace(m.chatToolName)) {
				m = m.appendSystemMessage(formatToolResultForChat(msg.name, msg.params, msg.result, msg.err))
				m.chatToolPending = false
				m.chatToolName = ""
			}
			if toolResultWorkspaceChanged(msg.result) {
				m = m.refreshToolMutationState("")
			}
			return m, nil
		}
		m.toolOutput = formatToolResultForPanel(msg.name, msg.params, msg.result)
		m.notice = fmt.Sprintf("Tool ran: %s (%dms)", msg.name, msg.result.DurationMs)
		if m.chatToolPending && strings.EqualFold(strings.TrimSpace(msg.name), strings.TrimSpace(m.chatToolName)) {
			m = m.appendSystemMessage(formatToolResultForChat(msg.name, msg.params, msg.result, nil))
			m.chatToolPending = false
			m.chatToolName = ""
		}
		if path := toolResultRelativePath(m.eng, msg.result); path != "" {
			m.filePath = path
			if idx := indexOfString(m.files, path); idx >= 0 {
				m.fileIndex = idx
			}
			if msg.name == "read_file" {
				m.filePreview = msg.result.Output
				m.fileSize = len([]byte(msg.result.Output))
			}
			if isMutationTool(msg.name) || toolResultWorkspaceChanged(msg.result) {
				m = m.refreshToolMutationState(path)
			}
		} else if isMutationTool(msg.name) || toolResultWorkspaceChanged(msg.result) {
			m = m.refreshToolMutationState("")
		}
		return m, nil

	case chatDeltaMsg:
		if m.streamIndex >= 0 && m.streamIndex < len(m.transcript) {
			m.transcript[m.streamIndex].Content += msg.delta
			m.transcript[m.streamIndex].Preview = chatDigest(m.transcript[m.streamIndex].Content)
		}
		return m, waitForStreamMsg(m.streamMessages)

	case spinnerTickMsg:
		m.spinnerFrame++
		if m.sending || m.agentLoopActive {
			return m, spinnerTickCmd()
		}
		m.spinnerTicking = false
		return m, nil

	case heartbeatTickMsg:
		// 1Hz heartbeat. Keeps the session timer and elapsed chips live
		// even when no events are in flight. Cheap — one int bump and a
		// repaint per second.
		return m, heartbeatTickCmd()

	case chatDoneMsg:
		m.annotateAssistantPatch(m.streamIndex)
		m.annotateAssistantToolUsage(m.streamIndex)
		if m.streamIndex >= 0 && m.streamIndex < len(m.transcript) && !m.streamStartedAt.IsZero() {
			m.transcript[m.streamIndex].DurationMs = int(time.Since(m.streamStartedAt).Milliseconds())
		}
		m.streamStartedAt = time.Time{}
		m.sending = false
		m.streamMessages = nil
		m.streamIndex = -1
		m.clearStreamCancel()
		m.resetAgentRuntime()
		m.pendingNoteCount = 0
		m.notice = "" // happy-path completion narrates itself via the transcript; no need to park a banner in the footer
		next, drainCmd := m.drainPendingQueue()
		return next, tea.Batch(loadStatusCmd(m.eng), loadLatestPatchCmd(m.eng), loadGitInfoCmd(m.projectRoot()), drainCmd)

	case gitInfoLoadedMsg:
		m.gitInfo = msg.info
		return m, nil

	case chatErrMsg:
		m.sending = false
		m.streamMessages = nil
		m.streamIndex = -1
		m.clearStreamCancel()
		m.resetAgentRuntime()
		m.pendingNoteCount = 0
		// Distinguish a user-driven cancel (esc) from a real provider or
		// network error. Context cancellation that arrives without the
		// userCancelledStream flag set is still treated as an error (e.g.
		// the process context got cancelled from above) — but the common
		// flow is "user pressed esc", which deserves a calm message and a
		// transcript marker so scrolling back makes it obvious the turn
		// was aborted, not silently truncated.
		wasCancelled := m.userCancelledStream || errors.Is(msg.err, context.Canceled)
		m.userCancelledStream = false
		if wasCancelled {
			m.notice = "Turn cancelled (esc). Partial output kept in transcript; /retry reopens it."
			m = m.appendSystemMessage("◦ Turn cancelled by user — partial assistant output above, if any, is what arrived before the cancel took effect.")
			if len(m.pendingQueue) > 0 {
				m.notice += fmt.Sprintf(" %d queued message(s) kept.", len(m.pendingQueue))
			}
			return m, nil
		}
		m.notice = "chat: " + msg.err.Error()
		if len(m.pendingQueue) > 0 {
			m.notice += fmt.Sprintf(" — %d queued message(s) kept.", len(m.pendingQueue))
		}
		return m, nil

	case streamClosedMsg:
		m.sending = false
		m.streamMessages = nil
		m.streamIndex = -1
		m.clearStreamCancel()
		m.resetAgentRuntime()
		m.pendingNoteCount = 0
		next, drainCmd := m.drainPendingQueue()
		return next, drainCmd

	case approvalRequestedMsg:
		// Only surface one prompt at a time. If a second request sneaks in
		// (shouldn't happen — agent loop is sequential) we deny it
		// immediately so the engine keeps moving instead of deadlocking.
		if m.pendingApproval != nil && msg.Pending != nil {
			msg.Pending.resolve(engine.ApprovalDecision{
				Approved: false,
				Reason:   "another approval in progress",
			})
			return m, nil
		}
		m.pendingApproval = msg.Pending
		// Snap to the Chat tab so the modal is actually visible — if the
		// user was browsing the Files panel when an agent step asked for
		// approval they need to see the prompt.
		if len(m.tabs) > 0 {
			m.activeTab = 0
		}
		return m, nil

	case tea.KeyMsg:
		// Approval modal steals all keys while active. We intercept before
		// anything else so a hasty tab-switch or ctrl+c doesn't leak a
		// decision into unrelated handlers or leave the agent loop hung.
		// ctrl+c still quits because a ragequit with an unanswered modal
		// must not wedge the agent — the deferred SetApprover(nil) + the
		// approver's own context cancellation take care of the rest.
		if m.pendingApproval != nil {
			switch msg.String() {
			case "ctrl+c", "ctrl+q":
				m.pendingApproval.resolve(engine.ApprovalDecision{
					Approved: false,
					Reason:   "tui quit",
				})
				m.pendingApproval = nil
				return m, tea.Quit
			case "y", "Y", "enter":
				pending := m.pendingApproval
				m.pendingApproval = nil
				pending.resolve(engine.ApprovalDecision{Approved: true})
				m.notice = "Approved " + pending.Req.Tool + "."
				return m, nil
			case "n", "N", "esc":
				pending := m.pendingApproval
				m.pendingApproval = nil
				pending.resolve(engine.ApprovalDecision{
					Approved: false,
					Reason:   "user denied",
				})
				m.notice = "Denied " + pending.Req.Tool + "."
				return m, nil
			default:
				// Swallow every other key while a prompt is pending so the
				// user doesn't accidentally drop noise into the composer.
				return m, nil
			}
		}
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit
		case "ctrl+u":
			// Unix readline-style "clear input line". Only useful on the
			// Chat tab — other panels don't have a live composer.
			if m.activeTab == 0 {
				m.setChatInput("")
				m.chatCursor = 0
				m.mentionIndex = 0
				m.slashIndex = 0
				m.slashArgIndex = 0
				m.quickActionIndex = 0
				m.notice = "Input cleared."
				return m, nil
			}
		case "ctrl+h":
			m.showHelpOverlay = !m.showHelpOverlay
			return m, nil
		case "ctrl+s":
			m.showStatsPanel = !m.showStatsPanel
			return m, nil
		case "ctrl+p":
			m.activeTab = 0
			m.setChatInput("/")
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
			return m, nil
		case "tab":
			if m.tabs[m.activeTab] != "Chat" {
				m.activeTab = (m.activeTab + 1) % len(m.tabs)
				return m, nil
			}
		case "shift+tab":
			if m.tabs[m.activeTab] != "Chat" {
				m.activeTab--
				if m.activeTab < 0 {
					m.activeTab = len(m.tabs) - 1
				}
				return m, nil
			}
		case "alt+1":
			m.activeTab = 0
			return m, nil
		case "alt+2":
			m.activeTab = 1
			return m, nil
		case "alt+3":
			m.activeTab = 2
			return m, nil
		case "alt+4":
			m.activeTab = 3
			return m, nil
		case "alt+5":
			m.activeTab = 4
			m = m.snapSetupCursorToActive()
			return m, nil
		case "alt+6":
			m.activeTab = 5
			return m, nil
		case "f1":
			m.activeTab = 0
			return m, nil
		case "f2":
			m.activeTab = 1
			return m, nil
		case "f3":
			m.activeTab = 2
			return m, nil
		case "f4":
			m.activeTab = 3
			return m, nil
		case "f5":
			m.activeTab = 4
			m = m.snapSetupCursorToActive()
			return m, nil
		case "f6":
			m.activeTab = 5
			return m, nil
		case "f7":
			m.activeTab = 6
			return m, nil
		case "alt+7":
			m.activeTab = 6
			return m, nil
		case "f8", "alt+8":
			m.activeTab = 7
			if m.memoryEntries == nil && !m.memoryLoading {
				m.memoryLoading = true
				return m, loadMemoryCmd(m.eng, m.memoryTier)
			}
			return m, nil
		case "f9", "alt+9":
			m.activeTab = 8
			if !m.codemapLoaded && !m.codemapLoading {
				m.codemapLoading = true
				return m, loadCodemapCmd(m.eng)
			}
			return m, nil
		case "f10", "alt+0":
			m.activeTab = 9
			if !m.conversationsLoaded && !m.conversationsLoading {
				m.conversationsLoading = true
				return m, loadConversationsCmd(m.eng)
			}
			return m, nil
		case "f11", "alt+t":
			m.activeTab = 10
			if !m.promptsLoaded && !m.promptsLoading {
				m.promptsLoading = true
				return m, loadPromptsCmd(m.eng)
			}
			return m, nil
		case "f12":
			// Security — no alt alias (alt+s is taken by Setup's save).
			// Scan is manual via `r` inside the panel so landing here is
			// cheap; we just flip the tab and show the empty-state hint.
			m.activeTab = 11
			return m, nil
		case "alt+y":
			// Plans — no F13 on most keyboards, so use alt+y (y for "why
			// did this split?"). Decomposition is offline and runs on
			// enter inside the panel.
			m.activeTab = 12
			return m, nil
		case "alt+w":
			// Context — w for "weigh the budget". Preview is offline so
			// just flip the tab; the empty state teaches what e/enter do.
			m.activeTab = 13
			return m, nil
		case "alt+o":
			// Providers — o for "prOviders". Router walk is synchronous
			// and cheap, so we populate on first activation rather than
			// dispatching a tea.Cmd.
			m.activeTab = 14
			if len(m.providersRows) == 0 && m.providersErr == "" {
				m = m.refreshProvidersRows()
			}
			return m, nil
		}

		switch m.tabs[m.activeTab] {
		case "Chat":
			return m.handleChatKey(msg)
		case "Status":
			if msg.String() == "r" {
				return m, loadStatusCmd(m.eng)
			}
		case "Files":
			return m.handleFilesKey(msg)
		case "Patch":
			switch msg.String() {
			case "d", "alt+d":
				return m, loadWorkspaceCmd(m.eng)
			case "l", "alt+l":
				return m, loadLatestPatchCmd(m.eng)
			case "n", "alt+n":
				return m.shiftPatchTarget(1)
			case "b", "alt+b":
				return m.shiftPatchTarget(-1)
			case "j", "alt+j":
				return m.shiftPatchHunk(1)
			case "k", "alt+k":
				return m.shiftPatchHunk(-1)
			case "f", "alt+f":
				return m.focusPatchFile()
			case "c", "alt+c":
				return m, applyPatchCmd(m.eng, m.latestPatch, true)
			case "a", "alt+a":
				return m, applyPatchCmd(m.eng, m.latestPatch, false)
			case "u", "alt+u":
				return m, undoConversationCmd(m.eng)
			}
		case "Setup":
			return m.handleSetupKey(msg)
		case "Tools":
			return m.handleToolsKey(msg)
		case "Activity":
			return m.handleActivityKey(msg)
		case "Memory":
			return m.handleMemoryKey(msg)
		case "CodeMap":
			return m.handleCodemapKey(msg)
		case "Conversations":
			return m.handleConversationsKey(msg)
		case "Prompts":
			return m.handlePromptsKey(msg)
		case "Security":
			return m.handleSecurityKey(msg)
		case "Plans":
			return m.handlePlansKey(msg)
		case "Context":
			return m.handleContextKey(msg)
		case "Providers":
			return m.handleProvidersKey(msg)
		}
	}
	return m, nil
}

func (m Model) handleChatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.commandPickerActive {
		return m.handleCommandPickerKey(msg)
	}
	// Dump the incoming key so we can see what bubbletea delivered. We
	// intentionally dump BEFORE the switch: the notice reflects the
	// arrival, then the render re-runs and shows the picker/input state
	// the user should compare against. Combined with m.input always being
	// rendered in the input box, this tells us both the event and its
	// effect.
	if m.keyLogEnabled {
		m.notice = fmt.Sprintf("key: %s · type=%d · runes=%q · alt=%t · input-before=%q",
			msg.String(), msg.Type, string(msg.Runes), msg.Alt, m.input)
	}
	m.syncChatCursor()
	switch msg.Type {
	case tea.KeyRunes:
		if len(msg.Runes) == 1 && strings.TrimSpace(m.input) == "" && len(m.transcript) == 0 && !m.sending {
			if template, ok := starterTemplateForDigit(msg.Runes[0]); ok {
				m.exitInputHistoryNavigation()
				m.input = template
				m.chatCursor = len([]rune(template))
				return m, nil
			}
		}
		m.exitInputHistoryNavigation()
		m.insertInputText(string(msg.Runes))
		m.slashIndex = 0
		m.slashArgIndex = 0
		m.mentionIndex = 0
		m.quickActionIndex = 0
		if m.keyLogEnabled {
			m.notice = fmt.Sprintf("KeyRunes inserted %q → input=%q", string(msg.Runes), m.input)
		}
		// When the user starts an @-mention but the project file index
		// hasn't landed yet (startup race, or the walk failed silently),
		// kick a refresh so the picker populates on the next frame
		// instead of leaving a dead empty-state.
		if strings.ContainsRune(string(msg.Runes), '@') && len(m.files) == 0 && m.eng != nil {
			return m, loadFilesCmd(m.eng)
		}
		return m, nil
	case tea.KeySpace:
		m.exitInputHistoryNavigation()
		m.insertInputText(" ")
		m.slashIndex = 0
		m.slashArgIndex = 0
		m.mentionIndex = 0
		m.quickActionIndex = 0
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		m.exitInputHistoryNavigation()
		m.deleteInputBeforeCursor()
		m.slashIndex = 0
		m.slashArgIndex = 0
		m.mentionIndex = 0
		m.quickActionIndex = 0
		return m, nil
	case tea.KeyDelete:
		m.exitInputHistoryNavigation()
		m.deleteInputAtCursor()
		m.slashIndex = 0
		m.slashArgIndex = 0
		m.mentionIndex = 0
		m.quickActionIndex = 0
		return m, nil
	case tea.KeyLeft:
		m.moveChatCursor(-1)
		return m, nil
	case tea.KeyRight:
		m.moveChatCursor(1)
		return m, nil
	case tea.KeyHome, tea.KeyCtrlA:
		m.moveChatCursorHome()
		return m, nil
	case tea.KeyEnd, tea.KeyCtrlE:
		if m.chatScrollback > 0 {
			m.chatScrollback = 0
			m.notice = "Transcript: jumped to latest"
			return m, nil
		}
		m.moveChatCursorEnd()
		return m, nil
	case tea.KeyCtrlLeft:
		// readline-style word-left. Leaves the picker indices alone so the
		// user can re-anchor mid-word without losing their selection.
		m.moveChatCursorWordLeft()
		return m, nil
	case tea.KeyCtrlRight:
		m.moveChatCursorWordRight()
		return m, nil
	case tea.KeyCtrlT:
		// Ctrl+T — open the file mention picker without typing '@'.
		// Turkish keyboards (Q layout) + MinTTY deliver '@' as alt+q
		// which can silently drop the '@' rune; users couldn't reach the
		// picker via @ at all. Ctrl+T is the guaranteed-deliverable
		// alternative — identical to typing '@' mid-composer except it
		// inserts a leading space when needed so the trailing token
		// becomes exactly '@', which is what activeMentionQuery checks.
		if !m.sending {
			m.exitInputHistoryNavigation()
			// Ensure the '@' we insert is the start of a fresh mention
			// token. If the cursor is mid-word (e.g. "helloX|") prepend
			// a space so we get "helloX @|" rather than "helloX@|"
			// (which would treat the whole word as the mention).
			m.syncChatCursor()
			runes := []rune(m.input)
			needSpace := m.chatCursor > 0 && m.chatCursor <= len(runes) &&
				!unicode.IsSpace(runes[m.chatCursor-1])
			if needSpace {
				m.insertInputText(" @")
			} else {
				m.insertInputText("@")
			}
			m.mentionIndex = 0
			m.notice = "File picker open — type to filter, tab/enter inserts, esc cancels."
			// Kick a refresh if the index is empty, same as the typed-@
			// path does, so the picker isn't stuck on "Indexing…".
			if len(m.files) == 0 && m.eng != nil {
				return m, loadFilesCmd(m.eng)
			}
			return m, nil
		}
		return m, nil
	case tea.KeyCtrlW:
		// Ctrl+W — kill word before cursor. Whitespace-only separator
		// keeps @mentions and [[file:...]] markers atomic.
		m.exitInputHistoryNavigation()
		m.deleteInputWordBeforeCursor()
		m.slashIndex = 0
		m.slashArgIndex = 0
		m.mentionIndex = 0
		m.quickActionIndex = 0
		return m, nil
	case tea.KeyCtrlK:
		// Ctrl+K — kill to end of line. Pairs with Ctrl+U (kill whole
		// line) so editors coming from bash/emacs feel at home.
		m.exitInputHistoryNavigation()
		m.deleteInputToEndOfLine()
		m.slashIndex = 0
		m.slashArgIndex = 0
		m.mentionIndex = 0
		m.quickActionIndex = 0
		return m, nil
	case tea.KeyPgUp:
		m.scrollTranscript(-8)
		return m, nil
	case tea.KeyPgDown:
		m.scrollTranscript(8)
		return m, nil
	case tea.KeyEsc:
		// Streaming turn? Esc cancels the per-stream context. The goroutine
		// in startChatStream races ctx.Done against the next token and
		// emits chatDoneMsg/chatErrMsg, which clears sending state; we just
		// fire the cancel and surface an immediate notice.
		if m.sending && m.cancelActiveStream() {
			m.notice = "Cancelling current turn… (provider may still finish the in-flight tool before stopping)"
			return m, nil
		}
		// Esc dismisses the parked-resume banner without tearing down the
		// parked state in the engine — the user can still /continue later.
		if m.resumePromptActive {
			m.resumePromptActive = false
			m.notice = "Resume prompt dismissed — parked loop kept; /continue re-opens it."
			return m, nil
		}
		return m, nil
	case tea.KeyShiftUp, tea.KeyCtrlUp:
		// Finer transcript scroll — Up/Down alone are taken by input
		// history + picker navigation, so we reserve the modifier variants
		// for chat scrolling. Three-message step matches the mouse wheel.
		m.scrollTranscript(-3)
		return m, nil
	case tea.KeyShiftDown, tea.KeyCtrlDown:
		m.scrollTranscript(3)
		return m, nil
	case tea.KeyUp:
		suggestions := m.buildChatSuggestionState()
		if !m.sending && m.inputHistoryIndex >= 0 && m.recallInputHistoryPrev() {
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
			m.notice = "History: previous input"
			return m, nil
		}
		if suggestions.slashMenuActive {
			items := suggestions.slashCommands
			if len(items) > 0 {
				idx := clampIndex(m.slashIndex, len(items))
				if idx > 0 {
					idx--
				}
				m.slashIndex = idx
				m.notice = "Command: " + items[m.slashIndex].Template
			}
			return m, nil
		}
		if len(suggestions.slashArgSuggestions) > 0 {
			idx := clampIndex(m.slashArgIndex, len(suggestions.slashArgSuggestions))
			if idx > 0 {
				idx--
			}
			m.slashArgIndex = idx
			m.notice = "Arg: " + suggestions.slashArgSuggestions[m.slashArgIndex]
			return m, nil
		}
		if len(suggestions.mentionSuggestions) > 0 {
			if len(suggestions.mentionSuggestions) > 0 {
				idx := clampIndex(m.mentionIndex, len(suggestions.mentionSuggestions))
				if idx > 0 {
					idx--
				}
				m.mentionIndex = idx
				m.notice = "Mention: " + suggestions.mentionSuggestions[m.mentionIndex].Path
			}
			return m, nil
		}
		if len(suggestions.quickActions) > 0 {
			idx := clampIndex(m.quickActionIndex, len(suggestions.quickActions))
			if idx > 0 {
				idx--
			}
			m.quickActionIndex = idx
			m.notice = "Quick action: " + suggestions.quickActions[idx].PreparedInput
			return m, nil
		}
		// Multi-line buffer navigation. When input spans rows, Up first walks
		// the buffer and only falls through to history navigation when the
		// cursor is already on the first row. Single-line input skips this
		// and goes straight to history, preserving the old behavior.
		if !m.sending && strings.ContainsRune(m.input, '\n') {
			if m.moveChatCursorRowUp() {
				return m, nil
			}
		}
		if !m.sending && m.recallInputHistoryPrev() {
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
			m.quickActionIndex = 0
			m.notice = "History: previous input"
			return m, nil
		}
		return m, nil
	case tea.KeyDown:
		suggestions := m.buildChatSuggestionState()
		if !m.sending && m.inputHistoryIndex >= 0 && m.recallInputHistoryNext() {
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
			m.notice = "History: next input"
			return m, nil
		}
		if suggestions.slashMenuActive {
			items := suggestions.slashCommands
			if len(items) > 0 {
				idx := clampIndex(m.slashIndex, len(items))
				if idx < len(items)-1 {
					idx++
				}
				m.slashIndex = idx
				m.notice = "Command: " + items[m.slashIndex].Template
			}
			return m, nil
		}
		if len(suggestions.slashArgSuggestions) > 0 {
			idx := clampIndex(m.slashArgIndex, len(suggestions.slashArgSuggestions))
			if idx < len(suggestions.slashArgSuggestions)-1 {
				idx++
			}
			m.slashArgIndex = idx
			m.notice = "Arg: " + suggestions.slashArgSuggestions[m.slashArgIndex]
			return m, nil
		}
		if len(suggestions.mentionSuggestions) > 0 {
			if len(suggestions.mentionSuggestions) > 0 {
				idx := clampIndex(m.mentionIndex, len(suggestions.mentionSuggestions))
				if idx < len(suggestions.mentionSuggestions)-1 {
					idx++
				}
				m.mentionIndex = idx
				m.notice = "Mention: " + suggestions.mentionSuggestions[m.mentionIndex].Path
			}
			return m, nil
		}
		if len(suggestions.quickActions) > 0 {
			idx := clampIndex(m.quickActionIndex, len(suggestions.quickActions))
			if idx < len(suggestions.quickActions)-1 {
				idx++
			}
			m.quickActionIndex = idx
			m.notice = "Quick action: " + suggestions.quickActions[idx].PreparedInput
			return m, nil
		}
		// Symmetric to KeyUp — buffer row navigation when input has \n.
		if !m.sending && strings.ContainsRune(m.input, '\n') {
			if m.moveChatCursorRowDown() {
				return m, nil
			}
		}
		if !m.sending && m.recallInputHistoryNext() {
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
			m.quickActionIndex = 0
			m.notice = "History: next input"
			return m, nil
		}
		return m, nil
	case tea.KeyTab:
		if !m.sending {
			suggestions := m.buildChatSuggestionState()
			// Autocomplete outcomes are already visible in the input box —
			// no need to echo them into the footer notice slot.
			if next, ok := autocompleteMentionSelectionFromSuggestions(m.input, m.mentionIndex, suggestions.mentionSuggestions); ok {
				m.setChatInput(next)
				m.mentionIndex = 0
				return m, nil
			}
			if next, ok := m.autocompleteSlashArg(); ok {
				m.setChatInput(next)
				m.slashArgIndex = 0
				return m, nil
			}
			if next, ok := m.autocompleteSlashCommand(); ok {
				m.setChatInput(next)
				return m, nil
			}
			if len(suggestions.quickActions) > 0 {
				selected := suggestions.quickActions[clampIndex(m.quickActionIndex, len(suggestions.quickActions))]
				m.setChatInput(selected.PreparedInput)
				return m, nil
			}
		}
		return m, nil
	case tea.KeyCtrlJ:
		// Ctrl+J — insert a literal newline. This is the reliable cross-
		// terminal way to get a newline in the composer (Shift+Enter is
		// indistinguishable from Enter on most terminals and was a lie in
		// the old help overlay). Alt+Enter is handled at the KeyEnter
		// branch below by checking msg.Alt.
		m.exitInputHistoryNavigation()
		m.insertInputText("\n")
		m.slashIndex = 0
		m.slashArgIndex = 0
		m.mentionIndex = 0
		m.quickActionIndex = 0
		return m, nil
	case tea.KeyEnter:
		// Alt+Enter also inserts a newline rather than submitting — some
		// terminals deliver Alt+Enter as KeyEnter with Alt=true. On
		// terminals without a real Alt key this is a no-op for regular
		// Enter and submission still works.
		if msg.Alt {
			m.exitInputHistoryNavigation()
			m.insertInputText("\n")
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
			m.quickActionIndex = 0
			return m, nil
		}
		suggestions := m.buildChatSuggestionState()
		if !m.sending && len(suggestions.mentionSuggestions) > 0 {
			if next, ok := autocompleteMentionSelectionFromSuggestions(m.input, m.mentionIndex, suggestions.mentionSuggestions); ok {
				m.setChatInput(next)
				m.mentionIndex = 0
				return m, nil
			}
		}
		raw := strings.TrimSpace(m.input)
		// Parked-resume affordance. When the loop is parked, a bare Enter
		// resumes; any typed text is forwarded to the resumed loop as a
		// /btw-style note so the user can redirect the continuation.
		if !m.sending && m.resumePromptActive && m.eng != nil && m.eng.HasParkedAgent() {
			m.setChatInput("")
			return m.startChatResume(raw)
		}
		if raw == "" {
			return m, nil
		}
		if m.sending {
			// Cap the queue so a user spamming Enter while a long stream
			// is in flight can't grow unbounded memory. 64 is enough
			// headroom for normal "ask three follow-ups in a row" flow
			// without becoming a DOS vector.
			if len(m.pendingQueue) >= pendingQueueCap {
				m.notice = fmt.Sprintf("Queue full (%d max) — wait for the current reply, then send again.", pendingQueueCap)
				m.setChatInput("")
				return m, nil
			}
			m.pendingQueue = append(m.pendingQueue, raw)
			m.setChatInput("")
			m.notice = fmt.Sprintf("Queued (%d/%d) — will send after the current reply finishes.", len(m.pendingQueue), pendingQueueCap)
			m = m.appendSystemMessage(fmt.Sprintf("▸ queued #%d: %s", len(m.pendingQueue), raw))
			return m, nil
		}
		if expanded, ok := m.expandSlashSelection(raw); ok {
			raw = expanded
		}
		m.pushInputHistory(raw)
		if next, cmd, handled := m.executeChatCommand(raw); handled {
			return next, cmd
		}
		question := m.chatPrompt()
		if question == "" {
			return m, nil
		}
		m.setChatInput("")
		return m.submitChatQuestion(question, suggestions.quickActions)
	}
	// Defensive catch-all for keys that didn't match any explicit case but
	// still carry printable runes. On Windows with non-standard keyboard
	// layouts (Turkish Q, AltGr combos, IME pass-through) bubbletea can
	// deliver a key event whose Type is something like KeyCtrlQ while
	// Runes=['@'] — the earlier code ignored Runes in that branch and the
	// '@' never reached the input buffer, which looked to the user like
	// "the @ key doesn't trigger the picker". If Runes is non-empty and
	// at least one rune is printable, insert them as text.
	if len(msg.Runes) > 0 {
		printable := false
		for _, r := range msg.Runes {
			if r >= 0x20 && r != 0x7f {
				printable = true
				break
			}
		}
		if printable {
			m.exitInputHistoryNavigation()
			m.insertInputText(string(msg.Runes))
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
			m.quickActionIndex = 0
			if m.keyLogEnabled {
				m.notice = fmt.Sprintf("FALLBACK inserted %q → input=%q", string(msg.Runes), m.input)
			}
			if strings.ContainsRune(string(msg.Runes), '@') && len(m.files) == 0 && m.eng != nil {
				return m, loadFilesCmd(m.eng)
			}
			return m, nil
		}
	}
	return m, nil
}

// Chat composer input editing (cursor, word boundaries, Ctrl+W/K,
// Home/End, history navigation) lives in input.go.

func (m Model) buildChatSuggestionState() chatSuggestionState {
	state := chatSuggestionState{
		slashMenuActive: m.slashMenuActive(),
	}
	if state.slashMenuActive {
		state.slashCommands = m.filteredSlashCommands()
	} else {
		state.slashArgSuggestions = m.activeSlashArgSuggestions()
	}
	if query, rangeSuffix, ok := activeMentionQuery(m.input); ok {
		state.mentionActive = true
		state.mentionQuery = query
		state.mentionRange = rangeSuffix
		state.mentionSuggestions = m.mentionSuggestions(query, 8)
	}
	if !state.slashMenuActive && !state.mentionActive && !m.commandPickerActive && !m.sending {
		state.quickActions = m.quickActionsForCurrentInput()
	}
	return state
}

func autocompleteMentionSelectionFromSuggestions(input string, mentionIndex int, suggestions []mentionRow) (string, bool) {
	if len(suggestions) == 0 {
		return "", false
	}
	idx := clampIndex(mentionIndex, len(suggestions))
	_, rangeSuffix, _ := activeMentionQuery(input)
	return replaceActiveMention(input, suggestions[idx].Path, rangeSuffix), true
}

func (m Model) quickActionsForCurrentInput() []quickActionSuggestion {
	raw := strings.TrimSpace(m.input)
	if raw == "" || strings.HasPrefix(raw, "/") {
		return nil
	}
	question := m.chatPrompt()
	if strings.TrimSpace(question) == "" {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(question))
	out := make([]quickActionSuggestion, 0, 4)
	seen := map[string]struct{}{}
	add := func(name string, params map[string]any, reason string) {
		prepared := quickActionPreparedInput(name, params)
		if prepared == "" {
			return
		}
		key := strings.ToLower(strings.TrimSpace(prepared))
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, quickActionSuggestion{
			Tool:          name,
			Params:        params,
			Reason:        reason,
			PreparedInput: prepared,
		})
	}
	if name, params, reason, ok := m.autoToolIntentFromQuestion(question); ok {
		add(name, params, reason)
	}
	if target := strings.TrimSpace(m.detectReferencedFile(question)); target != "" {
		start, end := extractReadLineRange(question)
		add("read_file", map[string]any{
			"path":       target,
			"line_start": start,
			"line_end":   end,
		}, "read referenced file")
		base := strings.TrimSpace(strings.TrimSuffix(filepath.Base(target), filepath.Ext(target)))
		if base != "" {
			add("grep_codebase", map[string]any{
				"pattern":     regexp.QuoteMeta(base),
				"max_results": 80,
			}, "search symbols related to referenced file")
		}
	}
	if pattern, ok := extractSearchIntentPattern(question, lower); ok {
		add("grep_codebase", map[string]any{
			"pattern":     strings.TrimSpace(pattern),
			"max_results": 80,
		}, "search codebase")
	}
	if path, recursive, maxEntries, ok := extractListIntent(question, lower); ok {
		params := map[string]any{
			"path":        blankFallback(strings.TrimSpace(path), "."),
			"max_entries": maxEntries,
		}
		if recursive {
			params["recursive"] = true
		}
		add("list_dir", params, "list matching directory scope")
	}
	if cmd, ok := extractRunIntentCommand(question, lower); ok {
		command, args := splitExecutableAndArgs(cmd)
		if command != "" {
			params := map[string]any{
				"command": command,
				"dir":     ".",
			}
			if strings.TrimSpace(args) != "" {
				params["args"] = strings.TrimSpace(args)
			}
			add("run_command", params, "run detected command")
		}
	}
	return out
}

func quickActionPreparedInput(name string, params map[string]any) string {
	name = strings.TrimSpace(strings.ToLower(name))
	switch name {
	case "read_file":
		path := strings.TrimSpace(fmt.Sprint(params["path"]))
		if path == "" || strings.EqualFold(path, "<nil>") {
			return ""
		}
		start := strings.TrimSpace(fmt.Sprint(params["line_start"]))
		end := strings.TrimSpace(fmt.Sprint(params["line_end"]))
		parts := []string{"/read", formatSlashArgToken(path)}
		if start != "" && !strings.EqualFold(start, "<nil>") {
			parts = append(parts, start)
		}
		if end != "" && !strings.EqualFold(end, "<nil>") {
			parts = append(parts, end)
		}
		return strings.Join(parts, " ")
	case "list_dir":
		path := strings.TrimSpace(fmt.Sprint(params["path"]))
		if path == "" || strings.EqualFold(path, "<nil>") {
			path = "."
		}
		parts := []string{"/ls", formatSlashArgToken(path)}
		if recursive, ok := params["recursive"].(bool); ok && recursive {
			parts = append(parts, "--recursive")
		}
		if maxEntries := strings.TrimSpace(fmt.Sprint(params["max_entries"])); maxEntries != "" && !strings.EqualFold(maxEntries, "<nil>") {
			parts = append(parts, "--max", maxEntries)
		}
		return strings.Join(parts, " ")
	case "grep_codebase":
		pattern := strings.TrimSpace(fmt.Sprint(params["pattern"]))
		if pattern == "" || strings.EqualFold(pattern, "<nil>") {
			return ""
		}
		return "/grep " + formatSlashArgToken(pattern)
	case "run_command":
		command := strings.TrimSpace(fmt.Sprint(params["command"]))
		if command == "" || strings.EqualFold(command, "<nil>") {
			return ""
		}
		args := strings.TrimSpace(fmt.Sprint(params["args"]))
		if strings.EqualFold(args, "<nil>") {
			args = ""
		}
		if args == "" {
			return "/run " + command
		}
		return "/run " + command + " " + args
	default:
		return ""
	}
}

// Slash-command picker (handleCommandPickerKey, startCommandPicker,
// applyCommandPicker*, providerPickerItems, modelPickerItems,
// toolPickerItems, readPickerItems, runPickerItems, grepPickerItems,
// availableModelsForProvider, modelsFromModelsDevCache,
// modelsDevModelKnown) lives in command_picker.go.

func parseChatCommandInput(raw string) (string, []string, string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "/") {
		return "", nil, "", nil
	}
	body := strings.TrimSpace(strings.TrimPrefix(raw, "/"))
	if body == "" {
		return "", nil, "", nil
	}
	head, tail, err := splitFirstTokenAndTail(body)
	if err != nil {
		return "", nil, "", err
	}
	cmd := strings.ToLower(strings.TrimSpace(head))
	rawArgs := strings.TrimSpace(tail)
	if rawArgs == "" {
		return cmd, nil, "", nil
	}
	args, err := splitToolParamTokens(rawArgs)
	if err != nil {
		return "", nil, "", err
	}
	return cmd, args, rawArgs, nil
}

func splitFirstTokenAndTail(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", nil
	}
	quote := rune(0)
	splitAt := -1
	for i, r := range raw {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			splitAt = i
			goto done
		}
	}
done:
	if quote != 0 {
		return "", "", fmt.Errorf("unterminated quoted value")
	}
	headRaw := raw
	tail := ""
	if splitAt >= 0 {
		headRaw = raw[:splitAt]
		tail = strings.TrimSpace(raw[splitAt:])
	}
	parts, err := splitToolParamTokens(headRaw)
	if err != nil {
		return "", "", err
	}
	head := ""
	if len(parts) > 0 {
		head = strings.TrimSpace(parts[0])
	}
	return head, tail, nil
}

func (m Model) executeChatCommand(raw string) (tea.Model, tea.Cmd, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "/") {
		return m, nil, false
	}
	cmd, args, rawArgs, err := parseChatCommandInput(raw)
	if err != nil {
		m.notice = "command parse: " + err.Error()
		return m.appendSystemMessage("Command parse error: " + err.Error()), nil, true
	}
	if cmd == "" {
		m.notice = "Slash command is empty."
		return m.appendSystemMessage("Slash command is empty. Try /help."), nil, true
	}

	switch cmd {
	case "help":
		m.input = ""
		if len(args) > 0 {
			return m.appendSystemMessage(renderTUICommandHelp(args[0])), nil, true
		}
		return m.appendSystemMessage(renderTUIHelp()), nil, true
	case "quit", "exit", "q":
		m.input = ""
		m.notice = "Goodbye."
		return m.appendSystemMessage("Exiting DFMC — goodbye."), tea.Quit, true
	case "clear":
		m.input = ""
		m.transcript = nil
		m.chatScrollback = 0
		m.notice = "Transcript cleared."
		return m.appendSystemMessage("Transcript cleared. Memory and conversation history are untouched."), nil, true
	case "export", "save":
		// Dump the current transcript to a markdown file under the project
		// root (or to the path given as /export path.md). Writes locally,
		// no network, no engine state touched — purely a view-layer save
		// for users who want to share a session out of DFMC.
		m.input = ""
		if len(m.transcript) == 0 {
			m.notice = "Nothing to export yet."
			return m.appendSystemMessage("Transcript is empty; nothing to export."), nil, true
		}
		target := strings.TrimSpace(strings.Join(args, " "))
		path, err := m.exportTranscript(target)
		if err != nil {
			m.notice = "Export failed: " + err.Error()
			return m.appendSystemMessage("Export failed: " + err.Error()), nil, true
		}
		m.notice = "Exported transcript → " + path
		return m.appendSystemMessage("▸ Transcript exported → " + path + " (" + fmt.Sprintf("%d lines", len(m.transcript)) + ")."), nil, true
	case "retry":
		// Regenerate the most recent assistant reply by resending the latest
		// user message. Trailing assistant/tool/system lines after that user
		// turn are dropped — the resend reopens that turn, it doesn't append
		// a fresh one. If nothing to retry, tell the user rather than
		// silently doing nothing.
		m.input = ""
		if m.sending {
			m.notice = "Cannot /retry while a turn is already streaming."
			return m.appendSystemMessage("A turn is already streaming — press esc to cancel it first, then /retry."), nil, true
		}
		lastUser := -1
		for i := len(m.transcript) - 1; i >= 0; i-- {
			if strings.EqualFold(m.transcript[i].Role, "user") {
				lastUser = i
				break
			}
		}
		if lastUser < 0 {
			m.notice = "Nothing to retry yet."
			return m.appendSystemMessage("No prior user message in this transcript to retry. Type a question first."), nil, true
		}
		question := strings.TrimSpace(m.transcript[lastUser].Content)
		if question == "" {
			m.notice = "Last user message was empty."
			return m.appendSystemMessage("The last user message was empty; nothing to regenerate."), nil, true
		}
		// Drop the previous user turn and everything after — submitChatQuestion
		// re-appends the user line plus a fresh assistant placeholder. Retries
		// that left the old reply visible confused users into thinking they'd
		// accidentally double-sent.
		m.transcript = m.transcript[:lastUser]
		m.notice = "Retrying last question…"
		next, cmd := m.submitChatQuestion(question, nil)
		return next, cmd, true
	case "edit":
		// Pull the most recent user message back into the composer so the
		// user can amend it, then press enter to resend. Complement of
		// /retry, which resubmits verbatim. The old user/assistant turn
		// pair is dropped on the edit so the user doesn't end up with two
		// near-identical user messages stacked when they send the amended
		// version.
		if m.sending {
			m.notice = "Cannot /edit while a turn is already streaming."
			return m.appendSystemMessage("A turn is already streaming — press esc to cancel it first, then /edit."), nil, true
		}
		lastUserIdx := -1
		for i := len(m.transcript) - 1; i >= 0; i-- {
			if strings.EqualFold(m.transcript[i].Role, "user") {
				lastUserIdx = i
				break
			}
		}
		if lastUserIdx < 0 {
			m.input = ""
			m.notice = "Nothing to edit yet."
			return m.appendSystemMessage("No prior user message to edit. Type a question first."), nil, true
		}
		prior := m.transcript[lastUserIdx].Content
		m.transcript = m.transcript[:lastUserIdx]
		m.setChatInput(prior)
		m.chatCursor = len([]rune(prior))
		m.chatCursorManual = true
		m.chatCursorInput = prior
		m.notice = "Editing last message — press enter to resend."
		return m, nil, true
	case "file", "files":
		// Slash-command fallback for the @ mention picker. Same trick as
		// Ctrl+T: insert a leading "@" so the existing mention-picker
		// machinery takes over. Particularly useful for users whose
		// keyboard layout makes Ctrl+T awkward too.
		m.input = ""
		if m.sending {
			m.notice = "Cannot open file picker while a turn is streaming."
			return m.appendSystemMessage("A turn is streaming — esc to cancel first."), nil, true
		}
		m.setChatInput("@")
		m.mentionIndex = 0
		m.notice = "File picker open — type to filter, tab/enter inserts, esc cancels."
		if len(m.files) == 0 && m.eng != nil {
			return m, loadFilesCmd(m.eng), true
		}
		return m, nil, true
	case "plan":
		// Enter plan mode — agent runs read-only, proposes changes as a
		// plan for the user to approve. Complements /retry and /edit:
		// users who want to survey before mutating finally get a first-
		// class switch instead of relying on prompt discipline.
		m.input = ""
		if m.planMode {
			m.notice = "Already in plan mode — type your question, or /code to exit."
			return m.appendSystemMessage("Plan mode is already ON. Your next prompt will investigate without modifying files. Use /code to exit."), nil, true
		}
		m.planMode = true
		m.notice = "Plan mode ON — investigate-only, no file writes. /code exits."
		return m.appendSystemMessage("▸ Plan mode ON. The agent will investigate with read-only tools (read_file, grep_codebase, ast_query, list_dir, glob, git_status, git_diff) and propose changes as a plan. Type /code to exit plan mode when you're ready to apply."), nil, true
	case "code":
		// Exit plan mode — subsequent prompts are free to mutate.
		m.input = ""
		if !m.planMode {
			m.notice = "Already in code mode — plan mode was not active."
			return m.appendSystemMessage("Not in plan mode. Prompts already allow file modifications."), nil, true
		}
		m.planMode = false
		m.notice = "Plan mode OFF — prompts can now modify files."
		return m.appendSystemMessage("▸ Plan mode OFF. Write/update prompts will now route through mutating tools (apply_patch, edit_file, write_file)."), nil, true
	case "compact":
		// Collapse older transcript entries into a single summary line so
		// long sessions stay scannable. Purely a view-layer operation —
		// engine memory, conversation history, and in-loop provider
		// messages are untouched. Runs offline (no LLM call).
		//
		// Default keeps the most recent 6 lines (configurable: /compact 4).
		// A single system line replaces the older tail with counts + a
		// pointer to the Conversations panel for full-fidelity recall.
		m.input = ""
		if m.sending {
			m.notice = "Cannot /compact while a turn is streaming."
			return m.appendSystemMessage("A turn is streaming — press esc to cancel it first, then /compact."), nil, true
		}
		keep := 6
		if len(args) > 0 {
			if n, err := strconv.Atoi(strings.TrimSpace(args[0])); err == nil && n > 0 && n < 200 {
				keep = n
			}
		}
		collapsed, collapsedCount, ok := compactTranscript(m.transcript, keep)
		if !ok {
			m.notice = "Nothing to compact yet."
			return m.appendSystemMessage(fmt.Sprintf("Transcript has %d lines — below keep=%d, nothing to compact.", len(m.transcript), keep)), nil, true
		}
		m.transcript = collapsed
		m.chatScrollback = 0
		note := fmt.Sprintf("Compacted %d older transcript lines. Full history lives in the Conversations panel.", collapsedCount)
		m.notice = fmt.Sprintf("Compacted %d lines (keep=%d).", collapsedCount, keep)
		return m.appendSystemMessage(note), nil, true
	case "approve", "approvals", "permissions":
		// Surface the tool-approval gate configuration: which tools are
		// gated, whether an approver is registered, whether a prompt is
		// currently pending. Read-only — editing the gate requires a
		// config change (opt-in by design; we don't want runtime slash
		// commands silently widening the attack surface).
		m.input = ""
		m.notice = "Approval gate state shown below."
		return m.appendSystemMessage(m.describeApprovalGate()), nil, true
	case "hooks":
		// List every lifecycle hook registered with the dispatcher —
		// event → name(condition) command. Counterpart to /approve for
		// the other half of the tool-lifecycle surface.
		m.input = ""
		m.notice = "Lifecycle hooks listed below."
		return m.appendSystemMessage(m.describeHooks()), nil, true
	case "stats", "tokens", "cost":
		// Session metrics at a glance: tool rounds, RTK-style compression
		// savings, context-window fill, agent loop progress. This makes
		// the 'token miser' thesis tangible — users should be able to
		// see how much they're saving, not just trust the claim.
		m.input = ""
		m.notice = "Session stats below."
		return m.appendSystemMessage(m.describeStats()), nil, true
	case "keylog":
		// Toggle key-event dump into m.notice. Used to diagnose Turkish-
		// keyboard AltGr delivery and similar terminal-specific weirdness
		// without needing a side logfile.
		m.input = ""
		m.keyLogEnabled = !m.keyLogEnabled
		state := "off"
		if m.keyLogEnabled {
			state = "on — press any key and read the footer"
		}
		m.notice = "Key log " + state
		return m.appendSystemMessage("Key event dump is " + state + ". Toggle again with /keylog."), nil, true
	case "coach":
		m.input = ""
		m.coachMuted = !m.coachMuted
		state := "on"
		if m.coachMuted {
			state = "muted"
		}
		m.notice = "Coach " + state + "."
		return m.appendSystemMessage("Coach notes are now " + state + " for this session. Toggle again with /coach."), nil, true
	case "hints":
		m.input = ""
		m.hintsVerbose = !m.hintsVerbose
		state := "hidden"
		if m.hintsVerbose {
			state = "visible"
		}
		m.notice = "Trajectory hints " + state + "."
		return m.appendSystemMessage("Trajectory coach hints between rounds are now " + state + ". Toggle again with /hints."), nil, true
	case "copy", "yank":
		m.input = ""
		return m.handleCopySlash(args)
	case "mouse":
		// Toggle bubbletea's mouse-event capture at runtime. With
		// capture ON the wheel scrolls the transcript natively but
		// terminal drag-to-select / right-click-copy is disabled. With
		// capture OFF you get the terminal's native selection — most
		// terminals also let Shift+drag bypass capture when it's on.
		m.input = ""
		var cmd tea.Cmd
		if m.mouseCaptureEnabled {
			m.mouseCaptureEnabled = false
			cmd = tea.DisableMouse
			m.notice = "Mouse capture off — drag to select / copy text directly."
		} else {
			m.mouseCaptureEnabled = true
			cmd = tea.EnableMouseCellMotion
			m.notice = "Mouse capture on — wheel scrolls transcript. Shift+drag bypasses capture in most terminals."
		}
		return m.appendSystemMessage("Mouse capture toggled. /mouse to flip again; set tui.mouse_capture in .dfmc/config.yaml for the default."), cmd, true
	case "status":
		m.input = ""
		return m.appendSystemMessage(m.statusCommandSummary()), loadStatusCmd(m.eng), true
	case "reload":
		m.input = ""
		if err := m.reloadEngineConfig(); err != nil {
			m.notice = "reload: " + err.Error()
			return m.appendSystemMessage("Runtime reload failed: " + err.Error()), nil, true
		}
		st := m.status
		return m.appendSystemMessage(fmt.Sprintf("Runtime reloaded.\nProvider/Model: %s / %s", blankFallback(st.Provider, "-"), blankFallback(st.Model, "-"))), loadStatusCmd(m.eng), true
	case "context":
		m.input = ""
		mode := ""
		if len(args) > 0 {
			mode = strings.ToLower(strings.TrimSpace(args[0]))
		}
		switch mode {
		case "full", "detail", "detailed", "report", "--full", "-v":
			return m.appendSystemMessage(m.contextCommandDetailedSummary()), nil, true
		case "why", "reasons", "--why":
			return m.appendSystemMessage(m.contextCommandWhySummary()), nil, true
		case "show":
			// Registry-documented subcommand — show the current context
			// selection (same as the default summary so users who follow
			// the `show` noun-first path don't hit a dead end).
			return m.appendSystemMessage(m.contextCommandSummary()), nil, true
		case "budget":
			return m.appendSystemMessage(m.contextCommandDetailedSummary()), nil, true
		case "recommend":
			return m.appendSystemMessage(m.contextCommandWhySummary()), nil, true
		case "brief":
			// Dump the MAGIC_DOC-style project brief — reuse the same
			// read path /magicdoc uses.
			return m.appendSystemMessage(m.magicDocSlash(nil)), nil, true
		case "add", "rm":
			// Pinning isn't wired into config-mutation yet — point the
			// user at the CLI flow instead of silently no-oping.
			payload := strings.TrimSpace(strings.Join(args[1:], " "))
			suffix := ""
			if payload != "" {
				suffix = " " + payload
			}
			return m.appendSystemMessage(fmt.Sprintf("/context %s is CLI-only right now. Run: dfmc context %s%s", mode, mode, suffix)), nil, true
		default:
			return m.appendSystemMessage(m.contextCommandSummary()), nil, true
		}
	case "tools":
		m.input = ""
		tools := m.availableTools()
		if len(tools) == 0 {
			return m.appendSystemMessage("No tools registered."), nil, true
		}
		return m.appendSystemMessage(m.describeToolsList(tools)), nil, true
	case "tool":
		if len(args) == 0 {
			m = m.startCommandPicker("tool", "", false)
			return m, nil, true
		}
		// `/tool show NAME` (and aliases) prints the ToolSpec without
		// running the tool — parity with `dfmc tool show` so operators
		// can see the arg shape inside the TUI session too.
		first := strings.TrimSpace(args[0])
		switch strings.ToLower(first) {
		case "show", "describe", "inspect", "help":
			if len(args) < 2 {
				return m.appendSystemMessage("Usage: /tool show NAME"), nil, true
			}
			m.input = ""
			return m.appendSystemMessage(m.describeToolSpec(strings.TrimSpace(args[1]))), nil, true
		}
		name := strings.TrimSpace(args[0])
		if !containsStringFold(m.availableTools(), name) {
			m = m.startCommandPicker("tool", name, false)
			return m, nil, true
		}
		_, rawParams, err := splitFirstTokenAndTail(rawArgs)
		if err != nil {
			return m.appendSystemMessage("Tool param parse error: " + err.Error()), nil, true
		}
		rawParams = strings.TrimSpace(rawParams)
		params := map[string]any{}
		if rawParams != "" {
			parsed, err := parseToolParamString(rawParams)
			if err != nil {
				return m.appendSystemMessage("Tool param parse error: " + err.Error()), nil, true
			}
			params = parsed
		}
		return m.startChatToolCommand(name, params), runToolCmd(m.eng, name, params), true
	case "ls":
		params, err := parseListDirChatArgs(args)
		if err != nil {
			return m.appendSystemMessage("Usage: /ls [PATH] [-r|--recursive] [--max N]"), nil, true
		}
		return m.startChatToolCommand("list_dir", params), runToolCmd(m.eng, "list_dir", params), true
	case "read":
		if len(args) == 0 {
			m = m.startCommandPicker("read", "", false)
			return m, nil, true
		}
		if target := strings.TrimSpace(args[0]); target != "" && !m.projectHasFile(target) && !containsStringFold(m.files, target) {
			m = m.startCommandPicker("read", target, false)
			return m, nil, true
		}
		params, err := parseReadFileChatArgs(args)
		if err != nil {
			return m.appendSystemMessage("Usage: /read PATH [LINE_START] [LINE_END]"), nil, true
		}
		return m.startChatToolCommand("read_file", params), runToolCmd(m.eng, "read_file", params), true
	case "grep":
		if len(args) == 0 {
			m = m.startCommandPicker("grep", "", false)
			return m, nil, true
		}
		params, err := parseGrepChatArgs(args)
		if err != nil {
			return m.appendSystemMessage("Usage: /grep PATTERN"), nil, true
		}
		return m.startChatToolCommand("grep_codebase", params), runToolCmd(m.eng, "grep_codebase", params), true
	case "run":
		if len(args) == 0 {
			m = m.startCommandPicker("run", "", false)
			return m, nil, true
		}
		params, err := parseRunCommandChatArgs(args)
		if err != nil {
			return m.appendSystemMessage("Usage: /run COMMAND [ARGS...]"), nil, true
		}
		return m.startChatToolCommand("run_command", params), runToolCmd(m.eng, "run_command", params), true
	case "diff":
		m.input = ""
		root := "."
		if m.eng != nil {
			root = strings.TrimSpace(m.eng.Status().ProjectRoot)
			if root == "" {
				root = "."
			}
		}
		diff, err := gitWorkingDiff(root, 32_000)
		if err != nil {
			m.notice = "diff: " + err.Error()
			return m.appendSystemMessage("Diff error: " + err.Error()), nil, true
		}
		if strings.TrimSpace(diff) == "" {
			return m.appendSystemMessage("Working tree is clean."), loadWorkspaceCmd(m.eng), true
		}
		m.notice = "Loaded worktree diff."
		return m.appendSystemMessage("Worktree diff:\n" + truncateCommandBlock(diff, 1600)), loadWorkspaceCmd(m.eng), true
	case "patch":
		m.input = ""
		if strings.TrimSpace(m.latestPatch) == "" {
			return m.appendSystemMessage("No assistant patch available."), nil, true
		}
		return m.appendSystemMessage(m.patchCommandSummary()), nil, true
	case "undo":
		m.input = ""
		if m.eng == nil {
			return m.appendSystemMessage("Undo unavailable: engine is nil."), nil, true
		}
		removed, err := m.eng.ConversationUndoLast()
		if err != nil {
			m.notice = "undo: " + err.Error()
			return m.appendSystemMessage("Undo error: " + err.Error()), nil, true
		}
		m.notice = fmt.Sprintf("Undone messages: %d", removed)
		return m.appendSystemMessage(fmt.Sprintf("Undone messages: %d", removed)), tea.Batch(loadLatestPatchCmd(m.eng), loadWorkspaceCmd(m.eng)), true
	case "apply":
		m.input = ""
		checkOnly := false
		for _, arg := range args {
			if strings.EqualFold(strings.TrimSpace(arg), "--check") {
				checkOnly = true
			}
		}
		if strings.TrimSpace(m.latestPatch) == "" {
			return m.appendSystemMessage("No assistant patch available."), nil, true
		}
		root := "."
		if m.eng != nil {
			root = strings.TrimSpace(m.eng.Status().ProjectRoot)
			if root == "" {
				root = "."
			}
		}
		if err := applyUnifiedDiff(root, m.latestPatch, checkOnly); err != nil {
			m.notice = "patch: " + err.Error()
			return m.appendSystemMessage("Patch error: " + err.Error()), nil, true
		}
		if checkOnly {
			m.notice = "Patch check passed."
			return m.appendSystemMessage("Patch check passed."), nil, true
		}
		changed, err := gitChangedFiles(root, 12)
		if err == nil {
			m.changed = changed
			m = m.focusChangedFiles(changed)
		}
		m.notice = "Patch applied."
		return m.appendSystemMessage("Patch applied."), tea.Batch(loadWorkspaceCmd(m.eng), loadLatestPatchCmd(m.eng)), true
	case "providers":
		names := m.availableProviders()
		if len(names) == 0 {
			m.notice = "No providers configured."
			return m.appendSystemMessage("No providers configured."), nil, true
		}
		m.input = ""
		return m.appendSystemMessage("Providers: " + strings.Join(names, ", ")), loadStatusCmd(m.eng), true
	case "provider":
		parts, persist := parseArgsWithPersist(args)
		if len(parts) == 0 {
			m = m.startCommandPicker("provider", "", persist)
			return m, nil, true
		}
		name := strings.TrimSpace(parts[0])
		model := strings.TrimSpace(strings.Join(parts[1:], " "))
		if !containsStringFold(m.availableProviders(), name) {
			m = m.startCommandPicker("provider", name, persist)
			return m, nil, true
		}
		if model == "" {
			model = m.defaultModelForProvider(name)
		}
		m = m.applyProviderModelSelection(name, model)
		m.input = ""
		if persist {
			path, err := m.persistProviderModelProjectConfig(name, model)
			if err != nil {
				m.notice = "provider persist: " + err.Error()
				return m.appendSystemMessage(fmt.Sprintf("Provider set to %s (%s)\nPersist error: %v", name, blankFallback(model, "-"), err)), loadStatusCmd(m.eng), true
			}
			m.notice = fmt.Sprintf("Provider set to %s (%s), saved to %s", name, blankFallback(model, "-"), filepath.ToSlash(path))
			return m.appendSystemMessage(fmt.Sprintf("Provider set to %s (%s)\nSaved project config: %s", name, blankFallback(model, "-"), filepath.ToSlash(path))), loadStatusCmd(m.eng), true
		}
		m.notice = fmt.Sprintf("Provider set to %s (%s)", name, blankFallback(model, "-"))
		return m.appendSystemMessage(fmt.Sprintf("Provider set to %s (%s)", name, blankFallback(model, "-"))), loadStatusCmd(m.eng), true
	case "models":
		current := m.currentProvider()
		if current == "" {
			return m.appendSystemMessage("No active provider."), nil, true
		}
		model := m.defaultModelForProvider(current)
		choices := m.availableModelsForProvider(current)
		message := fmt.Sprintf("Configured model for %s: %s", current, blankFallback(model, "-"))
		if len(choices) > 0 {
			message += "\nKnown models: " + strings.Join(choices, ", ")
		}
		m.input = ""
		return m.appendSystemMessage(message), nil, true
	case "model":
		providerName := m.currentProvider()
		model, persist := parseModelPersistArgs(args)
		if model == "" {
			m = m.startCommandPicker("model", "", persist)
			return m, nil, true
		}
		if choices := m.availableModelsForProvider(providerName); len(choices) > 0 && !containsStringFold(choices, model) {
			m = m.startCommandPicker("model", model, persist)
			return m, nil, true
		}
		m = m.applyProviderModelSelection(providerName, model)
		m.input = ""
		if persist {
			path, err := m.persistProviderModelProjectConfig(providerName, model)
			if err != nil {
				m.notice = "model persist: " + err.Error()
				return m.appendSystemMessage(fmt.Sprintf("Model set to %s (%s)\nPersist error: %v", model, blankFallback(providerName, "-"), err)), loadStatusCmd(m.eng), true
			}
			m.notice = fmt.Sprintf("Model set to %s (%s), saved to %s", model, blankFallback(providerName, "-"), filepath.ToSlash(path))
			return m.appendSystemMessage(fmt.Sprintf("Model set to %s (%s)\nSaved project config: %s", model, blankFallback(providerName, "-"), filepath.ToSlash(path))), loadStatusCmd(m.eng), true
		}
		m.notice = fmt.Sprintf("Model set to %s (%s)", model, blankFallback(providerName, "-"))
		return m.appendSystemMessage(fmt.Sprintf("Model set to %s (%s)", model, blankFallback(providerName, "-"))), loadStatusCmd(m.eng), true
	case "ask":
		m.input = ""
		payload := strings.TrimSpace(strings.Join(args, " "))
		if payload == "" {
			m.notice = "/ask needs a question."
			return m.appendSystemMessage("Usage: /ask <question>"), nil, true
		}
		next, cmdOut := m.submitChatQuestion(payload, nil)
		return next, cmdOut, true
	case "chat":
		m.input = ""
		m.notice = "Already in chat. Just type your message."
		return m.appendSystemMessage("You're already in the chat tab — type your message and press enter."), nil, true
	case "continue", "resume":
		m.input = ""
		if m.eng == nil || !m.eng.HasParkedAgent() {
			m.notice = "Nothing to resume — no parked agent loop."
			return m.appendSystemMessage("No parked agent loop. /continue only works after the loop pauses at its step cap."), nil, true
		}
		note := strings.TrimSpace(strings.Join(args, " "))
		next, cmdOut := m.startChatResume(note)
		return next, cmdOut, true
	case "split":
		m.input = ""
		query := strings.TrimSpace(strings.Join(args, " "))
		if query == "" {
			m.notice = "/split needs a task to decompose."
			return m.appendSystemMessage("Usage: /split <task> — runs the deterministic splitter and shows the subtasks it detects so you can dispatch them individually."), nil, true
		}
		return m.appendSystemMessage(renderSplitPlan(planning.SplitTask(query))), nil, true
	case "btw":
		m.input = ""
		note := strings.TrimSpace(strings.Join(args, " "))
		if note == "" {
			m.notice = "/btw needs a note."
			return m.appendSystemMessage("Usage: /btw <note> — queued text lands as a user message at the next tool-loop step boundary."), nil, true
		}
		if m.eng == nil {
			m.notice = "/btw: engine unavailable."
			return m.appendSystemMessage("/btw: engine is not initialized."), nil, true
		}
		m.eng.QueueAgentNote(note)
		m.pendingNoteCount++
		return m.appendSystemMessage("/btw queued: " + note + "\nIt will land as a user note before the next tool-loop step."), nil, true
	case "review", "explain", "refactor", "test", "doc":
		return m.runTemplateSlash(cmd, args, raw)
	case "analyze":
		m.input = ""
		return m.runAnalyzeSlash(args, false), nil, true
	case "scan":
		m.input = ""
		return m.runAnalyzeSlash(args, true), nil, true
	case "map":
		m.input = ""
		return m.appendSystemMessage(m.codemapSummary()), nil, true
	case "version":
		m.input = ""
		return m.appendSystemMessage(m.versionSummary()), nil, true
	case "doctor", "health":
		// Lightweight health snapshot that covers provider readiness, AST
		// backend, approval gate, hooks, and recent denials in one card.
		// Full `dfmc doctor` does network checks and --fix; this is the
		// in-chat version so users can sanity-check without leaving TUI.
		m.input = ""
		return m.appendSystemMessage(m.describeHealth()), loadStatusCmd(m.eng), true
	case "magicdoc", "magic":
		m.input = ""
		return m.appendSystemMessage(m.magicDocSlash(args)), nil, true
	case "conversation", "conv":
		m.input = ""
		return m.appendSystemMessage(m.conversationSlash(args)), nil, true
	case "memory":
		m.input = ""
		return m.appendSystemMessage(m.memorySlash(args)), nil, true
	case "prompt":
		m.input = ""
		return m.appendSystemMessage(m.promptSlash(args)), nil, true
	case "skill":
		m.input = ""
		return m.appendSystemMessage(m.skillSlash(args)), nil, true
	case "init", "completion", "man", "serve", "remote", "plugin", "config",
		"debug", "generate", "onboard", "audit", "mcp", "update", "tui":
		// CLI-only commands — surface a friendly pointer instead of
		// the generic "Unknown command" fallback.
		m.input = ""
		m.notice = "/" + cmd + ": run from CLI (not available in TUI)."
		return m.appendSystemMessage("/" + cmd + " is a CLI command. Run: dfmc " + cmd + (func() string {
			if len(args) > 0 {
				return " " + strings.Join(args, " ")
			}
			return ""
		})()), nil, true
	default:
		if suggestion := suggestSlashCommand(cmd); suggestion != "" {
			m.notice = "Unknown /" + cmd + " — try " + suggestion
			return m.appendSystemMessage("Unknown command: /" + cmd + "\nDid you mean " + suggestion + "?  Run /help for the full list."), nil, true
		}
		m.notice = "Unknown chat command: " + raw
		return m.appendSystemMessage("Unknown chat command: " + raw + "\nRun /help for the catalog."), nil, true
	}
}

func (m Model) appendSystemMessage(text string) Model {
	m.transcript = append(m.transcript, newChatLine("system", strings.TrimSpace(text)))
	m.chatScrollback = 0
	return m
}

// exportTranscript writes the current chat transcript to a markdown file.
// When `target` is empty it auto-generates a timestamped name under the
// project root; otherwise it resolves the user-specified path (absolute
// or relative to the project root). Returns the absolute path written.
//
// Layout is deliberately simple — one H2 per role, one blank line between
// bubbles — so the result renders cleanly in any markdown viewer and
// diffs nicely across sessions. Tool-event lines are prefixed '[tool]'
// so the reader can skim past them.
func (m Model) exportTranscript(target string) (string, error) {
	if len(m.transcript) == 0 {
		return "", fmt.Errorf("transcript is empty")
	}
	projectRoot := strings.TrimSpace(m.projectRoot())
	if projectRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve working directory: %w", err)
		}
		projectRoot = cwd
	}
	if target == "" {
		stamp := time.Now().Format("20060102-150405")
		target = filepath.Join(".dfmc", "exports", "transcript-"+stamp+".md")
	}
	// Resolve against project root when relative.
	if !filepath.IsAbs(target) {
		target = filepath.Join(projectRoot, target)
	}
	// Make sure the parent directory exists. MkdirAll is a no-op when it
	// already does, so safe to call unconditionally.
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("create export directory: %w", err)
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "# DFMC transcript — %s\n\n", time.Now().Format(time.RFC3339))
	if provider := strings.TrimSpace(m.status.Provider); provider != "" {
		fmt.Fprintf(&buf, "_provider:_ `%s`", provider)
		if model := strings.TrimSpace(m.status.Model); model != "" {
			fmt.Fprintf(&buf, " · _model:_ `%s`", model)
		}
		buf.WriteString("\n\n")
	}
	for _, line := range m.transcript {
		role := strings.ToLower(strings.TrimSpace(line.Role))
		content := strings.TrimRight(line.Content, "\n")
		if strings.TrimSpace(content) == "" {
			continue
		}
		switch role {
		case "user":
			fmt.Fprintf(&buf, "## user\n\n%s\n\n", content)
		case "assistant":
			fmt.Fprintf(&buf, "## assistant\n\n%s\n\n", content)
		case "tool":
			fmt.Fprintf(&buf, "### [tool] %s\n\n%s\n\n", strings.Join(line.ToolNames, ", "), content)
		case "system":
			fmt.Fprintf(&buf, "### [system]\n\n%s\n\n", content)
		default:
			fmt.Fprintf(&buf, "### [%s]\n\n%s\n\n", role, content)
		}
	}

	if err := os.WriteFile(target, []byte(buf.String()), 0o644); err != nil {
		return "", fmt.Errorf("write export file: %w", err)
	}
	return target, nil
}

// describeStats renders a one-card session-metrics snapshot: transcript
// size, agent loop progress, active tool fan-out, token budget fill,
// and RTK-style compression savings. Pure read over Model fields — no
// engine call, so it's cheap and safe to run mid-stream.
func (m Model) describeStats() string {
	lines := []string{"▸ Session stats"}

	elapsed := time.Duration(0)
	if !m.sessionStart.IsZero() {
		elapsed = time.Since(m.sessionStart).Round(time.Second)
	}
	lines = append(lines, fmt.Sprintf("  elapsed:     %s", elapsed))
	lines = append(lines, fmt.Sprintf("  messages:    %d transcript line(s)", len(m.transcript)))

	// Token budget. ContextIn carries the last computed budget if a turn
	// has run; otherwise fall back to the provider's MaxContext only.
	tokens, maxCtx := 0, 0
	if m.status.ContextIn != nil {
		tokens = m.status.ContextIn.TokenCount
		maxCtx = m.status.ContextIn.ProviderMaxContext
	}
	if maxCtx == 0 {
		maxCtx = m.status.ProviderProfile.MaxContext
	}
	if maxCtx > 0 {
		pct := 0
		if tokens > 0 {
			pct = int(float64(tokens) / float64(maxCtx) * 100)
		}
		lines = append(lines, fmt.Sprintf("  context in:  %s / %s tokens (%d%% of window)",
			formatThousands(tokens), formatThousands(maxCtx), pct))
	} else {
		lines = append(lines, "  context in:  (no provider window info yet)")
	}

	// Agent loop progress (cumulative across turns).
	if m.agentLoopToolRounds > 0 || m.agentLoopStep > 0 {
		phase := strings.TrimSpace(m.agentLoopPhase)
		if phase == "" {
			phase = "idle"
		}
		if m.agentLoopMaxToolStep > 0 {
			lines = append(lines, fmt.Sprintf("  agent:       %s · step %d/%d · %d tool round(s)",
				phase, m.agentLoopStep, m.agentLoopMaxToolStep, m.agentLoopToolRounds))
		} else {
			lines = append(lines, fmt.Sprintf("  agent:       %s · step %d · %d tool round(s)",
				phase, m.agentLoopStep, m.agentLoopToolRounds))
		}
		if last := strings.TrimSpace(m.agentLoopLastTool); last != "" {
			lines = append(lines, fmt.Sprintf("  last tool:   %s (%s, %dms)",
				last, blankFallback(m.agentLoopLastStatus, "?"), m.agentLoopLastDuration))
		}
	} else {
		lines = append(lines, "  agent:       no tool rounds this session yet")
	}

	// Fan-out live counters.
	if m.activeToolCount > 0 || m.activeSubagentCount > 0 {
		lines = append(lines, fmt.Sprintf("  in-flight:   %d tool(s), %d subagent(s)", m.activeToolCount, m.activeSubagentCount))
	}

	// RTK-style compression wins — the headline token-miser metric.
	if m.compressionRawChars > 0 {
		saved := m.compressionSavedChars
		raw := m.compressionRawChars
		pct := 0
		if raw > 0 {
			pct = int(float64(saved) / float64(raw) * 100)
		}
		lines = append(lines, fmt.Sprintf("  rtk savings: %s chars dropped (%d%% of %s raw output)",
			formatThousands(saved), pct, formatThousands(raw)))
	} else {
		lines = append(lines, "  rtk savings: (no tool output yet to compress)")
	}

	// Recent denials — short summary, full list lives in /approve.
	if m.eng != nil {
		if denials := m.eng.RecentDenials(); len(denials) > 0 {
			lines = append(lines, fmt.Sprintf("  denials:     %d blocked agent tool call(s) — see /approve", len(denials)))
		}

		// Prompt cache split — how much of the rendered system prompt
		// Anthropic can cache. Only visible when a sensible breakdown is
		// available; otherwise silent to keep the card tight.
		lastQuery := ""
		for i := len(m.transcript) - 1; i >= 0; i-- {
			if strings.EqualFold(m.transcript[i].Role, "user") {
				lastQuery = strings.TrimSpace(m.transcript[i].Content)
				break
			}
		}
		rec := m.eng.PromptRecommendation(lastQuery)
		if rec.CacheableTokens+rec.DynamicTokens > 0 {
			lines = append(lines, fmt.Sprintf("  cache split: %d%% stable · %s cacheable / %s dynamic",
				rec.CacheablePercent,
				formatThousands(rec.CacheableTokens),
				formatThousands(rec.DynamicTokens)))
		}
	}

	return strings.Join(lines, "\n")
}

// describeHealth renders a compact health snapshot: provider/model/AST
// readiness, tool surface, approval gate, hooks count, recent denials,
// memory store liveness. Intended as a "quick sanity check" the user
// runs from chat (/doctor or /health) without leaving the TUI. Full
// diagnostics still live in the `dfmc doctor` CLI (network, auto-fix).
func (m Model) describeHealth() string {
	lines := []string{"▸ DFMC health snapshot"}

	// Engine presence. If nil something is very wrong — but NewModel can
	// be passed nil in tests, so guard for it.
	if m.eng == nil {
		lines = append(lines, "  engine:   ✗ not initialized (no engine attached to model)")
		return strings.Join(lines, "\n")
	}

	// Provider profile. A misconfigured provider is the #1 reason users
	// report "agent isn't doing anything" — surface it first.
	provider := strings.TrimSpace(m.status.Provider)
	model := strings.TrimSpace(m.status.Model)
	providerLine := "?"
	switch {
	case provider == "":
		providerLine = "✗ no provider selected (run `dfmc config provider anthropic` or edit .dfmc/config.yaml)"
	case strings.EqualFold(provider, "offline") || strings.EqualFold(provider, "placeholder"):
		providerLine = fmt.Sprintf("◈ %s/%s — read-only (no mutating tool calls)", provider, blankFallback(model, "offline"))
	case !m.status.ProviderProfile.Configured:
		providerLine = fmt.Sprintf("⚠ %s/%s — profile not fully configured (missing API key or base_url?)", provider, blankFallback(model, "?"))
	default:
		providerLine = fmt.Sprintf("✓ %s/%s", provider, blankFallback(model, "?"))
	}
	lines = append(lines, "  provider: "+providerLine)

	// AST backend — regex is a warning because tree-sitter needs CGO.
	ast := strings.TrimSpace(m.status.ASTBackend)
	switch ast {
	case "":
		lines = append(lines, "  ast:      ⚠ backend unavailable")
	case "regex":
		lines = append(lines, "  ast:      ⚠ regex fallback (build with CGO_ENABLED=1 for tree-sitter)")
	default:
		lines = append(lines, "  ast:      ✓ "+ast)
	}

	// Tools surface.
	if m.eng.Tools == nil {
		lines = append(lines, "  tools:    ✗ engine.Tools is nil")
	} else {
		tools := m.eng.Tools.List()
		lines = append(lines, fmt.Sprintf("  tools:    ✓ %d registered", len(tools)))
	}

	// Memory store reachability. The degraded flag surfaces a failed
	// Memory.Load() from Init so the operator doesn't silently run on
	// an empty store when the bbolt file is corrupt or locked.
	switch {
	case m.eng.Memory == nil:
		lines = append(lines, "  memory:   ⚠ store not initialized")
	case m.status.MemoryDegraded:
		reason := strings.TrimSpace(m.status.MemoryLoadErr)
		if reason == "" {
			reason = "load failed"
		}
		lines = append(lines, "  memory:   ⚠ degraded — "+reason+" (running with empty store)")
	default:
		lines = append(lines, "  memory:   ✓ bbolt store open")
	}

	// Approval gate condensed to one line (/approve has the long form).
	gated := 0
	if m.eng.Config != nil {
		for _, s := range m.eng.Config.Tools.RequireApproval {
			if strings.TrimSpace(s) != "" {
				gated++
			}
		}
	}
	if gated == 0 {
		lines = append(lines, "  gate:     off — no tools require approval (/approve to learn more)")
	} else {
		lines = append(lines, fmt.Sprintf("  gate:     ON — %d tool(s) gated (/approve for details)", gated))
	}

	// Hooks count.
	hookTotal := 0
	for _, entries := range m.eng.Hooks.Inventory() {
		hookTotal += len(entries)
	}
	if hookTotal == 0 {
		lines = append(lines, "  hooks:    none registered (/hooks to see)")
	} else {
		lines = append(lines, fmt.Sprintf("  hooks:    %d registered (/hooks for details)", hookTotal))
	}

	// Recent denials — useful when user wonders why the agent "did
	// nothing" last turn.
	denials := m.eng.RecentDenials()
	if len(denials) > 0 {
		newest := denials[len(denials)-1]
		lines = append(lines, fmt.Sprintf("  denials:  %d this session — last: %s (%s ago)",
			len(denials), newest.Tool, time.Since(newest.At).Round(time.Second)))
	}

	// Project root — helps users verify DFMC is looking at the right tree.
	if root := strings.TrimSpace(m.projectRoot()); root != "" {
		lines = append(lines, "  project:  "+root)
	}

	return strings.Join(lines, "\n")
}

// describeHooks renders a snapshot of every lifecycle hook registered
// with the engine's dispatcher, grouped by event. Paired with /approve
// so the user can see the whole tool-lifecycle surface without digging
// through config.yaml. Returns a single multi-line string suitable for
// appendSystemMessage.
func (m Model) describeHooks() string {
	var dispatcher *hooks.Dispatcher
	if m.eng != nil {
		dispatcher = m.eng.Hooks
	}
	inventory := dispatcher.Inventory()
	lines := []string{"▸ Lifecycle hooks"}
	if len(inventory) == 0 {
		lines = append(lines,
			"  state:  none registered",
			"  enable: add entries under `hooks:` in .dfmc/config.yaml",
			"  events: user_prompt_submit, pre_tool, post_tool, session_start, session_end",
		)
		return strings.Join(lines, "\n")
	}
	// Render events in a stable order so repeated /hooks doesn't
	// reshuffle the output and confuse the reader.
	eventOrder := []hooks.Event{
		hooks.EventSessionStart,
		hooks.EventUserPromptSubmit,
		hooks.EventPreTool,
		hooks.EventPostTool,
		hooks.EventSessionEnd,
	}
	seen := make(map[hooks.Event]bool, len(eventOrder))
	for _, ev := range eventOrder {
		if entries, ok := inventory[ev]; ok {
			seen[ev] = true
			lines = append(lines, formatHookEvent(ev, entries)...)
		}
	}
	// Fold in any unknown events the dispatcher happened to register
	// (plugins, future additions) so nothing silently disappears.
	for ev, entries := range inventory {
		if seen[ev] {
			continue
		}
		lines = append(lines, formatHookEvent(ev, entries)...)
	}
	return strings.Join(lines, "\n")
}

// formatHookEvent emits a header line per event plus one line per hook.
// "cond=..." is only shown when the entry carries a condition expression
// — otherwise it adds noise.
func formatHookEvent(ev hooks.Event, entries []hooks.HookInventoryEntry) []string {
	out := make([]string, 0, 1+len(entries))
	out = append(out, fmt.Sprintf("  %s (%d)", ev, len(entries)))
	for _, h := range entries {
		name := strings.TrimSpace(h.Name)
		if name == "" {
			name = "(unnamed)"
		}
		cmd := truncateSingleLine(h.Command, 80)
		if cond := strings.TrimSpace(h.Condition); cond != "" {
			out = append(out, fmt.Sprintf("    · %s [cond: %s] → %s", name, cond, cmd))
		} else {
			out = append(out, fmt.Sprintf("    · %s → %s", name, cmd))
		}
	}
	return out
}

// describeApprovalGate returns a human-readable snapshot of the current
// tool-approval configuration for the /approve slash command. Lists the
// gated tools, whether a TUI approver is wired, and whether a prompt
// is currently pending. Read-only: editing the gate is a config change,
// not a slash action.
func (m Model) describeApprovalGate() string {
	var gated []string
	if m.eng != nil && m.eng.Config != nil {
		for _, raw := range m.eng.Config.Tools.RequireApproval {
			if s := strings.TrimSpace(raw); s != "" {
				gated = append(gated, s)
			}
		}
	}
	lines := []string{"▸ Tool approval gate"}
	if len(gated) == 0 {
		lines = append(lines,
			"  state:    off — no tools require approval (tools.require_approval is empty)",
			"  enable:   add tool names to .dfmc/config.yaml under tools.require_approval (or '*' for every tool)",
			"  bypass:   user-initiated /tool calls are never gated",
		)
	} else {
		lines = append(lines,
			"  state:    ON",
			"  gated:    "+strings.Join(gated, ", "),
			"  bypass:   user-initiated /tool calls are never gated; only agent/subagent calls prompt",
		)
	}
	if m.pendingApproval != nil {
		lines = append(lines, fmt.Sprintf("  pending:  %s (source=%s) — press y/enter to approve, n/esc to deny", m.pendingApproval.Req.Tool, m.pendingApproval.Req.Source))
	} else {
		lines = append(lines, "  pending:  none")
	}
	if m.eng != nil {
		denials := m.eng.RecentDenials()
		if len(denials) == 0 {
			lines = append(lines, "  recent:   no denials this session")
		} else {
			lines = append(lines, fmt.Sprintf("  recent:   %d denial(s) — newest first", len(denials)))
			// Walk oldest-first storage in reverse so the newest denial
			// is the first line the user reads.
			for i := len(denials) - 1; i >= 0; i-- {
				d := denials[i]
				age := time.Since(d.At).Round(time.Second)
				lines = append(lines, fmt.Sprintf("    · %s (%s, %s ago) — %s", d.Tool, d.Source, age, d.Reason))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// compactTranscript collapses all transcript entries older than the last
// `keep` into a single system-role summary line so a long session stays
// scannable. Purely a view-layer operation — the engine's own memory and
// conversation store are untouched.
//
// Returns the new transcript, the number of lines that were collapsed,
// and ok=true iff there was actually something to compact. We compact
// only when there are older lines AND they include at least one user or
// assistant turn — summarising a tail of system/tool chatter gains
// nothing and just inflates the notice.
func compactTranscript(lines []chatLine, keep int) ([]chatLine, int, bool) {
	if keep <= 0 {
		keep = 1
	}
	if len(lines) <= keep {
		return lines, 0, false
	}
	head := lines[:len(lines)-keep]
	tail := lines[len(lines)-keep:]

	// Count by role so the summary carries a useful one-glance fingerprint
	// ("5 user turns, 5 assistant replies, 12 tool events, 2 system notes").
	users, assistants, tools, systems, other := 0, 0, 0, 0, 0
	for _, ln := range head {
		switch strings.ToLower(strings.TrimSpace(ln.Role)) {
		case "user":
			users++
		case "assistant":
			assistants++
		case "tool":
			tools++
		case "system":
			systems++
		default:
			other++
		}
	}
	if users == 0 && assistants == 0 && tools == 0 {
		// Only a run of system lines to collapse — not worth a summary.
		return lines, 0, false
	}
	fingerprint := make([]string, 0, 5)
	if users > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d user", users))
	}
	if assistants > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d assistant", assistants))
	}
	if tools > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d tool", tools))
	}
	if systems > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d system", systems))
	}
	if other > 0 {
		fingerprint = append(fingerprint, fmt.Sprintf("%d other", other))
	}
	summary := newChatLine("system",
		fmt.Sprintf("▸ Transcript compacted — %s collapsed. Full history kept in Conversations panel.",
			strings.Join(fingerprint, ", ")))

	out := make([]chatLine, 0, 1+keep)
	out = append(out, summary)
	out = append(out, tail...)
	return out, len(head), true
}

// appendToolEventMessage inserts a tool-tagged transcript line so tool calls
// and results render with the TOOL badge rather than SYS. This is what makes
// the chat feel like a unified conversation — the events sit where they
// actually fired instead of being relegated to a separate side panel.
func (m Model) appendToolEventMessage(text string) Model {
	m.transcript = append(m.transcript, newChatLine("tool", strings.TrimSpace(text)))
	m.chatScrollback = 0
	return m
}

// appendCoachMessage inserts a coach-tagged transcript line carrying the
// background observer's commentary. Severity decides the subtle leading
// marker so warn/celebrate notes stand apart from plain info nudges without
// shouting; origin is appended as a muted tag so users can learn which rule
// fired (useful for giving feedback like "quiet the mutation_unvalidated
// rule"). Notes always land in the transcript — they're the user-facing
// surface of the tiny-touches coach, not ephemeral chatter.
func (m Model) appendCoachMessage(text, severity, origin string) Model {
	text = strings.TrimSpace(text)
	if text == "" {
		return m
	}
	marker := ""
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "warn":
		marker = warnStyle.Render("⚠") + " "
	case "celebrate":
		marker = okStyle.Render("✓") + " "
	}
	body := marker + text
	if origin = strings.TrimSpace(origin); origin != "" {
		body += " " + subtleStyle.Render("["+origin+"]")
	}
	m.transcript = append(m.transcript, newChatLine("coach", body))
	m.chatScrollback = 0
	m.appendActivity("coach: " + text)
	m.notice = text
	return m
}

// scrollTranscript shifts the chat head backwards by delta *lines* (negative
// = older/upward, positive = newer/downward) and clamps to a rough ceiling
// derived from the transcript size. The render layer (fitChatBody) clamps
// tighter based on actual rendered line count — scroll just tracks intent.
func (m *Model) scrollTranscript(delta int) {
	next := m.chatScrollback - delta
	if next < 0 {
		next = 0
	}
	maxBack := estimateTranscriptLines(m.transcript)
	if next > maxBack {
		next = maxBack
	}
	if next == m.chatScrollback {
		if next == 0 {
			m.notice = "Transcript: already at latest"
		} else {
			m.notice = "Transcript: at top of history"
		}
		return
	}
	m.chatScrollback = next
	if next == 0 {
		m.notice = "Transcript: back to latest"
	} else {
		m.notice = fmt.Sprintf("Transcript: scrolled back %d lines (PageDown / End resumes)", next)
	}
}

// estimateTranscriptLines returns a rough upper bound on the number of
// rendered lines the transcript will produce — used only as a scrollback
// ceiling so the user can't scroll into empty space indefinitely.
func estimateTranscriptLines(transcript []chatLine) int {
	total := 0
	for _, item := range transcript {
		// Header bar + content lines + spacer between messages.
		total += 2 + strings.Count(item.Content, "\n")
	}
	return total
}

// submitChatQuestion is the single send path used by both the raw Enter key
// and slash-command shortcuts that compose a prompt (/review, /explain, ...).
// It drains agent state, picks the best execution mode (quick-action tool,
// auto-tool intent, or streamed LLM answer), and returns the model + cmd.
// Callers are responsible for clearing input before calling.
func (m Model) submitChatQuestion(question string, quickActions []quickActionSuggestion) (Model, tea.Cmd) {
	question = strings.TrimSpace(question)
	if question == "" {
		return m, nil
	}
	m.resetAgentRuntime()
	m.resumePromptActive = false
	m.toolTimeline = nil
	m.chatScrollback = 0
	// Offline-mode guardrail. When the user sends what clearly looks like
	// an action ("update X", "write Y", "güncelle", "fix the bug", plus a
	// [[file:]] marker) but the active provider is the offline analyzer,
	// surface the mismatch before they wait on a stream that can't modify
	// anything. The offline analyzer happily reads the file and prints
	// heuristic observations — users reasonably mistake that for "my file
	// got updated and nothing happened". A prepended system message kills
	// the ambiguity without blocking the turn.
	if m.looksLikeActionRequest(question) && !m.hasToolCapableProvider() {
		m = m.appendSystemMessage(
			"⚠ This looks like an action request (write/update/edit), but the active provider is the offline analyzer — it can only summarize files, it cannot modify them. " +
				"Run /provider to pick a tool-capable provider (anthropic, openai, deepseek, kimi, zai, alibaba), then retry with /retry.",
		)
	}
	// Tool-use enforcement for action requests on tool-capable providers.
	// Weaker LLMs (e.g. GLM/Qwen/DeepSeek via openai-compat) routinely
	// respond to "update README.md" by describing the changes in prose
	// instead of calling apply_patch/edit_file/write_file — users then see
	// the file content echoed back and conclude "nothing happened".
	// Prepending an explicit directive dramatically raises the chance the
	// model routes through a real tool call. Strong models (Claude, GPT-4)
	// follow the directive anyway; weak models finally take the hint.
	// Applied only when: intent is clear, provider is tool-capable, and
	// the question doesn't already instruct tool use.
	// In plan mode, inject the opposite directive: investigate only, no
	// mutations. Takes precedence over the enforce-tool-use directive.
	if m.planMode {
		question = strings.TrimRight(question, "\n") +
			"\n\n[DFMC plan mode] You are in INVESTIGATE-ONLY mode. " +
			"Use ONLY read-only tools (read_file, grep_codebase, ast_query, list_dir, glob, git_status, git_diff, web_fetch, web_search). " +
			"Do NOT call write_file, edit_file, apply_patch, or run_command with destructive arguments. " +
			"Produce a concrete plan as the answer — numbered steps, files to touch, expected diffs — that the user can approve before any files are modified."
	} else {
		question = m.enforceToolUseForActionRequests(question)
	}
	if len(quickActions) > 0 {
		selected := quickActions[clampIndex(m.quickActionIndex, len(quickActions))]
		m.transcript = append(m.transcript, newChatLine("user", question))
		m = m.appendSystemMessage("Auto action: " + selected.Reason)
		m = m.startChatToolCommand(selected.Tool, selected.Params)
		return m, runToolCmd(m.eng, selected.Tool, selected.Params)
	}
	if name, params, reason, ok := m.autoToolIntentFromQuestion(question); ok {
		m.transcript = append(m.transcript, newChatLine("user", question))
		m = m.appendSystemMessage("Auto action: " + reason)
		m = m.startChatToolCommand(name, params)
		return m, runToolCmd(m.eng, name, params)
	}
	m.transcript = append(m.transcript,
		newChatLine("user", question),
		newChatLine("assistant", ""),
	)
	m.streamIndex = len(m.transcript) - 1
	m.sending = true
	m.streamStartedAt = time.Now()
	m.notice = "Streaming answer... (esc cancels)"
	// Per-stream context so esc can cancel this turn without killing the
	// whole TUI's ctx (which would kill timers and subscriptions too).
	streamCtx, cancel := context.WithCancel(m.ctx)
	m.streamCancel = cancel
	m.streamMessages = startChatStream(streamCtx, m.eng, question)
	return m, tea.Batch(waitForStreamMsg(m.streamMessages), m.ensureSpinnerTick())
}

// ensureSpinnerTick schedules the spinner tick when needed, but only if one
// isn't already in flight. Mutates m.spinnerTicking and returns the cmd (nil
// when no schedule is needed).
func (m *Model) ensureSpinnerTick() tea.Cmd {
	if m.spinnerTicking {
		return nil
	}
	if !m.sending && !m.agentLoopActive {
		return nil
	}
	m.spinnerTicking = true
	return spinnerTickCmd()
}

// drainPendingQueue pops the oldest queued message and submits it as if the
// user had just pressed enter. Called when the current stream finishes so
// follow-up messages flow without the user babysitting the composer.
func (m Model) drainPendingQueue() (Model, tea.Cmd) {
	if len(m.pendingQueue) == 0 {
		return m, nil
	}
	next := m.pendingQueue[0]
	m.pendingQueue = m.pendingQueue[1:]
	m = m.appendSystemMessage(fmt.Sprintf("▸ draining queued message (%d remaining): %s", len(m.pendingQueue), next))
	if expanded, ok := m.expandSlashSelection(next); ok {
		next = expanded
	}
	m.pushInputHistory(next)
	if nextModel, cmd, handled := m.executeChatCommand(next); handled {
		return nextModel.(Model), cmd
	}
	return m.submitChatQuestion(next, nil)
}

// startChatResume kicks off a resumed agent loop from the engine's parked
// state and wires the result into the same streaming path that submitChatQuestion
// uses, so the UI treats it identically.
func (m Model) startChatResume(note string) (Model, tea.Cmd) {
	m.resetAgentRuntime()
	m.resumePromptActive = false
	m.toolTimeline = nil
	m.chatScrollback = 0
	banner := "Resuming parked agent loop"
	if note != "" {
		banner += " with note: " + note
	}
	m = m.appendSystemMessage(banner + "...")
	m.transcript = append(m.transcript, newChatLine("assistant", ""))
	m.streamIndex = len(m.transcript) - 1
	m.sending = true
	m.streamStartedAt = time.Now()
	m.notice = "Resuming agent loop..."
	m.streamMessages = startChatResumeStream(m.ctx, m.eng, note)
	return m, tea.Batch(waitForStreamMsg(m.streamMessages), m.ensureSpinnerTick())
}

func newChatLine(role, content string) chatLine {
	return chatLine{
		Role:      strings.TrimSpace(role),
		Content:   content,
		Preview:   chatDigest(content),
		Timestamp: time.Now(),
	}
}

func (m Model) startChatToolCommand(name string, params map[string]any) Model {
	name = strings.TrimSpace(name)
	m.setChatInput("")
	m.chatToolPending = true
	m.chatToolName = name
	if params == nil {
		params = map[string]any{}
	}
	m.notice = "Running tool from chat: " + name
	m = m.appendSystemMessage("Running tool: " + name + " " + formatToolParams(params))
	return m
}

func formatToolResultForChat(name string, params map[string]any, res toolruntime.Result, err error) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	header := "Tool result: " + name
	if err != nil {
		text := strings.TrimSpace(err.Error())
		if text == "" {
			text = "unknown error"
		}
		body := ""
		if out := strings.TrimSpace(res.Output); out != "" {
			body = "\n" + truncateCommandBlock(out, 1000)
		}
		return header + " failed: " + text + body
	}
	summary := fmt.Sprintf("%s success (%dms)", header, res.DurationMs)
	out := strings.TrimSpace(res.Output)
	if out == "" {
		return summary
	}
	return summary + "\n" + truncateCommandBlock(out, 1200)
}

// Intent extraction & slash arg parsers (parseListDirChatArgs,
// parseReadFileChatArgs, parseGrepChatArgs, parseRunCommandChatArgs,
// looksLikeActionRequest, enforceToolUseForActionRequests,
// hasToolCapableProvider, autoToolIntentFromQuestion,
// hasReadIntentPrefix, extractRunIntentCommand,
// extractSearchIntentPattern, extractListIntent, extractBacktickBlock,
// splitExecutableAndArgs, detectReferencedFile, extractReadLineRange)
// live in intent.go.

func (m Model) availableProviders() []string {
	if m.eng == nil || m.eng.Config == nil {
		return nil
	}
	names := make([]string, 0, len(m.eng.Config.Providers.Profiles))
	for name := range m.eng.Config.Providers.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m Model) currentProvider() string {
	if providerName := strings.TrimSpace(m.status.Provider); providerName != "" {
		return providerName
	}
	if m.eng == nil {
		return ""
	}
	return strings.TrimSpace(m.eng.Status().Provider)
}

// snapSetupCursorToActive lands the Setup-tab cursor on whichever
// provider is currently in use. Invoked when the user opens the Setup
// tab so the active row is highlighted instead of always starting at
// index 0 — that "active provider invisible until you scroll" feel
// confused users into thinking nothing was selected.
func (m Model) snapSetupCursorToActive() Model {
	providers := m.availableProviders()
	if len(providers) == 0 {
		return m
	}
	active := strings.TrimSpace(m.currentProvider())
	if active == "" {
		return m
	}
	for i, name := range providers {
		if strings.EqualFold(name, active) {
			m.setupIndex = i
			return m
		}
	}
	return m
}

func (m Model) currentModel() string {
	if model := strings.TrimSpace(m.status.Model); model != "" {
		return model
	}
	if m.eng == nil {
		return ""
	}
	return strings.TrimSpace(m.eng.Status().Model)
}

func (m Model) defaultModelForProvider(name string) string {
	if m.eng == nil || m.eng.Config == nil {
		return ""
	}
	profile, ok := m.eng.Config.Providers.Profiles[strings.TrimSpace(name)]
	if !ok {
		return ""
	}
	return strings.TrimSpace(profile.Model)
}

func parseModelPersistArgs(args []string) (string, bool) {
	parts := make([]string, 0, len(args))
	persist := false
	for _, raw := range args {
		arg := strings.TrimSpace(raw)
		switch strings.ToLower(arg) {
		case "--persist", "--save":
			persist = true
		default:
			if arg != "" {
				parts = append(parts, arg)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, " ")), persist
}

func parseArgsWithPersist(args []string) ([]string, bool) {
	parts := make([]string, 0, len(args))
	persist := false
	for _, raw := range args {
		arg := strings.TrimSpace(raw)
		switch strings.ToLower(arg) {
		case "--persist", "--save":
			persist = true
		default:
			if arg != "" {
				parts = append(parts, arg)
			}
		}
	}
	return parts, persist
}

func (m Model) applyProviderModelSelection(providerName, model string) Model {
	providerName = strings.TrimSpace(providerName)
	model = strings.TrimSpace(model)
	if providerName == "" {
		return m
	}
	if m.eng != nil {
		if m.eng.Config != nil {
			if m.eng.Config.Providers.Profiles == nil {
				m.eng.Config.Providers.Profiles = map[string]config.ModelConfig{}
			}
			profile := m.eng.Config.Providers.Profiles[providerName]
			if model != "" {
				profile.Model = model
			}
			m.eng.Config.Providers.Profiles[providerName] = profile
		}
		m.eng.SetProviderModel(providerName, model)
		m.status = m.eng.Status()
		m.notice = formatProviderSwitchNotice(m.status.ProviderProfile)
	}
	return m
}

// formatProviderSwitchNotice produces a one-line confirmation after a
// provider/model switch. It names the profile, whether an endpoint and
// API key are configured, and flags the likely offline-fallback case up
// front so the user doesn't discover it only when a chat turn fails.
func formatProviderSwitchNotice(p engine.ProviderProfileStatus) string {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return ""
	}
	parts := []string{"provider → " + name}
	if model := strings.TrimSpace(p.Model); model != "" {
		parts = append(parts, "model: "+model)
	}
	if !p.Configured {
		if env := config.EnvVarForProvider(name); env != "" {
			parts = append(parts, fmt.Sprintf("⚠ no API key — set %s in .env or providers.profiles.%s.api_key (falling back to offline)", env, name))
		} else {
			parts = append(parts, fmt.Sprintf("⚠ no API key — set providers.profiles.%s.api_key in config.yaml (falling back to offline)", name))
		}
		return strings.Join(parts, " · ")
	}
	if base := strings.TrimSpace(p.BaseURL); base != "" {
		parts = append(parts, "endpoint: "+base)
	}
	return strings.Join(parts, " · ")
}

func (m Model) projectConfigPath() (string, error) {
	root := "."
	if m.eng != nil {
		root = strings.TrimSpace(m.eng.ProjectRoot)
	}
	if strings.TrimSpace(root) == "" {
		root = strings.TrimSpace(m.status.ProjectRoot)
	}
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("project root unavailable")
	}
	return filepath.Join(root, config.DefaultDirName, "config.yaml"), nil
}

func (m *Model) reloadEngineConfig() error {
	if m.eng == nil {
		return fmt.Errorf("engine is unavailable")
	}
	cwd := strings.TrimSpace(m.eng.ProjectRoot)
	if cwd == "" {
		cwd = strings.TrimSpace(m.status.ProjectRoot)
	}
	if cwd == "" {
		cwd = "."
	}
	if err := m.eng.ReloadConfig(cwd); err != nil {
		return err
	}
	m.status = m.eng.Status()
	return nil
}

func (m Model) persistProviderModelProjectConfig(providerName, model string) (string, error) {
	providerName = strings.TrimSpace(providerName)
	model = strings.TrimSpace(model)
	if providerName == "" {
		return "", fmt.Errorf("provider is empty")
	}
	if model == "" {
		return "", fmt.Errorf("model is empty")
	}
	path, err := m.projectConfigPath()
	if err != nil {
		return "", err
	}

	doc := map[string]any{}
	if data, readErr := os.ReadFile(path); readErr == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if unmarshalErr := yaml.Unmarshal(data, &doc); unmarshalErr != nil {
				return "", fmt.Errorf("parse project config: %w", unmarshalErr)
			}
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", fmt.Errorf("read project config: %w", readErr)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	if _, ok := doc["version"]; !ok {
		doc["version"] = 1
	}

	providersNode := ensureStringAnyMap(doc, "providers")
	providersNode["primary"] = providerName
	profilesNode := ensureStringAnyMap(providersNode, "profiles")
	profileNode := ensureStringAnyMap(profilesNode, providerName)
	profileNode["model"] = model
	if m.eng != nil && m.eng.Config != nil {
		if prof, ok := m.eng.Config.Providers.Profiles[providerName]; ok {
			if strings.TrimSpace(prof.Protocol) != "" {
				profileNode["protocol"] = strings.TrimSpace(prof.Protocol)
			}
			if strings.TrimSpace(prof.BaseURL) != "" {
				profileNode["base_url"] = strings.TrimSpace(prof.BaseURL)
			}
			if prof.MaxTokens > 0 {
				profileNode["max_tokens"] = prof.MaxTokens
			}
			if prof.MaxContext > 0 {
				profileNode["max_context"] = prof.MaxContext
			}
		}
	}

	out, marshalErr := yaml.Marshal(doc)
	if marshalErr != nil {
		return "", fmt.Errorf("marshal project config: %w", marshalErr)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create project config dir: %w", err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return "", fmt.Errorf("write project config: %w", err)
	}
	return path, nil
}

func ensureStringAnyMap(parent map[string]any, key string) map[string]any {
	if parent == nil {
		return map[string]any{}
	}
	if existing, ok := parent[key]; ok {
		if out, ok := toStringAnyMap(existing); ok {
			parent[key] = out
			return out
		}
	}
	out := map[string]any{}
	parent[key] = out
	return out
}

func toStringAnyMap(raw any) (map[string]any, bool) {
	switch value := raw.(type) {
	case map[string]any:
		return value, true
	case map[any]any:
		out := map[string]any{}
		for key, item := range value {
			text, ok := key.(string)
			if !ok {
				continue
			}
			out[text] = item
		}
		return out, true
	default:
		return nil, false
	}
}

func (m Model) providerProfile(name string) engine.ProviderProfileStatus {
	if m.eng == nil || m.eng.Config == nil {
		return engine.ProviderProfileStatus{Name: strings.TrimSpace(name)}
	}
	profile, ok := m.eng.Config.Providers.Profiles[strings.TrimSpace(name)]
	if !ok {
		return engine.ProviderProfileStatus{Name: strings.TrimSpace(name)}
	}
	return engine.ProviderProfileStatus{
		Name:       strings.TrimSpace(name),
		Model:      strings.TrimSpace(profile.Model),
		Protocol:   strings.TrimSpace(profile.Protocol),
		BaseURL:    strings.TrimSpace(profile.BaseURL),
		MaxTokens:  profile.MaxTokens,
		MaxContext: profile.MaxContext,
		Configured: strings.TrimSpace(profile.APIKey) != "" || strings.TrimSpace(profile.BaseURL) != "",
	}
}

func (m Model) availableTools() []string {
	if m.eng == nil {
		return nil
	}
	tools := append([]string(nil), m.eng.ListTools()...)
	sort.Strings(tools)
	return tools
}

func (m Model) toolDescription(name string) string {
	if m.eng == nil || m.eng.Tools == nil {
		return ""
	}
	tool, ok := m.eng.Tools.Get(name)
	if !ok {
		return ""
	}
	return strings.TrimSpace(tool.Description())
}

func (m Model) selectedTool() string {
	tools := m.availableTools()
	if len(tools) == 0 {
		return ""
	}
	if m.toolIndex < 0 {
		return tools[0]
	}
	if m.toolIndex >= len(tools) {
		return tools[len(tools)-1]
	}
	return tools[m.toolIndex]
}

func (m Model) toolPresetSummary(name string) string {
	if custom := strings.TrimSpace(m.toolOverride(name)); custom != "" {
		return custom
	}
	return m.defaultToolPreset(name)
}

func (m Model) defaultToolPreset(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "list_dir":
		target := blankFallback(m.toolTargetDir(), ".")
		return fmt.Sprintf("path=%s max_entries=80", target)
	case "read_file":
		target := m.toolTargetFile()
		if target == "" {
			return "select or pin a file first"
		}
		return fmt.Sprintf("path=%s line_start=1 line_end=200", target)
	case "grep_codebase":
		pattern := m.toolGrepPattern()
		if pattern == "" {
			return "type a search term in chat input or select a file first"
		}
		return fmt.Sprintf("pattern=%q max_results=40", pattern)
	case "write_file":
		return `path=tmp/demo.txt content="hello from tui" overwrite=true create_dirs=true`
	case "edit_file":
		target := m.toolTargetFile()
		if target == "" {
			target = "path/to/file.txt"
		}
		return fmt.Sprintf(`path=%s old_string="old" new_string="new" replace_all=false`, target)
	case "run_command":
		if preset := strings.TrimSpace(m.recommendedRunCommandPreset()); preset != "" {
			return preset
		}
		return `command=go args="version" dir=. timeout_ms=10000`
	default:
		return "no preset available"
	}
}

func (m Model) toolPresetParams(name string) (map[string]any, error) {
	if custom := strings.TrimSpace(m.toolOverride(name)); custom != "" {
		return parseToolParamString(custom)
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "list_dir":
		return map[string]any{
			"path":        m.toolTargetDir(),
			"max_entries": 80,
		}, nil
	case "read_file":
		target := m.toolTargetFile()
		if target == "" {
			return nil, fmt.Errorf("select or pin a file before running read_file")
		}
		return map[string]any{
			"path":       target,
			"line_start": 1,
			"line_end":   200,
		}, nil
	case "grep_codebase":
		pattern := m.toolGrepPattern()
		if pattern == "" {
			return nil, fmt.Errorf("type a search term in chat input or select a file first")
		}
		return map[string]any{
			"pattern":     pattern,
			"max_results": 40,
		}, nil
	case "run_command":
		if preset := strings.TrimSpace(m.toolPresetSummary(name)); preset != "" && preset != "no preset available" {
			return parseToolParamString(preset)
		}
		return nil, fmt.Errorf("no preset runner for %s", name)
	case "write_file", "edit_file":
		return nil, fmt.Errorf("press e to edit params before running %s", name)
	default:
		return nil, fmt.Errorf("no preset runner for %s", name)
	}
}

func (m Model) toolOverride(name string) string {
	if m.toolOverrides == nil {
		return ""
	}
	return strings.TrimSpace(m.toolOverrides[strings.TrimSpace(name)])
}

func (m Model) toolTargetFile() string {
	if pinned := strings.TrimSpace(m.pinnedFile); pinned != "" {
		return pinned
	}
	if selected := strings.TrimSpace(m.selectedFile()); selected != "" {
		return selected
	}
	if preview := strings.TrimSpace(m.filePath); preview != "" {
		return preview
	}
	return ""
}

func (m Model) toolTargetDir() string {
	target := m.toolTargetFile()
	if target == "" {
		return "."
	}
	dir := filepath.ToSlash(filepath.Dir(target))
	if dir == "." || dir == "" {
		return "."
	}
	return dir
}

func (m Model) toolGrepPattern() string {
	raw := strings.TrimSpace(m.input)
	if raw != "" && !strings.HasPrefix(raw, "/") {
		return regexp.QuoteMeta(truncateSingleLine(raw, 80))
	}
	target := m.toolTargetFile()
	if target == "" {
		return ""
	}
	base := filepath.Base(target)
	ext := filepath.Ext(base)
	base = strings.TrimSuffix(base, ext)
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}
	return regexp.QuoteMeta(base)
}

func (m Model) statusCommandSummary() string {
	st := m.status
	if m.eng != nil {
		st = m.eng.Status()
	}
	parts := []string{
		fmt.Sprintf("State: %v", st.State),
		fmt.Sprintf("Provider/Model: %s / %s", blankFallback(st.Provider, "-"), blankFallback(st.Model, "-")),
		fmt.Sprintf("Project: %s", blankFallback(st.ProjectRoot, "(none)")),
		fmt.Sprintf("AST: %s", blankFallback(st.ASTBackend, "-")),
	}
	if summary := formatProviderProfileSummaryTUI(st.ProviderProfile); summary != "" {
		parts = append(parts, "Profile: "+summary)
	}
	if summary := formatContextInSummaryTUI(st.ContextIn); summary != "" {
		parts = append(parts, "Context In: "+summary)
	}
	if why := formatContextInReasonSummaryTUI(st.ContextIn); why != "" {
		parts = append(parts, "Context Why: "+why)
	}
	return strings.Join(parts, "\n")
}

func (m Model) contextCommandSummary() string {
	recent := []string{}
	st := m.status
	if m.eng != nil {
		st = m.eng.Status()
		recent = append(recent, m.eng.MemoryWorking().RecentFiles...)
	}
	parts := []string{
		"Pinned: " + blankFallback(strings.TrimSpace(m.pinnedFile), "(none)"),
	}
	if len(recent) == 0 {
		parts = append(parts, "Recent context files: (none)")
	} else {
		parts = append(parts, "Recent context files: "+strings.Join(recent, ", "))
	}
	if summary := formatContextInSummaryTUI(m.status.ContextIn); summary != "" {
		parts = append(parts, "Last Context In: "+summary)
	}
	if why := formatContextInReasonSummaryTUI(st.ContextIn); why != "" {
		parts = append(parts, "Why: "+why)
	}
	if files := formatContextInTopFilesTUI(st.ContextIn, 3); files != "" {
		parts = append(parts, "Top files: "+files)
	}
	return strings.Join(parts, "\n")
}

func (m Model) contextCommandWhySummary() string {
	st := m.status
	if m.eng != nil {
		st = m.eng.Status()
	}
	report := st.ContextIn
	parts := []string{"Context why report:"}
	if report == nil {
		parts = append(parts, "No context report available yet.")
		return strings.Join(parts, "\n")
	}
	if len(report.Reasons) == 0 {
		parts = append(parts, "No explicit context reasons were recorded.")
		return strings.Join(parts, "\n")
	}
	for i, reason := range report.Reasons {
		if i >= 8 {
			parts = append(parts, fmt.Sprintf("... %d more reason(s)", len(report.Reasons)-i))
			break
		}
		reason = strings.TrimSpace(reason)
		if reason == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%d. %s", i+1, reason))
	}
	return strings.Join(parts, "\n")
}

func (m Model) contextCommandDetailedSummary() string {
	recent := []string{}
	st := m.status
	if m.eng != nil {
		st = m.eng.Status()
		recent = append(recent, m.eng.MemoryWorking().RecentFiles...)
	}
	report := st.ContextIn
	parts := []string{
		"Context report:",
		"Provider/Model: " + blankFallback(st.Provider, "-") + " / " + blankFallback(st.Model, "-"),
		"Pinned: " + blankFallback(strings.TrimSpace(m.pinnedFile), "(none)"),
	}
	if len(recent) == 0 {
		parts = append(parts, "Recent context files: (none)")
	} else {
		parts = append(parts, "Recent context files: "+strings.Join(recent, ", "))
	}
	if report == nil {
		parts = append(parts, "No context build report available yet.")
		return strings.Join(parts, "\n")
	}
	parts = append(parts,
		"Summary: "+blankFallback(formatContextInSummaryTUI(report), "-"),
		fmt.Sprintf("Runtime cap: provider_ctx=%d available_ctx=%d", report.ProviderMaxContext, report.ContextAvailable),
		fmt.Sprintf("Flags: include_tests=%t include_docs=%t compression=%s", report.IncludeTests, report.IncludeDocs, blankFallback(strings.TrimSpace(report.Compression), "-")),
	)
	if why := formatContextInReasonSummaryTUI(report); why != "" {
		parts = append(parts, "Why summary: "+why)
	}
	details := formatContextInDetailedFileLinesTUI(report, 6)
	if len(details) == 0 {
		parts = append(parts, "File evidence: (none)")
	} else {
		parts = append(parts, "File evidence:")
		for _, line := range details {
			parts = append(parts, " - "+line)
		}
	}
	return strings.Join(parts, "\n")
}

// Patch Lab (renderPatchView, patchCommandSummary, loadLatestPatchCmd,
// applyPatchCmd, focusPatchFile, shiftPatchTarget/Hunk, the patch*
// Model accessors, annotateAssistant{Patch,ToolUsage},
// matchAssistantConversationMessage, markLatestPatchInTranscript)
// lives in patch_view.go.

func (m Model) handleFilesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r", "alt+r":
		return m, loadFilesCmd(m.eng)
	case "down", "j", "alt+j":
		if len(m.files) == 0 {
			return m, nil
		}
		if m.fileIndex < len(m.files)-1 {
			m.fileIndex++
		}
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())
	case "up", "k", "alt+k":
		if len(m.files) == 0 {
			return m, nil
		}
		if m.fileIndex > 0 {
			m.fileIndex--
		}
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())
	case "enter":
		if len(m.files) == 0 {
			return m, nil
		}
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())
	case "p", "alt+p":
		return m.togglePinnedFile()
	case "i", "alt+i":
		return m.focusChatWithFileMarker(m.selectedFile(), "")
	case "e", "alt+e":
		return m.focusChatWithFileMarker(m.selectedFile(), "Explain")
	case "v", "alt+v":
		return m.focusChatWithFileMarker(m.selectedFile(), "Review")
	}
	return m, nil
}

func (m Model) handleSetupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	providers := m.availableProviders()
	if len(providers) == 0 {
		return m, nil
	}
	m.setupIndex = clampIndex(m.setupIndex, len(providers))
	if m.setupEditing {
		switch msg.Type {
		case tea.KeyRunes:
			m.setupDraft += string(msg.Runes)
			return m, nil
		case tea.KeySpace:
			m.setupDraft += " "
			return m, nil
		case tea.KeyBackspace, tea.KeyCtrlH:
			runes := []rune(m.setupDraft)
			if len(runes) > 0 {
				m.setupDraft = string(runes[:len(runes)-1])
			}
			return m, nil
		case tea.KeyEnter:
			target := providers[m.setupIndex]
			model := strings.TrimSpace(m.setupDraft)
			if model == "" {
				model = m.defaultModelForProvider(target)
			}
			m = m.applyProviderModelSelection(target, model)
			m.setupEditing = false
			m.setupDraft = ""
			m.notice = fmt.Sprintf("Setup applied: %s (%s)", target, blankFallback(model, "-"))
			m = m.appendSystemMessage(fmt.Sprintf("Setup applied: provider=%s model=%s", target, blankFallback(model, "-")))
			return m, loadStatusCmd(m.eng)
		case tea.KeyEsc:
			m.setupEditing = false
			m.setupDraft = ""
			m.notice = "Setup edit cancelled."
			return m, nil
		}
		return m, nil
	}
	switch msg.String() {
	case "down", "j", "alt+j":
		if m.setupIndex < len(providers)-1 {
			m.setupIndex++
		}
		m.notice = "Setup selection: " + providers[m.setupIndex]
		return m, nil
	case "up", "k", "alt+k":
		if m.setupIndex > 0 {
			m.setupIndex--
		}
		m.notice = "Setup selection: " + providers[m.setupIndex]
		return m, nil
	case "m", "alt+m":
		selected := providers[m.setupIndex]
		m.setupEditing = true
		m.setupDraft = m.defaultModelForProvider(selected)
		m.notice = "Editing model for " + selected
		return m, nil
	case "enter":
		target := providers[m.setupIndex]
		model := m.defaultModelForProvider(target)
		m = m.applyProviderModelSelection(target, model)
		m.notice = fmt.Sprintf("Setup applied: %s (%s)", target, blankFallback(model, "-"))
		m = m.appendSystemMessage(fmt.Sprintf("Setup applied: provider=%s model=%s", target, blankFallback(model, "-")))
		return m, loadStatusCmd(m.eng)
	case "s", "alt+s":
		target := providers[m.setupIndex]
		model := m.defaultModelForProvider(target)
		m = m.applyProviderModelSelection(target, model)
		path, err := m.persistProviderModelProjectConfig(target, model)
		if err != nil {
			m.notice = "setup save: " + err.Error()
			m = m.appendSystemMessage(fmt.Sprintf("Setup save failed: %v", err))
			return m, nil
		}
		m.notice = "Setup saved: " + filepath.ToSlash(path)
		m = m.appendSystemMessage(fmt.Sprintf("Setup saved: provider=%s model=%s path=%s", target, blankFallback(model, "-"), filepath.ToSlash(path)))
		return m, loadStatusCmd(m.eng)
	case "r", "alt+r":
		if err := m.reloadEngineConfig(); err != nil {
			m.notice = "setup reload: " + err.Error()
			m = m.appendSystemMessage("Setup reload failed: " + err.Error())
			return m, nil
		}
		m.notice = "Setup runtime reloaded."
		m = m.appendSystemMessage(fmt.Sprintf("Setup runtime reloaded: provider=%s model=%s", blankFallback(m.status.Provider, "-"), blankFallback(m.status.Model, "-")))
		return m, loadStatusCmd(m.eng)
	}
	return m, nil
}

func (m Model) handleToolsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	tools := m.availableTools()
	if len(tools) == 0 {
		m.notice = "No tools registered."
		return m, nil
	}
	m.toolIndex = clampIndex(m.toolIndex, len(tools))
	if m.toolEditing {
		switch msg.Type {
		case tea.KeyRunes:
			m.toolDraft += string(msg.Runes)
			return m, nil
		case tea.KeySpace:
			m.toolDraft += " "
			return m, nil
		case tea.KeyBackspace, tea.KeyCtrlH:
			runes := []rune(m.toolDraft)
			if len(runes) > 0 {
				m.toolDraft = string(runes[:len(runes)-1])
			}
			return m, nil
		case tea.KeyEnter:
			name := tools[m.toolIndex]
			if m.toolOverrides == nil {
				m.toolOverrides = map[string]string{}
			}
			trimmed := strings.TrimSpace(m.toolDraft)
			if trimmed == "" {
				delete(m.toolOverrides, name)
				m.notice = "Tool params reset: " + name
			} else {
				m.toolOverrides[name] = trimmed
				m.notice = "Tool params saved: " + name
			}
			m.toolEditing = false
			return m, nil
		case tea.KeyEsc:
			m.toolEditing = false
			m.notice = "Tool edit cancelled."
			return m, nil
		}
		return m, nil
	}
	switch msg.String() {
	case "down", "j", "alt+j":
		if m.toolIndex < len(tools)-1 {
			m.toolIndex++
		}
		m.notice = "Tool selection: " + tools[m.toolIndex]
		return m, nil
	case "up", "k", "alt+k":
		if m.toolIndex > 0 {
			m.toolIndex--
		}
		m.notice = "Tool selection: " + tools[m.toolIndex]
		return m, nil
	case "e", "alt+e":
		name := tools[m.toolIndex]
		m.toolEditing = true
		m.toolDraft = m.toolPresetSummary(name)
		m.notice = "Editing params for " + name
		return m, nil
	case "x", "alt+x":
		name := tools[m.toolIndex]
		if m.toolOverrides != nil {
			delete(m.toolOverrides, name)
		}
		m.toolDraft = ""
		m.notice = "Reset params for " + name
		return m, nil
	case "enter", "r", "alt+r":
		name := tools[m.toolIndex]
		params, err := m.toolPresetParams(name)
		if err != nil {
			m.toolOutput = fmt.Sprintf("Tool: %s\nStatus: blocked\n\n%s", name, err.Error())
			m.notice = "tool preset: " + err.Error()
			return m, nil
		}
		m.notice = "Running tool: " + name
		return m, runToolCmd(m.eng, name, params)
	}
	return m, nil
}

func (m Model) View() string {
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
	planMode := m.planMode
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

func (m Model) renderActiveView(width int, height int, pal tabPaletteEntry) string {
	if height < 4 {
		height = 4
	}
	contentWidth := width - 6
	if contentWidth < 20 {
		contentWidth = 20
	}
	innerHeight := height - 4
	if innerHeight < 1 {
		innerHeight = 1
	}
	var content string
	switch m.tabs[m.activeTab] {
	case "Status":
		content = fitPanelContentHeight(m.renderStatusView(contentWidth), innerHeight)
	case "Files":
		content = fitPanelContentHeight(m.renderFilesViewSized(contentWidth, innerHeight), innerHeight)
	case "Patch":
		content = fitPanelContentHeight(m.renderPatchView(contentWidth), innerHeight)
	case "Setup":
		content = fitPanelContentHeight(m.renderSetupView(contentWidth), innerHeight)
	case "Tools":
		content = fitPanelContentHeight(m.renderToolsView(contentWidth), innerHeight)
	case "Activity":
		content = fitPanelContentHeight(m.renderActivityView(contentWidth), innerHeight)
	case "Memory":
		content = fitPanelContentHeight(m.renderMemoryView(contentWidth), innerHeight)
	case "CodeMap":
		content = fitPanelContentHeight(m.renderCodemapView(contentWidth), innerHeight)
	case "Conversations":
		content = fitPanelContentHeight(m.renderConversationsView(contentWidth), innerHeight)
	case "Prompts":
		content = fitPanelContentHeight(m.renderPromptsView(contentWidth), innerHeight)
	case "Security":
		content = fitPanelContentHeight(m.renderSecurityView(contentWidth), innerHeight)
	case "Plans":
		content = fitPanelContentHeight(m.renderPlansView(contentWidth), innerHeight)
	case "Context":
		content = fitPanelContentHeight(m.renderContextView(contentWidth), innerHeight)
	case "Providers":
		content = fitPanelContentHeight(m.renderProvidersView(contentWidth), innerHeight)
	default:
		// Chat view is special — the input box (tail) must never be hidden
		// or the user stops being able to type. We render head and tail
		// separately and clip only the head so the tail always surfaces.
		// The right-side stats panel takes a fixed width slice when visible;
		// chat body shrinks to make room.
		panelVisible := m.statsPanelVisible(contentWidth)
		chatWidth := contentWidth
		if panelVisible {
			chatWidth = contentWidth - statsPanelWidth - 2
		}
		parts := m.renderChatViewParts(chatWidth, panelVisible)
		body := fitChatBody(parts.Head, parts.Tail, innerHeight, m.chatScrollback)
		if panelVisible {
			panel := renderStatsPanel(m.statsPanelInfo(), innerHeight)
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, "  ", panel)
		}
		content = body
	}
	// Per-tab outer border colour. docStyle's hardcoded #2F4F6A read
	// as "always the same panel" regardless of which tab the user
	// was on — a cheap and effective tell that the screen has
	// changed is repainting the frame.
	frame := lipgloss.NewStyle().
		Padding(1, 2).
		Background(colorPanelBg).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Border)
	return frame.Width(width).Height(height).Render(content)
}

// fitChatBody lays out the chat view so the tail (input box + pickers)
// always stays visible, and the head (header + transcript) gets clipped
// from the top to fit the remaining space. scrollbackLines shifts the
// head window backwards by that many lines — the wheel and pgup keys
// feed into this. A "↑ N earlier lines" marker replaces the clipped top.
func fitChatBody(head, tail string, maxLines, scrollbackLines int) string {
	if maxLines <= 0 {
		return head + "\n" + tail
	}
	headLines := splitLines(head)
	tailLines := splitLines(tail)
	if len(tailLines) >= maxLines {
		// Pathological case — tail alone overflows. Let the caller's
		// outer docStyle deal with it; we bail out gracefully.
		return strings.Join(tailLines, "\n")
	}
	available := maxLines - len(tailLines)
	if available < 3 {
		available = 3
	}
	if scrollbackLines < 0 {
		scrollbackLines = 0
	}
	end := len(headLines) - scrollbackLines
	if end > len(headLines) {
		end = len(headLines)
	}
	if end < 1 {
		end = 1
	}
	start := end - available
	if start < 0 {
		start = 0
	}
	if end-start > available {
		start = end - available
	}
	window := append([]string{}, headLines[start:end]...)
	if start > 0 {
		// Replace the very first visible line with the scroll hint so we
		// don't inflate beyond `available` — keep the budget honest.
		hint := subtleStyle.Render(fmt.Sprintf("  ↑ %d earlier lines  ·  wheel, pgup, shift+up to scroll", start))
		window[0] = hint
	}
	if end < len(headLines) {
		// If we're scrolled back, add a bottom hint replacing the last line.
		hint := subtleStyle.Render(fmt.Sprintf("  ↓ %d newer lines  ·  pgdown, end, shift+down to resume", len(headLines)-end))
		window[len(window)-1] = hint
	}
	return strings.Join(window, "\n") + "\n" + strings.Join(tailLines, "\n")
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// chatViewParts captures the scrollable head and the always-visible tail
// of the chat view. renderActiveView composes them with fitChatBody.
type chatViewParts struct {
	Head string
	Tail string
}

func (m Model) renderChatView(width int) string {
	parts := m.renderChatViewParts(width, false)
	if parts.Tail == "" {
		return parts.Head
	}
	return parts.Head + "\n" + parts.Tail
}

// renderChatViewParts produces the chat surface split into the scrollable
// head (header + transcript) and the pinned tail (input box + pickers +
// streaming indicator). renderActiveView glues them back together with
// line-aware clipping so the input never hides. When slimHeader is true the
// stats panel is visible to the right and the chat header drops duplicated
// fields (provider/model/ctx/tools) that the panel owns.
func (m Model) renderChatViewParts(width int, slimHeader bool) chatViewParts {
	suggestions := m.buildChatSuggestionState()
	headerInfo := m.chatHeaderInfo()
	headerInfo.Slim = slimHeader
	header := renderChatHeader(headerInfo, min(width, 140))
	lines := []string{
		header,
		renderDivider(min(width, 140)),
		"",
	}
	if len(m.transcript) == 0 {
		lines = append(lines, renderStarterPrompts(min(width, 120), headerInfo.Configured)...)
	}
	// assistantCounter tracks the 1-based ordinal of each assistant
	// message in the transcript so the renderer can stamp each one with
	// a `#N` chip. That chip is the integer the user passes to `/copy N`
	// to move a specific response to the clipboard.
	assistantCounter := 0
	for i, item := range m.transcript {
		if i > 0 {
			lines = append(lines, "")
		}
		durationMs := item.DurationMs
		if m.streamIndex == i && m.sending && !m.streamStartedAt.IsZero() {
			durationMs = int(time.Since(m.streamStartedAt).Milliseconds())
		}
		copyIdx := 0
		if strings.EqualFold(item.Role, "assistant") {
			assistantCounter++
			copyIdx = assistantCounter
		}
		hdr := renderMessageHeader(messageHeaderInfo{
			Role:         item.Role,
			Timestamp:    item.Timestamp,
			TokenCount:   item.TokenCount,
			DurationMs:   durationMs,
			ToolCalls:    item.ToolCalls,
			ToolFailures: item.ToolFailures,
			Streaming:    m.streamIndex == i && m.sending,
			SpinnerFrame: m.spinnerFrame,
			CopyIndex:    copyIdx,
		})
		content := chatBubbleContent(item, m.streamIndex == i && m.sending)
		lines = append(lines, renderMessageBubble(item.Role, content, hdr, width))
		if strings.EqualFold(item.Role, "assistant") {
			if strip := renderInlineToolChips(item.ToolChips, width); strip != "" {
				lines = append(lines, strip)
			}
			if summary := m.chatPatchSummary(item); summary != "" {
				lines = append(lines, subtleStyle.Render("    "+summary))
			}
		}
	}
	if m.agentLoopActive {
		// When the stats panel is visible it owns tool rounds / last tool; the
		// inline runtime card would just echo it, so skip the card and only
		// keep the context-scope hint (panel has no room for prose).
		if !slimHeader {
			card := renderRuntimeCard(runtimeSummary{
				Active:       m.agentLoopActive,
				Phase:        m.agentLoopPhase,
				Step:         m.agentLoopStep,
				MaxSteps:     m.agentLoopMaxToolStep,
				ToolRounds:   m.agentLoopToolRounds,
				LastTool:     m.agentLoopLastTool,
				LastStatus:   m.agentLoopLastStatus,
				LastDuration: m.agentLoopLastDuration,
				Provider:     m.agentLoopProvider,
				Model:        m.agentLoopModel,
			}, min(width, 120))
			if strings.TrimSpace(card) != "" {
				lines = append(lines, "", card)
			}
		}
		if scope := strings.TrimSpace(m.agentLoopContextScope); scope != "" {
			lines = append(lines, subtleStyle.Render(truncateSingleLine("  "+scope, width)))
		}
	}

	head := strings.Join(lines, "\n")

	// Tail — input box + pickers + streaming indicator. Built as its own
	// buffer so fitChatBody can keep it pinned at the bottom of the
	// rendered viewport regardless of how long the transcript grows.
	tailLines := []string{}
	if m.showHelpOverlay {
		tailLines = append(tailLines, "", m.renderHelpOverlay(min(width, 120)))
	}
	if m.resumePromptActive && !m.sending {
		tailLines = append(tailLines, "", renderResumeBanner(m.agentLoopStep, m.agentLoopMaxToolStep, min(width, 100)))
	}
	inputLine := renderChatInputLine(m.input, m.chatCursor, m.chatCursorManual, m.chatCursorInput, m.sending)
	tailLines = append(tailLines, "", sectionHeader("›", "Input"), renderInputBox(inputLine, min(width, 100)))

	// Approval modal — highest priority: if the agent has asked for
	// permission to run a gated tool we draw a blocking card right below
	// the composer, and suppress every other picker/strip until the user
	// resolves it. Rendered before pickers so it always wins real estate.
	if m.pendingApproval != nil {
		tailLines = append(tailLines, "", renderApprovalModal(m.pendingApproval, min(width-2, 110)))
	}

	// Picker priority: when @ or / picker is active, it must be the dominant
	// thing below the composer. Earlier versions rendered the context strip,
	// slashAssistHints and quickActions first, pushing the @ modal off-screen
	// in short terminals — users reported the picker "doesn't work" when in
	// fact it was rendering below the fold. Now the active picker owns the
	// real estate directly under the input and all other tail decoration is
	// suppressed until the user dismisses or commits it.
	pickerActive := m.pendingApproval != nil || suggestions.mentionActive || suggestions.slashMenuActive || m.commandPickerActive
	if suggestions.mentionActive {
		tailLines = append(tailLines, "", renderMentionPickerModal(suggestions, m.mentionIndex, len(m.files), min(width-2, 110)))
	} else if suggestions.slashMenuActive {
		tailLines = append(tailLines, "", renderSlashPickerModal(suggestions.slashCommands, m.slashIndex, min(width-2, 110)))
	}

	if !pickerActive {
		if strip := m.renderContextStrip(min(width, 120)); strip != "" {
			tailLines = append(tailLines, strip)
		}
	}
	lines = tailLines
	if m.commandPickerActive {
		kind := strings.TrimSpace(strings.ToLower(m.commandPickerKind))
		title := "Command Picker"
		switch kind {
		case "provider":
			title = "Provider Picker"
		case "model":
			title = "Model Picker"
		case "tool":
			title = "Tool Picker"
		case "read":
			title = "Read Picker"
		case "run":
			title = "Run Picker"
		case "grep":
			title = "Grep Picker"
		}
		mode := "session"
		if m.commandPickerPersist {
			mode = "persist → .dfmc/config.yaml"
		}
		lines = append(lines, sectionTitleStyle.Render(title))
		lines = append(lines, subtleStyle.Render("↑↓ move · tab cycle · enter apply · ctrl+s "+mode+" · esc close"))
		if query := strings.TrimSpace(m.commandPickerQuery); query != "" {
			lines = append(lines, subtleStyle.Render("filter: "+query))
		}
		items := m.filteredCommandPickerItems()
		if len(items) == 0 {
			if strings.EqualFold(kind, "model") && strings.TrimSpace(m.commandPickerQuery) != "" {
				lines = append(lines, "  "+subtleStyle.Render("No known model matched. Enter applies typed value: "+strings.TrimSpace(m.commandPickerQuery)))
			} else if (strings.EqualFold(kind, "tool") || strings.EqualFold(kind, "read") || strings.EqualFold(kind, "run") || strings.EqualFold(kind, "grep")) && strings.TrimSpace(m.commandPickerQuery) != "" {
				lines = append(lines, "  "+subtleStyle.Render("No exact match. Enter prepares typed value: "+strings.TrimSpace(m.commandPickerQuery)))
			} else {
				lines = append(lines, "  "+subtleStyle.Render("No matching entries."))
			}
		} else {
			selected := clampIndex(m.commandPickerIndex, len(items))
			start := 0
			if selected > 4 {
				start = selected - 4
			}
			end := start + 8
			if end > len(items) {
				end = len(items)
			}
			for i := start; i < end; i++ {
				prefix := "  "
				label := truncateSingleLine(formatCommandPickerItem(items[i]), width)
				if i == selected {
					prefix = "> "
					label = titleStyle.Render(label)
				}
				lines = append(lines, prefix+label)
			}
		}
	}
	// Non-picker tail decoration. Gated on pickerActive so the @ / slash
	// modals aren't competing with Slash Assist hints, Command args, or
	// Quick actions — those can reappear when the picker closes.
	if !pickerActive {
		if len(suggestions.slashArgSuggestions) > 0 {
			lines = append(lines, sectionTitleStyle.Render("Command args"))
			lines = append(lines, subtleStyle.Render("↑↓ move · tab fill"))
			selected := clampIndex(m.slashArgIndex, len(suggestions.slashArgSuggestions))
			start := 0
			if selected > 4 {
				start = selected - 4
			}
			end := start + 6
			if end > len(suggestions.slashArgSuggestions) {
				end = len(suggestions.slashArgSuggestions)
			}
			for i := start; i < end; i++ {
				prefix := "  "
				label := truncateSingleLine(suggestions.slashArgSuggestions[i], width)
				if i == selected {
					prefix = "> "
					label = titleStyle.Render(label)
				}
				lines = append(lines, prefix+label)
			}
		}
		if hints := m.slashAssistHints(); len(hints) > 0 {
			lines = append(lines, sectionTitleStyle.Render("Slash Assist"))
			for _, hint := range hints {
				hint = truncateSingleLine(strings.TrimSpace(hint), width)
				if hint == "" {
					continue
				}
				lines = append(lines, "  "+subtleStyle.Render(hint))
			}
		}
		if len(suggestions.quickActions) > 0 {
			lines = append(lines, sectionTitleStyle.Render("Quick actions"))
			lines = append(lines, subtleStyle.Render("↑↓ move · tab cycle · enter run"))
			selected := clampIndex(m.quickActionIndex, len(suggestions.quickActions))
			for i, action := range suggestions.quickActions {
				prefix := "  "
				label := truncateSingleLine(action.PreparedInput, width)
				if i == selected {
					prefix = "> "
					label = titleStyle.Render(label)
				}
				lines = append(lines, prefix+label)
				if reason := strings.TrimSpace(action.Reason); reason != "" {
					lines = append(lines, "  "+subtleStyle.Render(truncateSingleLine(reason, width)))
				}
			}
		}
	}
	if m.sending {
		phase := "drafting reply"
		if m.agentLoopActive {
			if p := strings.TrimSpace(m.agentLoopPhase); p != "" {
				phase = p
			}
		}
		lines = append(lines, "", renderStreamingIndicator(phase, m.spinnerFrame))
	}
	tail := strings.Join(lines, "\n")
	return chatViewParts{Head: head, Tail: tail}
}

// chatHeaderInfo snapshots the pieces of engine.Status + agent-loop state
// into the compact bundle renderChatHeader consumes.
func (m Model) chatHeaderInfo() chatHeaderInfo {
	provider := strings.TrimSpace(m.status.Provider)
	model := strings.TrimSpace(m.status.Model)
	maxCtx := m.status.ProviderProfile.MaxContext
	configured := m.status.ProviderProfile.Configured
	tokens := 0
	if m.status.ContextIn != nil {
		tokens = m.status.ContextIn.TokenCount
		if maxCtx == 0 && m.status.ContextIn.ProviderMaxContext > 0 {
			maxCtx = m.status.ContextIn.ProviderMaxContext
		}
	}
	toolsEnabled := m.eng != nil && m.eng.Tools != nil
	parked := m.eng != nil && m.eng.HasParkedAgent()
	gated := false
	if m.eng != nil && m.eng.Config != nil {
		gated = len(m.eng.Config.Tools.RequireApproval) > 0
	}
	return chatHeaderInfo{
		Provider:        provider,
		Model:           model,
		Configured:      configured || strings.EqualFold(provider, "offline"),
		MaxContext:      maxCtx,
		ContextTokens:   tokens,
		Pinned:          strings.TrimSpace(m.pinnedFile),
		ToolsEnabled:    toolsEnabled,
		Streaming:       m.sending,
		AgentActive:     m.agentLoopActive,
		AgentPhase:      m.agentLoopPhase,
		AgentStep:       m.agentLoopStep,
		AgentMax:        m.agentLoopMaxToolStep,
		QueuedCount:     len(m.pendingQueue),
		Parked:          parked,
		PendingNotes:    m.pendingNoteCount,
		ActiveTools:     m.activeToolCount,
		ActiveSubagents: m.activeSubagentCount,
		PlanMode:        m.planMode,
		ApprovalGated:   gated,
		ApprovalPending: m.pendingApproval != nil,
	}
}

// statsPanelInfo folds every stat the right-hand panel needs into a single
// snapshot struct. Kept on Model so the renderer stays pure.
func (m Model) statsPanelInfo() statsPanelInfo {
	head := m.chatHeaderInfo()
	elapsed := time.Duration(0)
	if !m.sessionStart.IsZero() {
		elapsed = time.Since(m.sessionStart)
	}
	toolCount := 0
	if m.eng != nil && m.eng.Tools != nil {
		toolCount = len(m.availableTools())
	}
	return statsPanelInfo{
		Provider:       head.Provider,
		Model:          head.Model,
		Configured:     head.Configured,
		ContextTokens:  head.ContextTokens,
		MaxContext:     head.MaxContext,
		Streaming:      head.Streaming,
		AgentActive:    head.AgentActive,
		AgentPhase:     head.AgentPhase,
		AgentStep:      head.AgentStep,
		AgentMaxSteps:  head.AgentMax,
		ToolRounds:     m.agentLoopToolRounds,
		LastTool:       m.agentLoopLastTool,
		LastStatus:     m.agentLoopLastStatus,
		LastDurationMs: m.agentLoopLastDuration,
		Parked:         head.Parked,
		QueuedCount:    head.QueuedCount,
		PendingNotes:   head.PendingNotes,
		ToolsEnabled:   head.ToolsEnabled,
		ToolCount:      toolCount,
		Branch:         m.gitInfo.Branch,
		Dirty:          m.gitInfo.Dirty,
		Detached:       m.gitInfo.Detached,
		Inserted:       m.gitInfo.Inserted,
		Deleted:        m.gitInfo.Deleted,
		SessionElapsed:        elapsed,
		MessageCount:          len(m.transcript),
		Pinned:                head.Pinned,
		CompressionSavedChars: m.compressionSavedChars,
		CompressionRawChars:   m.compressionRawChars,
	}
}

// statsPanelVisible returns true when the chat tab should render the
// right-side panel alongside the chat body. Driven by the ctrl+s toggle and
// a minimum-width guard so narrow terminals don't get squeezed.
func (m Model) statsPanelVisible(contentWidth int) bool {
	return m.showStatsPanel && contentWidth >= statsPanelMinContentWidth
}

func (m Model) renderStatusView(width int) string {
	inner := min(width, 80)
	divider := renderDivider(inner)
	group := func(icon, title string, rows []string) []string {
		out := []string{accentStyle.Bold(true).Render(icon) + " " + sectionTitleStyle.Render(strings.ToUpper(title))}
		for _, r := range rows {
			if strings.TrimSpace(r) == "" {
				continue
			}
			out = append(out, "  "+truncateForPanel(r, width-2))
		}
		return out
	}
	parts := []string{
		sectionHeader("◉", "Status"),
		subtleStyle.Render("r refresh · ctrl+h keys"),
		divider,
		"",
	}
	parts = append(parts, group("◉", "Project", []string{
		"Root:     " + blankFallback(m.status.ProjectRoot, "(none)"),
	})...)
	parts = append(parts, "")
	parts = append(parts, group("⎈", "Provider", []string{
		"Provider: " + blankFallback(m.status.Provider, "-") + " / " + blankFallback(m.status.Model, "-"),
		"Profile:  " + formatProviderProfileSummaryTUI(m.status.ProviderProfile),
		"Runtime:  " + providerConnectivityHintTUI(m.status),
		"Catalog:  " + formatModelsDevCacheSummaryTUI(m.status.ModelsDevCache),
	})...)
	parts = append(parts, "")
	parts = append(parts, group("≡", "AST", []string{
		"Backend:  " + blankFallback(m.status.ASTBackend, "-"),
		"Langs:    " + formatASTLanguageSummaryTUI(m.status.ASTLanguages),
		"Metrics:  " + formatASTMetricsSummaryTUI(m.status.ASTMetrics),
		"CodeMap:  " + formatCodeMapMetricsSummaryTUI(m.status.CodeMap),
	})...)
	// Memory panel: only render when degraded, to keep the Status
	// view terse in the healthy common case. A banner-style single
	// line is louder than a dedicated group when something's wrong.
	if m.status.MemoryDegraded {
		reason := strings.TrimSpace(m.status.MemoryLoadErr)
		if reason == "" {
			reason = "load failed"
		}
		parts = append(parts, "", warnStyle.Render("⚠ memory degraded — "+reason+" (running with empty store)"))
	}
	if summary := formatContextInSummaryTUI(m.status.ContextIn); summary != "" {
		parts = append(parts, "")
		rows := []string{"Last:     " + summary}
		if why := formatContextInReasonSummaryTUI(m.status.ContextIn); why != "" {
			rows = append(rows, "Why:      "+why)
		}
		if files := formatContextInTopFilesTUI(m.status.ContextIn, 3); files != "" {
			rows = append(rows, "Top:      "+files)
		}
		if details := formatContextInDetailedFileLinesTUI(m.status.ContextIn, 2); len(details) > 0 {
			for _, line := range details {
				rows = append(rows, "File:     "+line)
			}
		}
		parts = append(parts, group("▦", "Context In", rows)...)
	}
	if note := strings.TrimSpace(m.notice); note != "" {
		parts = append(parts, "", subtleStyle.Render(note))
	}
	return strings.Join(parts, "\n")
}

func (m Model) renderFilesView(width int) string {
	// Backwards-compatible default for tests that call without a height —
	// 24 rows roughly matches a stock terminal page so the visible list
	// stays close to the historic "14 entries + chrome" output.
	return m.renderFilesViewSized(width, 24)
}

// renderFilesViewSized renders the Files tab with the list + preview both
// scaled to the available vertical space. The previous fixed 14-row /
// 18-line caps left huge dead zones on tall terminals (1080p / 4K). Here
// the list grows to fill height-6 rows, and the preview gets the matching
// budget so the right pane uses the page instead of stranding 60% empty.
func (m Model) renderFilesViewSized(width, height int) string {
	listWidth := width / 3
	if listWidth < 28 {
		listWidth = 28
	}
	if listWidth > width-24 {
		listWidth = width / 2
	}
	previewWidth := width - listWidth - 3
	if previewWidth < 20 {
		previewWidth = 20
	}

	// Reserve rows for: section header (1), legend (1), divider (1),
	// blank (1), trailing blank+counter (2). Anything else is for entries.
	const listChrome = 6
	listRows := height - listChrome
	if listRows < 8 {
		listRows = 8
	}
	// Same budget for the preview pane — chrome is header (1), path (1),
	// divider (1), blank (1), and the trailing size line + blank (2).
	const previewChrome = 6
	previewRows := height - previewChrome
	if previewRows < 8 {
		previewRows = 8
	}

	listLines := []string{
		sectionHeader("▦", "Files"),
		subtleStyle.Render("j/k move · enter preview · r reload · p pin · i/e/v chat actions · ctrl+h keys"),
		renderDivider(listWidth - 2),
		"",
	}
	if len(m.files) == 0 {
		listLines = append(listLines,
			warnStyle.Render("No indexed project files yet."),
			"",
			subtleStyle.Render("Try one of these:"),
			subtleStyle.Render("  • switch to Chat and run ")+codeStyle.Render("/analyze"),
			subtleStyle.Render("  • press ")+codeStyle.Render("r")+subtleStyle.Render(" to refresh the file index"),
			subtleStyle.Render("  • confirm you launched ")+codeStyle.Render("dfmc")+subtleStyle.Render(" from a project root"),
		)
	} else {
		// Center the cursor inside the visible window: half the rows
		// above, half below. Pin to bounds at edges.
		half := listRows / 2
		start := m.fileIndex - half
		if start < 0 {
			start = 0
		}
		end := start + listRows
		if end > len(m.files) {
			end = len(m.files)
			start = end - listRows
			if start < 0 {
				start = 0
			}
		}
		for i := start; i < end; i++ {
			prefix := "  "
			label := truncateSingleLine(m.files[i], listWidth-4)
			if m.files[i] == strings.TrimSpace(m.pinnedFile) {
				label = "[p] " + label
			}
			if i == m.fileIndex {
				prefix = "> "
				label = titleStyle.Render(label)
			}
			listLines = append(listLines, prefix+label)
		}
		listLines = append(listLines, "", subtleStyle.Render(fmt.Sprintf("%d/%d files", m.fileIndex+1, len(m.files))))
	}

	previewLines := []string{
		sectionHeader("❐", "Preview"),
		subtleStyle.Render(blankFallback(m.filePath, "Select a file")),
		renderDivider(previewWidth - 2),
		"",
	}
	if strings.TrimSpace(m.filePath) != "" && m.filePath == strings.TrimSpace(m.pinnedFile) {
		previewLines = append(previewLines, subtleStyle.Render("Pinned for chat context"), "")
	}
	content := truncateForPanelSized(m.filePreview, previewWidth, previewRows)
	if content == "" {
		content = subtleStyle.Render("No preview loaded.")
	}
	previewLines = append(previewLines, content)
	if m.fileSize > 0 {
		previewLines = append(previewLines, "", subtleStyle.Render(fmt.Sprintf("size=%d bytes", m.fileSize)))
	}

	left := lipgloss.NewStyle().Width(listWidth).Render(strings.Join(listLines, "\n"))
	right := lipgloss.NewStyle().Width(previewWidth).Render(strings.Join(previewLines, "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right)
}


func (m Model) renderSetupView(width int) string {
	providers := m.availableProviders()
	m.setupIndex = clampIndex(m.setupIndex, len(providers))

	listWidth := width / 3
	if listWidth < 24 {
		listWidth = 24
	}
	if listWidth > width-24 {
		listWidth = width / 2
	}
	detailWidth := width - listWidth - 3
	if detailWidth < 20 {
		detailWidth = 20
	}

	listLines := []string{
		sectionHeader("⚙", "Setup"),
		subtleStyle.Render("enter apply · m edit model · s save · ctrl+h keys"),
		renderDivider(listWidth - 2),
		"",
	}
	if len(providers) == 0 {
		listLines = append(listLines,
			warnStyle.Render("No providers configured."),
			"",
			subtleStyle.Render("Get online in under a minute:"),
			subtleStyle.Render("  • set ")+codeStyle.Render("ANTHROPIC_API_KEY")+subtleStyle.Render(", ")+codeStyle.Render("OPENAI_API_KEY")+subtleStyle.Render(", or ")+codeStyle.Render("DEEPSEEK_API_KEY"),
			subtleStyle.Render("  • then run ")+codeStyle.Render("dfmc config sync-models")+subtleStyle.Render(" to refresh the catalog"),
			subtleStyle.Render("  • or keep using ")+accentStyle.Render("offline")+subtleStyle.Render(" provider for local analysis"),
		)
	} else {
		for i, name := range providers {
			prefix := "  "
			label := truncateSingleLine(name, listWidth-4)
			if i == m.setupIndex {
				prefix = "> "
				label = titleStyle.Render(label)
			}
			if strings.EqualFold(name, m.currentProvider()) {
				label += subtleStyle.Render("  (active)")
			}
			listLines = append(listLines, prefix+label)
		}
	}

	detailLines := []string{
		sectionHeader("◉", "Selection"),
		renderDivider(detailWidth - 2),
	}
	if len(providers) == 0 {
		detailLines = append(detailLines, subtleStyle.Render("Provider config unavailable."))
	} else {
		selected := providers[m.setupIndex]
		model := m.defaultModelForProvider(selected)
		profile := m.providerProfile(selected)
		detailLines = append(detailLines,
			fmt.Sprintf("Provider: %s", selected),
			fmt.Sprintf("Model:    %s", blankFallback(model, "-")),
			fmt.Sprintf("Protocol: %s", blankFallback(profile.Protocol, "-")),
			fmt.Sprintf("Context:  %s tokens", formatToolTokenCount(profile.MaxContext)),
			fmt.Sprintf("Output:   %s tokens", formatToolTokenCount(profile.MaxTokens)),
			fmt.Sprintf("Endpoint: %s", blankFallback(profile.BaseURL, "(default)")),
			"",
			subtleStyle.Render("enter applies · s saves to .dfmc/config.yaml · slash: /provider /model"),
		)
		if m.setupEditing {
			draft := m.setupDraft
			if draft == "" {
				draft = model
			}
			detailLines = append(detailLines,
				"",
				subtleStyle.Render("Model Editor (enter apply, esc cancel)"),
				"> "+draft+"|",
			)
		}
	}

	left := lipgloss.NewStyle().Width(listWidth).Render(strings.Join(listLines, "\n"))
	right := lipgloss.NewStyle().Width(detailWidth).Render(strings.Join(detailLines, "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right)
}

func (m Model) renderToolsView(width int) string {
	tools := m.availableTools()
	m.toolIndex = clampIndex(m.toolIndex, len(tools))

	listWidth := width / 3
	if listWidth < 24 {
		listWidth = 24
	}
	if listWidth > width-28 {
		listWidth = width / 2
	}
	detailWidth := width - listWidth - 3
	if detailWidth < 24 {
		detailWidth = 24
	}

	listLines := []string{
		sectionHeader("⚒", "Tools"),
		subtleStyle.Render("enter run · e edit params · x reset · ctrl+h keys"),
		renderDivider(listWidth - 2),
		"",
	}
	if len(tools) == 0 {
		listLines = append(listLines,
			warnStyle.Render("No registered tools."),
			"",
			subtleStyle.Render("Tool engine isn't wired up. Check the engine was started with"),
			subtleStyle.Render("tools enabled in ")+codeStyle.Render(".dfmc/config.yaml")+subtleStyle.Render(" or rerun ")+codeStyle.Render("dfmc init")+subtleStyle.Render("."),
		)
	} else {
		for i, name := range tools {
			prefix := "  "
			label := truncateSingleLine(name, listWidth-4)
			if i == m.toolIndex {
				prefix = "> "
				label = titleStyle.Render(label)
			}
			listLines = append(listLines, prefix+label)
		}
	}

	detailLines := []string{
		sectionHeader("▸", "Tool Detail"),
		renderDivider(detailWidth - 2),
	}
	if len(tools) == 0 {
		detailLines = append(detailLines, subtleStyle.Render("Tool engine unavailable."))
	} else {
		selected := tools[m.toolIndex]
		// Pull the rich spec (summary, purpose, risk, args with types/
		// defaults/enums, returns, examples, tags, cost hint) instead of
		// the prior 3-line "Name / Description / Params" digest. This is
		// the same shape as `dfmc tool show NAME` / `/tool show NAME` so
		// users see one canonical description per tool across surfaces.
		if m.eng != nil && m.eng.Tools != nil {
			if spec, ok := m.eng.Tools.Spec(selected); ok {
				detailLines = append(detailLines,
					highlightToolSpecLines(formatToolSpec(spec), detailWidth)...,
				)
			} else {
				detailLines = append(detailLines,
					fmt.Sprintf("Name:        %s", selected),
					subtleStyle.Render("(no spec registered)"),
				)
			}
		} else {
			detailLines = append(detailLines,
				fmt.Sprintf("Name:        %s", selected),
				fmt.Sprintf("Description: %s", truncateForPanel(m.toolDescription(selected), detailWidth)),
			)
		}
		// Show the user's current parameter override (or default preset)
		// — the spec describes the schema; this line shows what would
		// actually be sent on enter.
		detailLines = append(detailLines,
			"",
			subtleStyle.Render("Effective params"),
			truncateForPanelSized(m.toolPresetSummary(selected), detailWidth, 6),
			"",
		)
		if selected == "run_command" {
			if suggestions := m.runCommandSuggestions(); len(suggestions) > 0 {
				detailLines = append(detailLines, subtleStyle.Render("Suggested presets"))
				for _, suggestion := range suggestions {
					detailLines = append(detailLines, truncateForPanel("- "+suggestion, detailWidth))
				}
				detailLines = append(detailLines, "")
			}
		}
		if m.toolEditing {
			detailLines = append(detailLines,
				subtleStyle.Render("Param Editor"),
				truncateForPanel(m.toolDraft, detailWidth),
				"",
			)
		}
		detailLines = append(detailLines, sectionHeader("✓", "Last Result"))
		resultText := strings.TrimSpace(m.toolOutput)
		if resultText == "" {
			resultText = subtleStyle.Render("No tool run yet — press enter to run the selected tool.")
		}
		detailLines = append(detailLines, truncateForPanel(resultText, detailWidth))
	}

	left := lipgloss.NewStyle().Width(listWidth).Render(strings.Join(listLines, "\n"))
	right := lipgloss.NewStyle().Width(detailWidth).Render(strings.Join(detailLines, "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right)
}

// renderFooter paints a single dense status line: tab · ctx bar · git · churn
// · session (· notice when present). The chat header already owns provider,
// model, mode, and the parked/queued/btw badges — no need to duplicate. Keys
// hint is gone; the starter screen and `/` palette cover discovery.
func (m Model) renderFooter(width int) string {
	maxWidth := max(width-4, 16)

	tab := m.tabs[m.activeTab]
	segments := []string{titleStyle.Render(" " + tab + " ")}
	segments = append(segments, m.footerSegments()...)
	if pinned := strings.TrimSpace(m.pinnedFile); pinned != "" {
		segments = append(segments, accentStyle.Render("◆ "+truncateSingleLine(pinned, 22)))
	}
	if note := strings.TrimSpace(m.notice); note != "" {
		segments = append(segments, subtleStyle.Render("· ")+truncateSingleLine(note, 80))
	}
	sep := subtleStyle.Render("  ·  ")
	return truncateSingleLine(strings.Join(segments, sep), maxWidth)
}

// footerSegments is the metrics portion (ctx bar, git branch, churn, session).
// Extracted so tests and a future `?`-triggered overlay can compose it without
// the tab chip or pinned/notice trailers.
func (m Model) footerSegments() []string {
	out := []string{}
	tokens, maxCtx := 0, 0
	if m.status.ContextIn != nil {
		tokens = m.status.ContextIn.TokenCount
		maxCtx = m.status.ContextIn.ProviderMaxContext
	}
	if maxCtx == 0 {
		maxCtx = m.status.ProviderProfile.MaxContext
	}
	out = append(out, renderContextBar(tokens, maxCtx, 10))

	info := m.gitInfo
	if strings.TrimSpace(info.Branch) != "" {
		label := info.Branch
		if info.Detached {
			label = "(" + label + ")"
		}
		chip := accentStyle.Render("⎇ ") + boldStyle.Render(label)
		if info.Dirty {
			chip += warnStyle.Render("*")
		}
		out = append(out, chip)
	}
	if info.Inserted > 0 || info.Deleted > 0 {
		churn := okStyle.Render(fmt.Sprintf("+%d", info.Inserted)) +
			subtleStyle.Render(",") +
			failStyle.Render(fmt.Sprintf("-%d", info.Deleted))
		out = append(out, churn)
	}
	if !m.sessionStart.IsZero() {
		out = append(out, subtleStyle.Render("⏱ ")+boldStyle.Render(formatSessionDuration(time.Since(m.sessionStart))))
	}
	return out
}

// renderHelpOverlay paints a compact reference card when m.showHelpOverlay is
// on (toggled by ctrl+h). This replaces the persistent "keys:" footer line —
// the hints are still one keystroke away, without eating screen real estate
// in the resting state. Because the overlay is the only keyboard discovery
// surface, it lists every keybinding a user would otherwise have to guess.
func (m Model) renderHelpOverlay(width int) string {
	if width < 40 {
		width = 40
	}
	tab := m.tabs[m.activeTab]
	lines := []string{
		titleStyle.Render(" Keys ") + subtleStyle.Render("  ctrl+h to close"),
		"",
		boldStyle.Render(tab+" tab"),
	}
	for _, hint := range helpOverlayTabHints(tab) {
		lines = append(lines, "  "+hint)
	}
	lines = append(lines,
		"",
		boldStyle.Render("Global"),
		"  ctrl+p palette · alt+1..0/alt+t/alt+y/alt+w/alt+o or f1..f12 tabs · ctrl+h help · ctrl+s stats",
		"  ctrl+c/ctrl+q quit · ctrl+u clear chat input · esc cancels streaming turn (or dismisses parked banner)",
		"",
		boldStyle.Render("Chat composer"),
		"  ↑/↓ history · tab accept suggestion · @ mention file · / browse commands",
		"  @file:10-50 or @file#L10-L50 attaches a line range to the mention",
		"  ctrl+←/→ jump word · ctrl+w kill word · ctrl+k kill to end · ctrl+u clear line",
		"  ctrl+a/ctrl+e line home/end · home/end same · backspace deletes char",
		"  ctrl+t or /file open file picker (alias for @, useful on AltGr layouts)",
		"  /continue resumes a parked agent loop · /btw queues a note",
		"  /clear wipes transcript · /quit exits · /coach mutes notes · /hints toggles trajectory",
		"  /plan enters investigate-only mode · /code exits and re-enables mutations",
		"  /retry resends last user msg · /edit pulls last msg back to the composer",
	)
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		out = append(out, truncateSingleLine(ln, width))
	}
	return strings.Join(out, "\n")
}

// helpOverlayTabHints returns per-tab keybinding hints as individual lines so
// the overlay can group them under the tab header without prose.
func helpOverlayTabHints(tab string) []string {
	switch strings.TrimSpace(strings.ToLower(tab)) {
	case "chat":
		return []string{
			"enter send · ctrl+j or alt+enter newline · / commands · @ mention",
			"wheel · shift+↑/↓ · pgup/pgdn scroll transcript",
			"when parked: enter resumes · esc dismisses · type a note first to steer",
		}
	case "status":
		return []string{"r refresh status"}
	case "files":
		return []string{
			"j/k or alt+j/alt+k move · p pin · i insert marker",
			"e explain · v review",
		}
	case "patch":
		return []string{
			"d diff · l patch · n/b next/prev file · j/k next/prev hunk",
		}
	case "setup":
		return []string{
			"j/k provider · enter apply · m edit model · s save · r reload",
		}
	case "tools":
		return []string{
			"j/k select · enter run · e edit params · x reset · r rerun",
		}
	case "activity":
		return []string{
			"j/k scroll · pgup/pgdn page · g/G top/tail · p toggle follow · c clear",
		}
	case "memory":
		return []string{
			"j/k scroll · t cycle tier · / search · r refresh · c clear",
		}
	case "codemap":
		return []string{
			"j/k scroll · v cycle view (overview/hotspots/orphans/cycles) · r refresh",
		}
	case "conversations":
		return []string{
			"j/k scroll · enter preview (loads as active) · / search · r refresh · c clear search",
		}
	case "prompts":
		return []string{
			"j/k scroll · enter preview · / search · r refresh · c clear search",
		}
	case "security":
		return []string{
			"r rescan · v toggle secrets/vulns · j/k scroll · / search · c clear search",
		}
	case "plans":
		return []string{
			"e edit task · enter run · esc cancel edit · j/k scroll · c clear",
		}
	case "context":
		return []string{
			"e edit query · enter preview · esc cancel edit · c clear",
		}
	case "providers":
		return []string{
			"j/k scroll · r refresh · g/G top/bottom",
		}
	default:
		return []string{"alt+1..0/alt+t/alt+y/alt+w/alt+o tabs · ctrl+p palette · ctrl+q quit"}
	}
}


func loadStatusCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return statusLoadedMsg{}
		}
		return statusLoadedMsg{status: eng.Status()}
	}
}

func loadWorkspaceCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return workspaceLoadedMsg{}
		}
		root := strings.TrimSpace(eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
		diff, err := gitWorkingDiff(root, 120_000)
		if err != nil {
			return workspaceLoadedMsg{err: err}
		}
		changed, err := gitChangedFiles(root, 12)
		if err != nil {
			return workspaceLoadedMsg{err: err}
		}
		return workspaceLoadedMsg{diff: diff, changed: changed}
	}
}


func loadFilesCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return filesLoadedMsg{}
		}
		root := strings.TrimSpace(eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
		files, err := listProjectFiles(root, 5000)
		return filesLoadedMsg{files: files, err: err}
	}
}

func loadFilePreviewCmd(eng *engine.Engine, rel string) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return filePreviewLoadedMsg{}
		}
		root := strings.TrimSpace(eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
		content, size, err := readProjectFile(root, rel, 32_000)
		return filePreviewLoadedMsg{path: rel, content: content, size: size, err: err}
	}
}

func runToolCmd(eng *engine.Engine, name string, params map[string]any) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return toolRunMsg{name: name, params: params, err: fmt.Errorf("engine is nil")}
		}
		res, err := eng.CallTool(context.Background(), name, params)
		return toolRunMsg{name: name, params: params, result: res, err: err}
	}
}


func formatToolResultForPanel(name string, params map[string]any, res toolruntime.Result) string {
	lines := []string{
		fmt.Sprintf("Tool: %s", name),
		fmt.Sprintf("Success: %t", res.Success),
	}
	if len(params) > 0 {
		lines = append(lines, "Params: "+formatToolParams(params))
	}
	if res.DurationMs > 0 {
		lines = append(lines, fmt.Sprintf("Duration: %dms", res.DurationMs))
	}
	if res.Truncated {
		lines = append(lines, "Output: truncated")
	}
	output := strings.TrimSpace(res.Output)
	if output == "" {
		output = "(no text output)"
	}
	lines = append(lines, "", output)
	return strings.Join(lines, "\n")
}

func formatToolErrorForPanel(name string, params map[string]any, res toolruntime.Result, err error) string {
	lines := []string{
		fmt.Sprintf("Tool: %s", name),
		"Success: false",
	}
	if len(params) > 0 {
		lines = append(lines, "Params: "+formatToolParams(params))
	}
	if res.DurationMs > 0 {
		lines = append(lines, fmt.Sprintf("Duration: %dms", res.DurationMs))
	}
	lines = append(lines, "Error: "+err.Error())
	output := strings.TrimSpace(res.Output)
	if output != "" {
		lines = append(lines, "", output)
	}
	return strings.Join(lines, "\n")
}

func formatToolParams(params map[string]any) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, params[k]))
	}
	return strings.Join(parts, ", ")
}

func parseToolParamString(raw string) (map[string]any, error) {
	tokens, err := splitToolParamTokens(raw)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("param string is empty")
	}
	params := make(map[string]any, len(tokens))
	for _, token := range tokens {
		key, value, ok := strings.Cut(token, "=")
		if !ok {
			return nil, fmt.Errorf("expected key=value token, got %q", token)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return nil, fmt.Errorf("empty param key in %q", token)
		}
		params[key] = coerceToolParamValue(value)
	}
	return params, nil
}

func splitToolParamTokens(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var (
		tokens  []string
		current strings.Builder
		quote   rune
	)
	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}
	for _, r := range raw {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			current.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted value")
	}
	flush()
	return tokens, nil
}

func coerceToolParamValue(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	switch strings.ToLower(trimmed) {
	case "true":
		return true
	case "false":
		return false
	}
	if i, err := strconv.Atoi(trimmed); err == nil {
		return i
	}
	return trimmed
}

func toolResultRelativePath(eng *engine.Engine, res toolruntime.Result) string {
	if eng == nil {
		return ""
	}
	raw := strings.TrimSpace(fmt.Sprint(res.Data["path"]))
	if raw == "" || raw == "<nil>" {
		return ""
	}
	rel, err := filepath.Rel(eng.ProjectRoot, raw)
	if err != nil {
		return filepath.ToSlash(raw)
	}
	return filepath.ToSlash(rel)
}

func toolResultWorkspaceChanged(res toolruntime.Result) bool {
	if res.Data == nil {
		return false
	}
	switch v := res.Data["workspace_changed"].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func undoConversationCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return conversationUndoMsg{err: fmt.Errorf("engine is nil")}
		}
		removed, err := eng.ConversationUndoLast()
		return conversationUndoMsg{removed: removed, err: err}
	}
}

func startChatStream(ctx context.Context, eng *engine.Engine, question string) <-chan tea.Msg {
	out := make(chan tea.Msg, 64)
	go func() {
		defer close(out)
		if eng == nil {
			out <- chatErrMsg{err: fmt.Errorf("engine is nil")}
			return
		}
		stream, err := eng.StreamAsk(ctx, question)
		if err != nil {
			out <- chatErrMsg{err: err}
			return
		}
		for ev := range stream {
			switch ev.Type {
			case provider.StreamDelta:
				out <- chatDeltaMsg{delta: ev.Delta}
			case provider.StreamError:
				if ev.Err != nil {
					out <- chatErrMsg{err: ev.Err}
				} else {
					out <- chatErrMsg{err: fmt.Errorf("stream error")}
				}
				return
			case provider.StreamDone:
				out <- chatDoneMsg{}
				return
			}
		}
		out <- streamClosedMsg{}
	}()
	return out
}

// startChatResumeStream runs ResumeAgent in a goroutine and surfaces the
// resulting answer through the same chatDelta/chatDone/chatErr channel the
// normal stream path uses. Mirrors startChatStream so the UI needs no new
// wiring — this is the minimum integration surface for resume.
func startChatResumeStream(ctx context.Context, eng *engine.Engine, note string) <-chan tea.Msg {
	out := make(chan tea.Msg, 8)
	go func() {
		defer close(out)
		if eng == nil {
			out <- chatErrMsg{err: fmt.Errorf("engine is nil")}
			return
		}
		completion, err := eng.ResumeAgent(ctx, note)
		if err != nil {
			out <- chatErrMsg{err: err}
			return
		}
		if answer := strings.TrimSpace(completion.Answer); answer != "" {
			out <- chatDeltaMsg{delta: answer}
		}
		out <- chatDoneMsg{}
	}()
	return out
}

func waitForStreamMsg(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return streamClosedMsg{}
		}
		return msg
	}
}

func subscribeEventsCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil || eng.EventBus == nil {
			return nil
		}
		return eventSubscribedMsg{ch: eng.EventBus.Subscribe("*")}
	}
}

func waitForEventMsg(ch <-chan engine.Event) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return engineEventMsg{event: ev}
	}
}

func (m Model) handleEngineEvent(event engine.Event) Model {
	eventType := strings.TrimSpace(strings.ToLower(event.Type))
	if eventType == "" {
		return m
	}
	// Activity panel captures every event before any filtering — it's the
	// firehose so users can see what the agent actually did.
	m.recordActivityEvent(event)
	line := ""
	payload, _ := toStringAnyMap(event.Payload)
	switch eventType {
	case "agent:loop:start":
		m.agentLoopActive = true
		m.agentLoopPhase = "starting"
		m.agentLoopStep = 0
		m.agentLoopMaxToolStep = payloadInt(payload, "max_tool_steps", m.agentLoopMaxToolStep)
		m.agentLoopToolRounds = payloadInt(payload, "tool_rounds", 0)
		m.agentLoopProvider = payloadString(payload, "provider", m.agentLoopProvider)
		m.agentLoopModel = payloadString(payload, "model", m.agentLoopModel)
		// A fresh loop start means any previously parked banner is obsolete.
		m.resumePromptActive = false
		files := payloadInt(payload, "context_files", 0)
		tokens := payloadInt(payload, "context_tokens", 0)
		line = fmt.Sprintf("Agent loop started: max_tools=%d context=%df/%dtok", m.agentLoopMaxToolStep, files, tokens)
	case "agent:loop:thinking":
		m.agentLoopActive = true
		m.agentLoopPhase = "thinking"
		step := payloadInt(payload, "step", 0)
		if step > 0 {
			m.agentLoopStep = step
		}
		maxSteps := payloadInt(payload, "max_tool_steps", 0)
		if maxSteps > 0 {
			m.agentLoopMaxToolStep = maxSteps
		}
		rounds := payloadInt(payload, "tool_rounds", 0)
		if rounds >= 0 {
			m.agentLoopToolRounds = rounds
		}
		m.agentLoopProvider = payloadString(payload, "provider", m.agentLoopProvider)
		m.agentLoopModel = payloadString(payload, "model", m.agentLoopModel)
		if m.agentLoopStep > 0 && m.agentLoopMaxToolStep > 0 {
			line = fmt.Sprintf("Agent thinking: step %d/%d", m.agentLoopStep, m.agentLoopMaxToolStep)
		} else {
			line = "Agent thinking..."
		}
	case "tool:call":
		m.agentLoopActive = true
		m.agentLoopPhase = "tool-call"
		toolName := payloadString(payload, "tool", "tool")
		step := payloadInt(payload, "step", 0)
		m.agentLoopLastTool = toolName
		m.agentLoopLastStatus = "running"
		m.agentLoopLastDuration = 0
		if step > 0 {
			m.agentLoopStep = step
		}
		if rounds := payloadInt(payload, "tool_rounds", 0); rounds > 0 {
			m.agentLoopToolRounds = rounds
		}
		m.agentLoopProvider = payloadString(payload, "provider", m.agentLoopProvider)
		m.agentLoopModel = payloadString(payload, "model", m.agentLoopModel)
		paramsPreview := payloadString(payload, "params_preview", "")
		toolCallChip := toolChip{
			Name:    toolName,
			Status:  "running",
			Step:    step,
			Preview: paramsPreview,
		}
		m.pushToolChip(toolCallChip)
		m.pushStreamingMessageToolChip(toolCallChip)
		m.activeToolCount++
		if step > 0 {
			line = fmt.Sprintf("Agent tool call: %s (step %d)", toolName, step)
		} else {
			line = fmt.Sprintf("Agent tool call: %s", toolName)
		}
		if paramsPreview != "" {
			line += " " + paramsPreview
		}
	case "tool:result":
		m.agentLoopActive = true
		m.agentLoopPhase = "tool-result"
		toolName := payloadString(payload, "tool", "tool")
		duration := payloadInt(payload, "durationMs", 0)
		success := payloadBool(payload, "success", true)
		status := "ok"
		if !success {
			status = "failed"
		}
		m.agentLoopLastTool = toolName
		m.agentLoopLastStatus = status
		m.agentLoopLastDuration = duration
		preview := payloadString(payload, "output_preview", "")
		if preview != "" {
			m.agentLoopLastOutput = preview
		}
		step := payloadInt(payload, "step", 0)
		if step > 0 {
			m.agentLoopStep = step
			if step > m.agentLoopToolRounds {
				m.agentLoopToolRounds = step
			}
		}
		m.agentLoopProvider = payloadString(payload, "provider", m.agentLoopProvider)
		m.agentLoopModel = payloadString(payload, "model", m.agentLoopModel)
		chipPreview := preview
		if chipPreview == "" && !success {
			chipPreview = payloadString(payload, "error", "")
		}
		if batchCount := payloadInt(payload, "batch_count", 0); batchCount > 0 {
			batchParallel := payloadInt(payload, "batch_parallel", 0)
			batchOK := payloadInt(payload, "batch_ok", 0)
			batchFail := payloadInt(payload, "batch_fail", 0)
			parts := []string{fmt.Sprintf("%d calls", batchCount)}
			if batchParallel > 0 {
				parts = append(parts, fmt.Sprintf("%d parallel", batchParallel))
			}
			parts = append(parts, fmt.Sprintf("%d ok", batchOK))
			if batchFail > 0 {
				parts = append(parts, fmt.Sprintf("%d fail", batchFail))
			}
			chipPreview = strings.Join(parts, " · ")
		}
		savedChars := payloadInt(payload, "compression_saved_chars", 0)
		rawChars := payloadInt(payload, "output_chars", 0)
		payloadChars := payloadInt(payload, "payload_chars", 0)
		compressionPct := 0
		if ratio, ok := payload["compression_ratio"].(float64); ok && ratio >= 0 && ratio <= 1 {
			compressionPct = int((1 - ratio) * 100)
		} else if rawChars > 0 && savedChars > 0 {
			compressionPct = int((int64(savedChars) * 100) / int64(rawChars))
		}
		if savedChars > 0 && rawChars > 0 {
			m.compressionSavedChars += savedChars
			m.compressionRawChars += rawChars
		}
		finishedChip := toolChip{
			Name:            toolName,
			Status:          status,
			Step:            step,
			DurationMs:      duration,
			Preview:         chipPreview,
			OutputTokens:    payloadInt(payload, "output_tokens", 0),
			Truncated:       payloadBool(payload, "truncated", false),
			CompressedChars: payloadChars,
			SavedChars:      savedChars,
			CompressionPct:  compressionPct,
		}
		m.finishToolChip(finishedChip)
		m.finishStreamingMessageToolChip(finishedChip)
		if m.activeToolCount > 0 {
			m.activeToolCount--
		}
		if duration > 0 {
			line = fmt.Sprintf("Agent tool result: %s (%s, %dms)", toolName, status, duration)
		} else {
			line = fmt.Sprintf("Agent tool result: %s (%s)", toolName, status)
		}
		if preview != "" {
			line += " -> " + preview
		} else if !success {
			if errText := payloadString(payload, "error", ""); errText != "" {
				line += " -> " + truncateSingleLine(errText, 96)
			}
		}
	case "agent:loop:final":
		m.agentLoopPhase = "finalizing"
		if rounds := payloadInt(payload, "tool_rounds", 0); rounds >= 0 {
			m.agentLoopToolRounds = rounds
		}
		if step := payloadInt(payload, "step", 0); step > 0 {
			m.agentLoopStep = step
		}
		line = fmt.Sprintf("Agent loop finalizing answer after %d tool call(s).", m.agentLoopToolRounds)
	case "agent:loop:max_steps":
		m.agentLoopPhase = "max-steps"
		maxSteps := payloadInt(payload, "max_tool_steps", m.agentLoopMaxToolStep)
		if maxSteps > 0 {
			m.agentLoopMaxToolStep = maxSteps
		}
		line = fmt.Sprintf("Agent loop reached max tool steps (%d).", m.agentLoopMaxToolStep)
	case "agent:loop:error":
		m.agentLoopPhase = "error"
		errText := payloadString(payload, "error", "unknown error")
		line = "Agent loop error: " + errText
	case "agent:loop:parked":
		m.agentLoopPhase = "parked"
		m.agentLoopActive = false
		step := payloadInt(payload, "step", m.agentLoopStep)
		maxSteps := payloadInt(payload, "max_tool_steps", m.agentLoopMaxToolStep)
		m.agentLoopStep = step
		if maxSteps > 0 {
			m.agentLoopMaxToolStep = maxSteps
		}
		m.resumePromptActive = true
		// budget_exhausted already surfaces its own "exhausted %d/%d"
		// transcript line with token counts; suppress the generic parked
		// line in that case so the scrollback reads once, not twice.
		if payloadString(payload, "reason", "") == "budget_exhausted" {
			return m
		}
		line = fmt.Sprintf("Agent loop parked at step %d/%d — press Enter to resume, Esc to dismiss.", step, maxSteps)
	case "coach:note":
		if m.coachMuted {
			return m
		}
		text := payloadString(payload, "text", "")
		if strings.TrimSpace(text) == "" {
			return m
		}
		severity := payloadString(payload, "severity", "info")
		origin := payloadString(payload, "origin", "")
		m = m.appendCoachMessage(text, severity, origin)
		return m
	case "agent:coach:hint":
		if !m.hintsVerbose {
			return m
		}
		hints, _ := payload["hints"].([]any)
		for _, h := range hints {
			if s, ok := h.(string); ok && strings.TrimSpace(s) != "" {
				m = m.appendCoachMessage("→ "+s, "info", "trajectory")
			}
		}
		return m
	case "tool:error":
		switch payload := event.Payload.(type) {
		case string:
			line = "Tool error: " + strings.TrimSpace(payload)
		default:
			line = "Tool error occurred."
		}
	case "agent:subagent:start":
		task := payloadString(payload, "task", "task")
		role := payloadString(payload, "role", "")
		m.activeSubagentCount++
		chipName := "subagent"
		if role != "" {
			chipName = "subagent/" + role
		}
		preview := truncateSingleLine(task, 72)
		chip := toolChip{
			Name:    chipName,
			Status:  "subagent-running",
			Preview: preview,
		}
		m.pushToolChip(chip)
		m.pushStreamingMessageToolChip(chip)
		if role != "" {
			line = fmt.Sprintf("Subagent (%s) started: %s", role, preview)
		} else {
			line = "Subagent started: " + preview
		}
	case "agent:subagent:done":
		if m.activeSubagentCount > 0 {
			m.activeSubagentCount--
		}
		duration := payloadInt(payload, "duration_ms", 0)
		rounds := payloadInt(payload, "tool_rounds", 0)
		parked := payloadBool(payload, "parked", false)
		errText := payloadString(payload, "err", "")
		role := payloadString(payload, "role", "")
		status := "subagent-ok"
		chipPreview := fmt.Sprintf("%d rounds", rounds)
		if parked {
			chipPreview += " · parked"
		}
		if errText != "" {
			status = "subagent-failed"
			chipPreview = truncateSingleLine(errText, 72)
		}
		chipName := "subagent"
		if role != "" {
			chipName = "subagent/" + role
		}
		finished := toolChip{
			Name:       chipName,
			Status:     status,
			DurationMs: duration,
			Preview:    chipPreview,
		}
		m.finishToolChip(finished)
		m.finishStreamingMessageToolChip(finished)
		switch {
		case errText != "":
			line = fmt.Sprintf("Subagent failed (%dms): %s", duration, truncateSingleLine(errText, 120))
		case parked:
			line = fmt.Sprintf("Subagent parked after %d rounds (%dms).", rounds, duration)
		default:
			line = fmt.Sprintf("Subagent done: %d rounds (%dms).", rounds, duration)
		}
	case "context:built":
		files := payloadInt(payload, "files", 0)
		tokens := payloadInt(payload, "tokens", 0)
		task := payloadString(payload, "task", "general")
		comp := payloadString(payload, "compression", "-")
		line = fmt.Sprintf("Context built: %d files, %d tokens (%s, %s)", files, tokens, task, comp)
	case "provider:complete":
		if m.agentLoopActive {
			m.agentLoopPhase = "complete"
			m.agentLoopActive = false
			tokens := payloadInt(payload, "tokens", 0)
			providerName := payloadString(payload, "provider", m.agentLoopProvider)
			modelName := payloadString(payload, "model", m.agentLoopModel)
			line = fmt.Sprintf("Provider complete: %s/%s (%dtok)", providerName, modelName, tokens)
		}
	case "context:lifecycle:compacted":
		before := payloadInt(payload, "before_tokens", 0)
		after := payloadInt(payload, "after_tokens", 0)
		collapsed := payloadInt(payload, "rounds_collapsed", 0)
		removed := payloadInt(payload, "messages_removed", 0)
		preview := fmt.Sprintf("%d→%d tok · %d rounds", before, after, collapsed)
		m.pushToolChip(toolChip{
			Name:    "auto-compact",
			Status:  "compact",
			Preview: preview,
		})
		if collapsed > 0 {
			line = fmt.Sprintf("Context auto-compacted: %d→%d tokens (%d rounds, %d msgs removed).", before, after, collapsed, removed)
		} else {
			line = fmt.Sprintf("Context auto-compacted: %d→%d tokens.", before, after)
		}
	case "agent:loop:budget_exhausted":
		m.agentLoopPhase = "budget-exhausted"
		used := payloadInt(payload, "tokens_used", 0)
		budget := payloadInt(payload, "max_tool_tokens", 0)
		m.pushToolChip(toolChip{
			Name:    "token-budget",
			Status:  "budget",
			Preview: fmt.Sprintf("%d/%d tok", used, budget),
		})
		line = fmt.Sprintf("Agent loop exhausted token budget (%d/%d).", used, budget)
	case "provider:race:complete":
		winner := payloadString(payload, "winner", "?")
		tokens := payloadInt(payload, "tokens", 0)
		duration := payloadInt(payload, "duration_ms", 0)
		candidates, _ := payload["candidates"].([]any)
		var names []string
		for _, c := range candidates {
			if s, ok := c.(string); ok && strings.TrimSpace(s) != "" {
				names = append(names, s)
			}
		}
		m.pushToolChip(toolChip{
			Name:       "race",
			Status:     "race-ok",
			Preview:    fmt.Sprintf("won by %s", winner),
			DurationMs: duration,
		})
		if len(names) > 0 {
			line = fmt.Sprintf("Provider race: %s won [%s] (%dtok, %dms).", winner, strings.Join(names, ","), tokens, duration)
		} else {
			line = fmt.Sprintf("Provider race: %s won (%dtok, %dms).", winner, tokens, duration)
		}
	case "provider:race:failed":
		errText := payloadString(payload, "error", "all candidates errored")
		duration := payloadInt(payload, "duration_ms", 0)
		m.pushToolChip(toolChip{
			Name:       "race",
			Status:     "race-failed",
			Preview:    truncateSingleLine(errText, 72),
			DurationMs: duration,
		})
		line = fmt.Sprintf("Provider race failed (%dms): %s", duration, truncateSingleLine(errText, 140))
	case "agent:loop:auto_recover":
		before := payloadInt(payload, "before_tokens", 0)
		after := payloadInt(payload, "after_tokens", 0)
		collapsed := payloadInt(payload, "rounds_collapsed", 0)
		m.pushToolChip(toolChip{
			Name:    "auto-recover",
			Status:  "recover",
			Preview: fmt.Sprintf("%d→%d tok · %d rounds", before, after, collapsed),
		})
		if collapsed > 0 {
			line = fmt.Sprintf("Auto-recover: budget trip, compacted %d→%d tokens (%d rounds). Retrying.", before, after, collapsed)
		} else {
			line = "Auto-recover: budget trip, transcript slimmed. Retrying."
		}
	case "context:lifecycle:handoff":
		historyTokens := payloadInt(payload, "history_tokens", 0)
		briefTokens := payloadInt(payload, "brief_tokens", 0)
		sealed := payloadInt(payload, "messages_sealed", 0)
		newConv := payloadString(payload, "new_conversation", "")
		preview := fmt.Sprintf("%d→%d tok · %d msgs sealed", historyTokens, briefTokens, sealed)
		m.pushToolChip(toolChip{
			Name:    "auto-handoff",
			Status:  "handoff",
			Preview: preview,
		})
		if newConv != "" {
			line = fmt.Sprintf("Auto-new-session: rotated to %s (%d→%d tokens, %d msgs sealed).", newConv, historyTokens, briefTokens, sealed)
		} else {
			line = fmt.Sprintf("Auto-new-session: fresh conversation seeded (%d→%d tokens).", historyTokens, briefTokens)
		}
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return m
	}
	m.appendActivity(line)
	m.notice = line
	mirror := shouldMirrorEventToTranscript(eventType)
	// Tool failures are rare but critical — never silently drop them from
	// the transcript. A failed chip alone in a long turn is easy to miss,
	// so mirror the error event with its preview/error text.
	if !mirror && eventType == "tool:result" && !payloadBool(payload, "success", true) {
		mirror = true
	}
	if m.sending && mirror {
		m = m.appendToolEventMessage(line)
	}
	return m
}

func payloadString(data map[string]any, key, fallback string) string {
	if data == nil {
		return fallback
	}
	raw, ok := data[key]
	if !ok {
		return fallback
	}
	switch value := raw.(type) {
	case string:
		value = strings.TrimSpace(value)
		if value == "" {
			return fallback
		}
		return value
	default:
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			return fallback
		}
		return text
	}
}

func payloadInt(data map[string]any, key string, fallback int) int {
	if data == nil {
		return fallback
	}
	raw, ok := data[key]
	if !ok {
		return fallback
	}
	switch value := raw.(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	case float32:
		return int(value)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return n
		}
	}
	return fallback
}

func payloadBool(data map[string]any, key string, fallback bool) bool {
	if data == nil {
		return fallback
	}
	raw, ok := data[key]
	if !ok {
		return fallback
	}
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}

// shouldMirrorEventToTranscript decides which engine events earn a system
// message in the chat transcript. Per-step tool:call / tool:result chatter is
// deliberately excluded — the tool-chip row, footer notice slot, and activity
// log already carry that; duplicating into the transcript floods scrollback.
// Only events that reflect a real state change the user needs in history
// pass this filter.
func shouldMirrorEventToTranscript(eventType string) bool {
	switch strings.TrimSpace(strings.ToLower(eventType)) {
	case "agent:loop:error", "agent:loop:max_steps", "agent:loop:parked",
		"agent:loop:budget_exhausted",
		"context:lifecycle:compacted", "context:lifecycle:handoff",
		"coach:note":
		return true
	default:
		return false
	}
}

func (m *Model) appendActivity(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if n := len(m.activityLog); n > 0 && strings.EqualFold(strings.TrimSpace(m.activityLog[n-1]), line) {
		return
	}
	m.activityLog = append(m.activityLog, line)
	if len(m.activityLog) > 24 {
		drop := len(m.activityLog) - 24
		m.activityLog = m.activityLog[drop:]
	}
}

const maxToolTimelineChips = 18

// pushToolChip appends a new chip (typically a running tool call) to the
// rolling timeline and trims old entries.
func (m *Model) pushToolChip(chip toolChip) {
	chip.Name = strings.TrimSpace(chip.Name)
	if chip.Name == "" {
		return
	}
	m.toolTimeline = append(m.toolTimeline, chip)
	if len(m.toolTimeline) > maxToolTimelineChips {
		drop := len(m.toolTimeline) - maxToolTimelineChips
		m.toolTimeline = m.toolTimeline[drop:]
	}
}

// pushStreamingMessageToolChip mirrors a tool call onto the assistant
// transcript line that's currently streaming, so users see inline per-
// message chips (not just the global runtime strip). No-op when no message
// is actively streaming.
func (m *Model) pushStreamingMessageToolChip(chip toolChip) {
	chip.Name = strings.TrimSpace(chip.Name)
	if chip.Name == "" {
		return
	}
	if m.streamIndex < 0 || m.streamIndex >= len(m.transcript) {
		return
	}
	const maxPerMessage = 32
	line := &m.transcript[m.streamIndex]
	line.ToolChips = append(line.ToolChips, chip)
	if len(line.ToolChips) > maxPerMessage {
		drop := len(line.ToolChips) - maxPerMessage
		line.ToolChips = line.ToolChips[drop:]
	}
}

// finishStreamingMessageToolChip resolves the most recent running chip on
// the streaming assistant message with a terminal status.
func (m *Model) finishStreamingMessageToolChip(chip toolChip) {
	chip.Name = strings.TrimSpace(chip.Name)
	if chip.Name == "" {
		return
	}
	if m.streamIndex < 0 || m.streamIndex >= len(m.transcript) {
		return
	}
	wantRunning := "running"
	if strings.HasPrefix(strings.ToLower(chip.Status), "subagent-") {
		wantRunning = "subagent-running"
	}
	line := &m.transcript[m.streamIndex]
	for i := len(line.ToolChips) - 1; i >= 0; i-- {
		existing := line.ToolChips[i]
		if existing.Status != wantRunning {
			continue
		}
		if !strings.EqualFold(existing.Name, chip.Name) {
			continue
		}
		if chip.Step != 0 && existing.Step != 0 && existing.Step != chip.Step {
			continue
		}
		merged := existing
		merged.Status = chip.Status
		merged.DurationMs = chip.DurationMs
		if strings.TrimSpace(chip.Preview) != "" {
			merged.Preview = chip.Preview
		}
		if chip.Step > merged.Step {
			merged.Step = chip.Step
		}
		if chip.OutputTokens > 0 {
			merged.OutputTokens = chip.OutputTokens
		}
		if chip.Truncated {
			merged.Truncated = true
		}
		if chip.SavedChars > 0 {
			merged.SavedChars = chip.SavedChars
			merged.CompressedChars = chip.CompressedChars
			merged.CompressionPct = chip.CompressionPct
		}
		line.ToolChips[i] = merged
		return
	}
	m.pushStreamingMessageToolChip(chip)
}

// finishToolChip updates the most recent running chip for the same tool+step
// with a terminal status. Falls back to appending a fresh chip when no
// matching in-flight entry is found (e.g. result seen without a prior call).
func (m *Model) finishToolChip(chip toolChip) {
	chip.Name = strings.TrimSpace(chip.Name)
	if chip.Name == "" {
		return
	}
	wantRunning := "running"
	if strings.HasPrefix(strings.ToLower(chip.Status), "subagent-") {
		wantRunning = "subagent-running"
	}
	for i := len(m.toolTimeline) - 1; i >= 0; i-- {
		existing := m.toolTimeline[i]
		if existing.Status != wantRunning {
			continue
		}
		if !strings.EqualFold(existing.Name, chip.Name) {
			continue
		}
		if chip.Step != 0 && existing.Step != 0 && existing.Step != chip.Step {
			continue
		}
		merged := existing
		merged.Status = chip.Status
		merged.DurationMs = chip.DurationMs
		if strings.TrimSpace(chip.Preview) != "" {
			merged.Preview = chip.Preview
		}
		if chip.Step > merged.Step {
			merged.Step = chip.Step
		}
		if chip.OutputTokens > 0 {
			merged.OutputTokens = chip.OutputTokens
		}
		if chip.Truncated {
			merged.Truncated = true
		}
		if chip.SavedChars > 0 {
			merged.SavedChars = chip.SavedChars
			merged.CompressedChars = chip.CompressedChars
			merged.CompressionPct = chip.CompressionPct
		}
		m.toolTimeline[i] = merged
		return
	}
	m.pushToolChip(chip)
}

func (m *Model) resetAgentRuntime() {
	m.agentLoopActive = false
	m.agentLoopStep = 0
	m.agentLoopMaxToolStep = 0
	m.agentLoopToolRounds = 0
	m.agentLoopPhase = ""
	m.agentLoopProvider = ""
	m.agentLoopModel = ""
	m.agentLoopLastTool = ""
	m.agentLoopLastStatus = ""
	m.agentLoopLastDuration = 0
	m.agentLoopLastOutput = ""
	m.agentLoopContextScope = ""
}

func formatASTLanguageSummaryTUI(items []ast.BackendLanguageStatus) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Language) == "" || strings.TrimSpace(item.Active) == "" {
			continue
		}
		parts = append(parts, item.Language+"="+item.Active)
	}
	return strings.Join(parts, ", ")
}

func formatASTMetricsSummaryTUI(metrics ast.ParseMetrics) string {
	parts := make([]string, 0, 6)
	if metrics.Requests > 0 {
		parts = append(parts, fmt.Sprintf("requests=%d", metrics.Requests))
	}
	if metrics.Parsed > 0 {
		parts = append(parts, fmt.Sprintf("parsed=%d", metrics.Parsed))
	}
	if metrics.CacheHits > 0 || metrics.CacheMisses > 0 {
		parts = append(parts, fmt.Sprintf("cache=%d/%d", metrics.CacheHits, metrics.CacheMisses))
	}
	if metrics.AvgParseDurationMs > 0 {
		parts = append(parts, fmt.Sprintf("avg=%.1fms", metrics.AvgParseDurationMs))
	}
	if metrics.LastLanguage != "" || metrics.LastBackend != "" {
		parts = append(parts, fmt.Sprintf("last=%s/%s", blankFallback(metrics.LastLanguage, "-"), blankFallback(metrics.LastBackend, "-")))
	}
	if len(metrics.ByBackend) > 0 {
		parts = append(parts, "backend["+formatMetricMap(metrics.ByBackend)+"]")
	}
	if len(parts) == 0 {
		return "no parse activity"
	}
	return strings.Join(parts, " ")
}

func formatCodeMapMetricsSummaryTUI(metrics codemap.BuildMetrics) string {
	parts := make([]string, 0, 8)
	if metrics.Builds > 0 {
		parts = append(parts, fmt.Sprintf("builds=%d", metrics.Builds))
	}
	if metrics.FilesRequested > 0 || metrics.FilesProcessed > 0 {
		parts = append(parts, fmt.Sprintf("files=%d/%d", metrics.FilesProcessed, metrics.FilesRequested))
	}
	if metrics.LastDurationMs > 0 {
		parts = append(parts, fmt.Sprintf("last=%dms", metrics.LastDurationMs))
	}
	if metrics.LastGraphNodes > 0 || metrics.LastGraphEdges > 0 {
		parts = append(parts, fmt.Sprintf("graph=%dN/%dE", metrics.LastGraphNodes, metrics.LastGraphEdges))
	}
	if metrics.RecentBuilds > 1 {
		parts = append(parts, fmt.Sprintf("trend=%druns", metrics.RecentBuilds))
	}
	if len(metrics.RecentLanguages) > 0 {
		parts = append(parts, "langs["+formatMetricMap(metrics.RecentLanguages)+"]")
	}
	if len(parts) == 0 {
		return "no codemap activity"
	}
	return strings.Join(parts, " ")
}

func formatMetricMap(items map[string]int64) string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, items[key]))
	}
	return strings.Join(parts, ",")
}

func formatContextInSummaryTUI(report *engine.ContextInStatus) string {
	if report == nil {
		return ""
	}
	task := blankFallback(strings.TrimSpace(report.Task), "general")
	return fmt.Sprintf(
		"%df/%dtok budget=%d per-file=%d task=%s comp=%s explicit=%d",
		report.FileCount,
		report.TokenCount,
		report.MaxTokensTotal,
		report.MaxTokensPerFile,
		task,
		blankFallback(strings.TrimSpace(report.Compression), "-"),
		report.ExplicitFileMentions,
	)
}

func formatContextInReasonSummaryTUI(report *engine.ContextInStatus) string {
	if report == nil || len(report.Reasons) == 0 {
		return ""
	}
	limit := 3
	parts := make([]string, 0, limit+1)
	for _, reason := range report.Reasons {
		reason = truncateSingleLine(strings.TrimSpace(reason), 96)
		if reason == "" {
			continue
		}
		parts = append(parts, reason)
		if len(parts) >= limit {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	if len(report.Reasons) > len(parts) {
		parts = append(parts, "...more")
	}
	return strings.Join(parts, " | ")
}

func formatContextInTopFilesTUI(report *engine.ContextInStatus, limit int) string {
	if report == nil || len(report.Files) == 0 || limit <= 0 {
		return ""
	}
	if limit > len(report.Files) {
		limit = len(report.Files)
	}
	parts := make([]string, 0, limit)
	for _, file := range report.Files[:limit] {
		label := strings.TrimSpace(file.Path)
		if label == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s(score=%.2f tok=%d)", label, file.Score, file.TokenCount))
	}
	return strings.Join(parts, "; ")
}

func formatContextInDetailedFileLinesTUI(report *engine.ContextInStatus, limit int) []string {
	if report == nil || len(report.Files) == 0 || limit <= 0 {
		return nil
	}
	files := append([]engine.ContextInFileStatus(nil), report.Files...)
	sort.Slice(files, func(i, j int) bool {
		if files[i].Score == files[j].Score {
			if files[i].TokenCount == files[j].TokenCount {
				return strings.TrimSpace(files[i].Path) < strings.TrimSpace(files[j].Path)
			}
			return files[i].TokenCount > files[j].TokenCount
		}
		return files[i].Score > files[j].Score
	})
	if limit > len(files) {
		limit = len(files)
	}
	lines := make([]string, 0, limit)
	for _, file := range files[:limit] {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		meta := []string{}
		if file.Score > 0 {
			meta = append(meta, fmt.Sprintf("score=%.2f", file.Score))
		}
		if file.TokenCount > 0 {
			meta = append(meta, fmt.Sprintf("tok=%d", file.TokenCount))
		}
		if file.LineStart > 0 {
			end := max(file.LineEnd, file.LineStart)
			meta = append(meta, fmt.Sprintf("L%d-L%d", file.LineStart, end))
		}
		line := path
		if len(meta) > 0 {
			line += " (" + strings.Join(meta, ", ") + ")"
		}
		if reason := strings.TrimSpace(file.Reason); reason != "" {
			line += " - " + reason
		}
		lines = append(lines, line)
	}
	return lines
}

func truncateForPanel(text string, width int) string {
	return truncateForPanelSized(text, width, 18)
}

// truncateForPanelSized lets callers choose the row cap so panels can
// scale with the user's terminal height instead of the historic 18-line
// hard cap that left tall windows mostly empty.
func truncateForPanelSized(text string, width, maxLines int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if maxLines <= 0 {
		maxLines = 18
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > maxLines {
		lines = append(lines[:maxLines], "... [truncated]")
	}
	for i, line := range lines {
		if width > 0 && len([]rune(line)) > width {
			runes := []rune(line)
			trimTo := max(width-14, 0)
			lines[i] = string(runes[:trimTo]) + "... [trimmed]"
		}
	}
	return strings.Join(lines, "\n")
}

// chatBubbleContent returns the text the chat transcript should render for
// one message. Unlike chatPreviewForLine (which collapses to a one-line
// digest for compact side views), this is the full content, optionally
// decorated with a streaming caret while the assistant is still generating.
func chatBubbleContent(item chatLine, streaming bool) string {
	content := strings.TrimRight(item.Content, " \t\r\n")
	if streaming {
		if content == "" {
			return subtleStyle.Render("… thinking") + " ▎"
		}
		return content + " ▎"
	}
	return content
}

func renderChatInputLine(input string, cursor int, manual bool, manualInput string, sending bool) string {
	// Multi-line composition: a literal "\n" in the buffer becomes a new
	// physical row. Continuation rows get a "  " indent instead of the "> "
	// prompt so the prompt glyph never repeats. The cursor "|" lands on the
	// correct logical row. Sending/streaming displays the raw buffer without
	// a cursor since we're not collecting keystrokes at that moment.
	if sending {
		return renderSendingInputBuffer(input)
	}
	runes := []rune(input)
	total := len(runes)
	if manual && manualInput != input {
		manual = false
	}
	if !manual {
		cursor = total
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > total {
		cursor = total
	}
	before := string(runes[:cursor])
	after := string(runes[cursor:])
	withCursor := before + "|" + after
	logical := strings.Split(withCursor, "\n")
	out := make([]string, 0, len(logical))
	for i, row := range logical {
		prefix := "> "
		if i > 0 {
			prefix = "  "
		}
		out = append(out, prefix+row)
	}
	return strings.Join(out, "\n")
}

// renderSendingInputBuffer prints the frozen input while a turn is streaming
// (no cursor, just the text with the same prompt rules as the live editor).
func renderSendingInputBuffer(input string) string {
	if !strings.ContainsRune(input, '\n') {
		return "> " + input
	}
	logical := strings.Split(input, "\n")
	out := make([]string, 0, len(logical))
	for i, row := range logical {
		prefix := "> "
		if i > 0 {
			prefix = "  "
		}
		out = append(out, prefix+row)
	}
	return strings.Join(out, "\n")
}

func chatDigest(text string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(text, "\r\n", "\n"))
	if trimmed == "" {
		return ""
	}
	preview := trimmed
	if first, _, ok := strings.Cut(trimmed, "\n"); ok {
		first = strings.TrimSpace(first)
		if first == "" {
			first = "[multiline]"
		}
		preview = first + " ..."
	}
	return preview
}

func blankFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func formatProviderProfileSummaryTUI(profile engine.ProviderProfileStatus) string {
	name := strings.TrimSpace(profile.Name)
	model := strings.TrimSpace(profile.Model)
	protocol := strings.TrimSpace(profile.Protocol)
	baseURL := strings.TrimSpace(profile.BaseURL)
	if name == "" && model == "" && protocol == "" && baseURL == "" && profile.MaxContext <= 0 && profile.MaxTokens <= 0 {
		return "unavailable"
	}

	parts := make([]string, 0, 6)
	if name != "" || model != "" {
		parts = append(parts, fmt.Sprintf("%s/%s", blankFallback(name, "-"), blankFallback(model, "-")))
	}
	if protocol != "" {
		parts = append(parts, "proto="+protocol)
	}
	if profile.MaxContext > 0 {
		parts = append(parts, fmt.Sprintf("ctx=%d", profile.MaxContext))
	}
	if profile.MaxTokens > 0 {
		parts = append(parts, fmt.Sprintf("out=%d", profile.MaxTokens))
	}
	if baseURL != "" {
		parts = append(parts, "endpoint="+baseURL)
	}
	parts = append(parts, "configured="+fmt.Sprintf("%t", profile.Configured))
	return strings.Join(parts, " ")
}

func providerConnectivityHintTUI(st engine.Status) string {
	providerName := strings.ToLower(strings.TrimSpace(st.Provider))
	profile := st.ProviderProfile
	if providerName == "offline" {
		return "offline provider active"
	}
	if profile.Configured {
		return "provider credentials detected"
	}
	if providerName == "" {
		return "provider unknown"
	}
	return "provider may fallback offline (missing api_key/base_url); update env and run /reload"
}

func formatModelsDevCacheSummaryTUI(cache engine.ModelsDevCacheStatus) string {
	path := strings.TrimSpace(cache.Path)
	if path == "" {
		return "unavailable"
	}
	if !cache.Exists {
		return "missing"
	}
	parts := []string{"ready"}
	if !cache.UpdatedAt.IsZero() {
		parts = append(parts, "updated="+cache.UpdatedAt.Format("2006-01-02 15:04"))
	}
	if cache.SizeBytes > 0 {
		parts = append(parts, fmt.Sprintf("size=%d", cache.SizeBytes))
	}
	return strings.Join(parts, " ")
}

func isMutationTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "write_file", "edit_file":
		return true
	default:
		return false
	}
}


func (m Model) togglePinnedFile() (tea.Model, tea.Cmd) {
	target := strings.TrimSpace(m.selectedFile())
	if target == "" {
		m.notice = "No file selected."
		return m, nil
	}
	if strings.EqualFold(strings.TrimSpace(m.pinnedFile), target) {
		m.pinnedFile = ""
		m.notice = "Cleared pinned file."
		return m, nil
	}
	m.pinnedFile = target
	m.notice = "Pinned " + target + " for chat context."
	return m, nil
}

func (m Model) focusChatWithFileMarker(rel, action string) (tea.Model, tea.Cmd) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		m.notice = "No file selected."
		return m, nil
	}

	marker := fileMarker(rel)
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "explain":
		m.input = composeChatPrompt("Explain "+marker, "")
		m.notice = "Explain prompt prepared for " + rel
	case "review":
		m.input = composeChatPrompt("Review "+marker+" for bugs, risks, and missing tests.", "")
		m.notice = "Review prompt prepared for " + rel
	default:
		m.input = composeChatPrompt(m.input, marker)
		m.notice = "Inserted file marker for " + rel
	}
	m.activeTab = 0
	return m, nil
}

func (m Model) focusChangedFiles(changed []string) Model {
	if len(changed) == 0 {
		return m
	}
	target := strings.TrimSpace(m.pinnedFile)
	if target == "" || !containsStringFold(changed, target) {
		target = strings.TrimSpace(changed[0])
	}
	if target == "" {
		return m
	}
	for i, path := range m.files {
		if strings.EqualFold(strings.TrimSpace(path), target) {
			m.fileIndex = i
			return m
		}
	}
	return m
}

func (m Model) refreshToolMutationState(path string) Model {
	if m.eng == nil {
		return m
	}
	root := strings.TrimSpace(m.eng.Status().ProjectRoot)
	if root == "" {
		root = "."
	}
	if files, err := listProjectFiles(root, 500); err == nil {
		m.files = files
	}
	if diff, err := gitWorkingDiff(root, 120_000); err == nil {
		m.diff = diff
	}
	if changed, err := gitChangedFiles(root, 12); err == nil {
		m.changed = changed
		m = m.focusChangedFiles(changed)
	}
	path = strings.TrimSpace(path)
	if path != "" {
		m.filePath = path
		if idx := indexOfString(m.files, path); idx >= 0 {
			m.fileIndex = idx
		}
		if content, size, err := readProjectFile(root, path, 32_000); err == nil {
			m.filePreview = content
			m.fileSize = size
		}
	}
	m.activeTab = 3
	if len(m.changed) > 0 {
		m.notice = "Tool updated workspace: " + strings.Join(m.changed, ", ")
	} else {
		m.notice = "Tool updated workspace."
	}
	return m
}


// Slash-command autocomplete (slashMenuActive,
// activeSlashArgSuggestions, autocompleteSlashArg/Command,
// expandSlashSelection, slashAssistHints, slashCommandCatalog,
// slashTemplateOverrides, formatSlash*, toolParamKey* /
// toolValueToken*, the *Suggestions feeders) lives in
// slash_picker.go.

// isKnownChatCommandToken reports whether a bare word (without the leading /)
// matches a registered canonical command or alias in the shared registry, or
// one of the TUI-only slash utilities. Used by the input parser to classify
// tokens as commands vs. ordinary chat text.
func isKnownChatCommandToken(token string) bool {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return false
	}
	switch token {
	case "reload", "providers", "models", "tools", "ls", "read", "grep", "run", "diff", "patch", "apply", "undo",
		"continue", "resume", "btw", "quit", "exit", "q", "clear", "coach", "hints":
		return true
	}
	if _, ok := commands.DefaultRegistry().Lookup(token); ok {
		return true
	}
	return false
}

func (m Model) chatPrompt() string {
	question := strings.TrimSpace(expandAtFileMentionsWithRecent(m.input, m.files, m.engineRecentFiles()))
	if pinned := strings.TrimSpace(m.pinnedFile); pinned != "" {
		question = composeChatPrompt(question, fileMarker(pinned))
	}
	return strings.TrimSpace(question)
}

func composeChatPrompt(current, addition string) string {
	current = strings.TrimSpace(current)
	addition = strings.TrimSpace(addition)
	switch {
	case current == "":
		return addition
	case addition == "":
		return current
	case strings.Contains(current, addition):
		return current
	case strings.HasSuffix(current, "[[file:") || strings.HasSuffix(current, " ") || strings.HasSuffix(current, "\n"):
		return current + addition
	default:
		return current + " " + addition
	}
}

func fileMarker(rel string) string {
	return fileMarkerRange(rel, "")
}

// fileMarkerRange emits the context-manager marker with an optional line
// range suffix (`#L10` or `#L10-L50`). The context manager's regex only
// accepts `#L<start>[-L?<end>]`, so callers must pass a pre-normalized
// suffix (see splitMentionToken).
func fileMarkerRange(rel, rangeSuffix string) string {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" {
		return ""
	}
	rangeSuffix = strings.TrimSpace(rangeSuffix)
	return "[[file:" + rel + rangeSuffix + "]]"
}

func (m Model) recommendedRunCommandPreset() string {
	suggestions := m.runCommandSuggestions()
	if len(suggestions) == 0 {
		return `command=go args="version" dir=. timeout_ms=10000`
	}
	lowerInput := strings.ToLower(strings.TrimSpace(m.input))
	selected := strings.TrimSpace(m.toolTargetFile())
	for _, suggestion := range suggestions {
		switch {
		case strings.Contains(lowerInput, "build") && strings.Contains(suggestion, ` args="build`):
			return suggestion
		case strings.Contains(lowerInput, "test") && strings.Contains(suggestion, ` args="test`):
			return suggestion
		case selected != "" && strings.Contains(suggestion, "-count=1"):
			return suggestion
		}
	}
	for _, suggestion := range suggestions {
		if strings.Contains(suggestion, `args="version"`) || strings.Contains(suggestion, `args="status --short"`) {
			return suggestion
		}
	}
	return suggestions[0]
}

func (m Model) runCommandSuggestions() []string {
	root := "."
	if m.eng != nil {
		root = strings.TrimSpace(m.eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
	}
	selected := strings.TrimSpace(m.toolTargetFile())
	selectedDir := "."
	if selected != "" {
		selectedDir = filepath.ToSlash(filepath.Dir(selected))
		if selectedDir == "" {
			selectedDir = "."
		}
	}

	suggestions := make([]string, 0, 6)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if slices.Contains(suggestions, value) {
			return
		}
		suggestions = append(suggestions, value)
	}

	if m.projectHasFile("go.mod") {
		if selected != "" && selectedDir != "." {
			add(fmt.Sprintf(`command=go args="test ./%s -count=1" dir=. timeout_ms=20000`, selectedDir))
		}
		add(`command=go args="test ./... -count=1" dir=. timeout_ms=30000`)
		add(`command=go args="build ./cmd/dfmc" dir=. timeout_ms=30000`)
		if selected != "" && strings.HasSuffix(strings.ToLower(selected), ".go") {
			add(fmt.Sprintf(`command=gofmt args="-w %s" dir=. timeout_ms=10000`, selected))
		}
	}
	if m.projectHasFile("package.json") {
		add(`command=npm args="test" dir=. timeout_ms=30000`)
		add(`command=npm args="run build" dir=. timeout_ms=30000`)
	}
	if m.projectHasFile("pyproject.toml") || m.projectHasFile("requirements.txt") || m.projectHasFile("setup.py") {
		if selected != "" && strings.HasSuffix(strings.ToLower(selected), ".py") {
			add(fmt.Sprintf(`command=pytest args="%s -q" dir=. timeout_ms=30000`, selected))
		}
		add(`command=pytest args="-q" dir=. timeout_ms=30000`)
	}
	if m.projectHasFile("Cargo.toml") {
		add(`command=cargo args="test" dir=. timeout_ms=30000`)
		add(`command=cargo args="build" dir=. timeout_ms=30000`)
	}
	if m.projectHasFile("Makefile") {
		add(`command=make args="test" dir=. timeout_ms=30000`)
	}
	add(`command=go args="version" dir=. timeout_ms=10000`)
	add(`command=git args="status --short" dir=. timeout_ms=10000`)

	_ = root
	return suggestions
}

func (m Model) projectHasFile(name string) bool {
	name = filepath.ToSlash(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	for _, path := range m.files {
		if strings.EqualFold(filepath.ToSlash(strings.TrimSpace(path)), name) {
			return true
		}
	}
	root := "."
	if m.eng != nil {
		root = strings.TrimSpace(m.eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(name)))
	return err == nil && !info.IsDir()
}

func containsStringFold(items []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}

// activeMentionQuery extracts the file query and optional range suffix from
// the `@token` currently under the cursor. Returns (query, rangeSuffix, ok):
// - query: the file path prefix to rank against, stripped of any range
// - rangeSuffix: normalized `#L10[-L50]` form (empty when no range was typed)
// - ok: true only when the current token starts with `@` and has at least
//   one character of query body
func activeMentionQuery(input string) (string, string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", false
	}
	lastSpace := strings.LastIndexAny(input, " \t\n")
	token := input
	if lastSpace >= 0 {
		token = input[lastSpace+1:]
	}
	if !strings.HasPrefix(token, "@") {
		return "", "", false
	}
	body := strings.TrimPrefix(token, "@")
	query, rangeSuffix := splitMentionToken(body)
	return query, rangeSuffix, true
}

// mentionRow is a render-ready picker entry. Recent flags files the engine's
// working memory has recently touched so the UI can badge them without
// re-querying the engine at draw time.
type mentionRow struct {
	Path   string
	Recent bool
}

func (m Model) mentionSuggestions(query string, limit int) []mentionRow {
	ranker := newMentionRanker(m.files, m.engineRecentFiles())
	ranked := ranker.rank(query, limit)
	out := make([]mentionRow, 0, len(ranked))
	for _, c := range ranked {
		out = append(out, mentionRow{Path: c.path, Recent: c.recent})
	}
	return out
}

func replaceActiveMention(input, path, rangeSuffix string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return input
	}
	lastSpace := strings.LastIndexAny(input, " \t\n")
	prefix := ""
	tokenStart := 0
	if lastSpace >= 0 {
		prefix = input[:lastSpace+1]
		tokenStart = lastSpace + 1
	}
	token := input[tokenStart:]
	if !strings.HasPrefix(token, "@") {
		return input
	}
	return prefix + fileMarkerRange(path, rangeSuffix)
}

func expandAtFileMentionsWithRecent(input string, files, recent []string) string {
	tokens := strings.Fields(input)
	if len(tokens) == 0 {
		return input
	}
	changed := false
	for i, token := range tokens {
		if !strings.HasPrefix(token, "@") || len(token) < 2 {
			continue
		}
		body := filepath.ToSlash(strings.TrimSpace(strings.TrimPrefix(token, "@")))
		if body == "" {
			continue
		}
		query, rangeSuffix := splitMentionToken(body)
		if resolved, ok := resolveMentionQuery(files, recent, query); ok {
			tokens[i] = fileMarkerRange(resolved, rangeSuffix)
			changed = true
		}
	}
	if !changed {
		return input
	}
	return strings.Join(tokens, " ")
}

// clearStreamCancel drops the stored per-stream CancelFunc. Called from
// every chat-lifecycle terminus (done, err, closed, explicit cancel) so
// the next send starts clean and a stale cancel func can't be fired after
// the stream it owned already finished.
func (m *Model) clearStreamCancel() {
	m.streamCancel = nil
}

// cancelActiveStream aborts an in-flight chat stream if one is running.
// Returns true if a cancel fired — the caller uses that to decide whether
// to emit the "cancelled by user" notice vs. fall through to other esc
// behavior like dismissing the parked-resume banner. The userCancelled
// flag lets the chatErrMsg reader distinguish a clean user-driven stop
// from a provider/network error so we can tailor the message.
func (m *Model) cancelActiveStream() bool {
	if m.streamCancel == nil {
		return false
	}
	m.streamCancel()
	m.streamCancel = nil
	m.userCancelledStream = true
	return true
}

// renderContextStrip summarizes what will be attached to the next message:
// pinned file, inline [[file:...]] markers, fenced code blocks, and — the
// piece that actually matters to providers — a heuristic token count with
// percent-of-budget when the provider profile declares MaxContext. chars
// are kept too since they answer a different "am I about to spam?" concern
// but tokens drive what the API will accept.
// Returns "" when nothing is attached so we don't paint a dead strip.
func (m Model) renderContextStrip(width int) string {
	if width < 40 {
		width = 40
	}
	input := m.input

	pinned := strings.TrimSpace(m.pinnedFile)
	markerCount := countFileMarkers(input)
	fenceCount := countFencedBlocks(input)
	atMentions := countAtMentions(input)

	// Nothing to show — the strip disappears when the composer is resting.
	if pinned == "" && markerCount == 0 && fenceCount == 0 && atMentions == 0 && strings.TrimSpace(input) == "" {
		return ""
	}

	parts := []string{accentStyle.Render("📎 context")}
	if pinned != "" {
		parts = append(parts, subtleStyle.Render("pinned:")+" "+boldStyle.Render(pinned))
	}
	if markerCount > 0 {
		parts = append(parts, subtleStyle.Render("markers:")+" "+boldStyle.Render(fmt.Sprintf("%d", markerCount)))
	}
	if atMentions > 0 {
		// Unresolved @mentions — these still get resolved at send time, but
		// counting them separately shows users which pieces are bare refs vs
		// concrete [[file:]] markers.
		parts = append(parts, subtleStyle.Render("@refs:")+" "+boldStyle.Render(fmt.Sprintf("%d", atMentions)))
	}
	if fenceCount > 0 {
		parts = append(parts, subtleStyle.Render("fenced:")+" "+boldStyle.Render(fmt.Sprintf("%d", fenceCount)))
	}
	if trimmed := strings.TrimSpace(input); trimmed != "" {
		chars := len([]rune(trimmed))
		parts = append(parts, subtleStyle.Render("chars:")+" "+boldStyle.Render(fmt.Sprintf("%d", chars)))
		// Token projection: heuristic is fast, safe, zero-alloc enough for
		// every-frame rendering. When the provider declares MaxContext, show
		// percent-of-budget so users can tell at a glance whether they're
		// about to pack 200 tokens or 20000 into the next turn.
		tok := tokens.Estimate(trimmed)
		budget := m.status.ProviderProfile.MaxContext
		if budget <= 0 && m.status.ContextIn != nil {
			budget = m.status.ContextIn.ProviderMaxContext
		}
		tokenLabel := fmt.Sprintf("~%d", tok)
		if budget > 0 {
			pct := int(float64(tok) / float64(budget) * 100)
			tokenLabel = fmt.Sprintf("~%d (%d%% of %d)", tok, pct, budget)
		}
		parts = append(parts, subtleStyle.Render("tokens:")+" "+boldStyle.Render(tokenLabel))
	}

	joined := strings.Join(parts, subtleStyle.Render("  ·  "))
	return "  " + truncateSingleLine(joined, width-2)
}

// countFileMarkers counts `[[file:...]]` markers in the current input. The
// regex mirrors what the context manager resolves.
func countFileMarkers(s string) int {
	return strings.Count(s, "[[file:")
}

// countFencedBlocks counts complete triple-backtick blocks in the input.
// Odd fences (open but not yet closed) are treated as zero — the user is
// still mid-edit so we don't surface a partial count.
func countFencedBlocks(s string) int {
	n := strings.Count(s, "```")
	return n / 2
}

// countAtMentions counts bare `@token` refs that start after whitespace or
// at string start. Matches only well-formed references that the resolve
// pass would actually try to expand.
func countAtMentions(s string) int {
	if !strings.Contains(s, "@") {
		return 0
	}
	count := 0
	prevSpace := true
	for _, r := range s {
		if r == '@' && prevSpace {
			count++
		}
		prevSpace = r == ' ' || r == '\t' || r == '\n'
	}
	return count
}

// renderMentionPickerModal frames the @ file picker as a visible bordered
// box — the earlier inline list looked like a passive suggestion strip and
// users didn't realise they could commit with enter. The modal makes the
// state change obvious and teaches the keys on every render. Width is
// clamped by the caller so a tiny terminal doesn't crash the layout.
func renderMentionPickerModal(s chatSuggestionState, mentionIndex, totalFiles int, width int) string {
	if width < 40 {
		width = 40
	}
	// Title bar — uses the accent style so the eye locks on.
	title := accentStyle.Bold(true).Render("◆ File Picker") +
		subtleStyle.Render("  —  ") +
		boldStyle.Render("@"+s.MentionQuery())
	if s.MentionRange() != "" {
		title += subtleStyle.Render(" · range "+s.MentionRange())
	}

	countLine := ""
	switch {
	case len(s.MentionSuggestions()) > 0:
		countLine = subtleStyle.Render(fmt.Sprintf(
			"%d/%d files match", len(s.MentionSuggestions()), totalFiles))
	case totalFiles == 0:
		countLine = warnStyle.Render("file index empty")
	default:
		countLine = warnStyle.Render("no files match")
	}

	// Body — either the match rows, or a descriptive empty state.
	bodyLines := []string{}
	switch {
	case len(s.MentionSuggestions()) > 0:
		selected := clampIndex(mentionIndex, len(s.MentionSuggestions()))
		for i, row := range s.MentionSuggestions() {
			label := truncateSingleLine(row.Path, width-6)
			if row.Recent {
				label += " " + subtleStyle.Render("· recent")
			}
			if i == selected {
				bodyLines = append(bodyLines, mentionSelectedRowStyle.Render("▶ "+label))
			} else {
				bodyLines = append(bodyLines, "  "+label)
			}
		}
	case totalFiles == 0:
		bodyLines = append(bodyLines,
			subtleStyle.Render("Indexing project files…"),
			subtleStyle.Render("If this persists, open the Files tab (F3) and press r to reload,"),
			subtleStyle.Render("or confirm you launched dfmc from a project root."),
		)
	case s.MentionQuery() != "":
		bodyLines = append(bodyLines,
			subtleStyle.Render("No files matched '"+s.MentionQuery()+"'."),
			subtleStyle.Render("Refine the query or press esc to cancel."),
		)
	default:
		bodyLines = append(bodyLines,
			subtleStyle.Render("Type a path after @ to filter."),
			subtleStyle.Render("Ranges: auth.go:10-50 or auth.go#L10-L50 attaches that slice."),
		)
	}

	// Footer — always show how to drive it so users don't have to remember.
	footer := subtleStyle.Render(
		"↑/↓ move · tab/enter insert as [[file:…]] · esc cancel")

	parts := []string{title, countLine, ""}
	parts = append(parts, bodyLines...)
	parts = append(parts, "", footer)
	return mentionPickerStyle.Width(width).Render(strings.Join(parts, "\n"))
}

// MentionQuery and friends expose chatSuggestionState fields to callers in
// other files. Keeping them as methods rather than exporting the fields lets
// the render code remain in this file while tests can construct the state
// directly via the unexported fields.
func (s chatSuggestionState) MentionQuery() string          { return s.mentionQuery }
func (s chatSuggestionState) MentionRange() string          { return s.mentionRange }
func (s chatSuggestionState) MentionSuggestions() []mentionRow {
	return s.mentionSuggestions
}

// renderSlashPickerModal frames the `/` command picker in the same bordered
// modal style as the file picker. Consistency with the @ modal makes the
// composer feel like it has two first-class picker affordances rather than
// two different "kind of a dropdown" experiences.
func renderSlashPickerModal(items []slashCommandItem, slashIndex, width int) string {
	if width < 40 {
		width = 40
	}
	title := accentStyle.Bold(true).Render("◆ Commands") +
		subtleStyle.Render("  —  type to filter, enter to run")

	count := ""
	if len(items) > 0 {
		count = subtleStyle.Render(fmt.Sprintf("%d matching · window of 6", len(items)))
	} else {
		count = warnStyle.Render("no match")
	}

	body := []string{}
	if len(items) == 0 {
		body = append(body,
			subtleStyle.Render("No command matched the current prefix."),
			subtleStyle.Render("Press esc to dismiss, or /help for the full catalog."),
		)
	} else {
		selected := clampIndex(slashIndex, len(items))
		start := 0
		if selected > 4 {
			start = selected - 4
		}
		end := start + 6
		if end > len(items) {
			end = len(items)
		}
		for i := start; i < end; i++ {
			line := fmt.Sprintf("%s  %s", items[i].Template,
				subtleStyle.Render("· "+items[i].Description))
			label := truncateSingleLine(line, width-6)
			if i == selected {
				body = append(body, mentionSelectedRowStyle.Render("▶ "+label))
			} else {
				body = append(body, "  "+label)
			}
		}
	}

	footer := subtleStyle.Render("↑/↓ move · tab cycle · enter run · esc cancel")

	parts := []string{title, count, ""}
	parts = append(parts, body...)
	parts = append(parts, "", footer)
	return mentionPickerStyle.Width(width).Render(strings.Join(parts, "\n"))
}

func indexOfString(items []string, target string) int {
	target = strings.TrimSpace(target)
	for i, item := range items {
		if strings.TrimSpace(item) == target {
			return i
		}
	}
	return -1
}

// Patch parsing & apply (patchSectionPaths, totalPatchHunks,
// patchLineCounts, extractPatchedFiles, parseUnifiedDiffSections,
// normalizePatchPath, extractPatchHunks, gitWorkingDiff,
// latestAssistantUnifiedDiff, extractUnifiedDiff,
// looksLikeUnifiedDiff, applyUnifiedDiff) lives in patch_parse.go.

func clampIndex(index, length int) int {
	if length <= 0 {
		return 0
	}
	if index < 0 {
		return 0
	}
	if index >= length {
		return length - 1
	}
	return index
}

func truncateCommandBlock(text string, max int) string {
	trimmed := strings.TrimSpace(text)
	if max <= 0 || len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "\n... [truncated]"
}


func (m Model) selectedFile() string {
	if len(m.files) == 0 {
		return ""
	}
	if m.fileIndex < 0 {
		return m.files[0]
	}
	if m.fileIndex >= len(m.files) {
		return m.files[len(m.files)-1]
	}
	return m.files[m.fileIndex]
}

// truncateSingleLine clips `text` to at most `width` visible terminal cells.
// ANSI styling is preserved — we count display width, not runes or bytes —
// so a styled label like lipgloss.Bold("streaming") doesn't get clipped to
// "stre..." just because its escape sequences puffed the rune count.
func truncateSingleLine(text string, width int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if width <= 0 {
		return trimmed
	}
	if ansi.StringWidth(trimmed) <= width {
		return trimmed
	}
	if width <= 3 {
		return ansi.Truncate(trimmed, width, "")
	}
	return ansi.Truncate(trimmed, width, "…")
}

func formatCommandPickerItem(item commandPickerItem) string {
	value := strings.TrimSpace(item.Value)
	desc := strings.TrimSpace(item.Description)
	meta := strings.TrimSpace(item.Meta)
	switch {
	case desc != "" && meta != "":
		return value + " - " + desc + " - " + meta
	case desc != "":
		return value + " - " + desc
	case meta != "":
		return value + " - " + meta
	default:
		return value
	}
}

func fitPanelContentHeight(content string, maxLines int) string {
	if maxLines <= 0 {
		return content
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) > maxLines {
		if maxLines >= 2 {
			lines = append(lines[:maxLines-1], subtleStyle.Render("..."))
		} else {
			lines = lines[:maxLines]
		}
	}
	return strings.Join(lines, "\n")
}


func gitChangedFiles(projectRoot string, limit int) ([]string, error) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		root = "."
	}
	cmd := exec.Command("git", "-C", root, "status", "--short", "--")
	out, err := cmd.Output()
	if err != nil {
		if ee := (&exec.ExitError{}); errors.As(err, &ee) {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	text := strings.ReplaceAll(string(out), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	files := make([]string, 0, len(lines))
	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if len(raw) > 3 {
			files = append(files, strings.TrimSpace(raw[3:]))
		} else {
			files = append(files, strings.TrimSpace(raw))
		}
		if limit > 0 && len(files) >= limit {
			break
		}
	}
	return files, nil
}

func listProjectFiles(root string, limit int) ([]string, error) {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	out := make([]string, 0, limit)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".dfmc", "node_modules", "vendor", "dist", "bin":
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		if limit > 0 && len(out) >= limit {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && err != fs.SkipAll {
		return nil, err
	}
	return out, nil
}

func readProjectFile(root, rel string, maxBytes int) (string, int, error) {
	if strings.TrimSpace(rel) == "" {
		return "", 0, nil
	}
	target, err := resolvePathWithinRoot(root, rel)
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", 0, err
	}
	if info.IsDir() {
		return "", 0, fmt.Errorf("path is a directory: %s", rel)
	}
	if hasBinaryPreviewExtension(rel) {
		size := int(info.Size())
		return fmt.Sprintf("Binary preview disabled for %s.\nSize: %d bytes.\nUse an external viewer for this file type.", filepath.ToSlash(rel), size), size, nil
	}
	// Refuse to read secret-shaped files into the panel — even one auto-
	// preview of `.env` is enough to publish API keys to anyone watching
	// the screen. The user can still copy the file into a chat message
	// with explicit consent if they really need to inspect it.
	if looksLikeSecretFile(rel) {
		size := int(info.Size())
		notice := "🔒 Preview suppressed — this file matches a secret-bearing shape\n" +
			"  (" + filepath.ToSlash(rel) + ", " + fmt.Sprintf("%d bytes", size) + ").\n\n" +
			"Reasoning: the Files panel auto-previews on selection, so any keys in here\n" +
			"would land on screen the moment you opened the tab. If you genuinely need to\n" +
			"see the contents, ask in chat (e.g. \"show me .env\") so the read is explicit."
		return notice, size, nil
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", 0, err
	}
	size := len(data)
	if looksBinaryPreview(data) {
		return fmt.Sprintf("Binary preview disabled for %s.\nSize: %d bytes.\nUse an external viewer for this file type.", filepath.ToSlash(rel), size), size, nil
	}
	if maxBytes > 0 && size > maxBytes {
		data = append(data[:maxBytes], []byte("\n... [truncated]\n")...)
	}
	return string(data), size, nil
}

func resolvePathWithinRoot(root, rel string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target := rel
	if !filepath.IsAbs(target) {
		target = filepath.Join(absRoot, rel)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rootWithSep := absRoot
	if !strings.HasSuffix(rootWithSep, string(filepath.Separator)) {
		rootWithSep += string(filepath.Separator)
	}
	if absTarget != absRoot && !strings.HasPrefix(absTarget, rootWithSep) {
		return "", fmt.Errorf("path escapes project root: %s", rel)
	}
	return absTarget, nil
}

func looksBinaryPreview(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	sample := data
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	if !utf8.Valid(sample) {
		return true
	}
	bad := 0
	for _, b := range sample {
		if b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		if b < 0x20 || b == 0x7f {
			bad++
		}
	}
	return float64(bad)/float64(len(sample)) > 0.12
}

func hasBinaryPreviewExtension(path string) bool {
	switch strings.ToLower(strings.TrimSpace(filepath.Ext(path))) {
	case ".exe", ".dll", ".so", ".dylib", ".a", ".o", ".obj", ".class", ".jar", ".war", ".zip", ".tar", ".gz", ".7z", ".bz2", ".xz", ".png", ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".pdf", ".woff", ".woff2", ".ttf", ".otf":
		return true
	default:
		return false
	}
}

// renderTUIHelp builds the /help body: the registry-backed catalog of TUI
// verbs followed by a short section of TUI-only slash shortcuts and panel
// hotkeys that don't exist as standalone CLI commands.
func renderTUIHelp() string {
	reg := commands.DefaultRegistry()
	catalog := reg.RenderHelp(commands.SurfaceTUI, "Slash commands:")
	tail := strings.Join([]string{
		"",
		"",
		"TUI-only shortcuts:",
		"    /reload                      Reload engine configuration",
		"    /clear                       Clear transcript (memory untouched)",
		"    /compact [N]                 Collapse older transcript into a summary (keeps last N; default 6)",
		"    /approve                     Show tool-approval gate state (which tools prompt agent calls)",
		"    /hooks                       List lifecycle hooks registered per event",
		"    /doctor                      In-chat health snapshot (alias /health)",
		"    /stats                       Session metrics (alias /tokens, /cost)",
		"    /export [PATH]               Save transcript to markdown (default .dfmc/exports/transcript-*.md)",
		"    /quit                        Exit DFMC",
		"    /coach                       Mute or unmute background coach notes",
		"    /hints                       Show or hide between-round trajectory hints",
		"    /tools                       Show tool surface",
		"    /tool show NAME              Print the spec for NAME (args, risk, examples)",
		"    /diff                        Show staged patch diff",
		"    /patch                       Open the patch panel",
		"    /apply [--check]             Apply (or dry-run) the staged patch",
		"    /undo                        Undo the last assistant message",
		"    /ls [PATH] [-r] [--max N]    List files",
		"    /read PATH [START] [END]     Read a file range",
		"    /grep PATTERN                Search the project",
		"    /run COMMAND [ARGS...]       Run a shell command",
		"    /continue                    Resume a parked agent loop",
		"    /split TASK                  Decompose a broad task into subtasks",
		"    /btw NOTE                    Queue a note for the next tool-loop step",
		"",
		"Mentions: @file.go picks a file · @file.go:10-50 or @file.go#L10-L50 attaches a range.",
		"Panels: F1 Chat · F2 Status · F3 Files · F4 Patch · F5 Setup · F6 Tools · Ctrl+P palette",
		"Run /help <command> for details on a specific command.",
	}, "\n")
	return catalog + tail
}

// renderTUICommandHelp prints the detail view for a single registry command,
// or a short error + catalog pointer when unknown.
func renderTUICommandHelp(name string) string {
	reg := commands.DefaultRegistry()
	if detail := reg.RenderCommandHelp(name); detail != "" {
		return detail
	}
	return fmt.Sprintf("Unknown command: %s. Try /help for the catalog.", name)
}

// renderSplitPlan formats a planning.Plan as a chat transcript block. Each
// subtask gets a numbered bullet with its hint tag ("numbered-list",
// "stage", "conjunction") so the user can see *why* the splitter chose to
// break it. When the query doesn't decompose, the block says so — no silent
// no-op that leaves the user wondering if the command ran.
func renderSplitPlan(plan planning.Plan) string {
	if len(plan.Subtasks) <= 1 {
		return "/split — this task looks atomic (the splitter couldn't find parallel units). Ask it more narrowly or dispatch it as-is."
	}
	mode := "sequential"
	if plan.Parallel {
		mode = "parallel"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "/split — %d subtasks (%s, confidence %.2f):\n", len(plan.Subtasks), mode, plan.Confidence)
	for i, s := range plan.Subtasks {
		fmt.Fprintf(&b, "  %d. [%s] %s\n", i+1, s.Hint, strings.TrimSpace(s.Title))
		if desc := strings.TrimSpace(s.Description); desc != "" && desc != strings.TrimSpace(s.Title) {
			fmt.Fprintf(&b, "     %s\n", truncateSingleLine(desc, 160))
		}
	}
	if plan.Parallel {
		b.WriteString("\nDispatch each with a focused /ask or /continue — the model can fan them out in parallel.")
	} else {
		b.WriteString("\nRun them one at a time — the splitter detected ordering markers (first/then).")
	}
	return b.String()
}
