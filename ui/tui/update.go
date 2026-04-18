// bubbletea Update reducer for the TUI Model. Extracted from tui.go to keep
// the message dispatch table close to per-msg handlers in this package
// (handleEngineEvent, handleChatKey, handleFilesKey, etc.). Update itself
// is just a router — every case falls through to a focused helper.
//
// When adding a new tea.Msg type:
//   1. Define the message type alongside the cmd that emits it
//   2. Add a `case xxxMsg:` here and call into a Model.handleX method
//   3. Update.go MUST stay a pure dispatcher — no business logic inline

package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

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
		// double-scroll. The input box (tail) stays pinned; only the
		// transcript head clips. Shift+wheel jumps a half-page so power
		// users can travel a long history quickly. Ignore the other tabs
		// (their content is static enough to fit in-panel).
		if m.tabs[m.activeTab] != "Chat" {
			return m, nil
		}
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		step := mouseWheelStep
		if msg.Shift {
			step = mouseWheelPageStep
		}
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			m.scrollTranscript(-step)
		case tea.MouseButtonWheelDown:
			m.scrollTranscript(step)
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
		m.patchView.diff = msg.diff
		m.patchView.changed = msg.changed
		if strings.TrimSpace(msg.diff) == "" {
			m.notice = "Working tree is clean."
		} else if len(msg.changed) > 0 {
			m.notice = "Changed files: " + strings.Join(msg.changed, ", ")
		}
		return m, nil

	case latestPatchLoadedMsg:
		m.patchView.latestPatch = msg.patch
		m.patchView.set = parseUnifiedDiffSections(msg.patch)
		m.patchView.files = patchSectionPaths(m.patchView.set)
		if len(m.patchView.files) == 0 {
			m.patchView.files = extractPatchedFiles(msg.patch)
		}
		m.patchView.index = m.bestPatchIndex()
		m.patchView.hunk = 0
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
		m.filesView.entries = msg.files
		if len(m.filesView.entries) == 0 {
			m.filesView.index = 0
			m.filesView.path = ""
			m.filesView.preview = ""
			m.filesView.size = 0
			m.notice = "No project files found."
			return m, nil
		}
		selected := m.selectedFile()
		nextIndex := 0
		if selected != "" {
			for i, path := range m.filesView.entries {
				if path == selected {
					nextIndex = i
					break
				}
			}
		}
		m.filesView.index = nextIndex
		return m, loadFilePreviewCmd(m.eng, m.selectedFile())

	case filePreviewLoadedMsg:
		if msg.err != nil {
			m.notice = "preview: " + msg.err.Error()
			return m, nil
		}
		m.filesView.path = msg.path
		m.filesView.preview = msg.content
		m.filesView.size = msg.size
		if strings.TrimSpace(msg.path) != "" {
			m.notice = fmt.Sprintf("Previewing %s (%d bytes)", msg.path, msg.size)
		}
		return m, nil

	case memoryLoadedMsg:
		m.memory.loading = false
		if msg.err != nil {
			m.memory.err = msg.err.Error()
			return m, nil
		}
		m.memory.err = ""
		m.memory.entries = msg.entries
		if msg.tier != "" {
			m.memory.tier = msg.tier
		}
		if m.memory.scroll >= len(m.memory.entries) {
			m.memory.scroll = 0
		}
		return m, nil

	case codemapLoadedMsg:
		m.codemap.loading = false
		m.codemap.loaded = true
		if msg.err != nil {
			m.codemap.err = msg.err.Error()
			return m, nil
		}
		m.codemap.err = ""
		m.codemap.snap = msg.snap
		if m.codemap.scroll >= codemapViewRowCount(m.codemap.view, m.codemap.snap) {
			m.codemap.scroll = 0
		}
		return m, nil

	case conversationsLoadedMsg:
		m.conversations.loading = false
		m.conversations.loaded = true
		if msg.err != nil {
			m.conversations.err = msg.err.Error()
			return m, nil
		}
		m.conversations.err = ""
		m.conversations.entries = msg.entries
		if m.conversations.scroll >= len(m.conversations.entries) {
			m.conversations.scroll = 0
		}
		return m, nil

	case conversationPreviewMsg:
		if msg.err != nil {
			m.notice = "conversations: " + msg.err.Error()
			return m, nil
		}
		m.conversations.previewID = msg.id
		m.conversations.preview = msg.msgs
		// LoadReadOnly does NOT change the active conversation — Chat
		// keeps whatever was running. The notice has to make that
		// explicit so users don't assume f1 will jump them into the
		// previewed history.
		m.notice = fmt.Sprintf("Previewed conversation %s (%d messages) — read-only; Chat keeps the current session.", msg.id, len(msg.msgs))
		return m, nil

	case promptsLoadedMsg:
		m.prompts.loading = false
		m.prompts.loaded = true
		if msg.err != nil {
			m.prompts.err = msg.err.Error()
			return m, nil
		}
		m.prompts.err = ""
		m.prompts.templates = msg.templates
		if m.prompts.scroll >= len(m.prompts.templates) {
			m.prompts.scroll = 0
		}
		return m, nil

	case securityLoadedMsg:
		m.security.loading = false
		m.security.loaded = true
		if msg.err != nil {
			m.security.err = msg.err.Error()
			return m, nil
		}
		m.security.err = ""
		m.security.report = msg.report
		m.security.scroll = 0
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
			m.toolView.output = formatToolErrorForPanel(msg.name, msg.params, msg.result, msg.err)
			if m.chat.toolPending && strings.EqualFold(strings.TrimSpace(msg.name), strings.TrimSpace(m.chat.toolName)) {
				m = m.appendSystemMessage(formatToolResultForChat(msg.name, msg.params, msg.result, msg.err))
				m.chat.toolPending = false
				m.chat.toolName = ""
			}
			if toolResultWorkspaceChanged(msg.result) {
				m = m.refreshToolMutationState("")
			}
			return m, nil
		}
		m.toolView.output = formatToolResultForPanel(msg.name, msg.params, msg.result)
		m.notice = fmt.Sprintf("Tool ran: %s (%dms)", msg.name, msg.result.DurationMs)
		if m.chat.toolPending && strings.EqualFold(strings.TrimSpace(msg.name), strings.TrimSpace(m.chat.toolName)) {
			m = m.appendSystemMessage(formatToolResultForChat(msg.name, msg.params, msg.result, nil))
			m.chat.toolPending = false
			m.chat.toolName = ""
		}
		if path := toolResultRelativePath(m.eng, msg.result); path != "" {
			m.filesView.path = path
			if idx := indexOfString(m.filesView.entries, path); idx >= 0 {
				m.filesView.index = idx
			}
			if msg.name == "read_file" {
				m.filesView.preview = msg.result.Output
				m.filesView.size = len([]byte(msg.result.Output))
			}
			if isMutationTool(msg.name) || toolResultWorkspaceChanged(msg.result) {
				m = m.refreshToolMutationState(path)
			}
		} else if isMutationTool(msg.name) || toolResultWorkspaceChanged(msg.result) {
			m = m.refreshToolMutationState("")
		}
		return m, nil

	case chatDeltaMsg:
		if m.chat.streamIndex >= 0 && m.chat.streamIndex < len(m.chat.transcript) {
			m.chat.transcript[m.chat.streamIndex].Content += msg.delta
			m.chat.transcript[m.chat.streamIndex].Preview = chatDigest(m.chat.transcript[m.chat.streamIndex].Content)
		}
		return m, waitForStreamMsg(m.chat.streamMessages)

	case spinnerTickMsg:
		m.chat.spinnerFrame++
		if m.chat.sending || m.agentLoop.active {
			return m, spinnerTickCmd()
		}
		m.chat.spinnerTicking = false
		return m, nil

	case heartbeatTickMsg:
		// 1Hz heartbeat. Keeps the session timer and elapsed chips live
		// even when no events are in flight. Cheap — one int bump and a
		// repaint per second.
		return m, heartbeatTickCmd()

	case chatDoneMsg:
		m.annotateAssistantPatch(m.chat.streamIndex)
		m.annotateAssistantToolUsage(m.chat.streamIndex)
		if m.chat.streamIndex >= 0 && m.chat.streamIndex < len(m.chat.transcript) && !m.chat.streamStartedAt.IsZero() {
			m.chat.transcript[m.chat.streamIndex].DurationMs = int(time.Since(m.chat.streamStartedAt).Milliseconds())
		}
		m.chat.streamStartedAt = time.Time{}
		m.chat.sending = false
		m.chat.streamMessages = nil
		m.chat.streamIndex = -1
		m.clearStreamCancel()
		m.resetAgentRuntime()
		m.chat.pendingNoteCount = 0
		m.notice = "" // happy-path completion narrates itself via the transcript; no need to park a banner in the footer
		next, drainCmd := m.drainPendingQueue()
		return next, tea.Batch(loadStatusCmd(m.eng), loadLatestPatchCmd(m.eng), loadGitInfoCmd(m.projectRoot()), drainCmd)

	case gitInfoLoadedMsg:
		m.gitInfo = msg.info
		return m, nil

	case chatErrMsg:
		m.chat.sending = false
		m.chat.streamMessages = nil
		m.chat.streamIndex = -1
		m.clearStreamCancel()
		m.resetAgentRuntime()
		m.chat.pendingNoteCount = 0
		// Distinguish a user-driven cancel (esc) from a real provider or
		// network error. Context cancellation that arrives without the
		// userCancelledStream flag set is still treated as an error (e.g.
		// the process context got cancelled from above) — but the common
		// flow is "user pressed esc", which deserves a calm message and a
		// transcript marker so scrolling back makes it obvious the turn
		// was aborted, not silently truncated.
		wasCancelled := m.chat.userCancelledStream || errors.Is(msg.err, context.Canceled)
		m.chat.userCancelledStream = false
		if wasCancelled {
			m.notice = "Turn cancelled (esc). Partial output kept in transcript; /retry reopens it."
			m = m.appendSystemMessage("◦ Turn cancelled by user — partial assistant output above, if any, is what arrived before the cancel took effect.")
			if len(m.chat.pendingQueue) > 0 {
				m.notice += fmt.Sprintf(" %d queued message(s) kept.", len(m.chat.pendingQueue))
			}
			return m, nil
		}
		m.notice = "chat: " + msg.err.Error()
		if len(m.chat.pendingQueue) > 0 {
			m.notice += fmt.Sprintf(" — %d queued message(s) kept.", len(m.chat.pendingQueue))
		}
		return m, nil

	case streamClosedMsg:
		m.chat.sending = false
		m.chat.streamMessages = nil
		m.chat.streamIndex = -1
		m.clearStreamCancel()
		m.resetAgentRuntime()
		m.chat.pendingNoteCount = 0
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
				m.chat.cursor = 0
				m.slashMenu.mention = 0
				m.slashMenu.command = 0
				m.slashMenu.commandArg = 0
				m.slashMenu.quickAction = 0
				m.notice = "Input cleared."
				return m, nil
			}
		case "ctrl+h":
			m.ui.showHelpOverlay = !m.ui.showHelpOverlay
			return m, nil
		case "ctrl+s":
			m.ui.showStatsPanel = !m.ui.showStatsPanel
			return m, nil
		case "ctrl+p":
			m.activeTab = 0
			m.setChatInput("/")
			m.slashMenu.command = 0
			m.slashMenu.commandArg = 0
			m.slashMenu.mention = 0
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
			if m.memory.entries == nil && !m.memory.loading {
				m.memory.loading = true
				return m, loadMemoryCmd(m.eng, m.memory.tier)
			}
			return m, nil
		case "f9", "alt+9":
			m.activeTab = 8
			if !m.codemap.loaded && !m.codemap.loading {
				m.codemap.loading = true
				return m, loadCodemapCmd(m.eng)
			}
			return m, nil
		case "f10", "alt+0":
			m.activeTab = 9
			if !m.conversations.loaded && !m.conversations.loading {
				m.conversations.loading = true
				return m, loadConversationsCmd(m.eng)
			}
			return m, nil
		case "f11", "alt+t":
			m.activeTab = 10
			if !m.prompts.loaded && !m.prompts.loading {
				m.prompts.loading = true
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
			if len(m.providers.rows) == 0 && m.providers.err == "" {
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
				return m, applyPatchCmd(m.eng, m.patchView.latestPatch, true)
			case "a", "alt+a":
				return m, applyPatchCmd(m.eng, m.patchView.latestPatch, false)
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
