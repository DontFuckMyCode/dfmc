package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
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
	"github.com/dontfuckmycode/dfmc/internal/planning"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	toolruntime "github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type Options struct {
	AltScreen bool
}

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
	mentionQuery        string
	mentionRange        string
	mentionSuggestions  []mentionRow
	quickActions        []quickActionSuggestion
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
		tabs:              []string{"Chat", "Status", "Files", "Patch", "Setup", "Tools"},
		streamIndex:       -1,
		inputHistoryIndex: -1,
		toolOverrides: map[string]string{},
		// The chat body shows the welcome + starters on first paint; don't
		// park a duplicate banner in the footer notice slot (signal density).
		sessionStart:   time.Now(),
		showStatsPanel: true,
	}
}

func Run(ctx context.Context, eng *engine.Engine, opts Options) error {
	model := NewModel(ctx, eng)
	programOpts := []tea.ProgramOption{
		// Mouse wheel scrolls the chat transcript. Cell-motion is the
		// lighter of the two mouse modes — we only care about wheel events,
		// not drag, so we don't need all-motion tracking.
		tea.WithMouseCellMotion(),
	}
	if opts.AltScreen {
		programOpts = append(programOpts, tea.WithAltScreen())
	}
	p := tea.NewProgram(model, programOpts...)
	_, err := p.Run()
	return err
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
		m.resetAgentRuntime()
		m.pendingNoteCount = 0
		m.notice = "chat: " + msg.err.Error()
		if len(m.pendingQueue) > 0 {
			m.notice += fmt.Sprintf(" — %d queued message(s) kept.", len(m.pendingQueue))
		}
		return m, nil

	case streamClosedMsg:
		m.sending = false
		m.streamMessages = nil
		m.streamIndex = -1
		m.resetAgentRuntime()
		m.pendingNoteCount = 0
		next, drainCmd := m.drainPendingQueue()
		return next, drainCmd

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit
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
			return m, nil
		case "f6":
			m.activeTab = 5
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
		}
	}
	return m, nil
}

func (m Model) handleChatKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.commandPickerActive {
		return m.handleCommandPickerKey(msg)
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
	case tea.KeyPgUp:
		m.scrollTranscript(-8)
		return m, nil
	case tea.KeyPgDown:
		m.scrollTranscript(8)
		return m, nil
	case tea.KeyEsc:
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
	case tea.KeyEnter:
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
			m.pendingQueue = append(m.pendingQueue, raw)
			m.setChatInput("")
			m.notice = fmt.Sprintf("Queued (%d) — will send after the current reply finishes.", len(m.pendingQueue))
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
	return m, nil
}

func (m *Model) syncChatCursor() {
	max := len([]rune(m.input))
	if m.chatCursorManual && m.chatCursorInput != m.input {
		m.chatCursorManual = false
	}
	if !m.chatCursorManual {
		m.chatCursor = max
		m.chatCursorInput = m.input
		return
	}
	if m.chatCursor < 0 {
		m.chatCursor = 0
	}
	if m.chatCursor > max {
		m.chatCursor = max
	}
	m.chatCursorInput = m.input
}

func (m *Model) setChatInput(text string) {
	m.input = text
	m.chatCursorManual = false
	m.chatCursor = len([]rune(text))
	m.chatCursorInput = text
}

