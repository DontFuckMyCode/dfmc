// update_keypress_shortcuts.go — global keyboard shortcut table.
// Sibling of update_keypress.go which keeps the Update→KeyMsg entry
// (handleKeyMsg, handleApprovalKey, routeKeyByActiveTab); this file
// owns handleGlobalShortcuts only — the big msg.String() switch over
// ctrl/alt/F-keys, tab/shift+tab, and the stats-panel sub-mode toggles
// (alt+a/s/d/f/p). Returns handled=true when the key was consumed so
// the caller falls through to per-tab routing only on miss.

package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// handleGlobalShortcuts covers keys that work regardless of which tab
// is focused (with a few "only on Chat" guards). Returns handled=true
// when a key was consumed; the caller falls through to per-tab
// routing otherwise.
func (m Model) handleGlobalShortcuts(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c", "ctrl+q":
		// Ctrl+C cancels paste blocks first; then cancels streaming
		// (if actively sending); finally rage-quits.
		if m.activeTab == 0 && len(m.chat.pasteBlocks) > 0 {
			m.clearPasteBlocks()
			m.setChatInput("")
			m.notice = "Paste cancelled."
			return m, nil, true
		}
		if m.chat.sending {
			if m.cancelActiveStream() {
				m.notice = "Cancelling…"
				return m, nil, true
			}
		}
		return m, tea.Quit, true
	case "ctrl+u":
		// Unix readline-style "clear input line". Only useful on the
		// Chat tab — other panels don't have a live composer.
		if m.activeTab == 0 {
			m.clearPasteBlocks()
			m.setChatInput("")
			m.chat.cursor = 0
			m.slashMenu.mention = 0
			m.slashMenu.command = 0
			m.slashMenu.commandArg = 0
			m.slashMenu.quickAction = 0
			m.notice = "Input cleared."
			return m, nil, true
		}
	case "ctrl+h":
		m.ui.showHelpOverlay = !m.ui.showHelpOverlay
		return m, nil, true
	case "esc":
		// Help overlay first — its title strip advertises "esc to clear"
		// (when a filter is active) and "ctrl+h to close" (when empty).
		// We honour BOTH: a filtered overlay clears the filter on esc,
		// and an unfiltered overlay closes on esc. Without this branch
		// the title hint was lying — neither esc nor ctrl+h alone did
		// what users tried first.
		if m.ui.showHelpOverlay {
			filter := strings.TrimSpace(m.chat.input)
			if filter != "" {
				m.clearPasteBlocks()
				m.setChatInput("")
				m.chat.cursor = 0
				m.notice = "Filter cleared."
			} else {
				m.ui.showHelpOverlay = false
			}
			return m, nil, true
		}
		if nm, closed := m.closePanelOverlay(); closed {
			return nm, nil, true
		}
		if m.activeTab == 0 && m.ui.showTasksPanel {
			nm, cmd := m.handleTasksPanelKey(msg)
			return nm, cmd, true
		}
		if m.chat.sending && m.cancelActiveStream() {
			m.notice = "Cancelling…"
			return m, nil, true
		}
		if m.activeTab == 0 && m.ui.statsPanelFocusLocked {
			m.ui.statsPanelFocusLocked = false
			m.ui.statsPanelBoostUntil = time.Time{}
			m.notice = "Stats panel focus unlocked."
			return m, nil, true
		}
	case "j", "k", "up", "down", "enter", "r":
		if m.activeTab == 0 && m.ui.showTasksPanel {
			nm, cmd := m.handleTasksPanelKey(msg)
			return nm, cmd, true
		}
		// Route to stats panel handler when providers sub-mode is focused
		if m.activeTab == 0 && m.ui.statsPanelFocusLocked && m.ui.statsPanelMode == statsPanelModeProviders {
			nm, cmd := m.handleStatsPanelProviderKey(msg)
			return nm, cmd, true
		}
	case "ctrl+s":
		m.ui.selectionModeActive = false
		m.ui.statsPanelFocusLocked = false
		m.ui.statsPanelBoostUntil = time.Time{}
		m.ui.showStatsPanel = !m.ui.showStatsPanel
		if m.ui.showStatsPanel && m.ui.statsPanelMode == "" {
			m.ui.statsPanelMode = statsPanelModeOverview
		}
		return m, nil, true
	case "alt+x":
		if m.activeTab == 0 {
			nm, cmd := m.setSelectionMode(!m.ui.selectionModeActive)
			return nm, cmd, true
		}
	case "alt+a":
		if m.activeTab == 0 {
			m.activateStatsPanelMode(statsPanelModeOverview, "overview")
			return m, nil, true
		}
	case "alt+s":
		if m.activeTab == 0 {
			m.activateStatsPanelMode(statsPanelModeTodos, "todos")
			return m, nil, true
		}
	case "alt+d":
		if m.activeTab == 0 {
			m.activateStatsPanelMode(statsPanelModeTasks, "tasks")
			return m, nil, true
		}
	case "alt+f":
		if m.activeTab == 0 {
			m.activateStatsPanelMode(statsPanelModeSubagents, "subagents")
			return m, nil, true
		}
	case "alt+p":
		if m.activeTab == 0 {
			m.activateStatsPanelMode(statsPanelModeProviders, "providers")
			return m, nil, true
		}
	case "alt+m":
		// Direct model picker — open the same fuzzy picker that
		// `/model` opens but without the user having to type the
		// slash command first. One keystroke from chat to a
		// focused model switcher; arrow keys + enter applies and
		// auto-saves to the winning config scope.
		if m.activeTab == 0 && !m.commandPicker.active {
			m = m.startCommandPicker("model", "", false)
			return m, nil, true
		}
	case "alt+shift+p", "alt+P":
		// Same idea for providers. Lowercase alt+p stays bound to
		// the read-only stats sub-panel so keep alt+P (or the
		// alt+shift+p form some terminals send) for the picker.
		if m.activeTab == 0 && !m.commandPicker.active {
			m = m.startCommandPicker("provider", "", false)
			return m, nil, true
		}
	case "ctrl+p":
		m.activeTab = 0
		m.setChatInput("/")
		m.slashMenu.command = 0
		m.slashMenu.commandArg = 0
		m.slashMenu.mention = 0
		return m, nil, true
	case "ctrl+g":
		m = m.activateDiagnosticTab("Activity")
		return m, nil, true
	case "tab":
		if m.tabs[m.activeTab] != "Chat" {
			// Tab cycles through first-class tabs only — clear any open
			// overlay so the new tab body is actually visible (otherwise
			// the user sees the previous overlay frozen on top of a new
			// underlying tab and assumes Tab is broken).
			m.ui.panelOverlayKind = ""
			m.activeTab = (m.activeTab + 1) % len(m.tabs)
			return m, nil, true
		}
	case "shift+tab":
		if m.tabs[m.activeTab] != "Chat" {
			m.ui.panelOverlayKind = ""
			m.activeTab--
			if m.activeTab < 0 {
				m.activeTab = len(m.tabs) - 1
			}
			return m, nil, true
		}
	// F-key panel map. The 17 panels split into 8 first-class tabs and
	// 9 demoted overlays — together that's more than 12 F-keys, so the
	// remaining 5 overlays land on Shift+F1..Shift+F5. Every panel is
	// reachable by an F-key or Shift+F-key; nothing requires the user
	// to memorise an Alt+letter combo. Alt+1..Alt+8 still mirror F1..F8
	// for terminals that swallow F-keys (tmux, some web IDEs); the
	// older Alt+I / Alt+T / Ctrl+Y / Ctrl+W / Alt+R / Alt+H aliases
	// stay below as muscle-memory backstops.
	//
	// First-class tabs (F1..F8 — strip order). Each branch clears any
	// open panel overlay before switching: a user pressing F2 expects to
	// land on the Files tab, not Files-with-CodeMap-overlaid-on-top from
	// a stale F10 press. activateDiagnosticTab(label) for the eight tabs
	// already clears the overlay; we still clear explicitly for the
	// four hand-rolled branches (Chat/Files/Patch/Activity) for symmetry.
	case "f1", "alt+1":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 0 // Chat
		return m, nil, true
	case "f2", "alt+2":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 1 // Files
		return m, nil, true
	case "f3", "alt+3":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 2 // Patch
		return m, nil, true
	case "f4", "alt+4":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 3 // Workflow
		m = m.refreshWorkflowOnTabEnter()
		return m, nil, true
	case "f5", "alt+5":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 4 // Activity
		return m, nil, true
	case "f6", "alt+6":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 5 // Memory
		if m.memory.entries == nil && !m.memory.loading {
			m.memory.loading = true
			return m, loadMemoryCmd(m.eng, m.memory.tier), true
		}
		return m, nil, true
	case "f7", "alt+7":
		m = m.resetTabSwitchAffordances()
		m.ui.panelOverlayKind = ""
		m.activeTab = 6 // Conversations
		if !m.conversations.loaded && !m.conversations.loading {
			m.conversations.loading = true
			return m, loadConversationsCmd(m.eng), true
		}
		return m, nil, true
	case "f8", "alt+8":
		m = m.activateProvidersPanel("", false)
		return m, nil, true
	// Demoted overlays (F9..F12 — the four most-trafficked):
	case "f9":
		m = m.activateDiagnosticTab("Status")
		return m, nil, true
	case "f10":
		m = m.activateDiagnosticTab("CodeMap")
		if !m.codemap.loaded && !m.codemap.loading {
			m.codemap.loading = true
			return m, loadCodemapCmd(m.eng), true
		}
		return m, nil, true
	case "f11":
		// F11 was previously bound only to the help overlay because most
		// terminals eat it for fullscreen. That left Tools without an
		// F-key. We now route F11 to Tools when it does come through;
		// help is still reachable via Ctrl+H / Alt+H / /help and Alt+0
		// (kept below as a legacy alias).
		m = m.activateDiagnosticTab("Tools")
		return m, nil, true
	case "f12":
		m = m.activateDiagnosticTab("Security")
		// First-entry scan: F12 should land on a populated overlay, not
		// a blank "load" button. Mirrors F6 / F7 / F10 which already
		// auto-load their data on first open.
		if !m.security.loaded && !m.security.loading {
			m.security.loading = true
			return m, loadSecurityCmd(m.eng), true
		}
		return m, nil, true
	// The remaining five overlays land on Shift+F1..Shift+F5. Most
	// terminals emit the F13..F17 codes for shift+F1..shift+F5 (the
	// classic xterm convention; bubbletea exposes them as KeyF13..KeyF20
	// → "f13".."f20" via msg.String). Newer terminals (Kitty protocol /
	// modifyOtherKeys) may emit the literal "shift+f1" form, so both
	// shapes are bound. Order is rough usage frequency: Prompts > Plans
	// > Context > Orchestrate > Shortcuts.
	case "shift+f1", "f13":
		m = m.activateDiagnosticTab("Prompts")
		if !m.prompts.loaded && !m.prompts.loading {
			m.prompts.loading = true
			return m, loadPromptsCmd(m.eng), true
		}
		return m, nil, true
	case "shift+f2", "f14":
		m = m.activatePlansPanel("", false)
		return m, nil, true
	case "shift+f3", "f15":
		m = m.activateContextPanel("", false)
		return m, nil, true
	case "shift+f4", "f16":
		m = m.activateDiagnosticTab("Orchestrate")
		return m, nil, true
	case "shift+f5", "f17":
		m = m.activateDiagnosticTab("Shortcuts")
		return m, nil, true
	// Alt+9 / Alt+0 used to map to Memory/Conversations under the 17-tab
	// era; after the F-key remap they would silently disagree with their
	// F-key partners. Route both to the help overlay so the legacy
	// muscle memory lands on the help screen, where the user can see
	// the new mapping rather than confusedly land on the wrong tab.
	case "alt+9", "alt+0":
		m.ui.showHelpOverlay = !m.ui.showHelpOverlay
		return m, nil, true
	case "alt+i":
		// Alt+I kept as a legacy alias for Tools (was its F6 home in the
		// 17-tab era). Primary binding is now F11.
		m = m.activateDiagnosticTab("Tools")
		return m, nil, true
	case "alt+t":
		// Alt+T legacy alias for Prompts; primary is Shift+F1.
		m = m.activateDiagnosticTab("Prompts")
		if !m.prompts.loaded && !m.prompts.loading {
			m.prompts.loading = true
			return m, loadPromptsCmd(m.eng), true
		}
		return m, nil, true
	case "ctrl+i":
		m = m.activateDiagnosticTab("Status")
		return m, nil, true
	case "ctrl+y":
		m = m.activatePlansPanel("", false)
		return m, nil, true
	case "ctrl+w":
		if m.activeTab != 0 {
			m = m.activateContextPanel("", false)
			return m, nil, true
		}
	case "ctrl+o":
		m = m.activateProvidersPanel("", false)
		return m, nil, true
	case "alt+r":
		m = m.activateDiagnosticTab("Orchestrate")
		return m, nil, true
	case "alt+h":
		// Phase K (help unification): alt+h flips the same Ctrl+H help
		// overlay rather than the legacy Shortcuts panel. One help
		// surface, three triggers (ctrl+h / alt+h / /help).
		m.ui.showHelpOverlay = !m.ui.showHelpOverlay
		return m, nil, true
	}
	return m, nil, false
}
