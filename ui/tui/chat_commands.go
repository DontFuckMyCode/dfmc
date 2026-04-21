// Slash-command dispatcher for the chat panel. Extracted from tui.go to keep
// the model file focused on layout/state. executeChatCommand is the single
// entry point: it parses the raw line, routes to the matching handler
// (/help, /clear, /branch, /provider, /quit, /tools, /agent, /plan, …),
// and returns (next model, optional cmd, handled-bool). Handlers may both
// mutate the transcript and queue a tea.Cmd (e.g. quit, restart engine).
//
// Adding a new slash command:
//   1. Add it to the switch on `cmd` below
//   2. Wire the help/catalog entry in describe.go (slashCommandCatalog)
//   3. If it has args, extend the parseChatCommandInput contract
//
// All command handlers must return (Model, tea.Cmd, true). Returning false
// signals "not a slash command" — only used by the very first guard.

package tui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/planning"
)

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
		m.chat.input = ""
		if len(args) > 0 {
			return m.appendSystemMessage(renderTUICommandHelp(args[0])), nil, true
		}
		return m.appendSystemMessage(renderTUIHelp()), nil, true
	case "quit", "exit", "q":
		m.chat.input = ""
		m.notice = "Goodbye."
		return m.appendSystemMessage("Exiting DFMC — goodbye."), tea.Quit, true
	case "clear":
		m.chat.input = ""
		m.chat.transcript = nil
		m.chat.scrollback = 0
		m.notice = "Transcript cleared."
		return m.appendSystemMessage("Transcript cleared. Memory and conversation history are untouched."), nil, true
	case "export", "save":
		// Dump the current transcript to a markdown file under the project
		// root (or to the path given as /export path.md). Writes locally,
		// no network, no engine state touched — purely a view-layer save
		// for users who want to share a session out of DFMC.
		m.chat.input = ""
		if len(m.chat.transcript) == 0 {
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
		return m.appendSystemMessage("▸ Transcript exported → " + path + " (" + fmt.Sprintf("%d lines", len(m.chat.transcript)) + ")."), nil, true
	case "retry":
		// Regenerate the most recent assistant reply by resending the latest
		// user message. Trailing assistant/tool/system lines after that user
		// turn are dropped — the resend reopens that turn, it doesn't append
		// a fresh one. If nothing to retry, tell the user rather than
		// silently doing nothing.
		m.chat.input = ""
		if m.chat.sending {
			m.notice = "Cannot /retry while a turn is already streaming."
			return m.appendSystemMessage("A turn is already streaming — press esc to cancel it first, then /retry."), nil, true
		}
		lastUser := -1
		for i := len(m.chat.transcript) - 1; i >= 0; i-- {
			if m.chat.transcript[i].Role.Eq(chatRoleUser) {
				lastUser = i
				break
			}
		}
		if lastUser < 0 {
			m.notice = "Nothing to retry yet."
			return m.appendSystemMessage("No prior user message in this transcript to retry. Type a question first."), nil, true
		}
		question := strings.TrimSpace(m.chat.transcript[lastUser].Content)
		if question == "" {
			m.notice = "Last user message was empty."
			return m.appendSystemMessage("The last user message was empty; nothing to regenerate."), nil, true
		}
		// Drop the previous user turn and everything after — submitChatQuestion
		// re-appends the user line plus a fresh assistant placeholder. Retries
		// that left the old reply visible confused users into thinking they'd
		// accidentally double-sent.
		m.chat.transcript = m.chat.transcript[:lastUser]
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
		if m.chat.sending {
			m.notice = "Cannot /edit while a turn is already streaming."
			return m.appendSystemMessage("A turn is already streaming — press esc to cancel it first, then /edit."), nil, true
		}
		lastUserIdx := -1
		for i := len(m.chat.transcript) - 1; i >= 0; i-- {
			if m.chat.transcript[i].Role.Eq(chatRoleUser) {
				lastUserIdx = i
				break
			}
		}
		if lastUserIdx < 0 {
			m.chat.input = ""
			m.notice = "Nothing to edit yet."
			return m.appendSystemMessage("No prior user message to edit. Type a question first."), nil, true
		}
		prior := m.chat.transcript[lastUserIdx].Content
		m.chat.transcript = m.chat.transcript[:lastUserIdx]
		m.setChatInput(prior)
		m.chat.cursor = len([]rune(prior))
		m.chat.cursorManual = true
		m.chat.cursorInput = prior
		m.notice = "Editing last message — press enter to resend."
		return m, nil, true
	case "file", "files":
		// Slash-command fallback for the @ mention picker. Same trick as
		// Ctrl+T: insert a leading "@" so the existing mention-picker
		// machinery takes over. Particularly useful for users whose
		// keyboard layout makes Ctrl+T awkward too.
		m.chat.input = ""
		if m.chat.sending {
			m.notice = "Cannot open file picker while a turn is streaming."
			return m.appendSystemMessage("A turn is streaming — esc to cancel first."), nil, true
		}
		m.setChatInput("@")
		m.slashMenu.mention = 0
		m.notice = "File picker open — type to filter, tab/enter inserts, esc cancels."
		if len(m.filesView.entries) == 0 && m.eng != nil {
			return m, loadFilesCmd(m.eng), true
		}
		return m, nil, true
	case "plan":
		// Enter plan mode — agent runs read-only, proposes changes as a
		// plan for the user to approve. Complements /retry and /edit:
		// users who want to survey before mutating finally get a first-
		// class switch instead of relying on prompt discipline.
		m.chat.input = ""
		if m.ui.planMode {
			m.notice = "Already in plan mode — type your question, or /code to exit."
			return m.appendSystemMessage("Plan mode is already ON. Your next prompt will investigate without modifying files. Use /code to exit."), nil, true
		}
		m.ui.planMode = true
		m.notice = "Plan mode ON — investigate-only, no file writes. /code exits."
		return m.appendSystemMessage("▸ Plan mode ON. The agent will investigate with read-only tools (read_file, grep_codebase, ast_query, list_dir, glob, git_status, git_diff) and propose changes as a plan. Type /code to exit plan mode when you're ready to apply."), nil, true
	case "code":
		// Exit plan mode — subsequent prompts are free to mutate.
		m.chat.input = ""
		if !m.ui.planMode {
			m.notice = "Already in code mode — plan mode was not active."
			return m.appendSystemMessage("Not in plan mode. Prompts already allow file modifications."), nil, true
		}
		m.ui.planMode = false
		m.notice = "Plan mode OFF — prompts can now modify files."
		return m.appendSystemMessage("▸ Plan mode OFF. Write/update prompts will now route through mutating tools (apply_patch, edit_file, write_file)."), nil, true
	case "drive":
		// `/drive` is overloaded: plain task starts a new run, but
		// subcommands `stop`, `list`, `active`, `resume` mirror the
		// CLI surface so the user doesn't need to leave the TUI to
		// manage runs. Dispatched on args[0]; everything else is
		// treated as the task body.
		m.chat.input = ""
		if m.eng == nil {
			return m.appendSystemMessage("/drive: engine is not initialized."), nil, true
		}
		if len(args) > 0 {
			switch strings.ToLower(args[0]) {
			case "stop", "cancel":
				return m.handleDriveStopSlash(args[1:])
			case "active":
				return m.handleDriveActiveSlash()
			case "list":
				return m.handleDriveListSlash()
			case "resume":
				if len(args) < 2 {
					return m.appendSystemMessage("/drive resume <id> — pass a run ID to resume."), nil, true
				}
				runID, err := runDriveResumeAsync(m.eng, args[1])
				if err != nil {
					return m.appendSystemMessage("/drive resume error: " + err.Error()), nil, true
				}
				m.notice = "Drive resumed — watch the activity panel for resumed TODO progress."
				return m.appendSystemMessage("▸ Drive resume requested: " + runID + "\n   Progress will continue in the activity panel below."), nil, true
			}
		}
		task := strings.TrimSpace(strings.Join(args, " "))
		if task == "" {
			return m.appendSystemMessage("/drive usage:\n" +
				"  /drive <task>          — start a new run\n" +
				"  /drive stop [id]       — cancel an active run\n" +
				"  /drive active          — list runs currently running in this process\n" +
				"  /drive list            — list every persisted run\n" +
				"  /drive resume <id>     — resume a stopped run"), nil, true
		}
		runID, err := runDriveAsync(m.eng, task, m.workflow.routingDraft)
		if err != nil {
			return m.appendSystemMessage("/drive error: " + err.Error()), nil, true
		}
		m.notice = "Drive started — watch the activity panel for plan + per-TODO progress."
		return m.appendSystemMessage("▸ Drive started: " + truncateForLine(task, 100) + "\n   run_id: " + runID + "\n   Plan and per-TODO progress stream into the activity panel below."), nil, true
	case "compact":
		// Collapse older transcript entries into a single summary line so
		// long sessions stay scannable. Purely a view-layer operation —
		// engine memory, conversation history, and in-loop provider
		// messages are untouched. Runs offline (no LLM call).
		//
		// Default keeps the most recent 6 lines (configurable: /compact 4).
		// A single system line replaces the older tail with counts + a
		// pointer to the Conversations panel for full-fidelity recall.
		m.chat.input = ""
		if m.chat.sending {
			m.notice = "Cannot /compact while a turn is streaming."
			return m.appendSystemMessage("A turn is streaming — press esc to cancel it first, then /compact."), nil, true
		}
		keep := 6
		if len(args) > 0 {
			if n, err := strconv.Atoi(strings.TrimSpace(args[0])); err == nil && n > 0 && n < 200 {
				keep = n
			}
		}
		collapsed, collapsedCount, ok := compactTranscript(m.chat.transcript, keep)
		if !ok {
			m.notice = "Nothing to compact yet."
			return m.appendSystemMessage(fmt.Sprintf("Transcript has %d lines — below keep=%d, nothing to compact.", len(m.chat.transcript), keep)), nil, true
		}
		m.chat.transcript = collapsed
		m.chat.scrollback = 0
		note := fmt.Sprintf("Compacted %d older transcript lines. Full history lives in the Conversations panel.", collapsedCount)
		m.notice = fmt.Sprintf("Compacted %d lines (keep=%d).", collapsedCount, keep)
		return m.appendSystemMessage(note), nil, true
	case "approve", "approvals", "permissions":
		// Surface the tool-approval gate configuration: which tools are
		// gated, whether an approver is registered, whether a prompt is
		// currently pending. Read-only — editing the gate requires a
		// config change (opt-in by design; we don't want runtime slash
		// commands silently widening the attack surface).
		m.chat.input = ""
		m.notice = "Approval gate state shown below."
		return m.appendSystemMessage(m.describeApprovalGate()), nil, true
	case "hooks":
		// List every lifecycle hook registered with the dispatcher —
		// event → name(condition) command. Counterpart to /approve for
		// the other half of the tool-lifecycle surface.
		m.chat.input = ""
		m.notice = "Lifecycle hooks listed below."
		return m.appendSystemMessage(m.describeHooks()), nil, true
	case "stats", "tokens", "cost":
		// Session metrics at a glance: tool rounds, RTK-style compression
		// savings, context-window fill, agent loop progress. This makes
		// the 'token miser' thesis tangible — users should be able to
		// see how much they're saving, not just trust the claim.
		m.chat.input = ""
		m.notice = "Session stats below."
		return m.appendSystemMessage(m.describeStats()), nil, true
	case "workflow":
		m.chat.input = ""
		m.notice = "Workflow snapshot below."
		return m.appendSystemMessage(m.describeWorkflow()), nil, true
	case "todos", "todo":
		m.chat.input = ""
		m.notice = "Shared todo list below."
		return m.appendSystemMessage(m.describeTodos()), nil, true
	case "subagents", "workers":
		m.chat.input = ""
		m.notice = "Subagent activity below."
		return m.appendSystemMessage(m.describeSubagents()), nil, true
	case "queue":
		m.chat.input = ""
		return m.handleQueueSlash(args)
	case "keylog":
		// Toggle key-event dump into m.notice. Used to diagnose Turkish-
		// keyboard AltGr delivery and similar terminal-specific weirdness
		// without needing a side logfile.
		m.chat.input = ""
		m.ui.keyLogEnabled = !m.ui.keyLogEnabled
		state := "off"
		if m.ui.keyLogEnabled {
			state = "on — press any key and read the footer"
		}
		m.notice = "Key log " + state
		return m.appendSystemMessage("Key event dump is " + state + ". Toggle again with /keylog."), nil, true
	case "coach":
		m.chat.input = ""
		m.ui.coachMuted = !m.ui.coachMuted
		state := "on"
		if m.ui.coachMuted {
			state = "muted"
		}
		m.notice = "Coach " + state + "."
		return m.appendSystemMessage("Coach notes are now " + state + " for this session. Toggle again with /coach."), nil, true
	case "hints":
		m.chat.input = ""
		m.ui.hintsVerbose = !m.ui.hintsVerbose
		state := "hidden"
		if m.ui.hintsVerbose {
			state = "visible"
		}
		m.notice = "Trajectory hints " + state + "."
		return m.appendSystemMessage("Trajectory coach hints between rounds are now " + state + ". Toggle again with /hints."), nil, true
	case "intent":
		// /intent has three sub-commands:
		//   /intent           — toggle verbose (transcript pairs of raw → enriched)
		//   /intent show      — print the most recent decision in full
		//   /intent verbose   — alias of bare /intent
		m.chat.input = ""
		sub := ""
		if len(args) > 0 {
			sub = strings.ToLower(strings.TrimSpace(args[0]))
		}
		if sub == "show" {
			return m.appendSystemMessage(m.describeLastIntent()), nil, true
		}
		m.intent.verbose = !m.intent.verbose
		state := "hidden"
		if m.intent.verbose {
			state = "visible"
		}
		m.notice = "Intent rewrites " + state + "."
		return m.appendSystemMessage("Intent layer rewrites are now " + state + " in the transcript. /intent show prints the last decision in full."), nil, true
	case "copy", "yank":
		m.chat.input = ""
		return m.handleCopySlash(args)
	case "mouse":
		// Toggle bubbletea's mouse-event capture at runtime. With
		// capture ON the wheel scrolls the transcript natively but
		// terminal drag-to-select / right-click-copy is disabled. With
		// capture OFF you get the terminal's native selection — most
		// terminals also let Shift+drag bypass capture when it's on.
		m.chat.input = ""
		var cmd tea.Cmd
		m.ui.selectionModeActive = false
		if m.ui.mouseCaptureEnabled {
			m.ui.mouseCaptureEnabled = false
			cmd = tea.DisableMouse
			m.notice = "Mouse capture off — drag to select / copy text directly."
		} else {
			m.ui.mouseCaptureEnabled = true
			cmd = tea.EnableMouseCellMotion
			m.notice = "Mouse capture on — wheel scrolls transcript. Shift+drag bypasses capture in most terminals."
		}
		return m.appendSystemMessage("Mouse capture toggled. /mouse to flip again; set tui.mouse_capture in .dfmc/config.yaml for the default."), cmd, true
	case "select":
		m.chat.input = ""
		return m.toggleSelectionMode()
	case "status":
		m.chat.input = ""
		return m.appendSystemMessage(m.statusCommandSummary()), loadStatusCmd(m.eng), true
	case "reload":
		m.chat.input = ""
		if err := m.reloadEngineConfig(); err != nil {
			m.notice = "reload: " + err.Error()
			return m.appendSystemMessage("Runtime reload failed: " + err.Error()), nil, true
		}
		st := m.status
		return m.appendSystemMessage(fmt.Sprintf("Runtime reloaded.\nProvider/Model: %s / %s", blankFallback(st.Provider, "-"), blankFallback(st.Model, "-"))), loadStatusCmd(m.eng), true
	case "context":
		m.chat.input = ""
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
		// Two modes:
		//   /tools             — toggle the per-message tool-call strip
		//                        between the one-line summary (default)
		//                        and the full chip breakdown. The user
		//                        explicitly asked for the strip to be
		//                        collapsed by default so long answers
		//                        aren't drowned in tool noise.
		//   /tools list        — print the registered backend tool catalog
		//                        (the previous bare-/tools behaviour).
		m.chat.input = ""
		sub := ""
		if len(args) > 0 {
			sub = strings.ToLower(strings.TrimSpace(args[0]))
		}
		if sub == "list" || sub == "ls" || sub == "show" {
			tools := m.availableTools()
			if len(tools) == 0 {
				return m.appendSystemMessage("No tools registered."), nil, true
			}
			return m.appendSystemMessage(m.describeToolsList(tools)), nil, true
		}
		m.ui.toolStripExpanded = !m.ui.toolStripExpanded
		state := "collapsed (one-line summary)"
		if m.ui.toolStripExpanded {
			state = "expanded (full chip breakdown)"
		}
		m.notice = "Tool strip " + state + "."
		return m.appendSystemMessage("Tool-call strip is now " + state + ". Toggle again with /tools, or `/tools list` for the registered catalog."), nil, true
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
			m.chat.input = ""
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
		return m.startChatToolCommand(name, params), runToolCmd(m.ctx, m.eng, name, params), true
	case "ls":
		params, err := parseListDirChatArgs(args)
		if err != nil {
			return m.appendSystemMessage("Usage: /ls [PATH] [-r|--recursive] [--max N]"), nil, true
		}
		return m.startChatToolCommand("list_dir", params), runToolCmd(m.ctx, m.eng, "list_dir", params), true
	case "read":
		if len(args) == 0 {
			m = m.startCommandPicker("read", "", false)
			return m, nil, true
		}
		if target := strings.TrimSpace(args[0]); target != "" && !m.projectHasFile(target) && !containsStringFold(m.filesView.entries, target) {
			m = m.startCommandPicker("read", target, false)
			return m, nil, true
		}
		params, err := parseReadFileChatArgs(args)
		if err != nil {
			return m.appendSystemMessage("Usage: /read PATH [LINE_START] [LINE_END]"), nil, true
		}
		return m.startChatToolCommand("read_file", params), runToolCmd(m.ctx, m.eng, "read_file", params), true
	case "grep":
		if len(args) == 0 {
			m = m.startCommandPicker("grep", "", false)
			return m, nil, true
		}
		params, err := parseGrepChatArgs(args)
		if err != nil {
			return m.appendSystemMessage("Usage: /grep PATTERN"), nil, true
		}
		return m.startChatToolCommand("grep_codebase", params), runToolCmd(m.ctx, m.eng, "grep_codebase", params), true
	case "run":
		if len(args) == 0 {
			m = m.startCommandPicker("run", "", false)
			return m, nil, true
		}
		params, err := parseRunCommandChatArgs(args)
		if err != nil {
			return m.appendSystemMessage("Usage: /run COMMAND [ARGS...]"), nil, true
		}
		return m.startChatToolCommand("run_command", params), runToolCmd(m.ctx, m.eng, "run_command", params), true
	case "diff":
		m.chat.input = ""
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
		m.chat.input = ""
		if strings.TrimSpace(m.patchView.latestPatch) == "" {
			return m.appendSystemMessage("No assistant patch available."), nil, true
		}
		return m.appendSystemMessage(m.patchCommandSummary()), nil, true
	case "undo":
		m.chat.input = ""
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
		m.chat.input = ""
		checkOnly := false
		for _, arg := range args {
			if strings.EqualFold(strings.TrimSpace(arg), "--check") {
				checkOnly = true
			}
		}
		if strings.TrimSpace(m.patchView.latestPatch) == "" {
			return m.appendSystemMessage("No assistant patch available."), nil, true
		}
		root := "."
		if m.eng != nil {
			root = strings.TrimSpace(m.eng.Status().ProjectRoot)
			if root == "" {
				root = "."
			}
		}
		if err := applyUnifiedDiff(root, m.patchView.latestPatch, checkOnly); err != nil {
			m.notice = "patch: " + err.Error()
			return m.appendSystemMessage("Patch error: " + err.Error()), nil, true
		}
		if checkOnly {
			m.notice = "Patch check passed."
			return m.appendSystemMessage("Patch check passed."), nil, true
		}
		changed, err := gitChangedFiles(root, 12)
		if err == nil {
			m.patchView.changed = changed
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
		m.chat.input = ""
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
		m.chat.input = ""
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
		m.chat.input = ""
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
		m.chat.input = ""
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
		m.chat.input = ""
		payload := strings.TrimSpace(strings.Join(args, " "))
		if payload == "" {
			m.notice = "/ask needs a question."
			return m.appendSystemMessage("Usage: /ask <question>"), nil, true
		}
		next, cmdOut := m.submitChatQuestion(payload, nil)
		return next, cmdOut, true
	case "chat":
		m.chat.input = ""
		m.notice = "Already in chat. Just type your message."
		return m.appendSystemMessage("You're already in the chat tab — type your message and press enter."), nil, true
	case "continue", "resume":
		m.chat.input = ""
		if m.eng == nil || !m.eng.HasParkedAgent() {
			m.notice = "Nothing to resume — no parked agent loop."
			return m.appendSystemMessage("No parked agent loop. /continue only works after the loop pauses at its step cap."), nil, true
		}
		note := strings.TrimSpace(strings.Join(args, " "))
		next, cmdOut := m.startChatResume(note)
		return next, cmdOut, true
	case "split":
		m.chat.input = ""
		query := strings.TrimSpace(strings.Join(args, " "))
		if query == "" {
			m.notice = "/split needs a task to decompose."
			return m.appendSystemMessage("Usage: /split <task> — runs the deterministic splitter and shows the subtasks it detects so you can dispatch them individually."), nil, true
		}
		return m.appendSystemMessage(renderSplitPlan(planning.SplitTask(query))), nil, true
	case "btw":
		m.chat.input = ""
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
		m.chat.pendingNoteCount++
		return m.appendSystemMessage("/btw queued: " + note + "\nIt will land as a user note before the next tool-loop step."), nil, true
	case "review", "explain", "refactor", "test", "doc":
		return m.runTemplateSlash(cmd, args, raw)
	case "analyze":
		m.chat.input = ""
		return m.runAnalyzeSlash(args, false), nil, true
	case "scan":
		m.chat.input = ""
		return m.runAnalyzeSlash(args, true), nil, true
	case "map":
		m.chat.input = ""
		return m.appendSystemMessage(m.codemapSummary()), nil, true
	case "version":
		m.chat.input = ""
		return m.appendSystemMessage(m.versionSummary()), nil, true
	case "doctor", "health":
		// Lightweight health snapshot that covers provider readiness, AST
		// backend, approval gate, hooks, and recent denials in one card.
		// Full `dfmc doctor` does network checks and --fix; this is the
		// in-chat version so users can sanity-check without leaving TUI.
		m.chat.input = ""
		return m.appendSystemMessage(m.describeHealth()), loadStatusCmd(m.eng), true
	case "magicdoc", "magic":
		m.chat.input = ""
		return m.appendSystemMessage(m.magicDocSlash(args)), nil, true
	case "conversation", "conv":
		m.chat.input = ""
		return m.appendSystemMessage(m.conversationSlash(args)), nil, true
	case "memory":
		m.chat.input = ""
		return m.appendSystemMessage(m.memorySlash(args)), nil, true
	case "prompt":
		m.chat.input = ""
		return m.appendSystemMessage(m.promptSlash(args)), nil, true
	case "skill":
		m.chat.input = ""
		return m.appendSystemMessage(m.skillSlash(args)), nil, true
	case "init", "completion", "man", "serve", "remote", "plugin", "config",
		"debug", "generate", "onboard", "audit", "mcp", "update", "tui":
		// CLI-only commands — surface a friendly pointer instead of
		// the generic "Unknown command" fallback.
		m.chat.input = ""
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

func (m Model) toggleSelectionMode() (tea.Model, tea.Cmd, bool) {
	next, cmd := m.setSelectionMode(!m.ui.selectionModeActive)
	return next, cmd, true
}

func (m Model) handleQueueSlash(args []string) (tea.Model, tea.Cmd, bool) {
	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch sub {
	case "", "show", "list", "ls":
		m.notice = fmt.Sprintf("Queued messages: %d", len(m.chat.pendingQueue))
		return m.appendSystemMessage(m.describePendingQueue()), nil, true
	case "clear":
		count := len(m.chat.pendingQueue)
		m.chat.pendingQueue = nil
		m.notice = fmt.Sprintf("Queue cleared (%d removed).", count)
		return m.appendSystemMessage(fmt.Sprintf("Cleared %d queued message(s).", count)), nil, true
	case "drop", "rm", "remove", "del":
		if len(args) < 2 {
			return m.appendSystemMessage("Usage: /queue drop <index>"), nil, true
		}
		idx, err := strconv.Atoi(strings.TrimSpace(args[1]))
		if err != nil || idx < 1 || idx > len(m.chat.pendingQueue) {
			return m.appendSystemMessage(fmt.Sprintf("Queue index out of range. Use /queue to inspect the %d queued message(s).", len(m.chat.pendingQueue))), nil, true
		}
		removed := m.chat.pendingQueue[idx-1]
		m.chat.pendingQueue = append(m.chat.pendingQueue[:idx-1], m.chat.pendingQueue[idx:]...)
		m.notice = fmt.Sprintf("Dropped queued #%d.", idx)
		return m.appendSystemMessage(fmt.Sprintf("Dropped queued #%d: %s", idx, removed)), nil, true
	default:
		return m.appendSystemMessage("Usage: /queue [show|clear|drop N]"), nil, true
	}
}

func (m Model) setSelectionMode(active bool) (Model, tea.Cmd) {
	m.activeTab = 0
	if active {
		if m.ui.selectionModeActive {
			return m, nil
		}
		m.ui.selectionModeActive = true
		m.ui.selectionRestoreStats = m.ui.showStatsPanel
		m.ui.selectionRestoreMouse = m.ui.mouseCaptureEnabled
		m.ui.showStatsPanel = false
		m.ui.mouseCaptureEnabled = false
		m.notice = "Selection mode on — chat-only width, drag to select with terminal."
		return m.appendSystemMessage("Selection mode ON. Stats are hidden and mouse capture is off so terminal drag-select stays focused on the chat column. Use /select or alt+x again to restore the previous layout. Drag-scroll while selecting depends on your terminal."), tea.DisableMouse
	}
	prevStats := m.ui.selectionRestoreStats
	prevMouse := m.ui.selectionRestoreMouse
	m.ui.selectionModeActive = false
	m.ui.selectionRestoreStats = false
	m.ui.selectionRestoreMouse = false
	m.ui.showStatsPanel = prevStats
	m.ui.mouseCaptureEnabled = prevMouse
	m.notice = "Selection mode off — restored previous layout."
	var cmd tea.Cmd
	if prevMouse {
		cmd = tea.EnableMouseCellMotion
	} else {
		cmd = tea.DisableMouse
	}
	return m.appendSystemMessage("Selection mode OFF. Restored the previous stats-panel and mouse-capture state."), cmd
}