func (m *Model) insertInputText(text string) {
	if text == "" {
		return
	}
	m.syncChatCursor()
	runes := []rune(m.input)
	cursor := m.chatCursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	insert := []rune(text)
	updated := make([]rune, 0, len(runes)+len(insert))
	updated = append(updated, runes[:cursor]...)
	updated = append(updated, insert...)
	updated = append(updated, runes[cursor:]...)
	m.input = string(updated)
	m.chatCursor = cursor + len(insert)
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

func (m *Model) deleteInputBeforeCursor() {
	m.syncChatCursor()
	runes := []rune(m.input)
	if len(runes) == 0 || m.chatCursor <= 0 {
		return
	}
	cursor := m.chatCursor
	if cursor > len(runes) {
		cursor = len(runes)
	}
	updated := append([]rune(nil), runes[:cursor-1]...)
	updated = append(updated, runes[cursor:]...)
	m.input = string(updated)
	m.chatCursor = cursor - 1
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

func (m *Model) deleteInputAtCursor() {
	m.syncChatCursor()
	runes := []rune(m.input)
	if len(runes) == 0 {
		return
	}
	cursor := m.chatCursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(runes) {
		return
	}
	updated := append([]rune(nil), runes[:cursor]...)
	updated = append(updated, runes[cursor+1:]...)
	m.input = string(updated)
	m.chatCursor = cursor
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

func (m *Model) moveChatCursor(delta int) {
	m.syncChatCursor()
	cursor := m.chatCursor + delta
	if cursor < 0 {
		cursor = 0
	}
	max := len([]rune(m.input))
	if cursor > max {
		cursor = max
	}
	m.chatCursor = cursor
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

func (m *Model) moveChatCursorHome() {
	m.chatCursor = 0
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

func (m *Model) moveChatCursorEnd() {
	m.chatCursor = len([]rune(m.input))
	m.chatCursorManual = true
	m.chatCursorInput = m.input
}

func (m *Model) pushInputHistory(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	if n := len(m.inputHistory); n > 0 && strings.EqualFold(strings.TrimSpace(m.inputHistory[n-1]), raw) {
		m.inputHistoryIndex = -1
		m.inputHistoryDraft = ""
		return
	}
	m.inputHistory = append(m.inputHistory, raw)
	if len(m.inputHistory) > 80 {
		drop := len(m.inputHistory) - 80
		m.inputHistory = m.inputHistory[drop:]
	}
	m.inputHistoryIndex = -1
	m.inputHistoryDraft = ""
}

func (m *Model) recallInputHistoryPrev() bool {
	if len(m.inputHistory) == 0 {
		return false
	}
	if m.inputHistoryIndex < 0 {
		m.inputHistoryDraft = m.input
		m.inputHistoryIndex = len(m.inputHistory) - 1
	} else if m.inputHistoryIndex > 0 {
		m.inputHistoryIndex--
	}
	m.setChatInput(m.inputHistory[m.inputHistoryIndex])
	return true
}

func (m *Model) recallInputHistoryNext() bool {
	if len(m.inputHistory) == 0 || m.inputHistoryIndex < 0 {
		return false
	}
	if m.inputHistoryIndex < len(m.inputHistory)-1 {
		m.inputHistoryIndex++
		m.setChatInput(m.inputHistory[m.inputHistoryIndex])
		return true
	}
	draft := m.inputHistoryDraft
	m.inputHistoryIndex = -1
	m.inputHistoryDraft = ""
	m.setChatInput(draft)
	return true
}

func (m *Model) exitInputHistoryNavigation() {
	if m.inputHistoryIndex < 0 {
		return
	}
	m.inputHistoryIndex = -1
	m.inputHistoryDraft = ""
}

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
		state.mentionQuery = query
		state.mentionRange = rangeSuffix
		state.mentionSuggestions = m.mentionSuggestions(query, 8)
	}
	if !state.slashMenuActive && len(state.mentionSuggestions) == 0 && !m.commandPickerActive && !m.sending {
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

func (m Model) handleCommandPickerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m = m.closeCommandPicker()
		m.notice = "Command picker closed."
		return m, nil
	case tea.KeyCtrlS:
		m.commandPickerPersist = !m.commandPickerPersist
		if m.commandPickerPersist {
			m.notice = "Picker apply mode: persist to .dfmc/config.yaml"
		} else {
			m.notice = "Picker apply mode: session only"
		}
		return m, nil
	case tea.KeyUp:
		items := m.filteredCommandPickerItems()
		if len(items) == 0 {
			return m, nil
		}
		idx := clampIndex(m.commandPickerIndex, len(items))
		if idx > 0 {
			idx--
		}
		m.commandPickerIndex = idx
		m.notice = "Select: " + items[idx].Value
		return m, nil
	case tea.KeyDown:
		items := m.filteredCommandPickerItems()
		if len(items) == 0 {
			return m, nil
		}
		idx := clampIndex(m.commandPickerIndex, len(items))
		if idx < len(items)-1 {
			idx++
		}
		m.commandPickerIndex = idx
		m.notice = "Select: " + items[idx].Value
		return m, nil
	case tea.KeyTab:
		items := m.filteredCommandPickerItems()
		if len(items) == 0 {
			return m, nil
		}
		idx := clampIndex(m.commandPickerIndex, len(items))
		m.commandPickerQuery = items[idx].Value
		m.commandPickerIndex = 0
		m.syncInputWithCommandPicker()
		return m, nil
	case tea.KeyEnter:
		items := m.filteredCommandPickerItems()
		if len(items) == 0 {
			kind := strings.ToLower(strings.TrimSpace(m.commandPickerKind))
			if strings.EqualFold(kind, "model") && strings.TrimSpace(m.commandPickerQuery) != "" {
				return m.applyCommandPickerModel(strings.TrimSpace(m.commandPickerQuery))
			}
			if (strings.EqualFold(kind, "tool") || strings.EqualFold(kind, "read") || strings.EqualFold(kind, "run") || strings.EqualFold(kind, "grep")) && strings.TrimSpace(m.commandPickerQuery) != "" {
				return m.applyCommandPickerPreparedInput(strings.TrimSpace(m.commandPickerQuery))
			}
			m.notice = "No selectable item."
			return m, nil
		}
		idx := clampIndex(m.commandPickerIndex, len(items))
		selected := strings.TrimSpace(items[idx].Value)
		switch strings.ToLower(strings.TrimSpace(m.commandPickerKind)) {
		case "provider":
			return m.applyCommandPickerProvider(selected)
		case "model":
			return m.applyCommandPickerModel(selected)
		case "tool", "read", "run", "grep":
			return m.applyCommandPickerPreparedInput(selected)
		default:
			m.notice = "Unknown picker mode."
			return m, nil
		}
	case tea.KeyBackspace, tea.KeyCtrlH:
		if len(m.commandPickerQuery) > 0 {
			runes := []rune(m.commandPickerQuery)
			m.commandPickerQuery = string(runes[:len(runes)-1])
			m.commandPickerIndex = 0
			m.syncInputWithCommandPicker()
		}
		return m, nil
	case tea.KeySpace:
		m.commandPickerQuery += " "
		m.commandPickerIndex = 0
		m.syncInputWithCommandPicker()
		return m, nil
	case tea.KeyRunes:
		m.commandPickerQuery += string(msg.Runes)
		m.commandPickerIndex = 0
		m.syncInputWithCommandPicker()
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) startCommandPicker(kind, query string, persist bool) Model {
	kind = strings.ToLower(strings.TrimSpace(kind))
	query = strings.TrimSpace(query)
	m.commandPickerActive = true
	m.commandPickerKind = kind
	m.commandPickerPersist = persist
	m.commandPickerQuery = query
	m.commandPickerIndex = 0
	m.commandPickerAll = m.commandPickerBaseItems(kind)
	m.syncInputWithCommandPicker()
	label := strings.TrimSpace(kind)
	if label != "" {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	if label == "" {
		label = "Command"
	}
	m.notice = label + " picker ready. Enter to apply, Ctrl+S toggle persist."
	return m
}

func (m Model) closeCommandPicker() Model {
	m.commandPickerActive = false
	m.commandPickerKind = ""
	m.commandPickerQuery = ""
	m.commandPickerIndex = 0
	m.commandPickerPersist = false
	m.commandPickerAll = nil
	m.setChatInput("")
	return m
}

func (m *Model) syncInputWithCommandPicker() {
	if !m.commandPickerActive {
		return
	}
	query := strings.TrimSpace(m.commandPickerQuery)
	switch strings.ToLower(strings.TrimSpace(m.commandPickerKind)) {
	case "provider":
		if query == "" {
			m.setChatInput("/provider ")
		} else {
			m.setChatInput("/provider " + query)
		}
	case "model":
		if query == "" {
			m.setChatInput("/model ")
		} else {
			m.setChatInput("/model " + query)
		}
	case "tool":
		if query == "" {
			m.setChatInput("/tool ")
		} else {
			m.setChatInput("/tool " + query)
		}
	case "read":
		if query == "" {
			m.setChatInput("/read ")
		} else {
			m.setChatInput("/read " + query)
		}
	case "run":
		if query == "" {
			m.setChatInput("/run ")
		} else {
			m.setChatInput("/run " + query)
		}
	case "grep":
		if query == "" {
			m.setChatInput("/grep ")
		} else {
			m.setChatInput("/grep " + query)
		}
	}
}

func (m Model) commandPickerBaseItems(kind string) []commandPickerItem {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "provider":
		return m.providerPickerItems()
	case "model":
		return m.modelPickerItems(m.currentProvider())
	case "tool":
		return m.toolPickerItems()
	case "read":
		return m.readPickerItems()
	case "run":
		return m.runPickerItems()
	case "grep":
		return m.grepPickerItems()
	default:
		return nil
	}
}

func (m Model) filteredCommandPickerItems() []commandPickerItem {
	items := append([]commandPickerItem(nil), m.commandPickerAll...)
	if len(items) == 0 {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(m.commandPickerQuery))
	if query == "" {
		return items
	}
	prefix := make([]commandPickerItem, 0, len(items))
	contains := make([]commandPickerItem, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Value)
		if name == "" {
			continue
		}
		searchBlob := strings.ToLower(strings.Join([]string{name, item.Description, item.Meta}, " "))
		if strings.HasPrefix(strings.ToLower(name), query) {
			prefix = append(prefix, item)
			continue
		}
		if strings.Contains(searchBlob, query) {
			contains = append(contains, item)
		}
	}
	return append(prefix, contains...)
}

func (m Model) providerPickerItems() []commandPickerItem {
	names := m.availableProviders()
	if len(names) == 0 {
		return nil
	}
	items := make([]commandPickerItem, 0, len(names))
	active := strings.TrimSpace(m.currentProvider())
	for _, name := range names {
		profile := m.providerProfile(name)
		desc := blankFallback(profile.Protocol, "provider")
		if model := strings.TrimSpace(profile.Model); model != "" {
			desc += " | " + model
		}
		metaParts := []string{}
		if strings.EqualFold(name, active) {
			metaParts = append(metaParts, "active")
		}
		if profile.Configured {
			metaParts = append(metaParts, "configured")
		} else {
			metaParts = append(metaParts, "unconfigured")
		}
		if profile.MaxContext > 0 {
			metaParts = append(metaParts, fmt.Sprintf("ctx=%d", profile.MaxContext))
		}
		items = append(items, commandPickerItem{
			Value:       name,
			Description: desc,
			Meta:        strings.Join(metaParts, " | "),
		})
	}
	return items
}

func (m Model) modelPickerItems(providerName string) []commandPickerItem {
	models := m.availableModelsForProvider(providerName)
	if len(models) == 0 {
		return nil
	}
	currentProvider := strings.TrimSpace(m.currentProvider())
	currentModel := strings.TrimSpace(m.currentModel())
	defaultModel := strings.TrimSpace(m.defaultModelForProvider(providerName))
	items := make([]commandPickerItem, 0, len(models))
	for _, model := range models {
		metaParts := []string{}
		if strings.EqualFold(providerName, currentProvider) && strings.EqualFold(model, currentModel) {
			metaParts = append(metaParts, "active")
		}
		if strings.EqualFold(model, defaultModel) {
			metaParts = append(metaParts, "default")
		}
		if modelsDevModelKnown(providerName, model) {
			metaParts = append(metaParts, "catalog")
		}
		items = append(items, commandPickerItem{
			Value:       model,
			Description: "provider " + blankFallback(providerName, "-"),
			Meta:        strings.Join(metaParts, " | "),
		})
	}
	return items
}

func (m Model) toolPickerItems() []commandPickerItem {
	names := m.availableTools()
	if len(names) == 0 {
		return nil
	}
	items := make([]commandPickerItem, 0, len(names))
	for _, name := range names {
		desc := strings.TrimSpace(m.toolDescription(name))
		meta := strings.TrimSpace(m.toolPresetSummary(name))
		items = append(items, commandPickerItem{
			Value:       name,
			Description: desc,
			Meta:        meta,
		})
	}
	return items
}

func (m Model) readPickerItems() []commandPickerItem {
	if len(m.files) == 0 {
		return nil
	}
	items := make([]commandPickerItem, 0, len(m.files))
	target := strings.TrimSpace(m.toolTargetFile())
	for _, path := range m.files {
		path = filepath.ToSlash(strings.TrimSpace(path))
		if path == "" {
			continue
		}
		metaParts := []string{}
		if strings.EqualFold(path, target) {
			metaParts = append(metaParts, "current")
		}
		if strings.EqualFold(path, strings.TrimSpace(m.pinnedFile)) {
			metaParts = append(metaParts, "pinned")
		}
		items = append(items, commandPickerItem{
			Value:       path,
			Description: "file",
			Meta:        strings.Join(metaParts, " | "),
		})
	}
	return items
}

func (m Model) runPickerItems() []commandPickerItem {
	raw := m.runCommandSuggestions()
	if len(raw) == 0 {
		return nil
	}
	items := make([]commandPickerItem, 0, len(raw))
	for _, suggestion := range raw {
		params, err := parseToolParamString(suggestion)
		if err != nil {
			continue
		}
		command := strings.TrimSpace(fmt.Sprint(params["command"]))
		args := strings.TrimSpace(fmt.Sprint(params["args"]))
		if strings.EqualFold(args, "<nil>") {
			args = ""
		}
		value := command
		if args != "" {
			value += " " + args
		}
		metaParts := []string{}
		if dir := strings.TrimSpace(fmt.Sprint(params["dir"])); dir != "" && !strings.EqualFold(dir, "<nil>") {
			metaParts = append(metaParts, "dir="+dir)
		}
		if timeout := strings.TrimSpace(fmt.Sprint(params["timeout_ms"])); timeout != "" && !strings.EqualFold(timeout, "<nil>") {
			metaParts = append(metaParts, "timeout="+timeout)
		}
		items = append(items, commandPickerItem{
			Value:       value,
			Description: "guarded command preset",
			Meta:        strings.Join(metaParts, " | "),
		})
	}
	return items
}

func (m Model) grepPickerItems() []commandPickerItem {
	candidates := []string{}
	add := func(value, desc string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, item := range candidates {
			if strings.EqualFold(item, value) {
				return
			}
		}
		candidates = append(candidates, value)
	}
	if pattern := strings.TrimSpace(m.toolGrepPattern()); pattern != "" {
		add(pattern, "")
	}
	for _, item := range []string{"TODO", "FIXME", "panic\\(", "console\\.log", "fmt\\.Println"} {
		add(item, "")
	}
	items := make([]commandPickerItem, 0, len(candidates))
	for _, value := range candidates {
		desc := "search preset"
		if value == strings.TrimSpace(m.toolGrepPattern()) {
			desc = "derived from current context"
		}
		items = append(items, commandPickerItem{
			Value:       value,
			Description: desc,
			Meta:        "max_results=80",
		})
	}
	return items
}

func (m Model) applyCommandPickerProvider(selected string) (tea.Model, tea.Cmd) {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		m.notice = "Provider selection is empty."
		return m, nil
	}
	model := m.defaultModelForProvider(selected)
	m = m.applyProviderModelSelection(selected, model)
	persist := m.commandPickerPersist
	m = m.closeCommandPicker()
	if persist {
		path, err := m.persistProviderModelProjectConfig(selected, model)
		if err != nil {
			m.notice = "provider persist: " + err.Error()
			return m.appendSystemMessage(fmt.Sprintf("Provider set to %s (%s)\nPersist error: %v", selected, blankFallback(model, "-"), err)), loadStatusCmd(m.eng)
		}
		m.notice = fmt.Sprintf("Provider set to %s (%s), saved to %s", selected, blankFallback(model, "-"), filepath.ToSlash(path))
		return m.appendSystemMessage(fmt.Sprintf("Provider set to %s (%s)\nSaved project config: %s", selected, blankFallback(model, "-"), filepath.ToSlash(path))), loadStatusCmd(m.eng)
	}
	m.notice = fmt.Sprintf("Provider set to %s (%s)", selected, blankFallback(model, "-"))
	return m.appendSystemMessage(fmt.Sprintf("Provider set to %s (%s)", selected, blankFallback(model, "-"))), loadStatusCmd(m.eng)
}

func (m Model) applyCommandPickerModel(selected string) (tea.Model, tea.Cmd) {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		m.notice = "Model selection is empty."
		return m, nil
	}
	providerName := m.currentProvider()
	m = m.applyProviderModelSelection(providerName, selected)
	persist := m.commandPickerPersist
	m = m.closeCommandPicker()
	if persist {
		path, err := m.persistProviderModelProjectConfig(providerName, selected)
		if err != nil {
			m.notice = "model persist: " + err.Error()
			return m.appendSystemMessage(fmt.Sprintf("Model set to %s (%s)\nPersist error: %v", selected, blankFallback(providerName, "-"), err)), loadStatusCmd(m.eng)
		}
		m.notice = fmt.Sprintf("Model set to %s (%s), saved to %s", selected, blankFallback(providerName, "-"), filepath.ToSlash(path))
		return m.appendSystemMessage(fmt.Sprintf("Model set to %s (%s)\nSaved project config: %s", selected, blankFallback(providerName, "-"), filepath.ToSlash(path))), loadStatusCmd(m.eng)
	}
	m.notice = fmt.Sprintf("Model set to %s (%s)", selected, blankFallback(providerName, "-"))
	return m.appendSystemMessage(fmt.Sprintf("Model set to %s (%s)", selected, blankFallback(providerName, "-"))), loadStatusCmd(m.eng)
}

func (m Model) applyCommandPickerPreparedInput(selected string) (tea.Model, tea.Cmd) {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		m.notice = "Selection is empty."
		return m, nil
	}
	kind := strings.ToLower(strings.TrimSpace(m.commandPickerKind))
	switch kind {
	case "tool":
		m = m.closeCommandPicker()
		m.setChatInput("/tool " + selected + " ")
		m.notice = "Tool command prepared: " + selected
		return m, nil
	case "read":
		m = m.closeCommandPicker()
		m.setChatInput("/read " + formatSlashArgToken(selected) + " ")
		m.notice = "Read command prepared: " + selected
		return m, nil
	case "run":
		m = m.closeCommandPicker()
		m.setChatInput("/run " + selected)
		m.notice = "Run command prepared: " + selected
		return m, nil
	case "grep":
		m = m.closeCommandPicker()
		m.setChatInput("/grep " + formatSlashArgToken(selected))
		m.notice = "Grep command prepared: " + selected
		return m, nil
	default:
		m.notice = "Unknown picker mode."
		return m, nil
	}
}

