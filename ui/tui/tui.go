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
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"gopkg.in/yaml.v3"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/engine"
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
	mentionSuggestions  []string
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

	slashIndex    int
	slashArgIndex int
	mentionIndex  int

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

	agentLoopActive       bool
	agentLoopStep         int
	agentLoopMaxToolStep  int
	agentLoopToolRounds   int
	agentLoopPhase        string
	agentLoopProvider     string
	agentLoopModel        string
	agentLoopLastTool     string
	agentLoopLastStatus   string
	agentLoopLastOutput   string
	agentLoopContextScope string
	agentLoopEnterHint    string
	agentLoopExitHint     string
	agentLoopContractPre  string
	agentLoopContractPost string
	toolTimeline          []string

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

var (
	colorPanelBorder = lipgloss.Color("#2F4F6A")
	colorPanelBg     = lipgloss.Color("#0B1220")
	colorTitleBg     = lipgloss.Color("#11B981")
	colorTitleFg     = lipgloss.Color("#041014")
	colorMuted       = lipgloss.Color("#93A4BF")
	colorTabActiveBg = lipgloss.Color("#1E3A8A")
	colorTabActiveFg = lipgloss.Color("#E2EEFF")
	colorTabIdleFg   = lipgloss.Color("#7D92B2")
	colorStatusBg    = lipgloss.Color("#111A2A")
	colorStatusFg    = lipgloss.Color("#D9E6FF")

	docStyle = lipgloss.NewStyle().
			Padding(1, 2).
			Background(colorPanelBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorPanelBorder)

	titleStyle = lipgloss.NewStyle().
			Foreground(colorTitleFg).
			Background(colorTitleBg).
			Padding(0, 1).
			Bold(true)

	subtleStyle = lipgloss.NewStyle().
			Foreground(colorMuted)

	sectionTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#67E8F9")).
				Bold(true)

	tabActiveStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Background(colorTabActiveBg).
			Foreground(colorTabActiveFg).
			Bold(true)

	tabInactiveStyle = lipgloss.NewStyle().
				Padding(0, 2).
				Foreground(colorTabIdleFg)

	statusBarStyle = lipgloss.NewStyle().
			Padding(0, 1).
			Foreground(colorStatusFg).
			Background(colorStatusBg)

	userLineStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#8BC7FF"))
	assistantLineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#8AF0CF"))
	systemLineStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#F6D38A"))
	inputLineStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#E5F2FF"))
)

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
		toolOverrides:     map[string]string{},
		notice:            "Ctrl+P command palette. F1-F6 or Tab to switch panels. Use Alt+key variants for safer shortcuts (Alt+J/K, Alt+P, Alt+E, Alt+R, etc.).",
	}
}

func Run(ctx context.Context, eng *engine.Engine, opts Options) error {
	model := NewModel(ctx, eng)
	programOpts := []tea.ProgramOption{}
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
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
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

	case chatDoneMsg:
		m.annotateAssistantPatch(m.streamIndex)
		m.annotateAssistantToolUsage(m.streamIndex)
		m.sending = false
		m.streamMessages = nil
		m.streamIndex = -1
		m.resetAgentRuntime()
		m.input = ""
		m.notice = "Chat response completed."
		return m, tea.Batch(loadStatusCmd(m.eng), loadLatestPatchCmd(m.eng))

	case chatErrMsg:
		m.sending = false
		m.streamMessages = nil
		m.streamIndex = -1
		m.resetAgentRuntime()
		m.notice = "chat: " + msg.err.Error()
		return m, nil

	case streamClosedMsg:
		m.sending = false
		m.streamMessages = nil
		m.streamIndex = -1
		m.resetAgentRuntime()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "ctrl+q":
			return m, tea.Quit
		case "ctrl+p":
			m.activeTab = 0
			m.setChatInput("/")
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
			m.notice = "Command palette: choose with up/down and tab."
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
		if !m.sending {
			m.exitInputHistoryNavigation()
			m.insertInputText(string(msg.Runes))
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
		}
		return m, nil
	case tea.KeySpace:
		if !m.sending {
			m.exitInputHistoryNavigation()
			m.insertInputText(" ")
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
		}
		return m, nil
	case tea.KeyBackspace, tea.KeyCtrlH:
		if !m.sending {
			m.exitInputHistoryNavigation()
			m.deleteInputBeforeCursor()
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
		}
		return m, nil
	case tea.KeyDelete:
		if !m.sending {
			m.exitInputHistoryNavigation()
			m.deleteInputAtCursor()
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
		}
		return m, nil
	case tea.KeyLeft:
		if !m.sending {
			m.moveChatCursor(-1)
		}
		return m, nil
	case tea.KeyRight:
		if !m.sending {
			m.moveChatCursor(1)
		}
		return m, nil
	case tea.KeyHome, tea.KeyCtrlA:
		if !m.sending {
			m.moveChatCursorHome()
		}
		return m, nil
	case tea.KeyEnd, tea.KeyCtrlE:
		if !m.sending {
			m.moveChatCursorEnd()
		}
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
				m.notice = "Mention: " + suggestions.mentionSuggestions[m.mentionIndex]
			}
			return m, nil
		}
		if !m.sending && m.recallInputHistoryPrev() {
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
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
				m.notice = "Mention: " + suggestions.mentionSuggestions[m.mentionIndex]
			}
			return m, nil
		}
		if !m.sending && m.recallInputHistoryNext() {
			m.slashIndex = 0
			m.slashArgIndex = 0
			m.mentionIndex = 0
			m.notice = "History: next input"
			return m, nil
		}
		return m, nil
	case tea.KeyTab:
		if !m.sending {
			suggestions := m.buildChatSuggestionState()
			if next, ok := autocompleteMentionSelectionFromSuggestions(m.input, m.mentionIndex, suggestions.mentionSuggestions); ok {
				m.setChatInput(next)
				m.notice = "File mention inserted."
				m.mentionIndex = 0
				return m, nil
			}
			if next, ok := m.autocompleteSlashArg(); ok {
				m.setChatInput(next)
				m.slashArgIndex = 0
				m.notice = "Command arg completed."
				return m, nil
			}
			if next, ok := m.autocompleteSlashCommand(); ok {
				m.setChatInput(next)
				m.notice = "Command completed."
				return m, nil
			}
		}
		return m, nil
	case tea.KeyEnter:
		suggestions := m.buildChatSuggestionState()
		if !m.sending && len(suggestions.mentionSuggestions) > 0 {
			if next, ok := autocompleteMentionSelectionFromSuggestions(m.input, m.mentionIndex, suggestions.mentionSuggestions); ok {
				m.setChatInput(next)
				m.notice = "File mention inserted."
				m.mentionIndex = 0
				return m, nil
			}
		}
		raw := strings.TrimSpace(m.input)
		if m.sending || raw == "" {
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
		m.resetAgentRuntime()
		m.toolTimeline = nil
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
		m.notice = "Streaming answer..."
		m.streamMessages = startChatStream(m.ctx, m.eng, question)
		return m, waitForStreamMsg(m.streamMessages)
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
	if query, ok := activeMentionQuery(m.input); ok {
		state.mentionQuery = query
		state.mentionSuggestions = m.mentionSuggestions(query, 5)
	}
	return state
}

