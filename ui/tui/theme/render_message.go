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
	"time"

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
		stamp := info.Timestamp.Format("15:04:05")
		if rel := FormatRelativeTime(info.Timestamp, info.Now); rel != "" {
			stamp = stamp + " " + rel
		}
		parts = append(parts, SubtleStyle.Render(stamp))
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
	if badge := FormatModelBadge(info.Provider, info.Model); badge != "" {
		parts = append(parts, SubtleStyle.Render(badge))
	}
	if info.Cancelled {
		parts = append(parts, WarnStyle.Bold(true).Render("⊘ cancelled"))
	} else if info.Done && !info.Streaming {
		// Streaming wins over Done — the spinner is the user-visible
		// signal in flight, and a static ✓ next to a spinning braille
		// frame would read as a contradiction.
		parts = append(parts, OkStyle.Render("✓"))
	}
	return strings.Join(parts, " ")
}

// FormatRelativeTime renders a "(2m ago)" / "(just now)" suffix for the
// given timestamp against the reference now. Returns "" if either time
// is zero or the gap is implausible (negative / >30 days), keeping the
// header free of bogus chips when timestamps are missing.
func FormatRelativeTime(ts, now time.Time) string {
	if ts.IsZero() || now.IsZero() {
		return ""
	}
	d := now.Sub(ts)
	if d < 0 {
		return ""
	}
	if d > 30*24*time.Hour {
		return ""
	}
	switch {
	case d < 30*time.Second:
		return "(just now)"
	case d < time.Minute:
		return fmt.Sprintf("(%ds ago)", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("(%dm ago)", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		mins := int(d.Minutes()) - h*60
		if mins == 0 {
			return fmt.Sprintf("(%dh ago)", h)
		}
		return fmt.Sprintf("(%dh%02dm ago)", h, mins)
	default:
		return fmt.Sprintf("(%dd ago)", int(d.Hours()/24))
	}
}

// FormatModelBadge renders the provider/model chip shown in headers.
// Returns "" when both fields are empty so the header degrades cleanly.
// Model names are already unique enough (claude-opus-4-7, gpt-4o,
// kimi-k2, etc.) that adding the provider in front mostly just doubles
// the width — so when both are set we show only the model.
func FormatModelBadge(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if model != "" {
		return "· " + model
	}
	if provider != "" {
		return "· " + provider
	}
	return ""
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