func (m Model) availableModelsForProvider(providerName string) []string {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		providerName = strings.TrimSpace(m.currentProvider())
	}
	set := map[string]string{}
	add := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		key := strings.ToLower(model)
		if _, exists := set[key]; exists {
			return
		}
		set[key] = model
	}
	add(m.currentModel())
	add(m.defaultModelForProvider(providerName))
	if m.eng != nil && m.eng.Config != nil {
		if profile, ok := m.eng.Config.Providers.Profiles[providerName]; ok {
			add(profile.Model)
		}
	}
	for _, model := range modelsFromModelsDevCache(providerName) {
		add(model)
	}
	out := make([]string, 0, len(set))
	for _, model := range set {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

func modelsFromModelsDevCache(providerName string) []string {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return nil
	}
	catalog, err := config.LoadModelsDevCatalog(config.ModelsDevCachePath())
	if err != nil || len(catalog) == 0 {
		return nil
	}
	candidates := []config.ModelsDevProvider{}
	for key, item := range catalog {
		if strings.EqualFold(strings.TrimSpace(key), providerName) || strings.EqualFold(strings.TrimSpace(item.ID), providerName) {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	set := map[string]string{}
	for _, item := range candidates {
		for key, model := range item.Models {
			id := strings.TrimSpace(model.ID)
			if id == "" {
				id = strings.TrimSpace(key)
			}
			if id == "" {
				continue
			}
			lower := strings.ToLower(id)
			if _, ok := set[lower]; ok {
				continue
			}
			set[lower] = id
		}
	}
	out := make([]string, 0, len(set))
	for _, model := range set {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

func modelsDevModelKnown(providerName, modelName string) bool {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return false
	}
	for _, item := range modelsFromModelsDevCache(providerName) {
		if strings.EqualFold(strings.TrimSpace(item), modelName) {
			return true
		}
	}
	return false
}

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
		default:
			return m.appendSystemMessage(m.contextCommandSummary()), nil, true
		}
	case "tools":
		m.input = ""
		tools := m.availableTools()
		if len(tools) == 0 {
			return m.appendSystemMessage("No tools registered."), nil, true
		}
		return m.appendSystemMessage("Tools: " + strings.Join(tools, ", ") + "\nOpen the Tools panel with F6 for preset runs."), nil, true
	case "tool":
		if len(args) == 0 {
			m = m.startCommandPicker("tool", "", false)
			return m, nil, true
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
	case "doctor":
		m.input = ""
		return m.appendSystemMessage("Open /status or run `dfmc doctor` for full diagnostics. Current summary:\n" + m.statusCommandSummary()), loadStatusCmd(m.eng), true
	case "magicdoc", "magic":
		m.input = ""
		return m.appendSystemMessage(m.magicDocSlash(args)), nil, true
	case "conversation", "conv":
		m.input = ""
		return m.appendSystemMessage(m.conversationSlash(args)), nil, true
	case "memory":
		m.input = ""
		return m.appendSystemMessage(m.memorySlash(args)), nil, true
	case "init", "completion", "man", "serve", "remote", "plugin", "skill", "prompt", "config":
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
	m.notice = "Streaming answer..."
	m.streamMessages = startChatStream(m.ctx, m.eng, question)
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

func parseListDirChatArgs(args []string) (map[string]any, error) {
	params := map[string]any{
		"path":        ".",
		"max_entries": 120,
	}
	pathSet := false
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		switch {
		case arg == "-r" || arg == "--recursive":
			params["recursive"] = true
		case arg == "--max":
			if i+1 >= len(args) {
				return nil, fmt.Errorf("missing value for --max")
			}
			n, err := strconv.Atoi(strings.TrimSpace(args[i+1]))
			if err != nil {
				return nil, fmt.Errorf("invalid --max value")
			}
			params["max_entries"] = n
			i++
		case strings.HasPrefix(strings.ToLower(arg), "--max="):
			raw := strings.TrimSpace(strings.SplitN(arg, "=", 2)[1])
			n, err := strconv.Atoi(raw)
			if err != nil {
				return nil, fmt.Errorf("invalid --max value")
			}
			params["max_entries"] = n
		case strings.HasPrefix(arg, "-"):
			return nil, fmt.Errorf("unknown flag")
		default:
			if !pathSet {
				params["path"] = arg
				pathSet = true
			}
		}
	}
	return params, nil
}

func parseReadFileChatArgs(args []string) (map[string]any, error) {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return nil, fmt.Errorf("path required")
	}
	params := map[string]any{
		"path":       strings.TrimSpace(args[0]),
		"line_start": 1,
		"line_end":   200,
	}
	if len(args) >= 2 {
		start, err := strconv.Atoi(strings.TrimSpace(args[1]))
		if err != nil {
			return nil, fmt.Errorf("invalid line_start")
		}
		params["line_start"] = start
		if len(args) >= 3 {
			end, err := strconv.Atoi(strings.TrimSpace(args[2]))
			if err != nil {
				return nil, fmt.Errorf("invalid line_end")
			}
			params["line_end"] = end
		} else {
			params["line_end"] = start + 199
		}
	}
	return params, nil
}

func parseGrepChatArgs(args []string) (map[string]any, error) {
	pattern := strings.TrimSpace(strings.Join(args, " "))
	if pattern == "" {
		return nil, fmt.Errorf("pattern required")
	}
	return map[string]any{
		"pattern":     pattern,
		"max_results": 80,
	}, nil
}

func parseRunCommandChatArgs(args []string) (map[string]any, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("command required")
	}
	command := strings.TrimSpace(args[0])
	if command == "" {
		return nil, fmt.Errorf("command required")
	}
	params := map[string]any{
		"command": command,
		"dir":     ".",
	}
	if len(args) > 1 {
		rest := strings.TrimSpace(strings.Join(args[1:], " "))
		if rest != "" {
			params["args"] = rest
		}
	}
	return params, nil
}

func (m Model) autoToolIntentFromQuestion(question string) (string, map[string]any, string, bool) {
	question = strings.TrimSpace(question)
	if question == "" || strings.HasPrefix(question, "/") {
		return "", nil, "", false
	}
	lower := strings.ToLower(question)

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
			return "run_command", params, "detected command execution intent", true
		}
	}

	if pattern, ok := extractSearchIntentPattern(question, lower); ok {
		params := map[string]any{
			"pattern":     strings.TrimSpace(pattern),
			"max_results": 80,
		}
		return "grep_codebase", params, "detected search intent", true
	}

	if path, recursive, maxEntries, ok := extractListIntent(question, lower); ok {
		params := map[string]any{
			"path":        blankFallback(strings.TrimSpace(path), "."),
			"max_entries": maxEntries,
		}
		if recursive {
			params["recursive"] = true
		}
		return "list_dir", params, "detected listing intent", true
	}

	if hasReadIntentPrefix(lower) {
		target := m.detectReferencedFile(question)
		if target == "" {
			target = strings.TrimSpace(m.toolTargetFile())
		}
		if target != "" {
			start, end := extractReadLineRange(question)
			params := map[string]any{
				"path":       target,
				"line_start": start,
				"line_end":   end,
			}
			return "read_file", params, "detected file read intent", true
		}
	}

	return "", nil, "", false
}

func hasReadIntentPrefix(lower string) bool {
	for _, prefix := range []string{"read ", "oku ", "incele ", "goster ", "göster ", "ac ", "aç "} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func extractRunIntentCommand(question, lower string) (string, bool) {
	for _, prefix := range []string{"run ", "calistir ", "çalıştır ", "komut calistir ", "komut çalıştır "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(question[len(prefix):]), true
		}
	}
	if strings.HasPrefix(lower, "run:") {
		return strings.TrimSpace(question[len("run:"):]), true
	}
	backtick := extractBacktickBlock(question)
	if backtick != "" && (strings.HasPrefix(lower, "run ") || strings.HasPrefix(lower, "calistir ") || strings.HasPrefix(lower, "çalıştır ")) {
		return backtick, true
	}
	return "", false
}

func extractSearchIntentPattern(question, lower string) (string, bool) {
	for _, prefix := range []string{"grep ", "ara ", "search "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(question[len(prefix):]), strings.TrimSpace(question[len(prefix):]) != ""
		}
	}
	return "", false
}

