package tui

// chat_commands_dispatch.go — extracted-from-switch heavy /tool and
// /context handlers, plus the bottom-of-file helpers (shortRunID,
// toggle/setSelectionMode, handleQueueSlash) that the dispatcher and
// keymap call into. Sibling to chat_commands.go which keeps the main
// dispatcher + parseChatCommandInput contract.

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleToolSlash dispatches /tool — picker when no args, "show NAME"
// for ToolSpec inspect, otherwise execute NAME with parsed params.
func (m Model) handleToolSlash(args []string, rawArgs string) (tea.Model, tea.Cmd, bool) {
	if len(args) == 0 {
		m = m.startCommandPicker("tool", "", false)
		return m, nil, true
	}
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
}

// handleContextSlash dispatches /context — show / full / why / messages
// / drop subcommands. Default surfaces the short summary.
func (m Model) handleContextSlash(args []string) (tea.Model, tea.Cmd, bool) {
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
		return m.appendSystemMessage(m.contextCommandSummary()), nil, true
	case "budget":
		return m.appendSystemMessage(m.contextCommandDetailedSummary()), nil, true
	case "recommend":
		return m.appendSystemMessage(m.contextCommandWhySummary()), nil, true
	case "messages", "msgs", "list":
		return m.appendSystemMessage(m.contextCommandMessagesTable()), nil, true
	case "drop", "remove", "rm":
		return m.runContextDropCommand(args[1:])
	default:
		return m.appendSystemMessage(m.contextCommandSummary()), nil, true
	}
}

// handleToolsSlash dispatches /tools — toggle the chip strip, or
// "/tools list" prints the registered backend tool catalog.
func (m Model) handleToolsSlash(args []string) (tea.Model, tea.Cmd, bool) {
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
}

func (m Model) handleUpdateSlash(args []string) (tea.Model, tea.Cmd, bool) {
	m.chat.input = ""
	if m.eng == nil {
		return m.appendSystemMessage("Engine not initialized."), nil, true
	}
	update, err := m.eng.CheckForUpdates(m.ctx, m.eng.Version)
	if err != nil {
		return m.appendSystemMessage("Update check failed: " + err.Error()), nil, true
	}
	if !update.UpdateAvailable {
		return m.appendSystemMessage("You are on the latest version (" + m.eng.Version + ")."), nil, true
	}
	msg := fmt.Sprintf("A new version is available: %s (current: %s)\n\nDownload it here: %s",
		update.LatestVersion, update.CurrentVersion, update.ReleaseURL)
	return m.appendSystemMessage(msg), nil, true
}

// shortRunID returns the leading 8 chars of a Drive run id so the user has
// a stable, scannable handle ("Drive [abc12345] started") that still maps
// 1-to-1 to the persisted run. Drive run ids are unique by their first
// 8 chars in practice; the full id always lands in the system message line
// so /drive resume <id> remains unambiguous.
func shortRunID(runID string) string {
	if len(runID) <= 8 {
		return runID
	}
	return runID[:8]
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
