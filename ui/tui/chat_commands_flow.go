package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/planning"
)

func (m Model) handleAskSlash(args []string) (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	payload := strings.TrimSpace(strings.Join(args, " "))
	if payload == "" {
		m.notice = "/ask needs a question."
		return m.appendSystemMessage("Usage: /ask <question>\nExample: /ask why is the build slow on Windows?"), nil, true
	}
	next, cmdOut := m.submitChatQuestion(payload, nil)
	return next, cmdOut, true
}

func (m Model) handleContinueSlash(args []string) (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	if m.eng == nil || !m.eng.HasParkedAgent() {
		m.notice = "Nothing to resume - no parked agent loop."
		return m.appendSystemMessage("Nothing to resume - the agent isn't paused. /continue only works after a turn pauses at its step or token budget."), nil, true
	}
	note := strings.TrimSpace(strings.Join(args, " "))
	next, cmdOut := m.startChatResume(note)
	return next, cmdOut, true
}

func (m Model) handleSplitSlash(args []string) (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	query := strings.TrimSpace(strings.Join(args, " "))
	if query == "" {
		m.notice = "/split needs a task to decompose."
		return m.appendSystemMessage("Usage: /split <task> - runs the deterministic splitter and shows the subtasks it detects so you can dispatch them individually."), nil, true
	}
	return m.appendSystemMessage(renderSplitPlan(planning.SplitTask(query))), nil, true
}

func (m Model) handleBtwSlash(args []string) (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	note := strings.TrimSpace(strings.Join(args, " "))
	if note == "" {
		m.notice = "/btw needs a note."
		return m.appendSystemMessage("Usage: /btw <note> - queued text lands as a user message at the next tool-loop step boundary."), nil, true
	}
	if m.eng == nil {
		m.notice = "/btw: engine unavailable."
		return m.appendSystemMessage("/btw: engine is not initialized."), nil, true
	}
	m.eng.QueueAgentNote(note)
	m.chat.pendingNoteCount++
	return m.appendSystemMessage("/btw queued: " + note + "\nIt will land as a user note before the next tool-loop step."), nil, true
}