func extractListIntent(question, lower string) (string, bool, int, bool) {
	maxEntries := 120
	if strings.HasPrefix(lower, "listele") {
		tail := strings.TrimSpace(question[len("listele"):])
		tailLower := strings.ToLower(tail)
		recursive := strings.Contains(tailLower, "recursive") || strings.Contains(tailLower, "rekursif")
		path := tail
		if recursive {
			reRecursive := regexp.MustCompile(`(?i)\b(recursive|rekursif)\b`)
			path = reRecursive.ReplaceAllString(path, "")
		}
		path = strings.TrimSpace(path)
		if path == "" {
			path = "."
		}
		return path, recursive, maxEntries, true
	}
	if strings.HasPrefix(lower, "list") {
		tail := strings.TrimSpace(question[len("list"):])
		path := strings.TrimSpace(strings.TrimPrefix(tail, "files"))
		path = strings.TrimSpace(strings.TrimPrefix(path, "dir"))
		return blankFallback(path, "."), false, maxEntries, true
	}
	return "", false, 0, false
}

func extractBacktickBlock(text string) string {
	start := strings.Index(text, "`")
	if start < 0 {
		return ""
	}
	rest := text[start+1:]
	end := strings.Index(rest, "`")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func splitExecutableAndArgs(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if strings.HasPrefix(raw, "\"") {
		end := strings.Index(raw[1:], "\"")
		if end >= 0 {
			command := strings.TrimSpace(raw[1 : end+1])
			args := strings.TrimSpace(raw[end+2:])
			return command, args
		}
	}
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return "", ""
	}
	command := parts[0]
	args := ""
	if len(parts) > 1 {
		args = strings.Join(parts[1:], " ")
	}
	return command, args
}

func (m Model) detectReferencedFile(question string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return ""
	}
	markerRe := regexp.MustCompile(`\[\[file:([^\]]+)\]\]`)
	if matches := markerRe.FindStringSubmatch(question); len(matches) == 2 {
		target := filepath.ToSlash(strings.TrimSpace(matches[1]))
		if target != "" && containsStringFold(m.files, target) {
			return target
		}
	}
	candidates := strings.FieldsFunc(question, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == ',' || r == ';' || r == '(' || r == ')' || r == '[' || r == ']'
	})
	for _, raw := range candidates {
		token := strings.TrimSpace(strings.Trim(raw, "\"'`"))
		token = strings.TrimPrefix(token, "@")
		token = strings.TrimSuffix(token, ".")
		token = strings.TrimSuffix(token, ":")
		token = filepath.ToSlash(token)
		if token == "" {
			continue
		}
		if containsStringFold(m.files, token) {
			return token
		}
		if strings.Contains(token, "/") || strings.Contains(token, ".") {
			if m.projectHasFile(token) {
				return token
			}
		}
	}
	return ""
}

func extractReadLineRange(question string) (int, int) {
	lower := strings.ToLower(strings.TrimSpace(question))
	if !strings.Contains(lower, "line") && !strings.Contains(lower, "satir") && !strings.Contains(lower, "satır") {
		return 1, 200
	}
	re := regexp.MustCompile(`\b(\d{1,6})\b`)
	matches := re.FindAllStringSubmatch(question, 3)
	if len(matches) == 0 {
		return 1, 200
	}
	start, err := strconv.Atoi(matches[0][1])
	if err != nil || start <= 0 {
		start = 1
	}
	end := start + 199
	if len(matches) >= 2 {
		if parsed, err := strconv.Atoi(matches[1][1]); err == nil && parsed >= start {
			end = parsed
		}
	}
	return start, end
}

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

