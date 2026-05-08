package tui

// slash_turn_actions.go — bodies for the per-assistant-turn slash
// commands wired by the chip line under each answer: /pin, /unpin,
// /fork <n>, /save <n>. Phase E item 1.
//
// Pin/unpin is local UI state (m.chat.pinnedAssistantTurns); fork
// routes through the engine's ConversationBranchCreate; save writes a
// single-turn markdown export under .dfmc/exports/ next to the regular
// /export output. Each handler returns (Model, tea.Cmd, true) so the
// dispatcher in chat_commands.go can call them in one line.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// parseAssistantTurnArg accepts the user's `/pin 3`, `/fork 12`,
// `/save 7` argument and returns the parsed 1-based turn number.
// Strict positive-integer parsing — anything else returns ok=false so
// callers can show usage hints rather than guessing.
func parseAssistantTurnArg(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// findAssistantTurn walks the transcript and returns the index of the
// turnNum-th assistant message (1-based). Returns -1 when the turn
// number is out of range so callers can render a "no such turn" hint.
func findAssistantTurn(transcript []chatLine, turnNum int) int {
	if turnNum <= 0 {
		return -1
	}
	count := 0
	for i, line := range transcript {
		if line.Role.Eq(chatRoleAssistant) {
			count++
			if count == turnNum {
				return i
			}
		}
	}
	return -1
}

// handlePinTurnSlash flips the pin flag on a turn. pin=true sets the
// anchor; pin=false clears it. Pure UI state — no engine call, no
// persistence (anchors are scoped to the current TUI session).
func (m Model) handlePinTurnSlash(turnNum int, pin bool) (tea.Model, tea.Cmd, bool) {
	if findAssistantTurn(m.chat.transcript, turnNum) < 0 {
		m.notice = fmt.Sprintf("No assistant turn #%d in the transcript yet.", turnNum)
		return m.appendSystemMessage(fmt.Sprintf("No assistant turn #%d. The chip line under each assistant answer shows its number.", turnNum)), nil, true
	}
	if m.chat.pinnedAssistantTurns == nil {
		m.chat.pinnedAssistantTurns = map[int]bool{}
	}
	if pin {
		m.chat.pinnedAssistantTurns[turnNum] = true
		m.notice = fmt.Sprintf("Pinned assistant turn #%d — chip flips to ★.", turnNum)
		return m, nil, true
	}
	delete(m.chat.pinnedAssistantTurns, turnNum)
	m.notice = fmt.Sprintf("Unpinned assistant turn #%d.", turnNum)
	return m, nil, true
}

// handleForkTurnSlash creates a new conversation branch anchored at
// the given assistant turn. The branch name defaults to
// `fork-from-<n>-<stamp>` when the user didn't supply one — that keeps
// /fork 3 a true one-keystroke divergence shortcut. Engine wiring goes
// through ConversationBranchCreate (mirrors /conversation fork).
func (m Model) handleForkTurnSlash(turnNum int, name string) (tea.Model, tea.Cmd, bool) {
	// Validate the turn number before the engine check so the user gets
	// the more actionable message (a typo'd turn is theirs to fix; an
	// unready engine isn't).
	if findAssistantTurn(m.chat.transcript, turnNum) < 0 {
		m.notice = fmt.Sprintf("No assistant turn #%d to fork from.", turnNum)
		return m.appendSystemMessage(fmt.Sprintf("No assistant turn #%d. The chip line under each assistant answer shows its number.", turnNum)), nil, true
	}
	if m.eng == nil {
		m.notice = "Engine not ready — cannot fork conversation yet."
		return m.appendSystemMessage("/fork needs the engine to be initialised. Try again once the runtime card shows ready."), nil, true
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("fork-from-%d-%s", turnNum, time.Now().Format("20060102-150405"))
	}
	if err := m.eng.ConversationBranchCreate(name); err != nil {
		m.notice = "Fork failed: " + err.Error()
		return m.appendSystemMessage("/fork failed: " + err.Error()), nil, true
	}
	m.notice = fmt.Sprintf("Forked at turn #%d → branch %q (now active).", turnNum, name)
	return m.appendSystemMessage(fmt.Sprintf("▸ Forked at assistant turn #%d into branch %q. The active conversation now lives on the new branch.", turnNum, name)), nil, true
}

// handleSaveTurnSlash exports a single assistant turn (plus the user
// prompt that triggered it, when present) to a markdown file under
// .dfmc/exports/turn-<n>-<stamp>.md. Same export directory and format
// conventions as exportTranscript so the user's tooling around the
// transcript dump folder keeps working.
func (m Model) handleSaveTurnSlash(turnNum int) (tea.Model, tea.Cmd, bool) {
	idx := findAssistantTurn(m.chat.transcript, turnNum)
	if idx < 0 {
		m.notice = fmt.Sprintf("No assistant turn #%d to save.", turnNum)
		return m.appendSystemMessage(fmt.Sprintf("No assistant turn #%d. The chip line under each assistant answer shows its number.", turnNum)), nil, true
	}
	assistant := m.chat.transcript[idx]
	if strings.TrimSpace(assistant.Content) == "" {
		m.notice = fmt.Sprintf("Assistant turn #%d is empty — nothing to save.", turnNum)
		return m.appendSystemMessage(fmt.Sprintf("Assistant turn #%d is empty.", turnNum)), nil, true
	}

	// Walk back from the assistant turn to find the user prompt that
	// triggered it (the most recent user line before idx). When the
	// transcript is malformed and there's no preceding user line, we
	// still write the assistant content so the save is never lossy.
	var userPrompt string
	for j := idx - 1; j >= 0; j-- {
		if m.chat.transcript[j].Role.Eq(chatRoleUser) {
			userPrompt = strings.TrimRight(m.chat.transcript[j].Content, "\n")
			break
		}
	}

	projectRoot := strings.TrimSpace(m.projectRoot())
	if projectRoot == "" {
		cwd, err := os.Getwd()
		if err != nil {
			m.notice = "Save failed: " + err.Error()
			return m.appendSystemMessage("/save failed: " + err.Error()), nil, true
		}
		projectRoot = cwd
	}
	stamp := time.Now().Format("20060102-150405")
	path := filepath.Join(projectRoot, ".dfmc", "exports", fmt.Sprintf("turn-%d-%s.md", turnNum, stamp))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.notice = "Save failed: " + err.Error()
		return m.appendSystemMessage("/save failed: " + err.Error()), nil, true
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "# DFMC turn #%d — %s\n\n", turnNum, time.Now().Format(time.RFC3339))
	if provider := strings.TrimSpace(m.status.Provider); provider != "" {
		fmt.Fprintf(&buf, "_provider:_ `%s`", provider)
		if model := strings.TrimSpace(m.status.Model); model != "" {
			fmt.Fprintf(&buf, " · _model:_ `%s`", model)
		}
		buf.WriteString("\n\n")
	}
	if userPrompt != "" {
		fmt.Fprintf(&buf, "## user\n\n%s\n\n", userPrompt)
	}
	fmt.Fprintf(&buf, "## assistant (turn #%d)\n\n%s\n", turnNum, strings.TrimRight(assistant.Content, "\n"))

	if err := os.WriteFile(path, []byte(buf.String()), 0o644); err != nil {
		m.notice = "Save failed: " + err.Error()
		return m.appendSystemMessage("/save failed: " + err.Error()), nil, true
	}
	m.notice = "Saved turn #" + strconv.Itoa(turnNum) + " → " + path
	return m.appendSystemMessage("▸ Saved assistant turn #" + strconv.Itoa(turnNum) + " → " + path), nil, true
}
