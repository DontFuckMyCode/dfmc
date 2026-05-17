package theme

// render.go — theme rendering primitives shared by every other render
// surface in this package: role badges + line styles, the section
// header glyph, the runtime + todo strips, and the ANSI-aware
// TruncateSingleLine helper. All functions operate purely on data and
// lipgloss styles with no engine or model dependencies.
//
// Companion siblings (extracted to keep this file scannable):
//
//   - render_chat_header.go   CHAT header bar + workflow focus card
//   - render_message.go       message header, bubble, wrap helpers,
//                             divider, input box
//   - render_starters.go      first-run starter prompt grid +
//                             streaming + resume banners

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// --- role helpers -------------------------------------------------------

func RoleBadge(role string) string {
	role = strings.ToLower(strings.TrimSpace(role))
	switch role {
	case "user":
		return BadgeUserStyle.Render("USER")
	case "assistant":
		return BadgeAssistantStyle.Render("ASSISTANT")
	case "tool":
		return BadgeToolStyle.Render("TOOL")
	case "coach":
		return BadgeCoachStyle.Render("COACH")
	default:
		return BadgeSystemStyle.Render("SYS")
	}
}

func RoleLineStyle(role string) lipgloss.Style {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "user":
		return UserLineStyle
	case "assistant":
		return AssistantLineStyle
	case "tool":
		return ToolStyle
	case "coach":
		return CoachLineStyle
	default:
		return SystemLineStyle
	}
}

// --- section header -----------------------------------------------------

func SectionHeader(icon, label string) string {
	icon = strings.TrimSpace(icon)
	label = strings.TrimSpace(label)
	if icon == "" {
		return SectionTitleStyle.Render(label)
	}
	return SectionTitleStyle.Render(icon + " " + label)
}

// --- todo strip ---------------------------------------------------------

func RenderTodoStrip(items []TodoStripItem, width int) string {
	if len(items) == 0 {
		return ""
	}
	if width < 24 {
		width = 24
	}

	var done, doing, pending int
	var activeText string
	for _, it := range items {
		switch strings.ToLower(strings.TrimSpace(it.Status)) {
		case "completed", "done":
			done++
		case "in_progress", "active", "doing":
			doing++
			if activeText == "" {
				activeText = strings.TrimSpace(it.ActiveForm)
				if activeText == "" {
					activeText = strings.TrimSpace(it.Content)
				}
			}
		default:
			pending++
		}
	}
	if done == 0 && doing == 0 && pending == 0 {
		return ""
	}

	parts := []string{}
	if done > 0 {
		parts = append(parts, OkStyle.Render(fmt.Sprintf("%d done", done)))
	}
	if doing > 0 {
		parts = append(parts, AccentStyle.Render(fmt.Sprintf("%d doing", doing)))
	}
	if pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", pending))
	}
	headline := SubtleStyle.Render("▸ TODOs · " + strings.Join(parts, " · "))
	if activeText != "" {
		headline += " " + SubtleStyle.Render("→ "+TruncateSingleLine(activeText, width-30))
	}
	return "    " + TruncateSingleLine(headline, width-4)
}

// --- runtime card -------------------------------------------------------

func RenderRuntimeCard(rs RuntimeSummary, width int) string {
	if !rs.Active {
		return ""
	}
	parts := []string{}
	if rs.ToolRounds > 0 {
		parts = append(parts, SubtleStyle.Render(fmt.Sprintf("tools %d", rs.ToolRounds)))
	}
	if tool := strings.TrimSpace(rs.LastTool); tool != "" {
		icon, style := ChipIconStyle(rs.LastStatus)
		tail := icon + " " + tool
		if rs.LastDuration > 0 {
			tail += fmt.Sprintf(" %dms", rs.LastDuration)
		}
		parts = append(parts, style.Render(tail))
	}
	if len(parts) == 0 {
		return ""
	}
	return TruncateSingleLine(strings.Join(parts, "  ·  "), width)
}

// TruncateSingleLine truncates text to fit within width cells, adding "…"
// if truncation occurred. Exported so callers in ui/tui can use it directly.
func TruncateSingleLine(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(text) <= width {
		return text
	}
	// Count usable width excluding the "…"
	ellipsis := "…"
	ellipsisWidth := ansi.StringWidth(ellipsis)
	usable := width - ellipsisWidth
	if usable <= 0 {
		return ellipsis
	}
	var b strings.Builder
	widthSoFar := 0
	for _, r := range text {
		w := ansi.StringWidth(string(r))
		if widthSoFar+w > usable {
			b.WriteString(ellipsis)
			break
		}
		b.WriteRune(r)
		widthSoFar += w
	}
	return b.String()
}
