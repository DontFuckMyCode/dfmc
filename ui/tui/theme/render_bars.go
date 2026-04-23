package theme

// render_bars.go — progress bars, token meters, and numeric formatters.
//
// Split out of render.go so the "how do I show a progress/context
// meter" surface lives in one focused file. Everything here consumes
// `used/max` pairs or raw integers and returns styled strings — no
// engine state, no lipgloss styles defined here (those stay in
// styles.go). Adding a new meter variant usually means copying one of
// these functions and tweaking thresholds; keeping them together
// makes that low-risk.

import (
	"fmt"
	"strings"
)

func RenderTokenMeter(used, max int) string {
	if max <= 0 {
		if used <= 0 {
			return SubtleStyle.Render("ctx —")
		}
		return SubtleStyle.Render("ctx ") + BoldStyle.Render(FormatThousands(used)+" tok")
	}
	pct := 0
	if used > 0 {
		pct = int((int64(used) * 100) / int64(max))
	}
	style := OkStyle
	switch {
	case pct >= 85:
		style = FailStyle
	case pct >= 60:
		style = WarnStyle
	}
	label := fmt.Sprintf("%s / %s (%d%%)", FormatThousands(used), FormatThousands(max), pct)
	return SubtleStyle.Render("ctx ") + style.Bold(true).Render(label)
}

func RenderStepBar(step, maxSteps, cells, frame int) string {
	if cells < 4 {
		cells = 4
	}
	if maxSteps <= 0 {
		return SubtleStyle.Render(fmt.Sprintf("step %d", step))
	}
	if step < 0 {
		step = 0
	}
	if step > maxSteps {
		step = maxSteps
	}
	filled := (step * cells) / maxSteps
	if step > 0 && filled == 0 {
		filled = 1
	}
	style := OkStyle
	remaining := maxSteps - step
	switch {
	case remaining <= 1:
		style = FailStyle
	case remaining <= 3:
		style = WarnStyle
	}
	filledStr := strings.Repeat("█", filled)
	if filled > 0 && step < maxSteps && frame%2 == 1 {
		filledStr = strings.Repeat("█", filled-1) + "▓"
	}
	bar := style.Render(filledStr) + SubtleStyle.Render(strings.Repeat("░", cells-filled))
	label := fmt.Sprintf(" %d/%d", step, maxSteps)
	return "[" + bar + "]" + style.Bold(true).Render(label)
}

func RenderContextBar(used, max, cells int) string {
	return RenderContextBarFrame(used, max, cells, 0)
}

func RenderContextBarFrame(used, max, cells, frame int) string {
	if cells < 4 {
		cells = 4
	}
	if max <= 0 {
		return RenderTokenMeter(used, max)
	}
	pct := 0
	if used > 0 {
		pct = int((int64(used) * 100) / int64(max))
		if pct > 100 {
			pct = 100
		}
	}
	filled := (pct * cells) / 100
	if used > 0 && filled == 0 {
		filled = 1
	}
	style := OkStyle
	switch {
	case pct >= 85:
		style = FailStyle
	case pct >= 60:
		style = WarnStyle
	}
	filledStr := strings.Repeat("█", filled)
	if filled > 0 && filled < cells && pct >= 60 && frame%2 == 1 {
		filledStr = strings.Repeat("█", filled-1) + "▓"
	}
	bar := style.Render(filledStr) + SubtleStyle.Render(strings.Repeat("░", cells-filled))
	label := fmt.Sprintf("%s/%s (%d%%)", CompactTokens(used), CompactTokens(max), pct)
	return "[" + bar + "] " + style.Bold(true).Render(label)
}

func CompactTokens(n int) string {
	if n < 1_000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		if n%1_000 == 0 {
			return fmt.Sprintf("%dk", n/1_000)
		}
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	}
	if n%1_000_000 == 0 {
		return fmt.Sprintf("%dM", n/1_000_000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}

func FormatThousands(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	rem := len(s) % 3
	if rem > 0 {
		b.WriteString(s[:rem])
		if len(s) > rem {
			b.WriteString(",")
		}
	}
	for i := rem; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteString(",")
		}
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