func autocompleteMentionSelectionFromSuggestions(input string, mentionIndex int, suggestions []string) (string, bool) {
	if len(suggestions) == 0 {
		return "", false
	}
	idx := clampIndex(mentionIndex, len(suggestions))
	return replaceActiveMention(input, suggestions[idx]), true
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
		m.notice = "Selection completed."
		return m, nil
	case tea.KeyEnter:
		items := m.filteredCommandPickerItems()
		if len(items) == 0 {
			kind := strings.ToLower(strings.TrimSpace(m.commandPickerKind))
			if strings.EqualFold(kind, "model") && strings.TrimSpace(m.commandPickerQuery) != "" {
				return m.applyCommandPickerModel(strings.TrimSpace(m.commandPickerQuery))
			}
			if (strings.EqualFold(kind, "tool") || strings.EqualFold(kind, "read")) && strings.TrimSpace(m.commandPickerQuery) != "" {
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
		case "tool", "read":
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
		return m.appendSystemMessage(strings.Join([]string{
			"Commands:",
			"/help, /status, /context, /reload, /tools, /diff, /patch, /undo, /apply [--check]",
			"/context full|why (detailed context report or reason-only)",
			"/providers, /provider NAME [MODEL] [--persist], /models, /model NAME [--persist]",
			"/ls [PATH] [-r|--recursive] [--max N], /read PATH [START] [END], /grep PATTERN, /run COMMAND [ARGS...]",
			"/tool NAME key=value ... (quoted values allowed)",
			"Panels: F1 Chat, F2 Status, F3 Files, F4 Patch, F5 Setup, F6 Tools | Ctrl+P opens command palette",
		}, "\n")), nil, true
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
		params, err := parseGrepChatArgs(args)
		if err != nil {
			return m.appendSystemMessage("Usage: /grep PATTERN"), nil, true
		}
		return m.startChatToolCommand("grep_codebase", params), runToolCmd(m.eng, "grep_codebase", params), true
	case "run":
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
		m.notice = "Listed configured providers."
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
		m.notice = "Listed configured model."
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
	default:
		m.notice = "Unknown chat command: " + raw
		return m.appendSystemMessage("Unknown chat command: " + raw), nil, true
	}
}

func (m Model) appendSystemMessage(text string) Model {
	m.transcript = append(m.transcript, newChatLine("system", strings.TrimSpace(text)))
	return m
}

func newChatLine(role, content string) chatLine {
	return chatLine{
		Role:    strings.TrimSpace(role),
		Content: content,
		Preview: chatDigest(content),
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
	}
	return m
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
		if i == m.activeTab {
			tabs = append(tabs, tabActiveStyle.Render(tab))
		} else {
			tabs = append(tabs, tabInactiveStyle.Render(tab))
		}
	}

	header := titleStyle.Render("DFMC Workbench") + "\n" + subtleStyle.Render("Agentic coding cockpit") + "\n" + strings.Join(tabs, " ")
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
	var content string
	switch m.tabs[m.activeTab] {
	case "Status":
		content = m.renderStatusView(contentWidth)
	case "Files":
		content = m.renderFilesView(contentWidth)
	case "Patch":
		content = m.renderPatchView(contentWidth)
	case "Setup":
		content = m.renderSetupView(contentWidth)
	case "Tools":
		content = m.renderToolsView(contentWidth)
	default:
		content = m.renderChatView(contentWidth)
	}
	innerHeight := height - 4
	if innerHeight < 1 {
		innerHeight = 1
	}
	content = fitPanelContentHeight(content, innerHeight)
	return docStyle.Width(width).Height(height).Render(content)
}

