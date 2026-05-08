// update_keypress.go — the tea.KeyMsg branch of Update.
//
// Owns: approval-modal capture and the per-tab routing switch that
// delegates to handleChatKey / handlePatchKey / etc. The big global
// shortcut table (ctrl/alt/F-keys, tab/shift+tab, stats-panel toggles)
// lives in update_keypress_shortcuts.go.
//
// Layout:
//   handleKeyMsg              — top entry; handles modal + Turkish-
//                               keyboard alt+letter shielding before
//                               falling through to the global shortcut
//                               table and per-tab routing.
//   handleApprovalKey         — the modal capture path, peeled out
//                               so the entry function stays linear.
//   routeKeyByActiveTab       — per-tab dispatcher.

package tui

import (
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pendingApproval != nil {
		return m.handleApprovalKey(msg)
	}
	// Panel switcher captures ALL keys while open — including arrows,
	// enter, esc, and rune input that drives the live filter. We
	// short-circuit before the global shortcut table so opening the
	// switcher doesn't compete with chat/per-tab handlers.
	if m.panelSwitcher.active {
		if nm, cmd, handled := m.handlePanelSwitcherKey(msg); handled {
			return nm, cmd
		}
	}
	// Turkish keyboards on MinTTY / Windows Terminal occasionally
	// deliver a plain letter keystroke with Alt=true during paste
	// or fast typing (the same ESC-prefix quirk that ships '@' as
	// alt+q). If we let that hit the global alt+<letter> switch
	// below, typing "kelime" would trip alt+i → Status tab mid-
	// word. Shield the Chat composer: when the tab is Chat and
	// the user has active input, route alt+<letter> straight to
	// the chat handler where it inserts as a rune. Empty Chat
	// composer still honours alt+<letter> as a real shortcut.
	if m.activeTab == 0 && msg.Alt && msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		if unicode.IsLetter(msg.Runes[0]) && (len(m.chat.input) > 0 || len(m.chat.pasteBlocks) > 0) {
			// Allow stats-panel mode switches through even while typing;
			// these specific alt combos are not Turkish-character inputs.
			switch msg.String() {
			case "alt+a", "alt+s", "alt+d", "alt+f", "alt+p":
				// fall through to global shortcut handler
			default:
				return m.handleChatKey(msg)
			}
		}
	}
	if m.activeTab == 0 && isAtMentionOpenKey(msg) {
		return m.handleChatKey(msg)
	}
	if nm, cmd, handled := m.handleGlobalShortcuts(msg); handled {
		return nm, cmd
	}
	return m.routeKeyByActiveTab(msg)
}

// handleApprovalKey owns the approval modal's keystroke capture so the
// outer dispatcher stays focused on routing. ctrl+c still rage-quits
// because a wedged modal must not hang the agent — the deferred
// SetApprover(nil) + the approver's own context cancellation handle
// the rest.
func (m Model) handleApprovalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

// routeKeyByActiveTab is the per-tab dispatcher reached when no global
// shortcut matched. The Patch tab hosts an inline arrow-driven action
// menu so its block stays here rather than peeling out into another
// helper.
func (m Model) routeKeyByActiveTab(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Help overlay on non-Chat tabs takes the body — route j/k/pgup/
	// pgdn/g/G into its scroll state. Chat-tab help is the inline
	// composer-filtered widget, which uses different keys (handled in
	// chat_key.go), so we only intercept when the active tab is NOT
	// Chat. q closes the overlay (matches the close hint).
	if m.ui.showHelpOverlay && m.tabs[m.activeTab] != "Chat" {
		key := msg.String()
		if key == "q" {
			m.ui.showHelpOverlay = false
			return m, nil
		}
		m.helpOverlay.scroll = adjustScrollOnlyOffset(key, m.helpOverlay.scroll)
		return m, nil
	}
	// Phase A: nine demoted panels (Status, Tools, CodeMap, Prompts,
	// Security, Plans, Context, Orchestrate, Shortcuts) render as
	// panelOverlayKind overlays covering the active tab body. While an
	// overlay is open it owns the keyboard — route to the matching
	// per-panel handler before the tab dispatcher runs. Orchestrate
	// and Shortcuts are read-only digests (no rows, no actions); their
	// handlers in overlay_scroll_keys.go just adjust a scroll offset
	// because both panels routinely overflow the viewport on stock
	// terminal heights and `...` truncation hid content the user
	// couldn't reach.
	if kind := m.ui.panelOverlayKind; kind != "" {
		// `q` is the universal "close this overlay" key — the close hint
		// in panel_overlay.go advertises "esc · q to close" so honour it
		// here regardless of which overlay is open. Without this branch
		// the hint was lying for every demoted panel; only esc worked.
		if msg.String() == "q" {
			if nm, closed := m.closePanelOverlay(); closed {
				return nm, nil
			}
		}
		switch kind {
		case "status":
			return m.handleStatusKey(msg)
		case "tools":
			return m.handleToolsKey(msg)
		case "codemap":
			return m.handleCodemapKey(msg)
		case "prompts":
			return m.handlePromptsKey(msg)
		case "security":
			return m.handleSecurityKey(msg)
		case "plans":
			return m.handlePlansKey(msg)
		case "context":
			return m.handleContextKey(msg)
		case "orchestrate":
			return m.handleOrchestrateKey(msg)
		case "shortcuts":
			return m.handleShortcutsKey(msg)
		case "providerlog":
			return m.handleProviderLogKey(msg)
		}
	}
	if m.activeTab < 0 || m.activeTab >= len(m.tabs) {
		m.notice = "Internal: tab index out of range"
		return m, nil
	}
	switch m.tabs[m.activeTab] {
	case "Chat":
		return m.handleChatKey(msg)
	case "Files":
		return m.handleFilesKey(msg)
	case "Patch":
		if nm, cmd, handled := m.handleActionMenuKey(msg); handled {
			return nm, cmd
		}
		switch msg.String() {
		case "enter", "right", "l":
			// Enter / Right opens the menu — arrow-driven access to
			// apply / check / undo / next-file / next-hunk / focus /
			// reload-* without memorising a/c/u/n/b/j/k/f/d/l.
			return m.openPatchActionMenu(), nil
		case "d", "alt+d":
			return m, loadWorkspaceCmd(m.eng)
		case "alt+l":
			return m, loadLatestPatchCmd(m.eng)
		case "n", "alt+n":
			return m.shiftPatchTarget(1)
		case "b", "alt+b":
			return m.shiftPatchTarget(-1)
		case "j", "alt+j", "down":
			return m.shiftPatchHunk(1)
		case "k", "alt+k", "up":
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
	case "Activity":
		return m.handleActivityKey(msg)
	case "Memory":
		return m.handleMemoryKey(msg)
	case "Conversations":
		return m.handleConversationsKey(msg)
	case "Providers":
		return m.handleProvidersKey(msg)
	case "Workflow":
		return m.handleWorkflowKey(msg)
	}
	return m, nil
}
