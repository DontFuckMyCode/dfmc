package tui

import "strings"

// demotedPanelKinds maps user-facing labels of demoted (no longer in
// the tab strip) panels onto their overlay kind names. activate
// DiagnosticTab routes these through the panelOverlayKind flag
// instead of switching tabs. Keep keys in canonical case to match
// historical activateDiagnosticTab callers (Status, CodeMap, etc.).
var demotedPanelKinds = map[string]string{
	"Status":      "status",
	"Tools":       "tools",
	"CodeMap":     "codemap",
	"Prompts":     "prompts",
	"Security":    "security",
	"Plans":       "plans",
	"Context":     "context",
	"Orchestrate": "orchestrate",
	"Shortcuts":   "shortcuts",
	"Contexts":    "contexts",
	"ProviderLog": "providerlog",
	// Telegram — demoted overlay panel (WIP build tag)
	"Telegram":   "telegram",
	"ToolStatus": "toolstatus",
}

// activateDiagnosticTab is the single entry point for "go look at the
// X panel" navigation. First-class tabs (still in the strip) get the
// activeTab index AND clear any open overlay (otherwise the user lands
// on the new tab with a stale overlay frozen on top of it). Demoted
// panels just swap the overlay kind — replacing one overlay with
// another is the expected behaviour so the user can hop between
// overlays without esc-ing each time.
//
// Both branches always close any open action menu — the menu's
// `owner` field pins it to one panel, so leaving it open after a tab
// switch turns the menu into an alien-looking list of actions whose
// Enter would fire something the user didn't intend.
// resetTabSwitchAffordances is the centralised "user is leaving the
// current panel" cleanup. Every tab-switch entry point — activateDiagnosticTab,
// the F1..F8 / Tab / Shift+Tab branches in update_keypress_shortcuts.go,
// the activate{Plans,Context,Providers}Panel helpers — must call this
// before changing activeTab/panelOverlayKind, so a stale modal from
// the prior panel doesn't bleed onto the new one.
//
// Cleared:
//   - actionMenu (keyed by panel `owner`; firing it on the new panel
//     would run the wrong handler on Enter)
//   - showHelpOverlay (the user explicitly chose a panel — they're
//     not stacking help on top)
//   - toolView.editing + draft (Tools' param editor is keyboard-trapping;
//     leaving without committing strands the user in editing mode next
//     visit and silently captures their next keystrokes)
//   - selectionModeActive (Chat-tab-only feature for drag-selecting
//     transcript; renderActiveView puts a top divider on every tab
//     while it's set, so leaving Chat with it on clutters Files /
//     Patch / etc. with an unexplained line)
func (m Model) resetTabSwitchAffordances() Model {
	m = m.closeActionMenu()
	m.ui.showHelpOverlay = false
	m.ui.selectionModeActive = false
	if m.toolView.editing {
		m.toolView.editing = false
		m.toolView.draft = ""
	}
	return m
}

func (m Model) activateDiagnosticTab(label string) Model {
	m = m.resetTabSwitchAffordances()
	if kind, demoted := demotedPanelKinds[label]; demoted {
		m.ui.panelOverlayKind = kind
		return m
	}
	if idx := m.activityTabIndex(label); idx >= 0 {
		m.activeTab = idx
		m.ui.panelOverlayKind = ""
	}
	return m
}

func (m Model) activatePlansPanel(query string, refresh bool) Model {
	m = m.activateDiagnosticTab("Plans")
	previousQuery := strings.TrimSpace(m.plans.query)
	if seeded := strings.TrimSpace(query); seeded != "" {
		m.plans.query = seeded
	}
	currentQuery := strings.TrimSpace(m.plans.query)
	if refresh || (currentQuery != "" && (m.plans.plan == nil || !strings.EqualFold(previousQuery, currentQuery))) {
		m = m.runPlansSplit()
	}
	return m
}

func (m Model) activateContextPanel(query string, refresh bool) Model {
	m = m.activateDiagnosticTab("Context")
	previousQuery := strings.TrimSpace(m.contextPanel.query)
	if seeded := strings.TrimSpace(query); seeded != "" {
		m.contextPanel.query = seeded
	}
	currentQuery := strings.TrimSpace(m.contextPanel.query)
	if refresh || (currentQuery != "" && (m.contextPanel.preview == nil || !strings.EqualFold(previousQuery, currentQuery))) {
		m = m.runContextPreview()
	}
	return m
}

func (m Model) activateProvidersPanel(provider string, refresh bool) Model {
	m = m.activateDiagnosticTab("Providers")
	// refreshProvidersRows is cheap and idempotent — re-run whenever
	// rows are empty (first visit or after a clear) or refresh is forced.
	if refresh || len(m.providers.rows) == 0 {
		m = m.refreshProvidersRows()
		m.providers.loaded = true
	}
	if focused := strings.TrimSpace(provider); focused != "" {
		m = m.focusProviderRow(focused)
	}
	return m
}
