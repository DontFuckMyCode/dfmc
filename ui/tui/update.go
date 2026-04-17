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
