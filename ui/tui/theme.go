package tui

// theme.go — visual primitives for the TUI workbench.
//
// Rendering helpers have been extracted to the ui/tui/theme subpackage.
// This file re-exports them so existing call sites in ui/tui remain unchanged.

import (
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/ui/tui/theme"
)

// --- type aliases: maps tui package names to theme subpackage names -----
//
// These types were originally defined in this file. We alias them to their
// theme subpackage counterparts so existing call sites in ui/tui continue
// to compile without change. statsPanelMode and its constants live in
// panel_states.go so are not duplicated here.

type (
	toolChip          = theme.ToolChip
	todoStripItem     = theme.TodoStripItem
	runtimeSummary    = theme.RuntimeSummary
	messageHeaderInfo = theme.MessageHeaderInfo
	chatHeaderInfo    = theme.ChatHeaderInfo
	statsPanelInfo    = theme.StatsPanelInfo
	starterPrompt     = theme.StarterPrompt
)

// --- re-export palette vars --------------------------------------------------

var (
	colorPanelBorder = theme.ColorPanelBorder
	colorPanelBg     = theme.ColorPanelBg
	colorTitleFg     = theme.ColorTitleFg
	colorMuted       = theme.ColorMuted
	colorAccent      = theme.ColorAccent
	colorOk          = theme.ColorOk
	colorFail        = theme.ColorFail
	colorWarn        = theme.ColorWarn
	colorInfo        = theme.ColorInfo
	colorTabActiveBg = theme.ColorTabActiveBg
)

var (
	titleStyle              = theme.TitleStyle
	subtleStyle             = theme.SubtleStyle
	sectionTitleStyle       = theme.SectionTitleStyle
	statusBarStyle          = theme.StatusBarStyle
	boldStyle               = theme.BoldStyle
	codeStyle               = theme.CodeStyle
	accentStyle             = theme.AccentStyle
	okStyle                 = theme.OkStyle
	failStyle               = theme.FailStyle
	warnStyle               = theme.WarnStyle
	infoStyle               = theme.InfoStyle
	disabledStyle           = theme.DisabledStyle
	mentionPickerStyle      = theme.MentionPickerStyle
	mentionSelectedRowStyle = theme.MentionSelectedRowStyle
	bannerStyle             = theme.BannerStyle
	doneStyle               = theme.DoneStyle
	pendingStyle            = theme.PendingStyle
	runningStyle            = theme.RunningStyle
	blockedStyle            = theme.BlockedStyle
	skippedStyle            = theme.SkippedStyle
)

// --- stats panel constants --------------------------------------------------

const (
	statsPanelWidth                = theme.StatsPanelWidth
	statsPanelBoostWidthMin        = theme.StatsPanelBoostWidthMin
	statsPanelBoostMinContentWidth = theme.StatsPanelBoostMinContentWidth
	statsPanelMinContentWidth      = theme.StatsPanelMinContentWidth
)

// --- re-export functions -------------------------------------------------

func sectionHeader(icon, label string) string { return theme.SectionHeader(icon, label) }
func renderMarkdownBlocks(text string) []string      { return theme.RenderMarkdownBlocks(text) }
func renderToolChip(chip toolChip, width int) string { return theme.RenderToolChip(chip, width) }
func renderInlineToolChips(chips []toolChip, width int) string {
	return theme.RenderInlineToolChips(chips, width)
}
func renderInlineToolChipsSummary(chips []toolChip, width int) string {
	return theme.RenderInlineToolChipsSummary(chips, width)
}
func renderTodoStrip(items []todoStripItem, width int) string {
	return theme.RenderTodoStrip(items, width)
}
func renderRuntimeCard(rs runtimeSummary, width int) string {
	return theme.RenderRuntimeCard(rs, width)
}
func renderChatWorkflowFocusCard(info statsPanelInfo, width int) string {
	return theme.RenderChatWorkflowFocusCard(info, width)
}
func spinnerFrame(frame int) string                     { return theme.SpinnerFrame(frame) }
func renderMessageHeader(info messageHeaderInfo) string { return theme.RenderMessageHeader(info) }
func renderMessageBubble(role, content, header string, width int) string {
	return theme.RenderMessageBubble(role, content, header, width)
}
func renderDivider(width int) string               { return theme.RenderDivider(width) }
func renderInputBox(line string, width int) string { return theme.RenderInputBox(line, width) }
func renderChatHeader(info chatHeaderInfo, width int) string {
	return theme.RenderChatHeader(info, width)
}
func renderTokenMeter(used, max int) string {
	return theme.RenderTokenMeter(used, max)
}
func renderStepBar(step, maxSteps, cells, frame int) string {
	return theme.RenderStepBar(step, maxSteps, cells, frame)
}
func renderContextBar(used, max, cells int) string {
	return theme.RenderContextBar(used, max, cells)
}
func renderStreamingIndicator(phase string, frame int) string {
	return theme.RenderStreamingIndicator(phase, frame)
}
func renderResumeBanner(step, maxSteps, width int) string {
	return theme.RenderResumeBanner(step, maxSteps, width)
}
func renderStatsPanel(info statsPanelInfo, height int) string {
	return theme.RenderStatsPanel(info, height)
}
func renderStatsPanelSized(info statsPanelInfo, height int, panelWidth int) string {
	return theme.RenderStatsPanelSized(info, height, panelWidth)
}
func defaultStarterPrompts() []starterPrompt        { return theme.DefaultStarterPrompts() }
func starterTemplateForDigit(r rune) (string, bool) { return theme.StarterTemplateForDigit(r) }
func renderStarterPrompts(width int, configured bool) []string {
	return theme.RenderStarterPrompts(width, configured)
}

// formatInputBoxContent is used by tests — re-export from the theme package.
func formatInputBoxContent(content string, limit int) string {
	return theme.FormatInputBoxContent(content, limit)
}

// fileMarker lives in chat_helpers.go — suppress unused import warning.
var _ = fileMarker

// formatThousands re-exported from theme subpackage.
func formatThousands(n int) string { return theme.FormatThousands(n) }


// helper functions re-exported from theme subpackage.
func headerLevel(trimmed string) int                        { return theme.HeaderLevel(trimmed) }
func bulletLine(line string) (bullet, rest string, ok bool) { return theme.BulletLine(line) }
func wrapBubbleLine(line string, limit int) []string        { return theme.WrapBubbleLine(line, limit) }
func isTableHeader(line string) bool                        { return theme.IsTableHeader(line) }
func isTableSeparator(line string) bool                     { return theme.IsTableSeparator(line) }
func compactTokens(n int) string                            { return theme.CompactTokens(n) }

func init() {
	// Wire theme's FileMarker var to the real implementation in chat_helpers.
	// This must happen at init time before any RenderChatHeader call.
	theme.FileMarker = fileMarker
}

// --- unused stubs to keep import compatibility -------------------------
// Kept to prevent "declared but not used" errors for imports that existed
// in the original theme.go but are no longer needed here.

var (
	_ = lipgloss.Color("")
	_ = time.Time{}
)