func (m Model) renderChatView(width int) string {
	suggestions := m.buildChatSuggestionState()
	lines := []string{
		titleStyle.Render("Chat"),
		subtleStyle.Render("Enter send | Natural auto-actions: read/list/grep/run | / command menu | @ file mention | Ctrl+P commands | Alt+1..6 tabs | Ctrl+C quit"),
		"",
	}
	if pinned := strings.TrimSpace(m.pinnedFile); pinned != "" {
		lines = append(lines, subtleStyle.Render("Pinned context: "+fileMarker(pinned)), "")
	}
	start := 0
	if len(m.transcript) > 8 {
		start = len(m.transcript) - 8
	}
	for _, item := range m.transcript[start:] {
		role := "[" + strings.ToUpper(item.Role) + "] "
		content := chatPreviewForLine(item, width)
		switch item.Role {
		case "user":
			lines = append(lines, userLineStyle.Render(role+content))
		case "assistant":
			lines = append(lines, assistantLineStyle.Render(role+content))
			if summary := m.chatPatchSummary(item); summary != "" {
				lines = append(lines, subtleStyle.Render("    "+summary))
			}
		default:
			lines = append(lines, systemLineStyle.Render(role+content))
		}
	}
	if len(m.transcript) == 0 {
		lines = append(lines, subtleStyle.Render("No messages yet. Ask for a review, explanation, or refactor plan."))
	}
	if m.agentLoopActive {
		lines = append(lines, "", sectionTitleStyle.Render("Agent Runtime"))
		phase := blankFallback(strings.TrimSpace(m.agentLoopPhase), "running")
		runtimeLine := "  phase=" + phase
		if m.agentLoopStep > 0 {
			if m.agentLoopMaxToolStep > 0 {
				runtimeLine += fmt.Sprintf(" | step=%d/%d", m.agentLoopStep, m.agentLoopMaxToolStep)
			} else {
				runtimeLine += fmt.Sprintf(" | step=%d", m.agentLoopStep)
			}
		}
		if m.agentLoopToolRounds > 0 {
			runtimeLine += fmt.Sprintf(" | tools=%d", m.agentLoopToolRounds)
		}
		if provider := strings.TrimSpace(m.agentLoopProvider); provider != "" {
			model := strings.TrimSpace(m.agentLoopModel)
			if model != "" {
				runtimeLine += fmt.Sprintf(" | %s/%s", provider, model)
			} else {
				runtimeLine += fmt.Sprintf(" | %s", provider)
			}
		}
		lines = append(lines, truncateSingleLine(runtimeLine, width))
		if tool := strings.TrimSpace(m.agentLoopLastTool); tool != "" {
			status := blankFallback(strings.TrimSpace(m.agentLoopLastStatus), "running")
			lines = append(lines, truncateSingleLine("  last_tool="+tool+" ("+status+")", width))
		}
		if preview := strings.TrimSpace(m.agentLoopLastOutput); preview != "" {
			lines = append(lines, truncateSingleLine("  last_output="+preview, width))
		}
		if scope := strings.TrimSpace(m.agentLoopContextScope); scope != "" {
			lines = append(lines, truncateSingleLine("  context_scope="+scope, width))
		}
		if hint := strings.TrimSpace(m.agentLoopEnterHint); hint != "" {
			lines = append(lines, truncateSingleLine("  context_enter="+hint, width))
		}
		if hint := strings.TrimSpace(m.agentLoopExitHint); hint != "" {
			lines = append(lines, truncateSingleLine("  context_exit="+hint, width))
		}
		if contract := strings.TrimSpace(m.agentLoopContractPre); contract != "" {
			lines = append(lines, truncateSingleLine("  contract_pre="+contract, width))
		}
		if contract := strings.TrimSpace(m.agentLoopContractPost); contract != "" {
			lines = append(lines, truncateSingleLine("  contract_post="+contract, width))
		}
	}
	if len(m.toolTimeline) > 0 {
		lines = append(lines, "", sectionTitleStyle.Render("Tool Timeline"))
		start := 0
		if len(m.toolTimeline) > 7 {
			start = len(m.toolTimeline) - 7
		}
		for _, line := range m.toolTimeline[start:] {
			lines = append(lines, "  "+truncateSingleLine(line, width))
		}
	}
	if len(m.activityLog) > 0 {
		lines = append(lines, "", sectionTitleStyle.Render("Live Activity"))
		start := 0
		if len(m.activityLog) > 6 {
			start = len(m.activityLog) - 6
		}
		for _, line := range m.activityLog[start:] {
			lines = append(lines, "  "+truncateSingleLine(line, width))
		}
	}
	inputLine := renderChatInputLine(m.input, m.chatCursor, m.chatCursorManual, m.chatCursorInput, m.sending)
	lines = append(lines, "", sectionTitleStyle.Render("Input"), inputLineStyle.Render(inputLine))
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
		}
		mode := "session only"
		if m.commandPickerPersist {
			mode = "persist to .dfmc/config.yaml"
		}
		lines = append(lines, sectionTitleStyle.Render(title+" (up/down + tab + enter, esc close, ctrl+s persist)"))
		lines = append(lines, subtleStyle.Render("Apply mode: "+mode))
		if query := strings.TrimSpace(m.commandPickerQuery); query != "" {
			lines = append(lines, subtleStyle.Render("Filter: "+query))
		}
		items := m.filteredCommandPickerItems()
		if len(items) == 0 {
			if strings.EqualFold(kind, "model") && strings.TrimSpace(m.commandPickerQuery) != "" {
				lines = append(lines, "  "+subtleStyle.Render("No known model matched. Enter applies typed value: "+strings.TrimSpace(m.commandPickerQuery)))
			} else if (strings.EqualFold(kind, "tool") || strings.EqualFold(kind, "read")) && strings.TrimSpace(m.commandPickerQuery) != "" {
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
		if len(items) > 0 {
			lines = append(lines, sectionTitleStyle.Render("Command Menu (up/down + tab + enter)"))
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
				label := fmt.Sprintf("%s - %s", items[i].Template, items[i].Description)
				if i == selected {
					prefix = "> "
					label = titleStyle.Render(truncateSingleLine(label, width))
				}
				lines = append(lines, prefix+label)
			}
		}
	}
	if !suggestions.slashMenuActive {
		if len(suggestions.slashArgSuggestions) > 0 {
			lines = append(lines, sectionTitleStyle.Render("Command Args (up/down + tab)"))
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
	if suggestions.mentionQuery != "" && len(suggestions.mentionSuggestions) > 0 {
		lines = append(lines, sectionTitleStyle.Render("File Mentions (up/down + tab/enter)"))
		selected := clampIndex(m.mentionIndex, len(suggestions.mentionSuggestions))
		for i, path := range suggestions.mentionSuggestions {
			prefix := "  "
			label := truncateSingleLine(path, width)
			if i == selected {
				prefix = "> "
				label = titleStyle.Render(label)
			}
			lines = append(lines, prefix+label)
		}
	}
	if m.sending {
		lines = append(lines, subtleStyle.Render("Streaming in progress..."))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderStatusView(width int) string {
	parts := []string{
		titleStyle.Render("Status"),
		subtleStyle.Render("Press r to refresh."),
		"",
		fmt.Sprintf("Project:   %s", blankFallback(m.status.ProjectRoot, "(none)")),
		fmt.Sprintf("Provider:  %s / %s", blankFallback(m.status.Provider, "-"), blankFallback(m.status.Model, "-")),
		fmt.Sprintf("Profile:   %s", truncateForPanel(formatProviderProfileSummaryTUI(m.status.ProviderProfile), width)),
		fmt.Sprintf("Runtime:   %s", truncateForPanel(providerConnectivityHintTUI(m.status), width)),
		fmt.Sprintf("Models.dev: %s", truncateForPanel(formatModelsDevCacheSummaryTUI(m.status.ModelsDevCache), width)),
		fmt.Sprintf("AST:       %s", blankFallback(m.status.ASTBackend, "-")),
		fmt.Sprintf("Languages: %s", truncateForPanel(formatASTLanguageSummaryTUI(m.status.ASTLanguages), width)),
		fmt.Sprintf("AST Metrics: %s", truncateForPanel(formatASTMetricsSummaryTUI(m.status.ASTMetrics), width)),
		fmt.Sprintf("CodeMap:   %s", truncateForPanel(formatCodeMapMetricsSummaryTUI(m.status.CodeMap), width)),
		"",
		subtleStyle.Render(m.notice),
	}
	if summary := formatContextInSummaryTUI(m.status.ContextIn); summary != "" {
		insertAt := len(parts) - 2
		if insertAt < 0 {
			insertAt = len(parts)
		}
		contextLines := []string{fmt.Sprintf("Context In: %s", truncateForPanel(summary, width))}
		if why := formatContextInReasonSummaryTUI(m.status.ContextIn); why != "" {
			contextLines = append(contextLines, fmt.Sprintf("Context Why: %s", truncateForPanel(why, width)))
		}
		if files := formatContextInTopFilesTUI(m.status.ContextIn, 3); files != "" {
			contextLines = append(contextLines, fmt.Sprintf("Context Top: %s", truncateForPanel(files, width)))
		}
		if details := formatContextInDetailedFileLinesTUI(m.status.ContextIn, 2); len(details) > 0 {
			for _, line := range details {
				contextLines = append(contextLines, fmt.Sprintf("Context File: %s", truncateForPanel(line, width)))
			}
		}
		contextLines = append(contextLines, "")
		parts = append(parts[:insertAt], append(contextLines, parts[insertAt:]...)...)
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
		titleStyle.Render("Files"),
		subtleStyle.Render("Keys: j/k or alt+j/alt+k move | enter reload | r/alt+r refresh | p/alt+p pin | i/alt+i insert | e/alt+e explain | v/alt+v review"),
		"",
	}
	if len(m.files) == 0 {
		listLines = append(listLines, subtleStyle.Render("No indexed project files yet."))
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
		listLines = append(listLines, "", subtleStyle.Render(fmt.Sprintf("%d files | selected %d/%d", len(m.files), m.fileIndex+1, len(m.files))))
	}

	previewLines := []string{
		titleStyle.Render("Preview"),
		subtleStyle.Render(blankFallback(m.filePath, "Select a file")),
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
		diffPreview = "Working tree is clean."
	}
	patchPreview := truncateForPanel(m.patchPreviewText(), width)
	if patchPreview == "" {
		patchPreview = "No assistant patch available."
	}
	changed := "(none)"
	if len(m.changed) > 0 {
		changed = strings.Join(m.changed, ", ")
	}
	parts := []string{
		titleStyle.Render("Patch Lab"),
		subtleStyle.Render("Keys: d/alt+d diff | l/alt+l latest patch | n/b (or alt+n/alt+b) files | j/k (or alt+j/alt+k) hunks | f/alt+f focus | c/alt+c check | a/alt+a apply | u/alt+u undo"),
		"",
		"Changed: " + truncateForPanel(changed, width),
		"Patch Files: " + truncateForPanel(strings.Join(m.patchFilesOrNone(), ", "), width),
		"Patch Target: " + truncateForPanel(m.patchTargetSummary(), width),
		"Hunk Target: " + truncateForPanel(m.patchHunkSummary(), width),
		"",
		subtleStyle.Render("Worktree Diff"),
		diffPreview,
		"",
		subtleStyle.Render("Current Hunk"),
		patchPreview,
	}
	if info := m.patchFocusSummary(); info != "" {
		parts = append(parts, "", subtleStyle.Render(info))
	}
	if hints := m.patchReviewHints(); len(hints) > 0 {
		parts = append(parts, "", subtleStyle.Render("Review cues: "+strings.Join(hints, " | ")))
	}
	parts = append(parts,
		"",
		subtleStyle.Render(m.notice),
	)
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
		titleStyle.Render("Setup"),
		subtleStyle.Render("Keys: j/k or alt+j/alt+k move | enter apply | m/alt+m edit model | s/alt+s save config | r/alt+r reload runtime"),
		"",
	}
	if len(providers) == 0 {
		listLines = append(listLines, subtleStyle.Render("No providers configured."))
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
		titleStyle.Render("Selection"),
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
			fmt.Sprintf("Context:  %d", profile.MaxContext),
			fmt.Sprintf("Output:   %d", profile.MaxTokens),
			fmt.Sprintf("Endpoint: %s", blankFallback(profile.BaseURL, "(default)")),
			"",
			subtleStyle.Render("Press enter to apply this provider/model to the current TUI session."),
			subtleStyle.Render("Press s to save provider/model into project .dfmc/config.yaml."),
			subtleStyle.Render("Slash commands still work: /providers, /provider NAME, /models, /model NAME."),
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
		titleStyle.Render("Tools"),
		subtleStyle.Render("Keys: j/k or alt+j/alt+k move | enter run | r/alt+r rerun | e/alt+e edit params | x/alt+x reset"),
		subtleStyle.Render("Edit mode: type values, enter save, esc cancel. Quotes keep spaces together."),
		"",
	}
	if len(tools) == 0 {
		listLines = append(listLines, subtleStyle.Render("No registered tools."))
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
		titleStyle.Render("Tool Detail"),
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
		detailLines = append(detailLines, subtleStyle.Render("Last Result"))
		resultText := strings.TrimSpace(m.toolOutput)
		if resultText == "" {
			resultText = "No tool run yet. Press enter to run or e to edit params."
		}
		detailLines = append(detailLines, truncateForPanel(resultText, detailWidth))
	}

	left := lipgloss.NewStyle().Width(listWidth).Render(strings.Join(listLines, "\n"))
	right := lipgloss.NewStyle().Width(detailWidth).Render(strings.Join(detailLines, "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right)
}

func (m Model) renderFooter(width int) string {
	tab := m.tabs[m.activeTab]
	providerName := blankFallback(m.status.Provider, "-")
	modelName := blankFallback(m.status.Model, "-")
	mode := "ready"
	if m.sending {
		mode = "streaming"
	}
	stateParts := []string{
		"tab=" + tab,
		"provider=" + providerName,
		"model=" + modelName,
		"mode=" + mode,
	}
	if pinned := strings.TrimSpace(m.pinnedFile); pinned != "" {
		stateParts = append(stateParts, "pinned="+truncateSingleLine(pinned, 22))
	}
	if m.agentLoopActive {
		loop := "agent=on"
		if m.agentLoopStep > 0 && m.agentLoopMaxToolStep > 0 {
			loop = fmt.Sprintf("agent=%d/%d", m.agentLoopStep, m.agentLoopMaxToolStep)
		}
		stateParts = append(stateParts, loop)
	}
	stateLine := strings.Join(stateParts, " | ")

	helpLine := "keys: " + m.footerHintForTab(tab)
	if note := strings.TrimSpace(m.notice); note != "" {
		helpLine += " | last: " + truncateSingleLine(note, 44)
	}

	maxWidth := width - 4
	if maxWidth < 16 {
		maxWidth = 16
	}
	lines := []string{
		truncateSingleLine(stateLine, maxWidth),
		truncateSingleLine(helpLine, maxWidth),
	}
	return strings.Join(lines, "\n")
}

func (m Model) footerHintForTab(tab string) string {
	switch strings.TrimSpace(strings.ToLower(tab)) {
	case "chat":
		return "enter send, / commands, @ mention, ctrl+p menu, alt+1..6 tabs, ctrl+q quit"
	case "status":
		return "r refresh status, alt+1..6 tabs, ctrl+q quit"
	case "files":
		return "j/k or alt+j/alt+k move, p/alt+p pin, i/alt+i insert, e/alt+e explain, v/alt+v review"
	case "patch":
		return "d/alt+d diff, l/alt+l patch, n/b or alt+n/alt+b files, j/k or alt+j/alt+k hunks"
	case "setup":
		return "j/k or alt+j/alt+k provider, enter apply, m/alt+m edit, s/alt+s save, r/alt+r reload"
	case "tools":
		return "j/k or alt+j/alt+k select, enter run, e/alt+e edit, x/alt+x reset, r/alt+r rerun"
	default:
		return "alt+1..6 tabs, ctrl+p command palette, ctrl+q quit"
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
		files, err := listProjectFiles(root, 500)
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

func toolResultInt(data map[string]any, key string) int {
	if data == nil {
		return 0
	}
	switch v := data[key].(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
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
		files := payloadInt(payload, "context_files", 0)
		tokens := payloadInt(payload, "context_tokens", 0)
		line = fmt.Sprintf("Agent loop started: max_tools=%d context=%df/%dtok", m.agentLoopMaxToolStep, files, tokens)
	case "agent:loop:contract":
		m.agentLoopActive = true
		m.agentLoopPhase = "contract"
		m.agentLoopProvider = payloadString(payload, "provider", m.agentLoopProvider)
		m.agentLoopModel = payloadString(payload, "model", m.agentLoopModel)
		m.agentLoopContextScope = payloadString(payload, "context_snapshot", m.agentLoopContextScope)
		m.agentLoopContractPre = payloadString(payload, "pre_tool", m.agentLoopContractPre)
		m.agentLoopContractPost = payloadString(payload, "post_tool", m.agentLoopContractPost)
		line = "Agent loop contract ready."
	case "agent:loop:context_enter":
		m.agentLoopActive = true
		m.agentLoopPhase = "context-enter"
		if step := payloadInt(payload, "step", 0); step > 0 {
			m.agentLoopStep = step
		}
		if maxSteps := payloadInt(payload, "max_tool_steps", 0); maxSteps > 0 {
			m.agentLoopMaxToolStep = maxSteps
		}
		if rounds := payloadInt(payload, "tool_rounds", -1); rounds >= 0 {
			m.agentLoopToolRounds = rounds
		}
		m.agentLoopProvider = payloadString(payload, "provider", m.agentLoopProvider)
		m.agentLoopModel = payloadString(payload, "model", m.agentLoopModel)
		m.agentLoopContextScope = payloadString(payload, "context_snapshot", m.agentLoopContextScope)
		m.agentLoopEnterHint = payloadString(payload, "instruction", m.agentLoopEnterHint)
		if m.agentLoopStep > 0 && m.agentLoopMaxToolStep > 0 {
			line = fmt.Sprintf("Context enter: step %d/%d", m.agentLoopStep, m.agentLoopMaxToolStep)
		} else {
			line = "Context enter."
		}
		if hint := strings.TrimSpace(m.agentLoopEnterHint); hint != "" {
			line += " " + truncateSingleLine(hint, 120)
		}
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
		if step > 0 {
			m.agentLoopStep = step
		}
		if rounds := payloadInt(payload, "tool_rounds", 0); rounds > 0 {
			m.agentLoopToolRounds = rounds
		}
		m.agentLoopProvider = payloadString(payload, "provider", m.agentLoopProvider)
		m.agentLoopModel = payloadString(payload, "model", m.agentLoopModel)
		paramsPreview := payloadString(payload, "params_preview", "")
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
		if preview := payloadString(payload, "output_preview", ""); preview != "" {
			m.agentLoopLastOutput = preview
		}
		if step := payloadInt(payload, "step", 0); step > 0 {
			m.agentLoopStep = step
			if step > m.agentLoopToolRounds {
				m.agentLoopToolRounds = step
			}
		}
		m.agentLoopProvider = payloadString(payload, "provider", m.agentLoopProvider)
		m.agentLoopModel = payloadString(payload, "model", m.agentLoopModel)
		if duration > 0 {
			line = fmt.Sprintf("Agent tool result: %s (%s, %dms)", toolName, status, duration)
		} else {
			line = fmt.Sprintf("Agent tool result: %s (%s)", toolName, status)
		}
		if preview := payloadString(payload, "output_preview", ""); preview != "" {
			line += " -> " + preview
		} else if !success {
			if errText := payloadString(payload, "error", ""); errText != "" {
				line += " -> " + truncateSingleLine(errText, 96)
			}
		}
	case "agent:loop:context_exit":
		m.agentLoopActive = true
		m.agentLoopPhase = "context-exit"
		if step := payloadInt(payload, "step", 0); step > 0 {
			m.agentLoopStep = step
		}
		if rounds := payloadInt(payload, "tool_rounds", -1); rounds >= 0 {
			m.agentLoopToolRounds = rounds
		}
		if toolName := payloadString(payload, "tool", ""); toolName != "" {
			m.agentLoopLastTool = toolName
		}
		success := payloadBool(payload, "success", true)
		if success {
			m.agentLoopLastStatus = "ok"
		} else {
			m.agentLoopLastStatus = "failed"
		}
		m.agentLoopExitHint = payloadString(payload, "instruction", m.agentLoopExitHint)
		line = "Context exit"
		if toolName := strings.TrimSpace(m.agentLoopLastTool); toolName != "" {
			line += ": " + toolName
		}
		if hint := strings.TrimSpace(m.agentLoopExitHint); hint != "" {
			line += " -> " + truncateSingleLine(hint, 120)
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
	case "tool:error":
		switch payload := event.Payload.(type) {
		case string:
			line = "Tool error: " + strings.TrimSpace(payload)
		default:
			line = "Tool error occurred."
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
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return m
	}
	m.appendActivity(line)
	if shouldTrackToolTimeline(eventType) {
		m.appendToolTimeline(line)
		if strings.EqualFold(eventType, "context:built") {
			if reasons := payloadStringSlice(payload, "reasons"); len(reasons) > 0 {
				m.appendToolTimeline("Context why: " + truncateSingleLine(strings.Join(reasons, " | "), 180))
			}
		}
	}
	m.notice = line
	if m.sending && shouldMirrorEventToTranscript(eventType) {
		m = m.appendSystemMessage(line)
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

func payloadStringSlice(data map[string]any, key string) []string {
	if data == nil {
		return nil
	}
	raw, ok := data[key]
	if !ok || raw == nil {
		return nil
	}
	out := []string{}
	appendValue := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		out = append(out, value)
	}
	switch value := raw.(type) {
	case []string:
		for _, item := range value {
			appendValue(item)
		}
	case []any:
		for _, item := range value {
			appendValue(fmt.Sprint(item))
		}
	default:
		appendValue(fmt.Sprint(value))
	}
	return out
}

func shouldTrackToolTimeline(eventType string) bool {
	switch strings.TrimSpace(strings.ToLower(eventType)) {
	case "agent:loop:start", "agent:loop:contract", "agent:loop:context_enter", "tool:call", "tool:result", "agent:loop:context_exit", "agent:loop:final", "agent:loop:max_steps", "agent:loop:error", "context:built":
		return true
	default:
		return false
	}
}

func shouldMirrorEventToTranscript(eventType string) bool {
	switch strings.TrimSpace(strings.ToLower(eventType)) {
	case "agent:loop:context_enter", "tool:call", "tool:result", "agent:loop:context_exit", "agent:loop:error", "agent:loop:max_steps", "context:built":
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

func (m *Model) appendToolTimeline(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if n := len(m.toolTimeline); n > 0 && strings.EqualFold(strings.TrimSpace(m.toolTimeline[n-1]), line) {
		return
	}
	m.toolTimeline = append(m.toolTimeline, line)
	if len(m.toolTimeline) > 18 {
		drop := len(m.toolTimeline) - 18
		m.toolTimeline = m.toolTimeline[drop:]
	}
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
	m.agentLoopLastOutput = ""
	m.agentLoopContextScope = ""
	m.agentLoopEnterHint = ""
	m.agentLoopExitHint = ""
	m.agentLoopContractPre = ""
	m.agentLoopContractPost = ""
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
			end := file.LineEnd
			if end < file.LineStart {
				end = file.LineStart
			}
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
			trimTo := width - 14
			if trimTo < 0 {
				trimTo = 0
			}
			lines[i] = string(runes[:trimTo]) + "... [trimmed]"
		}
	}
	return strings.Join(lines, "\n")
}

func chatPreviewForLine(item chatLine, width int) string {
	base := strings.TrimSpace(item.Preview)
	if base == "" {
		base = chatDigest(item.Content)
	}
	if base == "" {
		return ""
	}
	return truncateSingleLine(base, chatPreviewWidth(width))
}

func fastChatPreview(text string, width int) string {
	base := chatDigest(text)
	if base == "" {
		return ""
	}
	return truncateSingleLine(base, chatPreviewWidth(width))
}

func chatPreviewWidth(width int) int {
	maxWidth := width
	if maxWidth <= 0 {
		maxWidth = 120
	}
	if maxWidth < 24 {
		maxWidth = 24
	}
	return maxWidth
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
	if idx := strings.IndexByte(trimmed, '\n'); idx >= 0 {
		first := strings.TrimSpace(trimmed[:idx])
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
			"Use up/down + tab to select from Command Menu.",
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

func (m Model) slashCommandCatalog() []slashCommandItem {
	return []slashCommandItem{
		{Command: "help", Template: "/help", Description: "show command help"},
		{Command: "status", Template: "/status", Description: "show runtime status"},
		{Command: "reload", Template: "/reload", Description: "reload config and env"},
		{Command: "context", Template: "/context", Description: "show context summary"},
		{Command: "ls", Template: "/ls .", Description: "list project files"},
		{Command: "read", Template: "/read " + blankFallback(m.toolTargetFile(), "path/to/file.go"), Description: "read file lines"},
		{Command: "grep", Template: "/grep TODO", Description: "search codebase regex"},
		{Command: "run", Template: "/run go test ./...", Description: "run guarded command"},
		{Command: "tool", Template: "/tool read_file path=" + blankFallback(m.toolTargetFile(), "README.md"), Description: "run any tool"},
		{Command: "providers", Template: "/providers", Description: "list configured providers"},
		{Command: "provider", Template: "/provider " + blankFallback(m.currentProvider(), "openai"), Description: "switch provider"},
		{Command: "models", Template: "/models", Description: "show configured model"},
		{Command: "model", Template: "/model " + blankFallback(m.currentModel(), "model-name"), Description: "override model"},
		{Command: "tools", Template: "/tools", Description: "list tools and open tools panel"},
		{Command: "diff", Template: "/diff", Description: "show worktree diff"},
		{Command: "patch", Template: "/patch", Description: "show latest patch summary"},
		{Command: "apply", Template: "/apply --check", Description: "check latest patch apply"},
		{Command: "undo", Template: "/undo", Description: "undo last exchange"},
	}
}

func isKnownChatCommandToken(token string) bool {
	switch strings.TrimSpace(strings.ToLower(token)) {
	case "help", "status", "reload", "context", "tools", "tool", "ls", "read", "grep", "run", "diff", "patch", "undo", "apply", "providers", "provider", "models", "model":
		return true
	default:
		return false
	}
}

func (m Model) chatPrompt() string {
	question := strings.TrimSpace(expandAtFileMentions(m.input, m.files))
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
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" {
		return ""
	}
	return "[[file:" + rel + "]]"
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
		for _, existing := range suggestions {
			if existing == value {
				return
			}
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

func activeMentionQuery(input string) (string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", false
	}
	lastSpace := strings.LastIndexAny(input, " \t\n")
	token := input
	if lastSpace >= 0 {
		token = input[lastSpace+1:]
	}
	if !strings.HasPrefix(token, "@") {
		return "", false
	}
	return strings.TrimPrefix(token, "@"), true
}

func (m Model) mentionSuggestions(query string, limit int) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 {
		limit = 5
	}
	out := make([]string, 0, limit)
	for _, path := range m.files {
		candidate := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
		if query == "" || strings.HasPrefix(candidate, query) || strings.Contains(candidate, query) {
			out = append(out, filepath.ToSlash(path))
			if len(out) >= limit {
				break
			}
		}
	}
	return out
}

func (m Model) mentionSuggestionsForInput(limit int) []string {
	query, ok := activeMentionQuery(m.input)
	if !ok {
		return nil
	}
	return m.mentionSuggestions(query, limit)
}

func (m Model) mentionMenuActive() bool {
	return len(m.mentionSuggestionsForInput(5)) > 0
}

func (m Model) autocompleteMentionSelection() (string, bool) {
	return autocompleteMentionSelectionFromSuggestions(m.input, m.mentionIndex, m.mentionSuggestionsForInput(5))
}

func (m Model) autocompleteMention() (string, bool) {
	return m.autocompleteMentionSelection()
}

func replaceActiveMention(input, path string) string {
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
	return prefix + fileMarker(path)
}

func expandAtFileMentions(input string, files []string) string {
	tokens := strings.Fields(input)
	if len(tokens) == 0 {
		return input
	}
	changed := false
	for i, token := range tokens {
		if !strings.HasPrefix(token, "@") || len(token) < 2 {
			continue
		}
		query := filepath.ToSlash(strings.TrimSpace(strings.TrimPrefix(token, "@")))
		if query == "" {
			continue
		}
		matches := make([]string, 0, 2)
		for _, path := range files {
			candidate := filepath.ToSlash(strings.TrimSpace(path))
			if strings.EqualFold(candidate, query) {
				matches = []string{candidate}
				break
			}
			if strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(query)) {
				matches = append(matches, candidate)
				if len(matches) > 1 {
					break
				}
			}
		}
		if len(matches) == 1 {
			tokens[i] = fileMarker(matches[0])
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

func truncateSingleLine(text string, width int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if width <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= width {
		return trimmed
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
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
