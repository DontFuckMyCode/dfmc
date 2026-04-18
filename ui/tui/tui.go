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
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/commands"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/planning"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
	toolruntime "github.com/dontfuckmycode/dfmc/internal/tools"
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
	ctx context.Context
	eng *engine.Engine

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
	// Setup wizard cursor + draft. See setupWizardState in panel_states.go.
	setupWizard setupWizardState
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

	// All read-only diagnostic panel states live in panel_states.go as
	// their own per-panel sub-structs. Embedding by named field instead
	// of flattening every panel's 6-9 fields onto Model keeps the type
	// declaration scannable and groups related state by panel.
	memory        memoryPanelState
	codemap       codemapPanelState
	conversations conversationsPanelState
	prompts       promptsPanelState
	security      securityPanelState
	plans         plansPanelState

	// Context panel state — diagnostic view over Engine.ContextBudgetPreview
	// and ContextRecommendations. Lets the user see the per-query token
	// budget before an Ask is actually sent. See contextPanelState in
	// panel_states.go.
	contextPanel contextPanelState

	// Providers panel state — diagnostic view over the provider router.
	// Rows are cached (refresh on 'r' or first tab activation) because
	// Hints() is cheap but there's no point redoing the walk on every
	// keystroke. See providersPanelState in panel_states.go.
	providers providersPanelState

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
// agent loop is alive. Each tick bumps m.chat.spinnerFrame so the streaming
// indicator, stats panel, and any other animated surface can paint motion
// instead of a static glyph.
type spinnerTickMsg struct{}

// spinnerInterval is the frame cadence. ~125ms lands at ~8fps, which reads as
// continuous motion without chewing CPU.
const spinnerInterval = 125 * time.Millisecond