func (m Model) patchCommandSummary() string {
	parts := []string{
		"Patch files: " + strings.Join(m.patchFilesOrNone(), ", "),
		"Patch target: " + m.patchTargetSummary(),
		"Hunk target: " + m.patchHunkSummary(),
	}
	if hints := m.patchReviewHints(); len(hints) > 0 {
		parts = append(parts, "Review cues: "+strings.Join(hints, " | "))
	}
	return strings.Join(parts, "\n")
}

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

	tabs := make([]string, 0, len(m.tabs))
	for i, tab := range m.tabs {
		label := tab
		if i == m.activeTab {
			tabs = append(tabs, tabActiveStyle.Render("● "+label))
		} else {
			tabs = append(tabs, tabInactiveStyle.Render("  "+label))
		}
	}

	banner := renderBanner("DFMC WORKBENCH", "agentic coding cockpit · token-miser")
	header := banner + "\n" + strings.Join(tabs, " ")
	footer := statusBarStyle.Width(width).Render(m.renderFooter(width))
	bodyHeight := height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyHeight < 6 {
		bodyHeight = 6
	}
	body := m.renderActiveView(bodyWidth, bodyHeight)

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) renderActiveView(width int, height int) string {
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
		content = fitPanelContentHeight(m.renderFilesView(contentWidth), innerHeight)
	case "Patch":
		content = fitPanelContentHeight(m.renderPatchView(contentWidth), innerHeight)
	case "Setup":
		content = fitPanelContentHeight(m.renderSetupView(contentWidth), innerHeight)
	case "Tools":
		content = fitPanelContentHeight(m.renderToolsView(contentWidth), innerHeight)
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
	return docStyle.Width(width).Height(height).Render(content)
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
	for i, item := range m.transcript {
		if i > 0 {
			lines = append(lines, "")
		}
		durationMs := item.DurationMs
		if m.streamIndex == i && m.sending && !m.streamStartedAt.IsZero() {
			durationMs = int(time.Since(m.streamStartedAt).Milliseconds())
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
	if suggestions.slashMenuActive {
		items := suggestions.slashCommands
		lines = append(lines, sectionTitleStyle.Render("Commands"))
		if len(items) == 0 {
			lines = append(lines, "  "+subtleStyle.Render("No matching command. Press esc to dismiss or /help for the catalog."))
		} else {
			lines = append(lines, subtleStyle.Render("↑↓ move · tab cycle · enter run"))
			selected := clampIndex(m.slashIndex, len(items))
			start := 0
			if selected > 4 {
				start = selected - 4
			}
			end := start + 6
			if end > len(items) {
				end = len(items)
			}
			for i := start; i < end; i++ {
				prefix := "  "
				label := truncateSingleLine(fmt.Sprintf("%s — %s", items[i].Template, items[i].Description), width)
				if i == selected {
					prefix = "> "
					label = titleStyle.Render(label)
				}
				lines = append(lines, prefix+label)
			}
		}
	}
	if !suggestions.slashMenuActive {
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
	if m.input != "" && strings.Contains(m.input, "@") {
		switch {
		case len(suggestions.mentionSuggestions) > 0:
			lines = append(lines, sectionTitleStyle.Render("File mentions"))
			hintTail := ""
			if suggestions.mentionRange != "" {
				hintTail = " · range " + suggestions.mentionRange
			}
			lines = append(lines, subtleStyle.Render("↑↓ move · tab/enter insert · suffix :10-50 or #L10-L50 for ranges"+hintTail))
			selected := clampIndex(m.mentionIndex, len(suggestions.mentionSuggestions))
			for i, row := range suggestions.mentionSuggestions {
				prefix := "  "
				label := truncateSingleLine(row.Path, width)
				if i == selected {
					prefix = "> "
					label = titleStyle.Render(label)
				}
				if row.Recent {
					label += " " + subtleStyle.Render("· recent")
				}
				lines = append(lines, prefix+label)
			}
		case suggestions.mentionQuery != "":
			lines = append(lines, sectionTitleStyle.Render("File mentions"))
			lines = append(lines, "  "+subtleStyle.Render("No files matched '"+suggestions.mentionQuery+"' — refine query or press esc."))
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

	listLines := []string{
		sectionHeader("▦", "Files"),
		subtleStyle.Render("enter reload · p pin · ctrl+h keys"),
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
		start := 0
		if m.fileIndex > 6 {
			start = m.fileIndex - 6
		}
		end := start + 14
		if end > len(m.files) {
			end = len(m.files)
		}
		if end-start < 14 && start > 0 {
			start = end - 14
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
	content := truncateForPanel(m.filePreview, previewWidth)
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

func (m Model) renderPatchView(width int) string {
	diffPreview := truncateForPanel(strings.TrimSpace(m.diff), width)
	if diffPreview == "" {
		diffPreview = subtleStyle.Render("Working tree is clean — nothing to review.")
	}
	patchPreview := truncateForPanel(m.patchPreviewText(), width)
	if patchPreview == "" {
		patchPreview = subtleStyle.Render("No assistant patch yet. Ask DFMC to refactor, fix, or rewrite a file in Chat — the generated diff lands here.")
	}
	changed := "(none)"
	if len(m.changed) > 0 {
		changed = strings.Join(m.changed, ", ")
	}
	parts := []string{
		sectionHeader("◈", "Patch Lab"),
		subtleStyle.Render("a apply · u undo · c check · ctrl+h keys"),
		renderDivider(min(width, 100)),
		"",
		"Changed:      " + truncateForPanel(changed, width),
		"Patch files:  " + truncateForPanel(strings.Join(m.patchFilesOrNone(), ", "), width),
		"Focus file:   " + truncateForPanel(m.patchTargetSummary(), width),
		"Focus hunk:   " + truncateForPanel(m.patchHunkSummary(), width),
		"",
		sectionHeader("⇄", "Worktree Diff"),
		diffPreview,
		"",
		sectionHeader("◇", "Current Hunk"),
		patchPreview,
	}
	if info := m.patchFocusSummary(); info != "" {
		parts = append(parts, "", subtleStyle.Render(info))
	}
	if hints := m.patchReviewHints(); len(hints) > 0 {
		parts = append(parts, "", subtleStyle.Render("Review cues: "+strings.Join(hints, " | ")))
	}
	if note := strings.TrimSpace(m.notice); note != "" {
		parts = append(parts, "", subtleStyle.Render(note))
	}
	return strings.Join(parts, "\n")
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
		detailLines = append(detailLines,
			fmt.Sprintf("Name:        %s", selected),
			fmt.Sprintf("Description: %s", truncateForPanel(m.toolDescription(selected), detailWidth)),
			fmt.Sprintf("Params:      %s", truncateForPanel(m.toolPresetSummary(selected), detailWidth)),
			"",
		)
		if selected == "run_command" {
			if suggestions := m.runCommandSuggestions(); len(suggestions) > 0 {
				detailLines = append(detailLines, subtleStyle.Render("Suggested Presets"))
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
		"  ctrl+p palette · alt+1..6 or f1..f6 tabs · ctrl+h help · ctrl+s stats · ctrl+q quit",
		"  esc cancels current stream · ctrl+c interrupts · ctrl+u clear input",
		"",
		boldStyle.Render("Chat composer"),
		"  ↑/↓ history · tab accept suggestion · @ mention file · / browse commands",
		"  @file:10-50 or @file#L10-L50 attaches a line range to the mention",
		"  /continue resumes a parked agent loop · /btw queues a note",
		"  /clear wipes transcript · /quit exits · /coach mutes notes · /hints toggles trajectory",
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
			"enter send · shift+enter newline · / commands · @ mention",
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
	default:
		return []string{"alt+1..6 tabs · ctrl+p palette · ctrl+q quit"}
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

func loadLatestPatchCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return latestPatchLoadedMsg{}
		}
		return latestPatchLoadedMsg{patch: latestAssistantUnifiedDiff(eng.ConversationActive())}
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

func applyPatchCmd(eng *engine.Engine, patch string, checkOnly bool) tea.Cmd {
	return func() tea.Msg {
		if eng == nil {
			return patchApplyMsg{err: fmt.Errorf("engine is nil"), checkOnly: checkOnly}
		}
		if strings.TrimSpace(patch) == "" {
			return patchApplyMsg{err: fmt.Errorf("no assistant patch loaded"), checkOnly: checkOnly}
		}
		root := strings.TrimSpace(eng.Status().ProjectRoot)
		if root == "" {
			root = "."
		}
		if err := applyUnifiedDiff(root, patch, checkOnly); err != nil {
			return patchApplyMsg{err: err, checkOnly: checkOnly}
		}
		if checkOnly {
			return patchApplyMsg{checkOnly: true}
		}
		changed, err := gitChangedFiles(root, 12)
		return patchApplyMsg{checkOnly: false, changed: changed, err: err}
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
	if m.sending && shouldMirrorEventToTranscript(eventType) {
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
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > 18 {
		lines = append(lines[:18], "... [truncated]")
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
	if sending {
		return "> " + input
	}
	runes := []rune(input)
	max := len(runes)
	if manual && manualInput != input {
		manual = false
	}
	if !manual {
		cursor = max
	}
	if cursor < 0 {
		cursor = 0
	}
	if cursor > max {
		cursor = max
	}
	before := string(runes[:cursor])
	after := string(runes[cursor:])
	return "> " + before + "|" + after
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

func (m Model) focusPatchFile() (tea.Model, tea.Cmd) {
	target := strings.TrimSpace(m.currentPatchPath())
	if target == "" {
		target = strings.TrimSpace(m.bestPatchFileTarget())
	}
	if target == "" {
		m.notice = "No patched file to focus."
		return m, nil
	}
	for i, path := range m.files {
		if strings.EqualFold(strings.TrimSpace(path), target) {
			m.fileIndex = i
			m.activeTab = 2
			m.notice = "Focused patched file " + target
			return m, loadFilePreviewCmd(m.eng, target)
		}
	}
	m.notice = "Patched file not present in file index: " + target
	return m, nil
}

func (m Model) shiftPatchTarget(delta int) (tea.Model, tea.Cmd) {
	if len(m.patchSet) == 0 {
		m.notice = "No patched file to navigate."
		return m, nil
	}
	m.patchIndex = (m.patchIndex + delta + len(m.patchSet)) % len(m.patchSet)
	m.patchHunk = 0
	m.notice = "Viewing patch for " + m.currentPatchPath()
	return m, nil
}

func (m Model) shiftPatchHunk(delta int) (tea.Model, tea.Cmd) {
	section := m.currentPatchSection()
	if section == nil || len(section.Hunks) == 0 {
		m.notice = "No patch hunk to navigate."
		return m, nil
	}
	m.patchHunk = (m.patchHunk + delta + len(section.Hunks)) % len(section.Hunks)
	m.notice = "Viewing hunk " + m.patchHunkSummary()
	return m, nil
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

func (m Model) patchFilesOrNone() []string {
	if len(m.patchFiles) == 0 {
		return []string{"(none)"}
	}
	return append([]string(nil), m.patchFiles...)
}

func (m Model) patchFocusSummary() string {
	parts := make([]string, 0, 2)
	if current := strings.TrimSpace(m.currentPatchPath()); current != "" {
		parts = append(parts, "Viewing "+current+".")
	}
	if pinned := strings.TrimSpace(m.pinnedFile); pinned != "" && containsStringFold(m.patchFiles, pinned) {
		parts = append(parts, "Pinned file is touched by latest patch.")
	}
	if selected := strings.TrimSpace(m.selectedFile()); selected != "" && containsStringFold(m.patchFiles, selected) {
		parts = append(parts, "Selected file is touched by latest patch.")
	}
	return strings.Join(parts, " ")
}

func (m Model) bestPatchFileTarget() string {
	if len(m.patchFiles) == 0 {
		return ""
	}
	if pinned := strings.TrimSpace(m.pinnedFile); pinned != "" && containsStringFold(m.patchFiles, pinned) {
		return pinned
	}
	if selected := strings.TrimSpace(m.selectedFile()); selected != "" && containsStringFold(m.patchFiles, selected) {
		return selected
	}
	return strings.TrimSpace(m.patchFiles[0])
}

func (m Model) bestPatchIndex() int {
	if len(m.patchSet) == 0 {
		return 0
	}
	candidates := []string{
		m.currentPatchPath(),
		strings.TrimSpace(m.pinnedFile),
		strings.TrimSpace(m.selectedFile()),
	}
	for _, target := range candidates {
		if target == "" {
			continue
		}
		for i, item := range m.patchSet {
			if strings.EqualFold(strings.TrimSpace(item.Path), target) {
				return i
			}
		}
	}
	return 0
}

func (m Model) currentPatchPath() string {
	section := m.currentPatchSection()
	if section == nil {
		return ""
	}
	return strings.TrimSpace(section.Path)
}

func (m Model) currentPatchSection() *patchSection {
	if m.patchIndex < 0 || m.patchIndex >= len(m.patchSet) {
		return nil
	}
	return &m.patchSet[m.patchIndex]
}

func (m Model) patchTargetSummary() string {
	section := m.currentPatchSection()
	if section == nil {
		return "(none)"
	}
	return fmt.Sprintf("%s (%d/%d, hunks=%d)", section.Path, m.patchIndex+1, len(m.patchSet), section.HunkCount)
}

func (m Model) patchHunkSummary() string {
	section := m.currentPatchSection()
	if section == nil || len(section.Hunks) == 0 {
		return "(none)"
	}
	index := m.patchHunk
	if index < 0 || index >= len(section.Hunks) {
		index = 0
	}
	header := strings.TrimSpace(section.Hunks[index].Header)
	if header == "" {
		header = "@@"
	}
	return fmt.Sprintf("%s (%d/%d)", header, index+1, len(section.Hunks))
}

func (m Model) patchPreviewText() string {
	section := m.currentPatchSection()
	if section == nil {
		return strings.TrimSpace(m.latestPatch)
	}
	if len(section.Hunks) == 0 {
		return strings.TrimSpace(section.Content)
	}
	index := m.patchHunk
	if index < 0 || index >= len(section.Hunks) {
		index = 0
	}
	return strings.TrimSpace(section.Hunks[index].Content)
}

func (m Model) patchReviewHints() []string {
	section := m.currentPatchSection()
	if section == nil {
		return nil
	}
	text := m.patchPreviewText()
	if strings.TrimSpace(text) == "" {
		return nil
	}
	hints := make([]string, 0, 4)
	additions, deletions := patchLineCounts(text)
	if additions > 0 || deletions > 0 {
		hints = append(hints, fmt.Sprintf("+%d/-%d lines", additions, deletions))
	}
	path := strings.ToLower(strings.TrimSpace(section.Path))
	if path != "" && !strings.Contains(path, "_test.") && !strings.Contains(path, "/test") {
		hints = append(hints, "consider test coverage")
	}
	if strings.Contains(text, "TODO") || strings.Contains(text, "FIXME") {
		hints = append(hints, "contains TODO/FIXME")
	}
	if strings.Contains(text, "panic(") || strings.Contains(text, "fmt.Println(") || strings.Contains(text, "console.log(") {
		hints = append(hints, "check debug or panic statements")
	}
	return hints
}

func (m Model) chatPatchSummary(item chatLine) string {
	if len(item.PatchFiles) == 0 && item.PatchHunks == 0 && item.ToolCalls == 0 {
		return ""
	}
	parts := make([]string, 0, 6)
	if len(item.PatchFiles) > 0 {
		parts = append(parts, fmt.Sprintf("patch: %s", strings.Join(item.PatchFiles, ", ")))
	}
	if item.PatchHunks > 0 {
		parts = append(parts, fmt.Sprintf("hunks=%d", item.PatchHunks))
	}
	if item.IsLatestPatch {
		parts = append(parts, "latest")
	}
	if current := strings.TrimSpace(m.currentPatchPath()); current != "" && containsStringFold(item.PatchFiles, current) {
		parts = append(parts, "current target")
	}
	if item.ToolCalls > 0 {
		toolSummary := fmt.Sprintf("tools=%d", item.ToolCalls)
		if len(item.ToolNames) > 0 {
			toolSummary = fmt.Sprintf("%s [%s]", toolSummary, strings.Join(item.ToolNames, ", "))
		}
		parts = append(parts, toolSummary)
	}
	if item.ToolFailures > 0 {
		parts = append(parts, fmt.Sprintf("failures=%d", item.ToolFailures))
	}
	return strings.Join(parts, " | ")
}

func (m *Model) annotateAssistantPatch(index int) {
	if index < 0 || index >= len(m.transcript) {
		return
	}
	if m.transcript[index].Role != "assistant" {
		return
	}
	sections := parseUnifiedDiffSections(m.transcript[index].Content)
	m.transcript[index].PatchFiles = patchSectionPaths(sections)
	m.transcript[index].PatchHunks = totalPatchHunks(sections)
}

func (m *Model) annotateAssistantToolUsage(index int) {
	if index < 0 || index >= len(m.transcript) {
		return
	}
	if m.transcript[index].Role != "assistant" || m.eng == nil || m.eng.Conversation == nil {
		return
	}
	msg, ok := m.matchAssistantConversationMessage(m.transcript[index].Content)
	if !ok {
		return
	}
	m.transcript[index].ToolCalls = len(msg.ToolCalls)
	m.transcript[index].ToolFailures = 0
	if len(msg.ToolCalls) == 0 && len(msg.Results) == 0 {
		return
	}
	names := make([]string, 0, len(msg.ToolCalls))
	seen := map[string]struct{}{}
	for _, call := range msg.ToolCalls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for _, result := range msg.Results {
		if !result.Success {
			m.transcript[index].ToolFailures++
		}
		if name := strings.TrimSpace(result.Name); name != "" {
			if _, ok := seen[name]; !ok {
				seen[name] = struct{}{}
				names = append(names, name)
			}
		}
	}
	m.transcript[index].ToolNames = names
}

func (m Model) matchAssistantConversationMessage(content string) (types.Message, bool) {
	if m.eng == nil || m.eng.Conversation == nil {
		return types.Message{}, false
	}
	active := m.eng.Conversation.Active()
	if active == nil {
		return types.Message{}, false
	}
	want := strings.TrimSpace(content)
	messages := active.Messages()
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != types.RoleAssistant {
			continue
		}
		if strings.TrimSpace(msg.Content) == want {
			return msg, true
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != types.RoleAssistant {
			continue
		}
		if len(msg.ToolCalls) > 0 || len(msg.Results) > 0 {
			return msg, true
		}
	}
	return types.Message{}, false
}

func (m *Model) markLatestPatchInTranscript(patch string) {
	for i := range m.transcript {
		m.transcript[i].IsLatestPatch = false
	}
	patch = strings.TrimSpace(strings.ReplaceAll(patch, "\r\n", "\n"))
	if patch == "" {
		return
	}
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].Role != "assistant" {
			continue
		}
		if strings.TrimSpace(extractUnifiedDiff(m.transcript[i].Content)) == patch {
			m.transcript[i].IsLatestPatch = true
			if len(m.transcript[i].PatchFiles) == 0 {
				m.annotateAssistantPatch(i)
			}
			return
		}
	}
}

func (m Model) slashMenuActive() bool {
	raw := strings.TrimLeft(m.input, " \t\r\n")
	if !strings.HasPrefix(raw, "/") {
		return false
	}
	body := strings.TrimPrefix(raw, "/")
	if body == "" {
		return true
	}
	return !strings.ContainsAny(body, " \t\r\n")
}

func (m Model) activeSlashArgSuggestions() []string {
	raw := strings.TrimLeft(m.input, " \t\r\n")
	if raw == "" || !strings.HasPrefix(raw, "/") || m.slashMenuActive() {
		return nil
	}
	cmd, args, _, err := parseChatCommandInput(raw)
	if err != nil || cmd == "" {
		return nil
	}
	trailingSpace := hasTrailingWhitespace(raw)
	switch cmd {
	case "provider":
		providers := m.availableProviders()
		if len(providers) == 0 {
			return nil
		}
		if len(args) == 0 {
			return providers
		}
		if len(args) == 1 && !trailingSpace {
			return filterSuggestionsByToken(providers, args[0])
		}
		providerName := strings.TrimSpace(args[0])
		if !containsStringFold(providers, providerName) {
			return filterSuggestionsByToken(providers, providerName)
		}
		models := m.availableModelsForProvider(providerName)
		if len(models) == 0 {
			return nil
		}
		if len(args) >= 2 && !trailingSpace {
			return filterSuggestionsByToken(models, args[len(args)-1])
		}
		return models
	case "model":
		models := m.availableModelsForProvider(m.currentProvider())
		if len(models) == 0 {
			return nil
		}
		if len(args) > 0 && !trailingSpace {
			return filterSuggestionsByToken(models, strings.Join(args, " "))
		}
		return models
	case "read":
		files := m.files
		if len(files) == 0 {
			return nil
		}
		if len(args) == 0 {
			return firstSuggestions(files, 12)
		}
		if len(args) == 1 && !trailingSpace {
			return filterSuggestionsByToken(files, args[0])
		}
		return nil
	case "tool":
		tools := m.availableTools()
		if len(tools) == 0 {
			return nil
		}
		if len(args) == 0 {
			return tools
		}
		if len(args) == 1 && !trailingSpace {
			return filterSuggestionsByToken(tools, args[0])
		}
		toolName := strings.TrimSpace(args[0])
		if !containsStringFold(tools, toolName) {
			return filterSuggestionsByToken(tools, toolName)
		}
		paramTokens := append([]string(nil), args[1:]...)
		if len(paramTokens) == 0 || trailingSpace {
			return m.toolParamKeySuggestions(toolName, paramTokens, "")
		}
		last := strings.TrimSpace(paramTokens[len(paramTokens)-1])
		if last == "" {
			return m.toolParamKeySuggestions(toolName, paramTokens, "")
		}
		if !strings.Contains(last, "=") {
			return m.toolParamKeySuggestions(toolName, paramTokens, last)
		}
		key, value, _ := strings.Cut(last, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" {
			return m.toolParamKeySuggestions(toolName, paramTokens, "")
		}
		if suggestions := m.toolValueTokenSuggestions(toolName, key, value); len(suggestions) > 0 {
			return suggestions
		}
		return nil
	default:
		return nil
	}
}

func (m Model) autocompleteSlashArg() (string, bool) {
	raw := strings.TrimLeft(m.input, " \t\r\n")
	if raw == "" || !strings.HasPrefix(raw, "/") || m.slashMenuActive() {
		return "", false
	}
	cmd, args, _, err := parseChatCommandInput(raw)
	if err != nil || cmd == "" {
		return "", false
	}
	suggestions := m.activeSlashArgSuggestions()
	if len(suggestions) == 0 {
		return "", false
	}
	selected := suggestions[clampIndex(m.slashArgIndex, len(suggestions))]
	trailingSpace := hasTrailingWhitespace(raw)
	switch cmd {
	case "provider":
		updated := append([]string(nil), args...)
		if len(updated) == 0 {
			updated = []string{selected}
		} else if len(updated) == 1 && !trailingSpace {
			updated[0] = selected
		} else if trailingSpace && len(updated) == 1 {
			updated = append(updated, selected)
		} else if len(updated) >= 2 {
			updated[len(updated)-1] = selected
		}
		return formatSlashCommandInput(cmd, updated), true
	case "model":
		updated := append([]string(nil), args...)
		if len(updated) == 0 {
			updated = []string{selected}
		} else {
			updated[len(updated)-1] = selected
		}
		return formatSlashCommandInput(cmd, updated), true
	case "read":
		updated := append([]string(nil), args...)
		if len(updated) == 0 {
			updated = []string{selected}
		} else {
			updated[0] = selected
		}
		return formatSlashCommandInput(cmd, updated), true
	case "tool":
		updated := append([]string(nil), args...)
		tools := m.availableTools()
		if len(updated) == 0 {
			updated = []string{selected}
			return formatSlashCommandInput(cmd, updated), true
		}
		if len(updated) == 1 && !trailingSpace {
			updated[0] = selected
			return formatSlashCommandInput(cmd, updated), true
		}
		if !containsStringFold(tools, strings.TrimSpace(updated[0])) {
			updated[0] = selected
			return formatSlashCommandInput(cmd, updated), true
		}
		if trailingSpace {
			updated = append(updated, selected)
		} else if len(updated) >= 2 {
			updated[len(updated)-1] = selected
		} else {
			updated = append(updated, selected)
		}
		return formatSlashCommandInput(cmd, updated), true
	default:
		return "", false
	}
}

func (m Model) slashAssistHints() []string {
	raw := strings.TrimSpace(m.input)
	if raw == "" || !strings.HasPrefix(raw, "/") || m.commandPickerActive {
		return nil
	}
	cmd, args, _, err := parseChatCommandInput(raw)
	if err != nil {
		return []string{"Command parse error: " + err.Error()}
	}
	if cmd == "" {
		return []string{
			"Type /help for all local commands.",
			"↑↓ + tab picks from Commands.",
		}
	}
	switch cmd {
	case "provider":
		lines := []string{
			"Usage: /provider NAME [MODEL] [--persist]",
			"Tip: /provider (without args) opens Provider Picker.",
		}
		if providers := m.availableProviders(); len(providers) > 0 {
			lines = append(lines, "Known providers: "+strings.Join(providers, ", "))
			if len(args) > 0 && !containsStringFold(providers, args[0]) {
				lines = append(lines, "Unknown provider token; Enter opens picker filtered by your input.")
			}
		}
		return lines
	case "model":
		providerName := blankFallback(m.currentProvider(), "-")
		lines := []string{
			"Usage: /model NAME [--persist]",
			"Tip: /model (without args) opens Model Picker.",
			"Active provider: " + providerName,
		}
		models := m.availableModelsForProvider(m.currentProvider())
		if len(models) > 0 {
			lines = append(lines, "Known models: "+strings.Join(models, ", "))
		}
		if len(args) > 0 && len(models) > 0 && !containsStringFold(models, strings.Join(args, " ")) {
			lines = append(lines, "Unknown model is allowed; Enter can apply typed value in model picker.")
		}
		return lines
	case "context":
		return []string{
			"Usage: /context [full|why]",
			"/context -> compact summary",
			"/context why -> retrieval reasons only",
			"/context full -> full report with per-file evidence",
		}
	case "read":
		target := blankFallback(m.toolTargetFile(), "path/to/file.go")
		return []string{
			"Usage: /read PATH [LINE_START] [LINE_END]",
			"Paths with spaces: /read \"" + target + "\" 1 120",
		}
	case "run":
		lines := []string{"Usage: /run COMMAND [ARGS...]"}
		for i, suggestion := range m.runCommandSuggestions() {
			if i >= 2 {
				break
			}
			lines = append(lines, "Example: /run "+suggestionToRunCommandInput(suggestion))
		}
		return lines
	case "tool":
		lines := []string{
			"Usage: /tool NAME key=value ...",
			"Example: /tool read_file path=\"README.md\" line_start=1 line_end=80",
		}
		if len(args) == 0 {
			if tools := m.availableTools(); len(tools) > 0 {
				lines = append(lines, "Known tools: "+strings.Join(tools, ", "))
			}
			return lines
		}
		toolName := strings.TrimSpace(args[0])
		if containsStringFold(m.availableTools(), toolName) {
			keys := m.toolParamKeySuggestions(toolName, nil, "")
			if len(keys) > 0 {
				lines = append(lines, "Param keys: "+strings.Join(keys, " "))
			}
		}
		return lines
	case "reload":
		return []string{
			"Usage: /reload",
			"Reloads .env/config into current session without restarting TUI.",
		}
	default:
		if m.slashMenuActive() {
			return nil
		}
		suggestions := m.slashSuggestionsForToken(cmd, 3)
		if len(suggestions) == 0 {
			return []string{"Unknown command. Try /help."}
		}
		lines := []string{"Unknown command. Did you mean:"}
		for _, item := range suggestions {
			lines = append(lines, item.Template+" - "+item.Description)
		}
		return lines
	}
}

func formatSlashCommandInput(cmd string, args []string) string {
	cmd = strings.TrimSpace(strings.TrimPrefix(cmd, "/"))
	if cmd == "" {
		return "/"
	}
	if len(args) == 0 {
		return "/" + cmd
	}
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		if token := formatSlashArgToken(arg); token == "" {
			continue
		} else {
			parts = append(parts, token)
		}
	}
	if len(parts) == 0 {
		return "/" + cmd
	}
	return "/" + cmd + " " + strings.Join(parts, " ")
}

func formatSlashArgToken(arg string) string {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return ""
	}
	if strings.Contains(arg, "=") {
		key, value, _ := strings.Cut(arg, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" {
			return formatSlashKVToken(key, value)
		}
	}
	if strings.ContainsAny(arg, " \t\r\n\"") {
		return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
	}
	return arg
}

func formatSlashKVToken(key, value string) string {
	key = strings.TrimSpace(strings.TrimSuffix(key, "="))
	if key == "" {
		return ""
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return key + "="
	}
	if strings.ContainsAny(value, " \t\r\n\"") {
		return key + `="` + strings.ReplaceAll(value, `"`, `\"`) + `"`
	}
	return key + "=" + value
}

func (m Model) autocompleteSlashCommand() (string, bool) {
	if !m.slashMenuActive() {
		return "", false
	}
	items := m.filteredSlashCommands()
	if len(items) == 0 {
		return "", false
	}
	idx := clampIndex(m.slashIndex, len(items))
	return items[idx].Template, true
}

func (m Model) expandSlashSelection(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "/") {
		return "", false
	}
	fields := strings.Fields(raw)
	if len(fields) > 1 {
		return "", false
	}
	token := strings.TrimPrefix(strings.ToLower(raw), "/")
	if isKnownChatCommandToken(token) {
		return "", false
	}
	items := m.filteredSlashCommands()
	if len(items) == 0 {
		return "", false
	}
	idx := clampIndex(m.slashIndex, len(items))
	return items[idx].Template, true
}

func (m Model) filteredSlashCommands() []slashCommandItem {
	query := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(m.input)), "/")
	catalog := m.slashCommandCatalog()
	if query == "" {
		return catalog
	}
	out := make([]slashCommandItem, 0, len(catalog))
	for _, item := range catalog {
		cmd := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(item.Command)), "/")
		if strings.HasPrefix(cmd, query) {
			out = append(out, item)
		}
	}
	return out
}

func (m Model) slashSuggestionsForToken(token string, limit int) []slashCommandItem {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return nil
	}
	catalog := m.slashCommandCatalog()
	prefix := make([]slashCommandItem, 0, len(catalog))
	contains := make([]slashCommandItem, 0, len(catalog))
	for _, item := range catalog {
		name := strings.ToLower(strings.TrimSpace(item.Command))
		switch {
		case strings.HasPrefix(name, token):
			prefix = append(prefix, item)
		case strings.Contains(name, token):
			contains = append(contains, item)
		}
	}
	out := append(prefix, contains...)
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func suggestionToRunCommandInput(suggestion string) string {
	params, err := parseToolParamString(suggestion)
	if err != nil {
		return "go test ./..."
	}
	command := strings.TrimSpace(fmt.Sprint(params["command"]))
	if command == "" {
		command = "go"
	}
	args := strings.TrimSpace(fmt.Sprint(params["args"]))
	if args == "" || strings.EqualFold(args, "<nil>") {
		return command
	}
	return command + " " + args
}

func hasTrailingWhitespace(text string) bool {
	if text == "" {
		return false
	}
	last := text[len(text)-1]
	return last == ' ' || last == '\t' || last == '\n' || last == '\r'
}

func (m Model) toolParamKeySuggestions(toolName string, existingTokens []string, prefix string) []string {
	keys := m.toolParamKeyCatalog(toolName)
	if len(keys) == 0 {
		return nil
	}
	used := map[string]struct{}{}
	for _, token := range existingTokens {
		key, _, ok := strings.Cut(strings.TrimSpace(token), "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		used[key] = struct{}{}
	}
	prefix = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(prefix, "=")))
	out := make([]string, 0, len(keys))
	for _, token := range keys {
		key := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(token, "=")))
		if key == "" {
			continue
		}
		if _, exists := used[key]; exists {
			continue
		}
		if prefix != "" && !strings.HasPrefix(key, prefix) && !strings.Contains(key, prefix) {
			continue
		}
		out = append(out, token)
	}
	return out
}

func (m Model) toolParamKeyCatalog(toolName string) []string {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "list_dir":
		return []string{"path=", "recursive=", "max_entries="}
	case "read_file":
		return []string{"path=", "line_start=", "line_end="}
	case "grep_codebase":
		return []string{"pattern=", "max_results="}
	case "run_command":
		return []string{"command=", "args=", "dir=", "timeout_ms="}
	case "write_file":
		return []string{"path=", "content=", "overwrite=", "create_dirs="}
	case "edit_file":
		return []string{"path=", "old_string=", "new_string=", "replace_all="}
	default:
		preset := strings.TrimSpace(m.toolPresetSummary(toolName))
		if preset == "" || strings.EqualFold(preset, "no preset available") {
			return nil
		}
		params, err := parseToolParamString(preset)
		if err != nil || len(params) == 0 {
			return nil
		}
		keys := make([]string, 0, len(params))
		for key := range params {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			keys = append(keys, key+"=")
		}
		sort.Strings(keys)
		return keys
	}
}

func (m Model) toolValueTokenSuggestions(toolName, key, valuePrefix string) []string {
	key = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(key, "=")))
	valuePrefix = strings.TrimSpace(valuePrefix)
	switch key {
	case "path":
		candidates := m.files
		if strings.EqualFold(strings.TrimSpace(toolName), "list_dir") {
			candidates = m.projectDirSuggestions()
		}
		candidates = filterSuggestionsByToken(candidates, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 12)
	case "dir":
		candidates := filterSuggestionsByToken(m.projectDirSuggestions(), valuePrefix)
		return mapSuggestionsToKV(key, candidates, 10)
	case "command":
		candidates := filterSuggestionsByToken(m.runCommandNameSuggestions(), valuePrefix)
		return mapSuggestionsToKV(key, candidates, 8)
	case "args":
		candidates := filterSuggestionsByToken(m.runCommandArgSuggestions(), valuePrefix)
		return mapSuggestionsToKV(key, candidates, 8)
	case "pattern":
		candidates := []string{}
		if pattern := strings.TrimSpace(m.toolGrepPattern()); pattern != "" {
			candidates = append(candidates, pattern)
		}
		candidates = append(candidates, "TODO", "FIXME")
		candidates = filterSuggestionsByToken(candidates, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 6)
	case "recursive", "overwrite", "create_dirs", "replace_all":
		candidates := filterSuggestionsByToken([]string{"true", "false"}, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 2)
	case "line_start", "line_end":
		candidates := filterSuggestionsByToken([]string{"1", "80", "120", "200"}, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 4)
	case "max_entries":
		candidates := filterSuggestionsByToken([]string{"40", "80", "120", "200"}, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 4)
	case "max_results":
		candidates := filterSuggestionsByToken([]string{"20", "40", "80", "120"}, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 4)
	case "timeout_ms":
		candidates := filterSuggestionsByToken([]string{"5000", "10000", "30000", "60000"}, valuePrefix)
		return mapSuggestionsToKV(key, candidates, 4)
	default:
		return nil
	}
}

