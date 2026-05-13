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
		frame := lipgloss.NewStyle().
			Background(colorPanelBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(pal.Border)
		return frame.Width(width).Height(height).Render(body)
	}
	contentWidth := width - 6
	if contentWidth < 20 {
		contentWidth = 20
	}
	innerHeight := height - 4
	if innerHeight < 1 {
		innerHeight = 1
	}
	// Agent switcher overlay — shown when ctrl+alt+a is pressed.
	if m.session != nil && m.session.overlayOpen {
		body := m.session.RenderAgentSwitcher(width)
		frame := lipgloss.NewStyle().
			Background(colorPanelBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(pal.Border)
		return frame.Width(width).Height(height).Render(body)
	}
	// Waiting-input overlay — shown when an agent needs user input.
	if m.session != nil && m.session.HasWaitingAgents() {
		body := m.renderWaitingInputOverlay(contentWidth, innerHeight)
		frame := lipgloss.NewStyle().
			Background(colorPanelBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(pal.Border)
		return frame.Width(width).Height(height).Render(body)
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
		content := body + "\n" + hint
		frame := lipgloss.NewStyle().
			Padding(1, 2).
			Background(colorPanelBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(pal.Border)
		return frame.Width(width).Height(height).Render(content)
	}
	// Demoted-panel overlay covers the active tab body whenever
	// panelOverlayKind is set. Falls through to the regular per-tab
	// switch when empty.
	if kind := m.ui.panelOverlayKind; kind != "" {
		content := m.renderPanelOverlayBody(kind, contentWidth, innerHeight)
		frame := lipgloss.NewStyle().
			Padding(1, 2).
			Background(colorPanelBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(pal.Border)
		return frame.Width(width).Height(height).Render(content)
	}
	var content string
	switch m.tabs[m.activeTab] {
	case "Files":
		content = fitPanelContentHeight(m.renderFilesViewSized(contentWidth, innerHeight), innerHeight)
	case "Patch":
		content = fitPanelContentHeight(m.renderPatchView(contentWidth), innerHeight)
	case "Workflow":
		content = fitPanelContentHeight(m.renderWorkflowView(contentWidth), innerHeight)
	case "Activity":
		content = m.renderActivityViewSized(contentWidth, innerHeight)
	case "Memory":
		content = fitPanelContentHeight(m.renderMemoryView(contentWidth), innerHeight)
	case "Conversations":
		content = fitPanelContentHeight(m.renderConversationsView(contentWidth), innerHeight)
	case "Providers":
		content = fitPanelContentHeight(m.renderProvidersView(contentWidth), innerHeight)
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
	if m.ui.selectionModeActive {
		div := lipgloss.NewStyle().
			Foreground(pal.Border).
			Render(strings.Repeat("─", max(contentWidth-4, 20)))
		return div + "\n" + content
	}
	frame := lipgloss.NewStyle().
		Padding(1, 2).
		Background(colorPanelBg).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(pal.Border)
	return frame.Width(width).Height(height).Render(content)
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
