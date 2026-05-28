// overlay_scroll_keys.go — keyboard handlers for the read-only
// reference overlays (Orchestrate, Shortcuts). Both panels are pure
// digests — there's nothing to select, edit, or run — but their content
// can easily be 80+ rows on a 30-row terminal. Without these handlers
// the user opens the overlay, sees a "..." truncation marker, and has
// no way to reach the rest. j/k/up/down step one line, pgup/pgdn step
// 10, g/G jump to top/end. Esc/q close the overlay (handled globally
// in update_keypress_shortcuts.go via closePanelOverlay).
//
// Lives next to panel_overlay.go (which renders the body) so the
// scroll contract — where the offset comes from, how the renderer
// applies it — is one Read away.

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// scrollOnlyOverlayPageStep is the per-press jump for pgup/pgdn on a
// read-only overlay. Ten lines ≈ one section header + a few body lines
// on Orchestrate / Shortcuts at typical column widths, which keeps the
// section structure scannable while paging.
const scrollOnlyOverlayPageStep = 10

// handleOrchestrateKey lives in render_orchestrate_keys.go — it now
// drives section-cursor navigation + an action menu on top of the
// shared scroll grammar.

func (m Model) handleShortcutsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.shortcuts.scroll = adjustScrollOnlyOffset(msg.String(), m.shortcuts.scroll)
	return m, nil
}

func (m Model) handleProviderLogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.providerLog.scroll = adjustScrollOnlyOffset(msg.String(), m.providerLog.scroll)
	return m, nil
}

// handleContextsOverlayKey lives in render_contexts_keys.go — it now
// drives section-cursor navigation + an action menu in addition to the
// shared scroll grammar.

// adjustScrollOnlyOffset implements the j/k/pgup/pgdn/g/G grammar used
// by both read-only overlays. Returns the new offset; the renderer
// (fitPanelContentScrollable) clamps to the actual content size, so
// "go too far down" is harmless — the next render snaps back.
func adjustScrollOnlyOffset(key string, scroll int) int {
	switch key {
	case "j", "down":
		return scroll + 1
	case "k", "up":
		if scroll <= 0 {
			return 0
		}
		return scroll - 1
	case "pgdown":
		return scroll + scrollOnlyOverlayPageStep
	case "pgup":
		if scroll <= scrollOnlyOverlayPageStep {
			return 0
		}
		return scroll - scrollOnlyOverlayPageStep
	case "g", "home":
		return 0
	// G / end jump to a large offset; the renderer clamps to maxScroll
	// so a single huge value is enough — no need to know the body size
	// here.
	case "G", "end":
		return 1 << 20
	}
	return scroll
}