func mapSuggestionsToKV(key string, values []string, limit int) []string {
	if len(values) == 0 {
		return nil
	}
	if limit > 0 && len(values) > limit {
		values = values[:limit]
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, key+"="+value)
	}
	return out
}

func (m Model) runCommandNameSuggestions() []string {
	raw := m.runCommandSuggestions()
	if len(raw) == 0 {
		return nil
	}
	set := map[string]string{}
	for _, suggestion := range raw {
		params, err := parseToolParamString(suggestion)
		if err != nil {
			continue
		}
		command := strings.TrimSpace(fmt.Sprint(params["command"]))
		if command == "" || strings.EqualFold(command, "<nil>") {
			continue
		}
		lower := strings.ToLower(command)
		if _, exists := set[lower]; exists {
			continue
		}
		set[lower] = command
	}
	out := make([]string, 0, len(set))
	for _, value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (m Model) runCommandArgSuggestions() []string {
	raw := m.runCommandSuggestions()
	if len(raw) == 0 {
		return nil
	}
	set := map[string]string{}
	for _, suggestion := range raw {
		params, err := parseToolParamString(suggestion)
		if err != nil {
			continue
		}
		args := strings.TrimSpace(fmt.Sprint(params["args"]))
		if args == "" || strings.EqualFold(args, "<nil>") {
			continue
		}
		lower := strings.ToLower(args)
		if _, exists := set[lower]; exists {
			continue
		}
		set[lower] = args
	}
	out := make([]string, 0, len(set))
	for _, value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func (m Model) projectDirSuggestions() []string {
	set := map[string]string{
		".": ".",
	}
	if dir := strings.TrimSpace(m.toolTargetDir()); dir != "" {
		set[strings.ToLower(dir)] = dir
	}
	for _, file := range m.files {
		file = filepath.ToSlash(strings.TrimSpace(file))
		if file == "" {
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(file))
		if dir == "" {
			dir = "."
		}
		lower := strings.ToLower(dir)
		if _, exists := set[lower]; exists {
			continue
		}
		set[lower] = dir
	}
	out := make([]string, 0, len(set))
	for _, value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func filterSuggestionsByToken(items []string, token string) []string {
	items = append([]string(nil), items...)
	if len(items) == 0 {
		return nil
	}
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return items
	}
	prefix := make([]string, 0, len(items))
	contains := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item)
		if name == "" {
			continue
		}
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, token) {
			prefix = append(prefix, name)
			continue
		}
		if strings.Contains(lower, token) {
			contains = append(contains, name)
		}
	}
	return append(prefix, contains...)
}

