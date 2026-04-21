package tui

import (
	"fmt"
	"strings"
)

// renderSlashPickerModal frames the `/` command picker in the same bordered
// modal style as the file picker for a consistent picker experience.
func renderSlashPickerModal(items []slashCommandItem, slashIndex, width int) string {
	if width < 40 {
		width = 40
	}
	title := accentStyle.Bold(true).Render("◆ Commands") +
		subtleStyle.Render("  —  type to filter, enter to run")

	count := ""
	if len(items) > 0 {
		count = subtleStyle.Render(fmt.Sprintf("%d matching · window of 6", len(items)))
	} else {
		count = warnStyle.Render("no match")
	}

	body := []string{}
	if len(items) == 0 {
		body = append(body,
			subtleStyle.Render("No command matched the current prefix."),
			subtleStyle.Render("Press esc to dismiss, or /help for the full catalog."),
		)
	} else {
		selected := clampIndex(slashIndex, len(items))
		start := 0
		if selected > 4 {
			start = selected - 4
		}
		end := start + 6
		if end > len(items) {
			end = len(items)
		}
		for i := start; i < end; i++ {
			line := fmt.Sprintf("%s  %s", items[i].Template, subtleStyle.Render("· "+items[i].Description))
			label := truncateSingleLine(line, width-6)
			if i == selected {
				selectedLine := fmt.Sprintf("▶ %s\n  %s", items[i].Template, items[i].Description)
				body = append(body, mentionSelectedRowStyle.Render(selectedLine))
			} else {
				body = append(body, "  "+label)
			}
		}
	}

	footer := subtleStyle.Render("↑/↓ move · tab cycle · enter run · esc cancel")

	parts := []string{title, count, ""}
	parts = append(parts, body...)
	parts = append(parts, "", footer)
	return mentionPickerStyle.Width(width).Render(strings.Join(parts, "\n"))
}
