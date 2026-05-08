package tui

// chat_commands_handlers.go — bodies for the four longest slash-command
// branches in chat_commands.go's executeChatCommand switch (/retry,
// /edit, /drive, /compact). Pulled out so the dispatcher stays close
// to a single-screen scan. Each handler returns the (model, cmd, true)
// triple the dispatcher expects, so call sites are one line.
// Companion siblings:
//
//   - chat_commands.go         executeChatCommand dispatcher + small
//                              case bodies for /help/clear/export/
//                              file/plan/code/btw/ask/chat/etc +
//                              helpers (shortRunID, toggleSelection
//                              Mode, handleQueueSlash, setSelectionMode)
//   - chat_commands_keys.go    keyboard router for the chat composer
//   - chat_commands_context.go /context show/budget/messages/drop body

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) handleRetrySlash() (tea.Model, tea.Cmd, bool) {
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
		m.notice = "No prior user message to retry — type a question first."
		return m.appendSystemMessage("/retry needs a prior user message in the transcript. Send a message first, then /retry rebuilds the assistant reply."), nil, true
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
}

func (m Model) handleEditSlash() (tea.Model, tea.Cmd, bool) {
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
		m.notice = "No prior user message to edit — type a question first."
		return m.appendSystemMessage("/edit needs a prior user message to pull back into the composer. Send a message first, then /edit lets you tweak and resend."), nil, true
	}
	prior := m.chat.transcript[lastUserIdx].Content
	m.chat.transcript = m.chat.transcript[:lastUserIdx]
	m.setChatInput(prior)
	m.chat.cursor = len([]rune(prior))
	m.chat.cursorManual = true
	m.chat.cursorInput = prior
	m.notice = "Editing last message — press enter to resend."
	return m, nil, true
}

func (m Model) handleDriveSlash(args []string) (tea.Model, tea.Cmd, bool) {
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
				return m.appendSystemMessage("/drive resume <id-or-prefix> — pass a run ID (or its 8-char prefix) to resume."), nil, true
			}
			// Accept short prefix — resolve against every persisted
			// run so the user can paste the visible chunk from
			// /drive list.
			resolved, ok, errMsg := resolveDriveRunID(args[1], m.allDriveRunIDs())
			if !ok {
				return m.appendSystemMessage("/drive resume: " + errMsg), nil, true
			}
			runID, err := runDriveResumeAsync(m.eng, resolved)
			if err != nil {
				return m.appendSystemMessage("/drive resume error: " + err.Error()), nil, true
			}
			m.notice = "Drive [" + shortRunID(runID) + "] resumed — pinned to Activity below."
			return m.appendSystemMessage("▸ Drive resume requested\n   run_id: " + runID + "\n   Progress will continue in the activity panel below."), nil, true
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
	m.notice = "Drive [" + shortRunID(runID) + "] started — pinned to Activity below."
	return m.appendSystemMessage("▸ Drive started · run_id: " + runID + "\n   Task: " + truncateForLine(task, 100) + "\n   Plan and per-TODO progress stream into the Activity panel below."), nil, true
}

func (m Model) handleCompactSlash(args []string) (tea.Model, tea.Cmd, bool) {
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
		m.notice = "Nothing to compact yet — transcript too short."
		return m.appendSystemMessage(fmt.Sprintf("Nothing to compact yet — transcript has only %d line%s. /compact starts trimming once you've got more than %d. Older history always stays in the Conversations panel.", len(m.chat.transcript), _s(len(m.chat.transcript)), keep)), nil, true
	}
	m.chat.transcript = collapsed
	m.chat.scrollback = 0
	note := fmt.Sprintf("Compacted %d older transcript lines. Full history lives in the Conversations panel.", collapsedCount)
	m.notice = fmt.Sprintf("Compacted %d lines (keep=%d).", collapsedCount, keep)
	return m.appendSystemMessage(note), nil, true
}
