// update_stream.go — handlers for the streaming-chat lifecycle messages
// (chatDelta / chatDone / chatErr / streamClosed), plus the periodic
// ticks (spinner / heartbeat) and the engine-event subscription glue
// (eventSubscribed / engineEvent), and the approval modal request.
// Anything tied to the live-stream state machine lives here so the
// rest of update.go can stay a thin dispatcher.

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

func (m Model) handleEventSubscribedMsg(msg eventSubscribedMsg) (tea.Model, tea.Cmd) {
	if msg.ch == nil {
		return m, nil
	}
	m.eventSub = msg.ch
	return m, waitForEventMsg(msg.ch)
}

func (m Model) handleEngineEventMsg(msg engineEventMsg) (tea.Model, tea.Cmd) {
	m = m.handleEngineEvent(msg.event)
	if m.eventSub == nil {
		return m, nil
	}
	next := waitForEventMsg(m.eventSub)
	if strings.EqualFold(strings.TrimSpace(msg.event.Type), "context:built") {
		return m, tea.Batch(next, loadStatusCmd(m.eng))
	}
	return m, next
}

func (m Model) handleChatDeltaMsg(msg chatDeltaMsg) (tea.Model, tea.Cmd) {
	if m.chat.streamIndex >= 0 && m.chat.streamIndex < len(m.chat.transcript) {
		m.chat.transcript[m.chat.streamIndex].Content += msg.delta
		m.chat.transcript[m.chat.streamIndex].Preview = chatDigest(m.chat.transcript[m.chat.streamIndex].Content)
		m.refreshChatLineTokenCount(m.chat.streamIndex)
	}
	return m, waitForStreamMsg(m.chat.streamMessages)
}

func (m Model) handleSpinnerTickMsg(_ spinnerTickMsg) (tea.Model, tea.Cmd) {
	m.chat.spinnerFrame++
	if m.chat.sending || m.agentLoop.active {
		return m, spinnerTickCmd()
	}
	m.chat.spinnerTicking = false
	return m, nil
}

// handleHeartbeatTickMsg keeps session timer / elapsed chips live even
// when no events are in flight. 1Hz cadence, one int bump and a
// repaint per second — cheap.
func (m Model) handleHeartbeatTickMsg(_ heartbeatTickMsg) (tea.Model, tea.Cmd) {
	if !m.chat.sending && !m.agentLoop.active && !m.statsPanelBoostActive(time.Now()) {
		return m, nil
	}
	return m, heartbeatTickCmd()
}

func (m Model) handleChatDoneMsg(msg chatDoneMsg) (tea.Model, tea.Cmd) {
	if m.dropEmptyStreamingAssistant() {
		m.chat.streamStartedAt = time.Time{}
		m.chat.streamInputTokens = 0
		m.chat.sending = false
		m.chat.streamMessages = nil
		m.chat.streamIndex = -1
		m.clearStreamCancel()
		m.resetAgentRuntime()
		m.chat.pendingNoteCount = 0
		m.notice = ""
		next, drainCmd := m.drainPendingQueue()
		return next, tea.Batch(loadStatusCmd(m.eng), loadLatestPatchCmd(m.eng), loadGitInfoCmd(m.projectRoot()), drainCmd)
	}
	m.annotateAssistantPatch(m.chat.streamIndex)
	m.annotateAssistantToolUsage(m.chat.streamIndex)
	if m.chat.streamIndex >= 0 && m.chat.streamIndex < len(m.chat.transcript) && !m.chat.streamStartedAt.IsZero() {
		m.chat.transcript[m.chat.streamIndex].DurationMs = int(time.Since(m.chat.streamStartedAt).Milliseconds())
		if msg.usage != nil && msg.usage.OutputTokens > 0 {
			m.chat.transcript[m.chat.streamIndex].TokenCount = msg.usage.OutputTokens
		}
	}
	m.moveStreamingAssistantToTranscriptEnd()
	m.chat.streamStartedAt = time.Time{}
	m.chat.streamInputTokens = 0
	m.chat.sending = false
	m.chat.streamMessages = nil
	m.chat.streamIndex = -1
	m.clearStreamCancel()
	m.resetAgentRuntime()
	m.chat.pendingNoteCount = 0
	m.notice = ""
	next, drainCmd := m.drainPendingQueue()
	return next, tea.Batch(loadStatusCmd(m.eng), loadLatestPatchCmd(m.eng), loadGitInfoCmd(m.projectRoot()), drainCmd)
}

func (m Model) handleChatErrMsg(msg chatErrMsg) (tea.Model, tea.Cmd) {
	m.dropEmptyStreamingAssistant()
	m.chat.sending = false
	m.chat.streamMessages = nil
	m.chat.streamIndex = -1
	m.chat.streamInputTokens = 0
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
		// Mark the partial assistant line so the header surfaces a
		// ⊘ chip; without this the row is indistinguishable from a
		// clean completion at a glance.
		if m.chat.streamIndex >= 0 && m.chat.streamIndex < len(m.chat.transcript) {
			m.chat.transcript[m.chat.streamIndex].Cancelled = true
		}
		m.notice = "Turn cancelled (esc). Partial output kept in transcript; /retry reopens it."
		m = m.appendSystemMessage("◦ Turn cancelled by user — partial assistant output above, if any, is what arrived before the cancel took effect.")
		if len(m.chat.pendingQueue) > 0 {
			m.notice += fmt.Sprintf(" %d queued message(s) kept.", len(m.chat.pendingQueue))
		}
		return m, nil
	}
	m.notice = "chat: " + truncateNotice(msg.err)
	if len(m.chat.pendingQueue) > 0 {
		m.notice += fmt.Sprintf(" — %d queued message(s) kept.", len(m.chat.pendingQueue))
	}
	return m, nil
}

func (m Model) handleStreamClosedMsg(_ streamClosedMsg) (tea.Model, tea.Cmd) {
	m.dropEmptyStreamingAssistant()
	m.chat.sending = false
	m.chat.streamMessages = nil
	m.chat.streamIndex = -1
	m.chat.streamInputTokens = 0
	m.clearStreamCancel()
	m.resetAgentRuntime()
	m.chat.pendingNoteCount = 0
	next, drainCmd := m.drainPendingQueue()
	return next, drainCmd
}

func (m *Model) dropEmptyStreamingAssistant() bool {
	idx := m.chat.streamIndex
	if idx < 0 || idx >= len(m.chat.transcript) {
		return false
	}
	line := m.chat.transcript[idx]
	if line.Role != chatRoleAssistant || strings.TrimSpace(line.Content) != "" {
		return false
	}
	m.chat.transcript = append(m.chat.transcript[:idx], m.chat.transcript[idx+1:]...)
	return true
}

func (m Model) handleApprovalRequestedMsg(msg approvalRequestedMsg) (tea.Model, tea.Cmd) {
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
}
