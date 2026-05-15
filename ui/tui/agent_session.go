package tui

// agent_session.go — Multi-agent session TUI integration.
//
// This file adds multi-agent session awareness to the TUI. Each agent has
// its own isolated conversation view, accessible via Ctrl+Alt+N switch.
//
// Key integration points:
//   - Model.session: *sessionUI (added in tui.go)
//   - update.go: handles Ctrl+Alt+N key events, agent switch overlay
//   - render_layout.go: renders agent status bar and agent switcher overlay
//   - chat_state.go: per-agent transcript (chatState per agent, keyed by AgentID)
//
// Bridge to internal/session package:
//   - EngineProvider implementation wires session agents to the real Engine
//   - Session.Attention() → SharedAttention → TUI overlay for waiting_user_input

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/dontfuckmycode/dfmc/internal/session"
)

// sessionUI holds TUI-specific state for the multi-agent session.
// It wraps a *session.Session (set during Phase 4 engine wiring) to
// expose session-level methods for the TUI.
type sessionUI struct {
	// The underlying session. Set via AttachToSession in Phase 4.
	s *session.Session

	// activeAgent is the AgentID currently shown in the TUI.
	activeAgent session.AgentID

	// overlayOpen is true when the agent switcher modal is shown.
	overlayOpen bool

	// waitingInput holds agents that need user input (StatusWaitingUserInput).
	// The TUI shows a modal for the most recently waiting agent.
	waitingInput map[session.AgentID]*waitingInputState

	// waitingDismissed is true when the user pressed Esc to dismiss the
	// waiting-input overlay but agents are still waiting. The overlay
	// reappears when the agent receives another task or on next render pass.
	waitingDismissed bool
}

// AttachToSession connects the sessionUI to a real session.Session.
func (s *sessionUI) AttachToSession(sess *session.Session) {
	s.s = sess
}

// AgentCount returns the number of agents in the session.
func (s *sessionUI) AgentCount() int {
	if s == nil || s.s == nil {
		return 1
	}
	return s.s.AgentCount()
}

// AgentTree returns the agent tree from the session.
func (s *sessionUI) AgentTree() []session.AgentTreeNode {
	if s == nil || s.s == nil {
		return nil
	}
	return s.s.AgentTree()
}

// waitingInputState holds context for an agent that is waiting for user input.
type waitingInputState struct {
	agentID   session.AgentID
	agentName string
	task      string
}

// newSessionUI creates the session UI state.
func newSessionUI() *sessionUI {
	return &sessionUI{
		activeAgent:  session.RootAgentID,
		waitingInput: make(map[session.AgentID]*waitingInputState),
	}
}

// SwitchToAgent switches the visible TUI to the given agent.
func (s *sessionUI) SwitchToAgent(id session.AgentID) {
	s.activeAgent = id
}

// ActiveAgent returns the currently visible agent's ID.
func (s *sessionUI) ActiveAgent() session.AgentID {
	return s.activeAgent
}

// watchStatusEvents listens to agent status changes and updates the sessionUI
// when agents need user input. Runs in a goroutine started from NewModel.
// It exits when ctx is cancelled so tests don't leak goroutines.
// Guard: if ch is nil, return immediately. ch is nil when session package
// hasn't wired StatusHookChannel (e.g. in tests that construct Model directly
// without going through NewModel's engine wiring path).
func (m *Model) watchStatusEvents(ctx context.Context, ch <-chan session.StatusEvent) {
	if ch == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			if e.New == session.StatusWaitingUserInput {
				task := e.Task
				if task == "" {
					task = "task"
				}
				m.session.AddWaitingInput(e.ID, fmt.Sprintf("agent-%d", e.ID), task)
			}
		}
	}
}

