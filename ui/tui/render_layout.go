package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func (m Model) renderActiveView(width int, height int, pal tabPaletteEntry) string {
	if height < 4 {
		height = 4
	}
	// Panel switcher overlay — appears on top of whatever tab is active.
	// Must be checked before any tab-specific content so it covers everything.
	if m.panelSwitcher.active {
		body := m.renderPanelSwitcher(width)
		// Border-only frame (no padding): inner box is width-2 x height-2.
		body = clipBlock(body, width-2, height-2)
		frame := lipgloss.NewStyle().
			Background(colorPanelBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(pal.Border)
		return frame.Width(max(width-2, 0)).Height(max(height-2, 0)).Render(body)
	}
	contentWidth := width - 6
	if contentWidth < 20 {
		contentWidth = 20
	}
	innerHeight := height - 4
	if innerHeight < 1 {
		innerHeight = 1
	}
	// Help overlay covers the active body on EVERY tab except Chat.
	// On Chat the overlay renders as a small section inside the chat
	// console (Phase K: lets the composer double as a live filter). On
	// other tabs there is no composer so the help overlay must take
	// the whole body — otherwise pressing Ctrl+H on Files / Patch /
	// Activity silently set the flag and the user saw nothing change.
	// The chat-tab branch leaves m.ui.showHelpOverlay untouched and
	// the inline widget in chat_console_composer.go handles rendering.
	if m.ui.showHelpOverlay && m.tabs[m.activeTab] != "Chat" {
		body, _ := fitPanelContentScrollable(m.renderHelpOverlay(contentWidth), innerHeight-1, m.helpOverlay.scroll)
		hint := subtleStyle.Render("esc · ctrl+h to close · type into chat composer to filter (chat tab only)")
		content := clipBlock(body+"\n"+hint, contentWidth, innerHeight)
		frame := lipgloss.NewStyle().
			Padding(1, 2).
			Background(colorPanelBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(pal.Border)
		return frame.Width(max(width-2, 0)).Height(max(height-2, 0)).Render(content)
	}
	// Demoted-panel overlay covers the active tab body whenever
	// panelOverlayKind is set. Falls through to the regular per-tab
	// switch when empty.
	if kind := m.ui.panelOverlayKind; kind != "" {
		content := clipBlock(m.renderPanelOverlayBody(kind, contentWidth, innerHeight), contentWidth, innerHeight)
		frame := lipgloss.NewStyle().
			Padding(1, 2).
			Background(colorPanelBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(pal.Border)
		return frame.Width(max(width-2, 0)).Height(max(height-2, 0)).Render(content)
	}
	var content string
	switch m.tabs[m.activeTab] {
	case "Files":
		panelHeight := panelContentHeightForActionMenu(innerHeight, m.actionMenu.open && m.actionMenu.owner == "Files")
		content = fitPanelContentHeight(m.renderFilesViewSized(contentWidth, panelHeight), innerHeight)
	case "Patch":
		panelHeight := panelContentHeightForActionMenu(innerHeight, m.actionMenu.open && m.actionMenu.owner == "Patch")
		content = fitPanelContentHeight(m.renderPatchViewSized(contentWidth, panelHeight), innerHeight)
	case "Workflow":
		panelHeight := panelContentHeightForActionMenu(innerHeight, m.actionMenu.open && m.actionMenu.owner == "Workflow")
		content = fitPanelContentHeight(m.renderWorkflowViewSized(contentWidth, panelHeight), innerHeight)
	case "Activity":
		content = m.renderActivityViewSized(contentWidth, innerHeight)
	case "Memory":
		panelHeight := panelContentHeightForActionMenu(innerHeight, m.actionMenu.open && m.actionMenu.owner == "Memory")
		content = fitPanelContentHeight(m.renderMemoryViewSized(contentWidth, panelHeight), innerHeight)
	case "Conversations":
		panelHeight := panelContentHeightForActionMenu(innerHeight, m.actionMenu.open && m.actionMenu.owner == "Conversations")
		content = fitPanelContentHeight(m.renderConversationsViewSized(contentWidth, panelHeight), innerHeight)
	case "Providers":
		panelHeight := panelContentHeightForActionMenu(innerHeight, m.actionMenu.open && m.actionMenu.owner == "Providers")
		content = fitPanelContentHeight(m.renderProvidersViewSized(contentWidth, panelHeight), innerHeight)
	default:
		panelVisible := m.statsPanelVisible(contentWidth)
		boosted := m.statsPanelBoostActive(time.Now())
		chatWidth := contentWidth
		panelWidth := statsPanelWidth
		if panelVisible {
			panelWidth = m.statsPanelRenderWidth(contentWidth)
			chatWidth = contentWidth - panelWidth - 2
		}
		parts := m.renderChatViewParts(chatWidth, panelVisible)
		// fitChatBody clips to innerHeight — pass scrollback as the
		// line-based clip offset so the ↑ hint roughly tracks how far we've
		// scrolled from the anchor. scrollback is a turn count; convert to
		// lines using a fixed ~2-lines-per-turn estimate.
		headLineCount := len(splitLines(parts.Head))
		scrollClip := m.chat.scrollback
		if scrollClip > headLineCount {
			scrollClip = headLineCount
		}
		body := fitChatBodyWithScrollbar(parts.Head, parts.Tail, innerHeight, scrollClip, chatWidth)
		// Hold the chat body to its width budget so the horizontal split
		// stays exact: some static hint lines (empty-state prompt, composer
		// key legend) can run wider than a narrow chatWidth, and an over-wide
		// left block would shove the stats panel right and clip its border.
		body = clipBlock(body, chatWidth, innerHeight)
		if m.ui.showTasksPanel {
			body = m.renderTasksPanelOverlay(body, contentWidth, innerHeight)
		} else if panelVisible {
			if boosted {
				body = lipgloss.NewStyle().Faint(true).Render(body)
			}
			info := m.statsPanelInfo()
			info.StatsPanelScroll = m.ui.statsPanelScroll
			panel := renderStatsPanelSized(info, innerHeight, panelWidth)
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, "  ", panel)
		}
		content = body
	}
	// First-class tabs: anchor the action menu (if open) to the bottom of the
	// body so it always lands fully on screen instead of overflowing the
	// frame. Demoted-panel overlays do the same inside renderPanelOverlayBody.
	content = m.overlayActionMenu(content, contentWidth, innerHeight)
	if m.ui.selectionModeActive {
		// Frameless copy/selection mode: a divider plus the raw body. No
		// border to protect us here, so clip the body to the full inner box
		// and keep the whole block within height (divider + body) so a long
		// transcript cannot push the terminal into a scroll.
		divWidth := max(min(contentWidth-4, width), 1)
		div := lipgloss.NewStyle().
			Foreground(pal.Border).
			Render(strings.Repeat("─", divWidth))
		body := clipBlock(content, width, max(height-1, 0))
		return clipBlock(div+"\n"+body, width, height)
	}
	content = clipBlock(content, contentWidth, innerHeight)
	frame := lipgloss.NewStyle().
		Padding(1, 2).
		Background(colorPanelBg).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Border)
	return frame.Width(max(width-2, 0)).Height(max(height-2, 0)).Render(content)
}

// clipBlock clips a content block to fit inside a `width` x `height` cell box
// WITHOUT padding it out: lines longer than width are truncated (ANSI-aware so
// color resets survive), and rows beyond height are dropped. This is the
// counterpart lipgloss is missing — .Width()/.Height() pad short content but
// never trim overflow, so a body taller/wider than its frame's inner box would
// spill past the border and wrap in the terminal (the "kayma"/broken-line
// class). Callers run this on content BEFORE handing it to a bordered frame, so
// the frame only ever pads, never overflows. height<=0 means "do not clip
// vertically"; width<=0 means "do not clip horizontally".
func clipBlock(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	if height > 0 && len(lines) > height {
		lines = lines[:height]
	}
	if width > 0 {
		for i, ln := range lines {
			if ansi.StringWidth(ln) > width {
				lines[i] = ansi.Truncate(ln, width, "")
			}
		}
	}
	return strings.Join(lines, "\n")
}

// normalizeScreen is the final belt-and-suspenders pass over the WHOLE View()
// output. It guarantees two invariants that keep the workbench from ever
// shifting or breaking its rules when the terminal is resized to any shape:
//   - every line is at most `width` cells wide (no wrap → no horizontal drift)
//   - the output is exactly `height` rows (clip overflow / pad short → the box
//     always fills the terminal, never scrolls it)
//
// In the normal case (chrome + sized body already add up to height and nothing
// overflows) this is a no-op; it only bites at pathological sizes where the
// per-frame math cannot physically satisfy the request (e.g. a 1-row terminal).
func normalizeScreen(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	if width > 0 {
		for i, ln := range lines {
			if ansi.StringWidth(ln) > width {
				lines[i] = ansi.Truncate(ln, width, "")
			}
		}
	}
	if height > 0 {
		if len(lines) > height {
			lines = lines[:height]
		} else {
			for len(lines) < height {
				lines = append(lines, "")
			}
		}
	}
	return strings.Join(lines, "\n")
}

// panelContentHeightForActionMenu used to carve a fixed 8-row slot out of the
// panel for the action menu, but the menu is now composited centrally by
// overlayActionMenu (which clips the body to the menu's actual height). So the
// panel always gets the full height; the parameter is retained for call-site
// readability. Kept as a function (not inlined) so the historical reservation
// point stays greppable.
func panelContentHeightForActionMenu(height int, _ bool) int {
	return height
}

func reservedPanelLeftPad(reservedWidth, panelWidth int) string {
	pad := reservedWidth - panelWidth
	if pad <= 0 {
		return ""
	}
	return strings.Repeat(" ", pad)
}

// fitChatBody lays out the chat view. When tail is empty, the whole chat
// console is one bottom-anchored scrollable feed. Legacy callers may still
// pass a tail, in which case tail remains pinned and only head scrolls.
//
// scrollbackLines is the raw line offset from the END of headLines.
// We compute the window so that higher scrollbackValues reveal earlier
// content: end = len(headLines) - scrollbackLines, then start = end - available.
// The scrollback value itself (in turns) is managed by scrollTranscript;
// fitChatBody just applies the line-based offset it receives.
func fitChatBody(head, tail string, maxLines, scrollbackLines int) string {
	return fitChatBodyWithScrollbar(head, tail, maxLines, scrollbackLines, 0)
}

func fitChatBodyWithScrollbar(head, tail string, maxLines, scrollbackLines, width int) string {
	if maxLines <= 0 {
		return head + "\n" + tail
	}
	headLines := splitLines(head)
	tailLines := splitLines(tail)
	if len(tailLines) >= maxLines {
		return strings.Join(tailLines, "\n")
	}
	available := maxLines - len(tailLines)
	if available < 3 {
		available = 3
	}
	if scrollbackLines < 0 {
		scrollbackLines = 0
	}
	// end is the index in headLines that should be at the BOTTOM of the
	// visible window. Clamp so we never exceed the array bounds.
	end := len(headLines) - scrollbackLines
	if end > len(headLines) {
		end = len(headLines)
	}
	if end < 1 {
		end = 1
	}
	start := end - available
	if start < 0 {
		start = 0
	}
	window := append([]string{}, headLines[start:end]...)
	if len(tailLines) == 0 && len(window) < maxLines {
		pad := maxLines - len(window)
		if pad > 0 {
			window = append(make([]string, pad), window...)
		}
	}
	if start > 0 {
		hint := subtleStyle.Render(fmt.Sprintf("  ↑ %d earlier lines  ·  wheel, pgup, shift+up to scroll", start))
		window[0] = hint
	}
	if end < len(headLines) {
		hint := subtleStyle.Render(fmt.Sprintf("  ↓ %d newer lines  ·  pgdown, end, shift+down to resume", len(headLines)-end))
		window[len(window)-1] = hint
	}
	if width > 0 {
		window = renderChatScrollbar(window, len(headLines), start, end, width)
	}
	return strings.Join(window, "\n") + "\n" + strings.Join(tailLines, "\n")
}

func renderChatScrollbar(lines []string, total, start, end, width int) []string {
	visible := len(lines)
	if width < 12 || visible < 2 || total <= visible || start < 0 || end <= start {
		return lines
	}
	contentWidth := width - 2
	if contentWidth < 8 {
		return lines
	}
	thumbSize := (visible * visible) / total
	if thumbSize < 1 {
		thumbSize = 1
	}
	if thumbSize > visible {
		thumbSize = visible
	}
	travel := visible - thumbSize
	thumbStart := 0
	if travel > 0 && total > visible {
		thumbStart = (start * travel) / (total - visible)
	}
	out := append([]string{}, lines...)
	for i, line := range out {
		marker := "│"
		if i >= thumbStart && i < thumbStart+thumbSize {
			marker = "█"
		}
		clipped := truncateSingleLine(line, contentWidth)
		if i == 0 {
			position := "scroll latest"
			if end < total {
				position = fmt.Sprintf("scroll %d/%d", start+1, max(total-visible+1, 1))
			}
			clipped = truncateSingleLine(clipped+"  "+subtleStyle.Render(position), contentWidth)
		}
		pad := width - 1 - ansi.StringWidth(clipped)
		if pad < 1 {
			pad = 1
		}
		out[i] = clipped + strings.Repeat(" ", pad) + subtleStyle.Render(marker)
	}
	return out
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
}

// oneLine collapses internal whitespace so the panel stays aligned
// even when entries carry embedded newlines or tabs.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// chatViewParts captures the scrollable head and the always-visible tail
// of the chat view. renderActiveView composes them with fitChatBody.
type chatViewParts struct {
	Head string
	Tail string
}

func (m Model) renderChatView(width int) string {
	parts := m.renderChatViewParts(width, false)
	if parts.Tail == "" {
		return parts.Head
	}
	return parts.Head + "\n" + parts.Tail
}

func (m Model) renderChatViewParts(width int, slimHeader bool) chatViewParts {
	return m.renderChatConsoleViewParts(width, slimHeader)
}
