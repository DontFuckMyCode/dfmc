package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/engine"
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
	// Mouse capture is OFF by default so terminal drag-to-select / copy
	// just works. Users who prefer wheel-scroll can flip tui.mouse_capture
	// in their config — the TUI will read it below and enable cell-motion
	// tracking. A runtime toggle (/mouse) lets you switch mid-session
	// without restarting.
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
		path := paramStr(params, "path")
		if path == "" {
			return ""
		}
		parts := []string{"/read", formatSlashArgToken(path)}
		if start := paramStr(params, "line_start"); start != "" {
			parts = append(parts, start)
		}
		if end := paramStr(params, "line_end"); end != "" {
			parts = append(parts, end)
		}
		return strings.Join(parts, " ")
	case "list_dir":
		path := paramStr(params, "path")
		if path == "" {
			path = "."
		}
		parts := []string{"/ls", formatSlashArgToken(path)}
		if recursive, ok := params["recursive"].(bool); ok && recursive {
			parts = append(parts, "--recursive")
		}
		if maxEntries := paramStr(params, "max_entries"); maxEntries != "" {
			parts = append(parts, "--max", maxEntries)
		}
		return strings.Join(parts, " ")
	case "grep_codebase":
		pattern := paramStr(params, "pattern")
		if pattern == "" {
			return ""
		}
		return "/grep " + formatSlashArgToken(pattern)
	case "run_command":
		command := paramStr(params, "command")
		if command == "" {
			return ""
		}
		args := paramStr(params, "args")
		if args == "" {
			return "/run " + command
		}
		// H3: tokens with whitespace must be quoted so `/run cmd "arg with spaces"`
		// survives the slash-handler's whitespace tokenizer. Without this,
		// `git commit -m "fix bug"` gets split on every space and the model's
		// suggested command becomes nonsense.
		return "/run " + command + " " + formatRunArgList(args)
	default:
		return ""
	}
}

// formatRunArgList walks the args string token-by-token and re-quotes any
// whitespace-bearing piece using formatSlashArgToken. Bare alphanumeric
// flags like `-m` pass through untouched. Pre-fix the args string was
// concatenated raw, so any quoted argument the underlying tool spec
// contained (e.g. `git commit -m "fix"`) would be re-tokenized by the
// slash dispatcher and lose its quoting — the H3 review caught this.
func formatRunArgList(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return ""
	}
	tokens, err := splitRespectingQuotes(args)
	if err != nil {
		return formatSlashArgToken(args)
	}
	formatted := make([]string, len(tokens))
	for i, tok := range tokens {
		formatted[i] = formatSlashArgToken(tok)
	}
	return strings.Join(formatted, " ")
}

// splitRespectingQuotes splits on whitespace but keeps quoted segments
// (single or double) atomic. Backslash escapes the next char inside the
// quote. Tokens are returned without surrounding quotes; the formatter
// re-applies them only when the token contains whitespace.
func splitRespectingQuotes(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i++
				continue
			}
			if c == quote {
				quote = 0
				continue
			}
			cur.WriteByte(c)
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == ' ' || c == '\t' {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteByte(c)
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quoted value")
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out, nil
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
	m.chat.transcript = append(m.chat.transcript, newChatLine(chatRoleSystem, strings.TrimSpace(text)))
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
	m.chat.transcript = append(m.chat.transcript, newChatLine(chatRoleTool, strings.TrimSpace(text)))
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
func (m Model) appendCoachMessage(text string, severity coachSeverity, origin string, action string) Model {
	text = strings.TrimSpace(text)
	if text == "" {
		return m
	}
	marker := ""
	switch severity {
	case coachSeverityWarn:
		marker = warnStyle.Render("⚠") + " "
	case coachSeverityCelebrate:
		marker = okStyle.Render("✓") + " "
	}
	body := marker + text
	if action = strings.TrimSpace(action); action != "" {
		body += "\n" + subtleStyle.Render("Suggested: ") + action
	}
	if origin = strings.TrimSpace(origin); origin != "" {
		body += " " + subtleStyle.Render("["+origin+"]")
	}
	m.chat.transcript = append(m.chat.transcript, newChatLine(chatRoleCoach, body))
	m.chat.scrollback = 0
	m.appendActivity("coach: " + text)
	if action != "" {
		m.notice = text + " | Suggested: " + action
	} else {
		m.notice = text
	}
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
		m.chat.transcript = append(m.chat.transcript, newChatLine(chatRoleUser, question))
		m = m.appendSystemMessage("Auto action: " + selected.Reason)
		m = m.startChatToolCommand(selected.Tool, selected.Params)
		return m, runToolCmd(m.ctx, m.eng, selected.Tool, selected.Params)
	}
	if name, params, reason, ok := m.autoToolIntentFromQuestion(question); ok {
		m.chat.transcript = append(m.chat.transcript, newChatLine(chatRoleUser, question))
		m = m.appendSystemMessage("Auto action: " + reason)
		m = m.startChatToolCommand(name, params)
		return m, runToolCmd(m.ctx, m.eng, name, params)
	}
	m.chat.transcript = append(m.chat.transcript,
		newChatLine(chatRoleUser, question),
		newChatLine(chatRoleAssistant, ""),
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
	m.chat.transcript = append(m.chat.transcript, newChatLine(chatRoleAssistant, ""))
	m.chat.streamIndex = len(m.chat.transcript) - 1
	m.chat.sending = true
	m.chat.streamStartedAt = time.Now()
	m.notice = "Resuming agent loop..."
	m.chat.streamMessages = startChatResumeStream(m.ctx, m.eng, note)
	return m, tea.Batch(waitForStreamMsg(m.chat.streamMessages), m.ensureSpinnerTick())
}

// newChatLine constructs a chatLine with a typed role. Pre-fix the role
// was a bare string and call sites used literals like "system" / "user"
// — a typo ("asistant") compiled clean and silently routed to the wrong
// renderer branch. Forcing chatRole here means every call site goes
// through one of the chatRole* constants and the compiler catches typos.
func newChatLine(role chatRole, content string) chatLine {
	return chatLine{
		Role:      role,
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
	warnings := toolResultWarnings(name, res)
	out := strings.TrimSpace(res.Output)
	if out == "" && len(warnings) == 0 {
		return summary
	}
	parts := []string{summary}
	if len(warnings) > 0 {
		parts = append(parts, strings.Join(warnings, "\n"))
	}
	if out != "" {
		parts = append(parts, truncateCommandBlock(out, 1200))
	}
	return strings.Join(parts, "\n")
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
// defaultModelForProvider, loadDriveRoutingFromProjectConfig,
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
		return m, runToolCmd(m.ctx, m.eng, name, params)
	}
	return m, nil
}

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