func firstSuggestions(items []string, limit int) []string {
	if len(items) == 0 || limit <= 0 {
		return nil
	}
	if len(items) <= limit {
		return append([]string(nil), items...)
	}
	return append([]string(nil), items[:limit]...)
}

// slashCommandCatalog assembles the list of slash commands shown in the TUI
// command menu. The canonical catalog comes from commands.DefaultRegistry()
// filtered to the TUI surface; per-command template overrides live here so
// the menu auto-fills context-aware defaults (e.g. current model, pinned
// file). TUI-only utilities that don't exist as CLI verbs — /ls, /grep, /run,
// /read, /diff, /patch, /apply, /undo, /reload, /providers, /models, /tools —
// are appended explicitly so the picker stays useful.
func (m Model) slashCommandCatalog() []slashCommandItem {
	reg := commands.DefaultRegistry()
	overrides := m.slashTemplateOverrides()
	seen := map[string]struct{}{}
	out := make([]slashCommandItem, 0, 40)

	add := func(name, template, desc string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			return
		}
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		if template == "" {
			template = "/" + key
		}
		out = append(out, slashCommandItem{Command: key, Template: template, Description: desc})
	}

	// TUI-only slash shortcuts come first so that when a prefix matches both a
	// TUI extra and a registry command (e.g. `/prov` → `/providers` vs.
	// `/provider`), the TUI-friendly plural form wins — that matches the
	// established pre-registry behavior users built muscle memory around.
	coachLabel := "mute"
	if m.coachMuted {
		coachLabel = "unmute"
	}
	hintsLabel := "show"
	if m.hintsVerbose {
		hintsLabel = "hide"
	}
	extras := []slashCommandItem{
		{Command: "reload", Template: "/reload", Description: "reload config + env"},
		{Command: "clear", Template: "/clear", Description: "clear transcript (memory untouched)"},
		{Command: "quit", Template: "/quit", Description: "exit DFMC"},
		{Command: "providers", Template: "/providers", Description: "list configured providers"},
		{Command: "models", Template: "/models", Description: "show configured model"},
		{Command: "tools", Template: "/tools", Description: "list tools and open panel"},
		{Command: "ls", Template: "/ls .", Description: "list project files"},
		{Command: "read", Template: "/read " + blankFallback(m.toolTargetFile(), "path/to/file.go"), Description: "read file lines"},
		{Command: "grep", Template: "/grep TODO", Description: "search codebase (regex)"},
		{Command: "run", Template: "/run go test ./...", Description: "run a guarded command"},
		{Command: "diff", Template: "/diff", Description: "show worktree diff"},
		{Command: "patch", Template: "/patch", Description: "show latest patch summary"},
		{Command: "apply", Template: "/apply --check", Description: "dry-run apply latest patch"},
		{Command: "undo", Template: "/undo", Description: "undo last exchange"},
		{Command: "continue", Template: "/continue", Description: "resume a parked agent loop"},
		{Command: "split", Template: "/split ", Description: "decompose a broad task into focused subtasks"},
		{Command: "btw", Template: "/btw ", Description: "inject a note at the next tool-loop step"},
		{Command: "coach", Template: "/coach", Description: coachLabel + " the background coach notes"},
		{Command: "hints", Template: "/hints", Description: hintsLabel + " between-round trajectory hints"},
	}
	for _, x := range extras {
		add(x.Command, x.Template, x.Description)
	}

	for _, cmd := range reg.ForSurface(commands.SurfaceTUI) {
		template := overrides[cmd.Name]
		add(cmd.Name, template, cmd.Summary)
		for _, sub := range cmd.Subcommands {
			key := cmd.Name + " " + sub.Name
			add(key, "/"+key, sub.Summary)
		}
	}
	return out
}

