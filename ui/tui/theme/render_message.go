package theme

// render_message.go — message bubble + input box rendering.
// Companion siblings:
//
//   - render.go               role helpers, section header, todo strip,
//                             runtime card, TruncateSingleLine
//   - render_chat_header.go   CHAT header bar + workflow focus card
//   - render_starters.go      starter prompt grid, streaming/resume
//                             banners
//
// RenderMessageHeader paints the metadata strip above each turn:
// role badge, copy index, streaming spinner, timestamp, duration,
// token count, tool-call counter (with failure decoration). The
// bubble itself walks rendered markdown blocks through ANSI-aware
// wrap helpers (WrapBubbleLine / HardWrapByCells). Composer-side
// rendering (RenderInputBox / FormatInputBoxContent) shares the
// same ANSI wrap path.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func RenderMessageHeader(info MessageHeaderInfo) string {
	parts := []string{RoleBadge(info.Role)}
	if info.CopyIndex > 0 {
		parts = append(parts, SubtleStyle.Render(fmt.Sprintf("#%d", info.CopyIndex)))
	}
	if info.Streaming {
		parts = append(parts, InfoStyle.Bold(true).Render(SpinnerFrame(info.SpinnerFrame)))
	}
	if !info.Timestamp.IsZero() {
		parts = append(parts, SubtleStyle.Render(info.Timestamp.Format("15:04:05")))
	}
	if info.DurationMs > 0 {
		parts = append(parts, SubtleStyle.Render(FormatDurationChip(info.DurationMs)))
	}
	if info.TokenCount > 0 {
		parts = append(parts, SubtleStyle.Render(fmt.Sprintf("%s tok", FormatThousands(info.TokenCount))))
	}
	if info.ToolCalls > 0 {
		chip := fmt.Sprintf("⚒ %d", info.ToolCalls)
		if info.ToolFailures > 0 {
			parts = append(parts, AccentStyle.Render(chip)+" "+FailStyle.Bold(true).Render(fmt.Sprintf("✗ %d", info.ToolFailures)))
		} else {
			parts = append(parts, AccentStyle.Render(chip))
		}
	}
	return strings.Join(parts, " ")
}

func FormatDurationChip(ms int) string {
	if ms <= 0 {
		return ""
	}
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	if ms < 60_000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	mins := ms / 60_000
	secs := (ms % 60_000) / 1000
	return fmt.Sprintf("%dm%02ds", mins, secs)
}

var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func SpinnerFrame(frame int) string {
	if frame < 0 {
		frame = -frame
	}
	return spinnerFrames[frame%len(spinnerFrames)]
}

func RenderMessageBubble(role, content, header string, width int) string {
	style := RoleLineStyle(role)
	bar := style.Render("▎")
	out := []string{bar + " " + header}
	content = strings.TrimSpace(content)
	if content == "" {
		return strings.Join(out, "\n")
	}
	if width <= 4 {
		out = append(out, bar+" "+style.Render(content))
		return strings.Join(out, "\n")
	}
	contentWidth := width - 2
	for _, line := range RenderMarkdownBlocks(content, contentWidth) {
		for _, wrapped := range WrapBubbleLine(line, contentWidth) {
			out = append(out, bar+" "+wrapped)
		}
	}
	return strings.Join(out, "\n")
}

func WrapBubbleLine(line string, limit int) []string {
	if limit <= 0 {
		return []string{line}
	}
	if ansi.StringWidth(line) <= limit {
		return []string{line}
	}
	wrapped := ansi.Wrap(line, limit, " 	,;:.!?/\\_-")
	if wrapped == "" {
		return []string{line}
	}
	parts := strings.Split(wrapped, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if ansi.StringWidth(p) <= limit {
			out = append(out, p)
			continue
		}
		out = append(out, HardWrapByCells(p, limit)...)
	}
	return out
}

func HardWrapByCells(s string, limit int) []string {
	if limit <= 0 || ansi.StringWidth(s) <= limit {
		return []string{s}
	}
	out := []string{}
	cur := strings.Builder{}
	width := 0
	for _, r := range s {
		w := ansi.StringWidth(string(r))
		if width+w > limit {
			out = append(out, cur.String())
			cur.Reset()
			width = 0
		}
		cur.WriteRune(r)
		width += w
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func RenderDivider(width int) string {
	if width <= 0 {
		return ""
	}
	if width > 200 {
		width = 200
	}
	return DividerStyle.Render(strings.Repeat("─", width))
}

func RenderInputBox(line string, width int) string {
	if width < 10 {
		return InputLineStyle.Render(line)
	}
	inner := FormatInputBoxContent(line, width-4)
	return InputBoxStyle.Width(width).Render(InputLineStyle.Render(inner))
}

func FormatInputBoxContent(content string, limit int) string {
	if content == "" || limit <= 0 {
		return content
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	logical := strings.Split(content, "\n")
	out := make([]string, 0, len(logical))
	for _, line := range logical {
		if ansi.StringWidth(line) <= limit {
			out = append(out, line)
			continue
		}
		wrapped := ansi.Wrap(line, limit, " 	,;:.!?/\\_-")
		if wrapped == "" {
			out = append(out, line)
			continue
		}
		for _, p := range strings.Split(wrapped, "\n") {
			if ansi.StringWidth(p) <= limit {
				out = append(out, p)
				continue
			}
			out = append(out, HardWrapByCells(p, limit)...)
		}
	}
	return strings.Join(out, "\n")
}