// sendToWaitingAgent delivers the user's input to the first waiting agent.
// Returns whether a waiting agent was found and the command to run.
func (m *Model) sendToWaitingAgent(input string) bool {
	if m.session == nil || !m.session.HasWaitingAgents() {
		return false
	}
	var targetID session.AgentID
	for id := range m.session.waitingInput {
		targetID = id
		break
	}
	if targetID == 0 {
		return false
	}
	m.session.RemoveWaitingInput(targetID)
	if m.session.s != nil {
		if err := m.session.s.SendToAgent(targetID, input); err != nil {
			m.notice = fmt.Sprintf("failed to send to agent: %v", err)
			return true
		}
	}
	return true
}

// AddWaitingInput registers an agent as waiting for user input.
// task is the delegation task description the agent was working on.
func (s *sessionUI) AddWaitingInput(id session.AgentID, name, task string) {
	s.waitingInput[id] = &waitingInputState{
		agentID:   id,
		agentName: name,
		task:      task,
	}
	// Whenever an agent reports in, the overlay is live again (not dismissed).
	s.waitingDismissed = false
}

// RemoveWaitingInput clears the waiting state for an agent.
func (s *sessionUI) RemoveWaitingInput(id session.AgentID) {
	delete(s.waitingInput, id)
}

// HasWaitingAgents returns true if any agent is waiting for user input
// and the user has not dismissed the overlay.
func (s *sessionUI) HasWaitingAgents() bool {
	return len(s.waitingInput) > 0 && !s.waitingDismissed
}

// DismissWaitingInput dismisses the waiting overlay. The overlay will
// reappear when the next agent enters waiting_user_input status.
func (s *sessionUI) DismissWaitingInput() {
	s.waitingDismissed = true
}

// renderAgentSwitcher renders the agent switcher overlay when open.
func (s *sessionUI) RenderAgentSwitcher(width int) string {
	if s == nil || !s.overlayOpen {
		return ""
	}
	tree := s.AgentTree()
	lines := []string{
		accentStyle.Bold(true).Render("  AGENTS  "),
		"",
	}
	for _, node := range tree {
		prefix := "  "
		if node.ID != session.RootAgentID {
			prefix = "  ├─ "
		}
		name := fmt.Sprintf("Agent %d", node.ID)
		if node.ID == s.activeAgent {
			name = boldStyle.Render(name + " ◀")
		} else {
			name = subtleStyle.Render(name)
		}
		status := s.statusLabel(node.Status)
		line := prefix + name + "  " + subtleStyle.Render(status)
		lines = append(lines, line)
	}
	lines = append(lines, "")
	lines = append(lines, subtleStyle.Render("  ctrl+alt+1..5 to switch · ctrl+alt+a to close"))
	body := strings.Join(lines, "\n")
	frame := lipgloss.NewStyle().
		Background(colorPanelBg).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentStyle.GetForeground())
	return frame.Width(width).Height(min(len(lines)+4, 20)).Render(body)
}

func (s *sessionUI) statusLabel(status session.AgentStatus) string {
	switch status {
	case session.StatusIdle:
		return "idle"
	case session.StatusRunning:
		return "running"
	case session.StatusWaitingDelegation:
		return "waiting"
	case session.StatusWaitingUserInput:
		return "needs input"
	case session.StatusParked:
		return "parked"
	case session.StatusDone:
		return "done"
	case session.StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// renderWaitingInputOverlay renders the modal when an agent needs user input.
func (m Model) renderWaitingInputOverlay(width, height int) string {
	if m.session == nil {
		return ""
	}
	lines := []string{
		accentStyle.Bold(true).Render("  AGENT NEEDS INPUT  "),
		"",
	}
	for id, state := range m.session.waitingInput {
		lines = append(lines, fmt.Sprintf("  Agent %d — %s", id, state.agentName))
		lines = append(lines, fmt.Sprintf("  Task: %s", oneLine(state.task)))
		lines = append(lines, fmt.Sprintf("  %s", warnStyle.Render("awaiting your response...")))
		lines = append(lines, "")
	}
	lines = append(lines, subtleStyle.Render("  type your reply below — it will be sent to the waiting agent"))
	lines = append(lines, subtleStyle.Render("  esc to minimize · ctrl+alt+1..5 to switch agents"))
	body := strings.Join(lines, "\n")
	return body
}