func (m Model) slashTemplateOverrides() map[string]string {
	return map[string]string{
		"tool":         "/tool read_file path=" + blankFallback(m.toolTargetFile(), "README.md"),
		"provider":     "/provider " + blankFallback(m.currentProvider(), "openai"),
		"model":        "/model " + blankFallback(m.currentModel(), "model-name"),
		"review":       "/review " + blankFallback(m.toolTargetFile(), "path/to/file.go"),
		"explain":      "/explain " + blankFallback(m.toolTargetFile(), "path/to/file.go"),
		"refactor":     "/refactor " + blankFallback(m.toolTargetFile(), "path/to/file.go"),
		"test":         "/test " + blankFallback(m.toolTargetFile(), "path/to/file.go"),
		"doc":          "/doc " + blankFallback(m.toolTargetFile(), "path/to/file.go"),
		"ask":          "/ask your question...",
		"conversation": "/conversation list",
		"memory":       "/memory list",
		"magicdoc":     "/magicdoc update",
		"context":      "/context",
	}
}

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

func indexOfString(items []string, target string) int {
	target = strings.TrimSpace(target)
	for i, item := range items {
		if strings.TrimSpace(item) == target {
			return i
		}
	}
	return -1
}

func patchSectionPaths(items []patchSection) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if path := strings.TrimSpace(item.Path); path != "" {
			out = append(out, path)
		}
	}
	return out
}

func totalPatchHunks(items []patchSection) int {
	total := 0
	for _, item := range items {
		total += item.HunkCount
	}
	return total
}

func patchLineCounts(text string) (int, int) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	additions := 0
	deletions := 0
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "@@"):
			continue
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}

func extractPatchedFiles(patch string) []string {
	text := strings.ReplaceAll(strings.TrimSpace(patch), "\r\n", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, 8)
	seen := map[string]struct{}{}
	add := func(path string) {
		path = filepath.ToSlash(strings.TrimSpace(path))
		path = strings.TrimPrefix(path, "a/")
		path = strings.TrimPrefix(path, "b/")
		if path == "" || path == "/dev/null" || path == "dev/null" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				add(parts[3])
			}
		case strings.HasPrefix(line, "+++ "):
			add(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
		}
	}
	return out
}

func parseUnifiedDiffSections(patch string) []patchSection {
	text := strings.ReplaceAll(strings.TrimSpace(patch), "\r\n", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	sections := make([]patchSection, 0, 8)
	current := patchSection{}
	currentLines := make([]string, 0, 32)

	flush := func() {
		if len(currentLines) == 0 {
			return
		}
		current.Content = strings.Join(currentLines, "\n")
		current.Hunks = extractPatchHunks(current.Content)
		if len(current.Hunks) > 0 {
			current.HunkCount = len(current.Hunks)
		}
		if strings.TrimSpace(current.Path) == "" {
			paths := extractPatchedFiles(current.Content)
			if len(paths) > 0 {
				current.Path = paths[0]
			}
		}
		if strings.TrimSpace(current.Path) != "" {
			sections = append(sections, current)
		}
		current = patchSection{}
		currentLines = currentLines[:0]
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") && len(currentLines) > 0 {
			flush()
		}
		currentLines = append(currentLines, line)
		switch {
		case strings.HasPrefix(line, "diff --git "):
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				current.Path = normalizePatchPath(parts[3])
			}
		case strings.HasPrefix(line, "+++ "):
			path := normalizePatchPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
			if path != "" {
				current.Path = path
			}
		case strings.HasPrefix(line, "@@"):
			current.HunkCount++
		}
	}
	flush()
	return sections
}

func normalizePatchPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	if path == "" || path == "/dev/null" || path == "dev/null" {
		return ""
	}
	return path
}

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

func extractPatchHunks(diff string) []patchHunk {
	text := strings.ReplaceAll(strings.TrimSpace(diff), "\r\n", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	prefix := make([]string, 0, 8)
	hunks := make([]patchHunk, 0, 8)
	current := patchHunk{}
	currentLines := make([]string, 0, 16)
	inHunk := false

	flush := func() {
		if !inHunk || len(currentLines) == 0 {
			return
		}
		current.Content = strings.Join(currentLines, "\n")
		hunks = append(hunks, current)
		current = patchHunk{}
		currentLines = currentLines[:0]
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			flush()
			inHunk = true
			current = patchHunk{Header: strings.TrimSpace(line)}
			currentLines = append(currentLines[:0], prefix...)
			currentLines = append(currentLines, line)
			continue
		}
		if !inHunk {
			prefix = append(prefix, line)
			continue
		}
		currentLines = append(currentLines, line)
	}
	flush()
	return hunks
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

func gitWorkingDiff(projectRoot string, maxBytes int64) (string, error) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		root = "."
	}
	cmd := exec.Command("git", "-C", root, "diff", "--")
	out, err := cmd.Output()
	if err != nil {
		if ee := (&exec.ExitError{}); errors.As(err, &ee) {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	if maxBytes > 0 && int64(len(out)) > maxBytes {
		out = out[:maxBytes]
		return string(out) + "\n... [truncated]\n", nil
	}
	return string(out), nil
}

func latestAssistantUnifiedDiff(active *conversation.Conversation) string {
	if active == nil {
		return ""
	}
	msgs := active.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != types.RoleAssistant {
			continue
		}
		if patch := extractUnifiedDiff(msgs[i].Content); strings.TrimSpace(patch) != "" {
			return patch
		}
	}
	return ""
}

func extractUnifiedDiff(in string) string {
	text := strings.TrimSpace(strings.ReplaceAll(in, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	for _, marker := range []string{"```diff", "```patch", "```"} {
		idx := 0
		for {
			start := strings.Index(text[idx:], marker)
			if start < 0 {
				break
			}
			start += idx
			blockStart := strings.Index(text[start:], "\n")
			if blockStart < 0 {
				break
			}
			blockStart += start + 1
			end := strings.Index(text[blockStart:], "\n```")
			if end < 0 {
				break
			}
			end += blockStart
			block := strings.TrimSpace(text[blockStart:end])
			if looksLikeUnifiedDiff(block) {
				return block
			}
			idx = end + 4
		}
	}
	if looksLikeUnifiedDiff(text) {
		return text
	}
	return ""
}

func looksLikeUnifiedDiff(diff string) bool {
	d := "\n" + strings.TrimSpace(diff) + "\n"
	if strings.Contains(d, "\ndiff --git ") {
		return true
	}
	return strings.Contains(d, "\n--- ") && strings.Contains(d, "\n+++ ") && strings.Contains(d, "\n@@ ")
}

func applyUnifiedDiff(projectRoot, patch string, checkOnly bool) error {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		root = "."
	}
	patch = strings.ReplaceAll(patch, "\r\n", "\n")
	if patch != "" && !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	args := []string{"-C", root, "apply", "--whitespace=nowarn", "--recount"}
	if checkOnly {
		args = append(args, "--check")
	}
	cmd := exec.Command("git", args...)
	cmd.Stdin = strings.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
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
		"    /quit                        Exit DFMC",
		"    /coach                       Mute or unmute background coach notes",
		"    /hints                       Show or hide between-round trajectory hints",
		"    /tools                       Show tool surface",
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
