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
		body = fitPanelContentHeight(m.renderStatusViewV2(contentWidth), bodyHeight)
	case "tools":
		body = fitPanelContentHeight(m.renderToolsView(contentWidth), bodyHeight)
	case "codemap":
		body = fitPanelContentHeight(m.renderCodemapView(contentWidth), bodyHeight)
	case "prompts":
		body = fitPanelContentHeight(m.renderPromptsView(contentWidth), bodyHeight)
	case "security":
		body = fitPanelContentHeight(m.renderSecurityView(contentWidth), bodyHeight)
	case "plans":
		body = fitPanelContentHeight(m.renderPlansView(contentWidth), bodyHeight)
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
		body = fitPanelContentHeight(m.renderContextsView(contentWidth), bodyHeight)
	case "providerlog":
		// Provider call archive — long read-only digest of every
		// provider:complete event today. Scroll grammar matches
		// orchestrate/shortcuts (j/k/pgup/pgdn/g/G).
		body, _ = fitPanelContentScrollable(m.renderProviderLogView(contentWidth), bodyHeight, m.providerLog.scroll)
	default:
		body = subtleStyle.Render("(unknown overlay: " + kind + ")")
	}
	hint := subtleStyle.Render("esc · q to close · " + panelOverlayLabel(kind))
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
	default:
		return strings.ToUpper(kind)
	}
}

// closePanelOverlay clears the overlay flag if one is active, returning
// (model, true) when there was something to close. Esc/q handlers call
// this before falling through to other dismissal logic.
func (m Model) closePanelOverlay() (Model, bool) {
	if m.ui.panelOverlayKind == "" {
		return m, false
	}
	m.ui.panelOverlayKind = ""
	return m, true
}