// spinnerTickCmd schedules the next spinner frame. The caller is responsible
// for only scheduling one at a time (see Model.chat.spinnerTicking).
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
	return Model{
		ctx:          ctx,
		eng:          eng,
		tabs:         []string{"Chat", "Status", "Files", "Patch", "Setup", "Tools", "Activity", "Memory", "CodeMap", "Conversations", "Prompts", "Security", "Plans", "Context", "Providers"},
		activity:     activityPanelState{follow: true},
		memory:       memoryPanelState{tier: memoryTierAll},
		codemap:      codemapPanelState{view: codemapViewOverview},
		security:     securityPanelState{view: securityViewSecrets},
		chat:         chatState{streamIndex: -1},
		inputHistory: inputHistoryState{index: -1},
		toolView:     toolViewState{overrides: map[string]string{}},
		// The chat body shows the welcome + starters on first paint; don't
		// park a duplicate banner in the footer notice slot (signal density).
		sessionStart: time.Now(),
		ui: uiToggles{
			showStatsPanel: true,
			keyLogEnabled:  os.Getenv("DFMC_KEYLOG") == "1",
		},
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

// Update (the bubbletea reducer / message dispatcher) lives in update.go.
// handleChatKey (chat panel keyboard router) lives in chat_key.go.

func (m Model) buildChatSuggestionState() chatSuggestionState {
	state := chatSuggestionState{
		slashMenuActive: m.slashMenuActive(),
	}
	if state.slashMenuActive {
		state.slashCommands = m.filteredSlashCommands()
	} else {
		state.slashArgSuggestions = m.activeSlashArgSuggestions()
	}
	if query, rangeSuffix, ok := activeMentionQuery(m.chat.input); ok {
		state.mentionActive = true
		state.mentionQuery = query
		state.mentionRange = rangeSuffix
		state.mentionSuggestions = m.mentionSuggestions(query, 8)
	}
	if !state.slashMenuActive && !state.mentionActive && !m.commandPicker.active && !m.chat.sending {
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
	raw := strings.TrimSpace(m.chat.input)
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

// executeChatCommand (the slash-command dispatcher) lives in chat_commands.go.

func (m Model) appendSystemMessage(text string) Model {
	m.chat.transcript = append(m.chat.transcript, newChatLine("system", strings.TrimSpace(text)))
	m.chat.scrollback = 0
	return m
}

// Diagnostic cards & transcript transforms (exportTranscript,
// describeStats, describeHealth, describeHooks, formatHookEvent,
// describeApprovalGate, compactTranscript) live in describe.go.

// appendToolEventMessage inserts a tool-tagged transcript line so tool calls
// and results render with the TOOL badge rather than SYS. This is what makes
// the chat feel like a unified conversation — the events sit where they
// actually fired instead of being relegated to a separate side panel.
func (m Model) appendToolEventMessage(text string) Model {
	m.chat.transcript = append(m.chat.transcript, newChatLine("tool", strings.TrimSpace(text)))
	m.chat.scrollback = 0
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
	m.chat.transcript = append(m.chat.transcript, newChatLine("coach", body))
	m.chat.scrollback = 0
	m.appendActivity("coach: " + text)
	m.notice = text
	return m
}

// scrollTranscript shifts the chat head backwards by delta *lines* (negative
// = older/upward, positive = newer/downward) and clamps to a rough ceiling
// derived from the transcript size. The render layer (fitChatBody) clamps
// tighter based on actual rendered line count — scroll just tracks intent.
func (m *Model) scrollTranscript(delta int) {
	next := m.chat.scrollback - delta
	if next < 0 {
		next = 0
	}
	maxBack := estimateTranscriptLines(m.chat.transcript)
	if next > maxBack {
		next = maxBack
	}
	if next == m.chat.scrollback {
		if next == 0 {
			m.notice = "Transcript: already at latest"
		} else {
			m.notice = "Transcript: at top of history"
		}
		return
	}
	m.chat.scrollback = next
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
	m.ui.resumePromptActive = false
	m.agentLoop.toolTimeline = nil
	m.chat.scrollback = 0
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
	if m.ui.planMode {
		question = strings.TrimRight(question, "\n") +
			"\n\n[DFMC plan mode] You are in INVESTIGATE-ONLY mode. " +
			"Use ONLY read-only tools (read_file, grep_codebase, ast_query, list_dir, glob, git_status, git_diff, web_fetch, web_search). " +
			"Do NOT call write_file, edit_file, apply_patch, or run_command with destructive arguments. " +
			"Produce a concrete plan as the answer — numbered steps, files to touch, expected diffs — that the user can approve before any files are modified."
	} else {
		question = m.enforceToolUseForActionRequests(question)
	}
	if len(quickActions) > 0 {
		selected := quickActions[clampIndex(m.slashMenu.quickAction, len(quickActions))]
		m.chat.transcript = append(m.chat.transcript, newChatLine("user", question))
		m = m.appendSystemMessage("Auto action: " + selected.Reason)
		m = m.startChatToolCommand(selected.Tool, selected.Params)
		return m, runToolCmd(m.eng, selected.Tool, selected.Params)
	}
	if name, params, reason, ok := m.autoToolIntentFromQuestion(question); ok {
		m.chat.transcript = append(m.chat.transcript, newChatLine("user", question))
		m = m.appendSystemMessage("Auto action: " + reason)
		m = m.startChatToolCommand(name, params)
		return m, runToolCmd(m.eng, name, params)
	}
	m.chat.transcript = append(m.chat.transcript,
		newChatLine("user", question),
		newChatLine("assistant", ""),
	)
	m.chat.streamIndex = len(m.chat.transcript) - 1
	m.chat.sending = true
	m.chat.streamStartedAt = time.Now()
	m.notice = "Streaming answer... (esc cancels)"
	// Per-stream context so esc can cancel this turn without killing the
	// whole TUI's ctx (which would kill timers and subscriptions too).
	streamCtx, cancel := context.WithCancel(m.ctx)
	m.chat.streamCancel = cancel
	m.chat.streamMessages = startChatStream(streamCtx, m.eng, question)
	return m, tea.Batch(waitForStreamMsg(m.chat.streamMessages), m.ensureSpinnerTick())
}

// ensureSpinnerTick schedules the spinner tick when needed, but only if one
// isn't already in flight. Mutates m.chat.spinnerTicking and returns the cmd (nil
// when no schedule is needed).
func (m *Model) ensureSpinnerTick() tea.Cmd {
	if m.chat.spinnerTicking {
		return nil
	}
	if !m.chat.sending && !m.agentLoop.active {
		return nil
	}
	m.chat.spinnerTicking = true
	return spinnerTickCmd()
}

// drainPendingQueue pops the oldest queued message and submits it as if the
// user had just pressed enter. Called when the current stream finishes so
// follow-up messages flow without the user babysitting the composer.
func (m Model) drainPendingQueue() (Model, tea.Cmd) {
	if len(m.chat.pendingQueue) == 0 {
		return m, nil
	}
	next := m.chat.pendingQueue[0]
	m.chat.pendingQueue = m.chat.pendingQueue[1:]
	m = m.appendSystemMessage(fmt.Sprintf("▸ draining queued message (%d remaining): %s", len(m.chat.pendingQueue), next))
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
	m.ui.resumePromptActive = false
	m.agentLoop.toolTimeline = nil
	m.chat.scrollback = 0
	banner := "Resuming parked agent loop"
	if note != "" {
		banner += " with note: " + note
	}
	m = m.appendSystemMessage(banner + "...")
	m.chat.transcript = append(m.chat.transcript, newChatLine("assistant", ""))
	m.chat.streamIndex = len(m.chat.transcript) - 1
	m.chat.sending = true
	m.chat.streamStartedAt = time.Now()
	m.notice = "Resuming agent loop..."
	m.chat.streamMessages = startChatResumeStream(m.ctx, m.eng, note)
	return m, tea.Batch(waitForStreamMsg(m.chat.streamMessages), m.ensureSpinnerTick())
}

func newChatLine(role, content string) chatLine {
	return chatLine{
		Role:      chatRole(strings.TrimSpace(role)),
		Content:   content,
		Preview:   chatDigest(content),
		Timestamp: time.Now(),
	}
}

func (m Model) startChatToolCommand(name string, params map[string]any) Model {
	name = strings.TrimSpace(name)
	m.setChatInput("")
	m.chat.toolPending = true
	m.chat.toolName = name
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

// Provider/model selection & project config persistence
// (availableProviders, currentProvider, currentModel,
// defaultModelForProvider, snapSetupCursorToActive,
// parseModelPersistArgs, parseArgsWithPersist,
// applyProviderModelSelection, formatProviderSwitchNotice,
// projectConfigPath, reloadEngineConfig,
// persistProviderModelProjectConfig, ensureStringAnyMap,
// toStringAnyMap, providerProfile) lives in provider.go.

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
	if m.toolView.index < 0 {
		return tools[0]
	}
	if m.toolView.index >= len(tools) {
		return tools[len(tools)-1]
	}
	return tools[m.toolView.index]
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
	if m.toolView.overrides == nil {
		return ""
	}
	return strings.TrimSpace(m.toolView.overrides[strings.TrimSpace(name)])
}

func (m Model) toolTargetFile() string {
	if pinned := strings.TrimSpace(m.filesView.pinned); pinned != "" {
		return pinned
	}
	if selected := strings.TrimSpace(m.selectedFile()); selected != "" {
		return selected
	}
	if preview := strings.TrimSpace(m.filesView.path); preview != "" {
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
	raw := strings.TrimSpace(m.chat.input)
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
		"Pinned: " + blankFallback(strings.TrimSpace(m.filesView.pinned), "(none)"),
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
		"Pinned: " + blankFallback(strings.TrimSpace(m.filesView.pinned), "(none)"),
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
		if len(m.filesView.entries) == 0 {
			return m, nil
		}
		if m.filesView.index < len(m.filesView.entries)-1 {
			m.filesView.index++
		}
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())
	case "up", "k", "alt+k":
		if len(m.filesView.entries) == 0 {
			return m, nil
		}
		if m.filesView.index > 0 {
			m.filesView.index--
		}
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())
	case "enter":
		if len(m.filesView.entries) == 0 {
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
	m.setupWizard.index = clampIndex(m.setupWizard.index, len(providers))
	if m.setupWizard.editing {
		switch msg.Type {
		case tea.KeyRunes:
			m.setupWizard.draft += string(msg.Runes)
			return m, nil
		case tea.KeySpace:
			m.setupWizard.draft += " "
			return m, nil
		case tea.KeyBackspace, tea.KeyCtrlH:
			runes := []rune(m.setupWizard.draft)
			if len(runes) > 0 {
				m.setupWizard.draft = string(runes[:len(runes)-1])
			}
			return m, nil
		case tea.KeyEnter:
			target := providers[m.setupWizard.index]
			model := strings.TrimSpace(m.setupWizard.draft)
			if model == "" {
				model = m.defaultModelForProvider(target)
			}
			m = m.applyProviderModelSelection(target, model)
			m.setupWizard.editing = false
			m.setupWizard.draft = ""
			m.notice = fmt.Sprintf("Setup applied: %s (%s)", target, blankFallback(model, "-"))
			m = m.appendSystemMessage(fmt.Sprintf("Setup applied: provider=%s model=%s", target, blankFallback(model, "-")))
			return m, loadStatusCmd(m.eng)
		case tea.KeyEsc:
			m.setupWizard.editing = false
			m.setupWizard.draft = ""
			m.notice = "Setup edit cancelled."
			return m, nil
		}
		return m, nil
	}
	switch msg.String() {
	case "down", "j", "alt+j":
		if m.setupWizard.index < len(providers)-1 {
			m.setupWizard.index++
		}
		m.notice = "Setup selection: " + providers[m.setupWizard.index]
		return m, nil
	case "up", "k", "alt+k":
		if m.setupWizard.index > 0 {
			m.setupWizard.index--
		}
		m.notice = "Setup selection: " + providers[m.setupWizard.index]
		return m, nil
	case "m", "alt+m":
		selected := providers[m.setupWizard.index]
		m.setupWizard.editing = true
		m.setupWizard.draft = m.defaultModelForProvider(selected)
		m.notice = "Editing model for " + selected
		return m, nil
	case "enter":
		target := providers[m.setupWizard.index]
		model := m.defaultModelForProvider(target)
		m = m.applyProviderModelSelection(target, model)
		m.notice = fmt.Sprintf("Setup applied: %s (%s)", target, blankFallback(model, "-"))
		m = m.appendSystemMessage(fmt.Sprintf("Setup applied: provider=%s model=%s", target, blankFallback(model, "-")))
		return m, loadStatusCmd(m.eng)
	case "s", "alt+s":
		target := providers[m.setupWizard.index]
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
	m.toolView.index = clampIndex(m.toolView.index, len(tools))
	if m.toolView.editing {
		switch msg.Type {
		case tea.KeyRunes:
			m.toolView.draft += string(msg.Runes)
			return m, nil
		case tea.KeySpace:
			m.toolView.draft += " "
			return m, nil
		case tea.KeyBackspace, tea.KeyCtrlH:
			runes := []rune(m.toolView.draft)
			if len(runes) > 0 {
				m.toolView.draft = string(runes[:len(runes)-1])
			}
			return m, nil
		case tea.KeyEnter:
			name := tools[m.toolView.index]
			if m.toolView.overrides == nil {
				m.toolView.overrides = map[string]string{}
			}
			trimmed := strings.TrimSpace(m.toolView.draft)
			if trimmed == "" {
				delete(m.toolView.overrides, name)
				m.notice = "Tool params reset: " + name
			} else {
				m.toolView.overrides[name] = trimmed
				m.notice = "Tool params saved: " + name
			}
			m.toolView.editing = false
			return m, nil
		case tea.KeyEsc:
			m.toolView.editing = false
			m.notice = "Tool edit cancelled."
			return m, nil
		}
		return m, nil
	}
	switch msg.String() {
	case "down", "j", "alt+j":
		if m.toolView.index < len(tools)-1 {
			m.toolView.index++
		}
		m.notice = "Tool selection: " + tools[m.toolView.index]
		return m, nil
	case "up", "k", "alt+k":
		if m.toolView.index > 0 {
			m.toolView.index--
		}
		m.notice = "Tool selection: " + tools[m.toolView.index]
		return m, nil
	case "e", "alt+e":
		name := tools[m.toolView.index]
		m.toolView.editing = true
		m.toolView.draft = m.toolPresetSummary(name)
		m.notice = "Editing params for " + name
		return m, nil
	case "x", "alt+x":
		name := tools[m.toolView.index]
		if m.toolView.overrides != nil {
			delete(m.toolView.overrides, name)
		}
		m.toolView.draft = ""
		m.notice = "Reset params for " + name
		return m, nil
	case "enter", "r", "alt+r":
		name := tools[m.toolView.index]
		params, err := m.toolPresetParams(name)
		if err != nil {
			m.toolView.output = fmt.Sprintf("Tool: %s\nStatus: blocked\n\n%s", name, err.Error())
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
		body := fitChatBody(parts.Head, parts.Tail, innerHeight, m.chat.scrollback)
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
	if len(m.chat.transcript) == 0 {
		lines = append(lines, renderStarterPrompts(min(width, 120), headerInfo.Configured)...)
	}
	// assistantCounter tracks the 1-based ordinal of each assistant
	// message in the transcript so the renderer can stamp each one with
	// a `#N` chip. That chip is the integer the user passes to `/copy N`
	// to move a specific response to the clipboard.
	assistantCounter := 0
	for i, item := range m.chat.transcript {
		if i > 0 {
			lines = append(lines, "")
		}
		durationMs := item.DurationMs
		if m.chat.streamIndex == i && m.chat.sending && !m.chat.streamStartedAt.IsZero() {
			durationMs = int(time.Since(m.chat.streamStartedAt).Milliseconds())
		}
		copyIdx := 0
		if item.Role.Eq(chatRoleAssistant) {
			assistantCounter++
			copyIdx = assistantCounter
		}
		hdr := renderMessageHeader(messageHeaderInfo{
			Role:         string(item.Role),
			Timestamp:    item.Timestamp,
			TokenCount:   item.TokenCount,
			DurationMs:   durationMs,
			ToolCalls:    item.ToolCalls,
			ToolFailures: item.ToolFailures,
			Streaming:    m.chat.streamIndex == i && m.chat.sending,
			SpinnerFrame: m.chat.spinnerFrame,
			CopyIndex:    copyIdx,
		})
		content := chatBubbleContent(item, m.chat.streamIndex == i && m.chat.sending)
		lines = append(lines, renderMessageBubble(string(item.Role), content, hdr, width))
		if item.Role.Eq(chatRoleAssistant) {
			if strip := renderInlineToolChips(item.ToolChips, width); strip != "" {
				lines = append(lines, strip)
			}
			if summary := m.chatPatchSummary(item); summary != "" {
				lines = append(lines, subtleStyle.Render("    "+summary))
			}
		}
	}
	if m.agentLoop.active {
		// When the stats panel is visible it owns tool rounds / last tool; the
		// inline runtime card would just echo it, so skip the card and only
		// keep the context-scope hint (panel has no room for prose).
		if !slimHeader {
			card := renderRuntimeCard(runtimeSummary{
				Active:       m.agentLoop.active,
				Phase:        m.agentLoop.phase,
				Step:         m.agentLoop.step,
				MaxSteps:     m.agentLoop.maxToolStep,
				ToolRounds:   m.agentLoop.toolRounds,
				LastTool:     m.agentLoop.lastTool,
				LastStatus:   m.agentLoop.lastStatus,
				LastDuration: m.agentLoop.lastDuration,
				Provider:     m.agentLoop.provider,
				Model:        m.agentLoop.model,
			}, min(width, 120))
			if strings.TrimSpace(card) != "" {
				lines = append(lines, "", card)
			}
		}
		if scope := strings.TrimSpace(m.agentLoop.contextScope); scope != "" {
			lines = append(lines, subtleStyle.Render(truncateSingleLine("  "+scope, width)))
		}
	}

	head := strings.Join(lines, "\n")

	// Tail — input box + pickers + streaming indicator. Built as its own
	// buffer so fitChatBody can keep it pinned at the bottom of the
	// rendered viewport regardless of how long the transcript grows.
	tailLines := []string{}
	if m.ui.showHelpOverlay {
		tailLines = append(tailLines, "", m.renderHelpOverlay(min(width, 120)))
	}
	if m.ui.resumePromptActive && !m.chat.sending {
		tailLines = append(tailLines, "", renderResumeBanner(m.agentLoop.step, m.agentLoop.maxToolStep, min(width, 100)))
	}
	inputLine := renderChatInputLine(m.chat.input, m.chat.cursor, m.chat.cursorManual, m.chat.cursorInput, m.chat.sending)
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
	pickerActive := m.pendingApproval != nil || suggestions.mentionActive || suggestions.slashMenuActive || m.commandPicker.active
	if suggestions.mentionActive {
		tailLines = append(tailLines, "", renderMentionPickerModal(suggestions, m.slashMenu.mention, len(m.filesView.entries), min(width-2, 110)))
	} else if suggestions.slashMenuActive {
		tailLines = append(tailLines, "", renderSlashPickerModal(suggestions.slashCommands, m.slashMenu.command, min(width-2, 110)))
	}

	if !pickerActive {
		if strip := m.renderContextStrip(min(width, 120)); strip != "" {
			tailLines = append(tailLines, strip)
		}
	}
	lines = tailLines
	if m.commandPicker.active {
		kind := strings.TrimSpace(strings.ToLower(m.commandPicker.kind))
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
		if m.commandPicker.persist {
			mode = "persist → .dfmc/config.yaml"
		}
		lines = append(lines, sectionTitleStyle.Render(title))
		lines = append(lines, subtleStyle.Render("↑↓ move · tab cycle · enter apply · ctrl+s "+mode+" · esc close"))
		if query := strings.TrimSpace(m.commandPicker.query); query != "" {
			lines = append(lines, subtleStyle.Render("filter: "+query))
		}
		items := m.filteredCommandPickerItems()
		if len(items) == 0 {
			if strings.EqualFold(kind, "model") && strings.TrimSpace(m.commandPicker.query) != "" {
				lines = append(lines, "  "+subtleStyle.Render("No known model matched. Enter applies typed value: "+strings.TrimSpace(m.commandPicker.query)))
			} else if (strings.EqualFold(kind, "tool") || strings.EqualFold(kind, "read") || strings.EqualFold(kind, "run") || strings.EqualFold(kind, "grep")) && strings.TrimSpace(m.commandPicker.query) != "" {
				lines = append(lines, "  "+subtleStyle.Render("No exact match. Enter prepares typed value: "+strings.TrimSpace(m.commandPicker.query)))
			} else {
				lines = append(lines, "  "+subtleStyle.Render("No matching entries."))
			}
		} else {
			selected := clampIndex(m.commandPicker.index, len(items))
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
			selected := clampIndex(m.slashMenu.commandArg, len(suggestions.slashArgSuggestions))
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
			selected := clampIndex(m.slashMenu.quickAction, len(suggestions.quickActions))
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
	if m.chat.sending {
		phase := "drafting reply"
		if m.agentLoop.active {
			if p := strings.TrimSpace(m.agentLoop.phase); p != "" {
				phase = p
			}
		}
		lines = append(lines, "", renderStreamingIndicator(phase, m.chat.spinnerFrame))
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
		Pinned:          strings.TrimSpace(m.filesView.pinned),
		ToolsEnabled:    toolsEnabled,
		Streaming:       m.chat.sending,
		AgentActive:     m.agentLoop.active,
		AgentPhase:      m.agentLoop.phase,
		AgentStep:       m.agentLoop.step,
		AgentMax:        m.agentLoop.maxToolStep,
		QueuedCount:     len(m.chat.pendingQueue),
		Parked:          parked,
		PendingNotes:    m.chat.pendingNoteCount,
		ActiveTools:     m.telemetry.activeToolCount,
		ActiveSubagents: m.telemetry.activeSubagentCount,
		PlanMode:        m.ui.planMode,
		ApprovalGated:   gated,
		ApprovalPending: m.pendingApproval != nil,
		IntentLast:      intentChipLabel(m.intent),
	}
}

// intentChipLabel returns the short string the header chip shows for
// the most recent intent decision. Empty when no decision has fired or
// the layer fell back (we don't paint a chip for fallback because it
// was a no-op — surfacing "intent: fallback" on every turn would just
// be visual noise telling the user "the layer didn't do anything").
func intentChipLabel(s intentState) string {
	if s.lastDecisionAtMs == 0 || s.lastSource != "llm" {
		return ""
	}
	return s.lastIntent
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
		ToolRounds:     m.agentLoop.toolRounds,
		LastTool:       m.agentLoop.lastTool,
		LastStatus:     m.agentLoop.lastStatus,
		LastDurationMs: m.agentLoop.lastDuration,
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
		MessageCount:          len(m.chat.transcript),
		Pinned:                head.Pinned,
		CompressionSavedChars: m.telemetry.compressionSavedChars,
		CompressionRawChars:   m.telemetry.compressionRawChars,
		SpinnerFrame:          m.chat.spinnerFrame,
	}
}

// statsPanelVisible returns true when the chat tab should render the
// right-side panel alongside the chat body. Driven by the ctrl+s toggle and
// a minimum-width guard so narrow terminals don't get squeezed.
func (m Model) statsPanelVisible(contentWidth int) bool {
	return m.ui.showStatsPanel && contentWidth >= statsPanelMinContentWidth
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
	if len(m.filesView.entries) == 0 {
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
		start := m.filesView.index - half
		if start < 0 {
			start = 0
		}
		end := start + listRows
		if end > len(m.filesView.entries) {
			end = len(m.filesView.entries)
			start = end - listRows
			if start < 0 {
				start = 0
			}
		}
		for i := start; i < end; i++ {
			prefix := "  "
			label := truncateSingleLine(m.filesView.entries[i], listWidth-4)
			if m.filesView.entries[i] == strings.TrimSpace(m.filesView.pinned) {
				label = "[p] " + label
			}
			if i == m.filesView.index {
				prefix = "> "
				label = titleStyle.Render(label)
			}
			listLines = append(listLines, prefix+label)
		}
		listLines = append(listLines, "", subtleStyle.Render(fmt.Sprintf("%d/%d files", m.filesView.index+1, len(m.filesView.entries))))
	}

	previewLines := []string{
		sectionHeader("❐", "Preview"),
		subtleStyle.Render(blankFallback(m.filesView.path, "Select a file")),
		renderDivider(previewWidth - 2),
		"",
	}
	if strings.TrimSpace(m.filesView.path) != "" && m.filesView.path == strings.TrimSpace(m.filesView.pinned) {
		previewLines = append(previewLines, subtleStyle.Render("Pinned for chat context"), "")
	}
	content := truncateForPanelSized(m.filesView.preview, previewWidth, previewRows)
	if content == "" {
		content = subtleStyle.Render("No preview loaded.")
	}
	previewLines = append(previewLines, content)
	if m.filesView.size > 0 {
		previewLines = append(previewLines, "", subtleStyle.Render(fmt.Sprintf("size=%d bytes", m.filesView.size)))
	}

	left := lipgloss.NewStyle().Width(listWidth).Render(strings.Join(listLines, "\n"))
	right := lipgloss.NewStyle().Width(previewWidth).Render(strings.Join(previewLines, "\n"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right)
}


func (m Model) renderSetupView(width int) string {
	providers := m.availableProviders()
	m.setupWizard.index = clampIndex(m.setupWizard.index, len(providers))

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
			if i == m.setupWizard.index {
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
		selected := providers[m.setupWizard.index]
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
		if m.setupWizard.editing {
			draft := m.setupWizard.draft
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
	m.toolView.index = clampIndex(m.toolView.index, len(tools))

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
			if i == m.toolView.index {
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
		selected := tools[m.toolView.index]
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
		if m.toolView.editing {
			detailLines = append(detailLines,
				subtleStyle.Render("Param Editor"),
				truncateForPanel(m.toolView.draft, detailWidth),
				"",
			)
		}
		detailLines = append(detailLines, sectionHeader("✓", "Last Result"))
		resultText := strings.TrimSpace(m.toolView.output)
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
	if pinned := strings.TrimSpace(m.filesView.pinned); pinned != "" {
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

// renderHelpOverlay paints a compact reference card when m.ui.showHelpOverlay is
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

// Engine event router (handleEngineEvent), tool-chip helpers
// (pushToolChip, push/finishStreamingMessageToolChip, finishToolChip),
// payload* getters, shouldMirrorEventToTranscript, appendActivity,
// resetAgentRuntime live in engine_events.go.

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
	if strings.EqualFold(strings.TrimSpace(m.filesView.pinned), target) {
		m.filesView.pinned = ""
		m.notice = "Cleared pinned file."
		return m, nil
	}
	m.filesView.pinned = target
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
		m.chat.input = composeChatPrompt("Explain "+marker, "")
		m.notice = "Explain prompt prepared for " + rel
	case "review":
		m.chat.input = composeChatPrompt("Review "+marker+" for bugs, risks, and missing tests.", "")
		m.notice = "Review prompt prepared for " + rel
	default:
		m.chat.input = composeChatPrompt(m.chat.input, marker)
		m.notice = "Inserted file marker for " + rel
	}
	m.activeTab = 0
	return m, nil
}

func (m Model) focusChangedFiles(changed []string) Model {
	if len(changed) == 0 {
		return m
	}
	target := strings.TrimSpace(m.filesView.pinned)
	if target == "" || !containsStringFold(changed, target) {
		target = strings.TrimSpace(changed[0])
	}
	if target == "" {
		return m
	}
	for i, path := range m.filesView.entries {
		if strings.EqualFold(strings.TrimSpace(path), target) {
			m.filesView.index = i
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
		m.filesView.entries = files
	}
	if diff, err := gitWorkingDiff(root, 120_000); err == nil {
		m.patchView.diff = diff
	}
	if changed, err := gitChangedFiles(root, 12); err == nil {
		m.patchView.changed = changed
		m = m.focusChangedFiles(changed)
	}
	path = strings.TrimSpace(path)
	if path != "" {
		m.filesView.path = path
		if idx := indexOfString(m.filesView.entries, path); idx >= 0 {
			m.filesView.index = idx
		}
		if content, size, err := readProjectFile(root, path, 32_000); err == nil {
			m.filesView.preview = content
			m.filesView.size = size
		}
	}
	m.activeTab = 3
	if len(m.patchView.changed) > 0 {
		m.notice = "Tool updated workspace: " + strings.Join(m.patchView.changed, ", ")
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
	question := strings.TrimSpace(expandAtFileMentionsWithRecent(m.chat.input, m.filesView.entries, m.engineRecentFiles()))
	if pinned := strings.TrimSpace(m.filesView.pinned); pinned != "" {
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
	lowerInput := strings.ToLower(strings.TrimSpace(m.chat.input))
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
	for _, path := range m.filesView.entries {
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
	ranker := newMentionRanker(m.filesView.entries, m.engineRecentFiles())
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
	m.chat.streamCancel = nil
}

// cancelActiveStream aborts an in-flight chat stream if one is running.
// Returns true if a cancel fired — the caller uses that to decide whether
// to emit the "cancelled by user" notice vs. fall through to other esc
// behavior like dismissing the parked-resume banner. The userCancelled
// flag lets the chatErrMsg reader distinguish a clean user-driven stop
// from a provider/network error so we can tailor the message.
func (m *Model) cancelActiveStream() bool {
	if m.chat.streamCancel == nil {
		return false
	}
	m.chat.streamCancel()
	m.chat.streamCancel = nil
	m.chat.userCancelledStream = true
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
	input := m.chat.input

	pinned := strings.TrimSpace(m.filesView.pinned)
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
	if len(m.filesView.entries) == 0 {
		return ""
	}
	if m.filesView.index < 0 {
		return m.filesView.entries[0]
	}
	if m.filesView.index >= len(m.filesView.entries) {
		return m.filesView.entries[len(m.filesView.entries)-1]
	}
	return m.filesView.entries[m.filesView.index]
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
