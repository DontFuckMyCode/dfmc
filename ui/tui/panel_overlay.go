package tui

// panel_overlay.go — full-body overlay for demoted panels. The 17-tab
// strip was reduced to 8 first-class tabs; the remaining nine panels
// (Status, Tools, CodeMap, Prompts, Security, Plans, Context,
// Orchestrate, Shortcuts) now render as overlays covering the active
// tab body. activateDiagnosticTab sets m.ui.panelOverlayKind; this
// file dispatches that kind onto the matching renderXView function and
// appends a small "esc to close" hint so the user always knows how to
// dismiss it.
//
// Kept separate from render_layout.go because the overlay dispatch is
// the single point of truth for the demoted-panels migration; future
// changes (additional demotions, per-overlay header chrome) live here
// rather than poisoning the per-tab switch.

import (
	"strings"
)

// renderPanelOverlayBody dispatches kind onto the matching panel
// renderer, appending a single trailing hint line so the user knows
// esc closes the overlay. Returns the inner body string; the caller
// (renderActiveView) wraps it in the rounded frame.
func (m Model) renderPanelOverlayBody(kind string, contentWidth, innerHeight int) string {
	if innerHeight < 4 {
		innerHeight = 4
	}
	bodyHeight := innerHeight - 1 // reserve 1 row for the hint
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	var body string
	switch kind {
	case "status":
		panelHeight := panelContentHeightForActionMenu(bodyHeight, m.actionMenu.open && m.actionMenu.owner == "Status")
		body = fitPanelContentHeight(m.renderStatusViewSized(contentWidth, panelHeight), bodyHeight)
	case "tools":
		panelHeight := panelContentHeightForActionMenu(bodyHeight, m.actionMenu.open && m.actionMenu.owner == "Tools")
		body = fitPanelContentHeight(m.renderToolsViewSized(contentWidth, panelHeight), bodyHeight)
	case "codemap":
		panelHeight := panelContentHeightForActionMenu(bodyHeight, m.actionMenu.open && m.actionMenu.owner == "CodeMap")
		body = fitPanelContentHeight(m.renderCodemapViewSized(contentWidth, panelHeight), bodyHeight)
	case "prompts":
		panelHeight := panelContentHeightForActionMenu(bodyHeight, m.actionMenu.open && m.actionMenu.owner == "Prompts")
		body = fitPanelContentHeight(m.renderPromptsViewSized(contentWidth, panelHeight), bodyHeight)
	case "security":
		panelHeight := panelContentHeightForActionMenu(bodyHeight, m.actionMenu.open && m.actionMenu.owner == "Security")
		body = fitPanelContentHeight(m.renderSecurityViewSized(contentWidth, panelHeight), bodyHeight)
	case "plans":
		panelHeight := panelContentHeightForActionMenu(bodyHeight, m.actionMenu.open && m.actionMenu.owner == "Plans")
		body = fitPanelContentHeight(m.renderPlansViewSized(contentWidth, panelHeight), bodyHeight)
	case "context":
		body = fitPanelContentHeight(m.renderContextViewSized(contentWidth, bodyHeight), bodyHeight)
	case "orchestrate":
		// Orchestrate is a long read-only digest (main agent, subagents,
		// todos, drive run, tokens, recent activity). The viewport almost
		// never fits the whole thing, so render scrollable rather than
		// silently truncating with "...".
		body, _ = fitPanelContentScrollable(m.renderOrchestrateView(contentWidth), bodyHeight, m.orchestrate.scroll)
	case "shortcuts":
		// Shortcuts is the cheat-sheet — also long, also read-only, also
		// gets truncated below ~40 rows on stock terminals. Same fix.
		body, _ = fitPanelContentScrollable(m.renderShortcutsView(contentWidth), bodyHeight, m.shortcuts.scroll)
	case "contexts":
		body, _ = fitPanelContentScrollable(m.renderContextsView(contentWidth), bodyHeight, m.contexts.scroll)
	case "providerlog":
		// Provider call archive — long read-only digest of every
		// provider:complete event today. Scroll grammar matches
		// orchestrate/shortcuts (j/k/pgup/pgdn/g/G).
		body, _ = fitPanelContentScrollable(m.renderProviderLogView(contentWidth), bodyHeight, m.providerLog.scroll)
	case "telegram":
		// Telegram bot messages — shows connection status and incoming/outgoing messages.
		// Requires `go build -tags telegram_bot_wip` and --telegram-token flag.
		body = fitPanelContentHeight(m.renderTelegramPanelSized(contentWidth), bodyHeight)
	case "toolstatus":
		body = fitPanelContentHeight(m.renderToolStatusViewSized(contentWidth, bodyHeight), bodyHeight)
	default:
		body = subtleStyle.Render("(unknown overlay: " + kind + ")")
	}
	// Anchor the action menu (if open for this overlay) to the bottom of the
	// body, bounded to fit, so it never overflows the frame.
	body = m.overlayActionMenu(body, contentWidth, bodyHeight)
	// The panel name is already shown three times in the chrome (tab-strip
	// badge, brand line, footer chip), so the in-body hint only carries the
	// close affordance — no redundant label repeat (signal-density rule).
	hint := subtleStyle.Render("esc · q to close")
	return body + "\n" + hint
}

// panelOverlayLabel turns the internal kind string into the display
// label users see in the close-hint footer.
func panelOverlayLabel(kind string) string {
	switch kind {
	case "status":
		return "STATUS"
	case "tools":
		return "TOOLS"
	case "codemap":
		return "CODEMAP"
	case "prompts":
		return "PROMPTS"
	case "security":
		return "SECURITY"
	case "plans":
		return "PLANS"
	case "context":
		return "CONTEXT"
	case "orchestrate":
		return "ORCHESTRATE"
	case "shortcuts":
		return "SHORTCUTS"
	case "contexts":
		return "CONTEXTS"
	case "providerlog":
		return "PROVIDER LOG"
	case "telegram":
		return "TELEGRAM"
	case "toolstatus":
		return "TOOL STATUS"
	default:
		return strings.ToUpper(kind)
	}
}

// activePanelName is the human label for whatever the body is currently
// showing: a demoted-panel overlay (the F9+ panels) when one is open,
// otherwise the active top-level tab. The title strip and footer use this so
// they reflect the panel on screen — without it they kept showing the
// underlying tab name and looked "stuck" on the last tab when an overlay
// (Status, Tools, CodeMap, ...) was opened over it.
func (m Model) activePanelName() string {
	if kind := m.ui.panelOverlayKind; kind != "" {
		return panelOverlayLabel(kind)
	}
	if m.activeTab >= 0 && m.activeTab < len(m.tabs) {
		return m.tabs[m.activeTab]
	}
	return ""
}

// closePanelOverlay clears the overlay flag if one is active, returning
// (model, true) when there was something to close. Esc/q handlers call
// this before falling through to other dismissal logic.
func (m Model) closePanelOverlay() (Model, bool) {
	if m.ui.panelOverlayKind != "" {
		m.ui.panelOverlayKind = ""
		return m, true
	}
	return m, false
}
