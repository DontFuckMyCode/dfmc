package tui

// prompts_render.go — rendering surface for the Prompts panel. Sibling
// of prompts.go which keeps the load command, key dispatch, and
// arrow-driven action menu. Pure render: filteredPrompts,
// formatPromptRow, formatPromptPreview, wrapPromptLines, nonEmpty,
// renderPromptsView, promptsTopBanner.
//
// nonEmpty is package-shared (used by provider_panel_render_legacy.go's
// row formatter too) — kept here so its only "real" render-time caller
// stays adjacent.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

func filteredPrompts(templates []promptlib.Template, query string) []promptlib.Template {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return templates
	}
	out := templates[:0:0]
	for _, t := range templates {
		hay := strings.ToLower(strings.Join([]string{
			t.ID, t.Type, t.Task, t.Role, t.Language, t.Profile, t.Description,
		}, " "))
		if strings.Contains(hay, q) {
			out = append(out, t)
		}
	}
	return out
}

// formatPromptRow renders one template as a single line. Shape:
// `▶ type · task  role/lang/profile  (compose, prio=N)  id`. The compose
// tag is only shown when explicitly set (empty compose defaults to
// replace-on-best-match and the catalog docs explain that).
func formatPromptRow(t promptlib.Template, selected bool, width int) string {
	head := accentStyle.Render(nonEmpty(t.Type, "?"))
	if t.Task != "" {
		head += subtleStyle.Render(" · ") + t.Task
	}
	axes := make([]string, 0, 3)
	if t.Role != "" {
		axes = append(axes, t.Role)
	}
	if t.Language != "" {
		axes = append(axes, t.Language)
	}
	if t.Profile != "" {
		axes = append(axes, t.Profile)
	}
	tail := ""
	if len(axes) > 0 {
		tail += "  " + subtleStyle.Render(strings.Join(axes, "/"))
	}
	meta := make([]string, 0, 2)
	if t.Compose != "" {
		meta = append(meta, t.Compose)
	}
	if t.Priority != 0 {
		meta = append(meta, fmt.Sprintf("prio=%d", t.Priority))
	}
	if len(meta) > 0 {
		tail += subtleStyle.Render("  (" + strings.Join(meta, ", ") + ")")
	}
	if t.ID != "" {
		tail += subtleStyle.Render("  " + t.ID)
	}
	marker := "  "
	if selected {
		marker = accentStyle.Render("▶ ")
	}
	line := marker + head + tail
	if width > 0 {
		line = truncateSingleLine(line, width)
	}
	return line
}

// formatPromptPreview renders the template's description and body. Body
// is split into lines and each is truncated to width; newlines are
// preserved because prompts are meant to be read as multi-line text.
func formatPromptPreview(t promptlib.Template, width int) []string {
	out := []string{}
	if t.Description != "" {
		out = append(out, "  "+subtleStyle.Render("description"))
		for _, line := range wrapPromptLines(t.Description, width) {
			out = append(out, "    "+line)
		}
		out = append(out, "")
	}
	out = append(out, "  "+subtleStyle.Render("body"))
	body := t.Body
	if len(body) > promptsPreviewChars {
		body = body[:promptsPreviewChars-1] + "…"
	}
	for _, line := range strings.Split(body, "\n") {
		if width > 0 {
			line = truncateSingleLine(line, width)
		}
		out = append(out, "    "+line)
	}
	return out
}

// wrapPromptLines is a tiny wrapper that splits a paragraph into
// width-bounded lines. Used for the description field (body preserves
// its own line breaks).
func wrapPromptLines(s string, width int) []string {
	if width <= 8 {
		return []string{s}
	}
	max := width - 6
	out := []string{}
	for _, paragraph := range strings.Split(s, "\n") {
		words := strings.Fields(paragraph)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, w := range words[1:] {
			if len(line)+1+len(w) > max {
				out = append(out, line)
				line = w
				continue
			}
			line += " " + w
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func nonEmpty(a, fallback string) string {
	if strings.TrimSpace(a) == "" {
		return fallback
	}
	return a
}

func (m Model) renderPromptsView(width int) string {
	width = clampInt(width, 24, 1000)
	banner := m.promptsTopBanner(width)
	hint := subtleStyle.Render("j/k scroll · enter preview · / search · r refresh · c clear")
	queryLine := subtleStyle.Render("query ")
	if strings.TrimSpace(m.prompts.query) != "" {
		queryLine += boldStyle.Render(m.prompts.query)
	} else {
		queryLine += subtleStyle.Render("(none)")
	}
	if m.prompts.searchActive {
		queryLine += subtleStyle.Render("  · typing, enter to commit")
	}
	lines := []string{banner, queryLine, hint, renderDivider(width - 2)}

	if m.prompts.err != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.prompts.err))
		return strings.Join(lines, "\n")
	}
	if m.prompts.loading {
		lines = append(lines, "", subtleStyle.Render("loading..."))
		return strings.Join(lines, "\n")
	}

	filtered := filteredPrompts(m.prompts.templates, m.prompts.query)
	if len(filtered) == 0 {
		lines = append(lines, "")
		if len(m.prompts.templates) == 0 {
			lines = append(lines,
				subtleStyle.Render("No prompt templates loaded."),
				subtleStyle.Render("Prompts are reusable system-prompt overlays composed by task / role / language. The library merges defaults with project + user overrides at runtime."),
				subtleStyle.Render("Add YAML/JSON/MD files under ~/.dfmc/prompts (global) or .dfmc/prompts (project), then `r` to reload."),
			)
		} else {
			lines = append(lines,
				warnStyle.Render(fmt.Sprintf("No matches for %q in %d prompts.", m.prompts.query, len(m.prompts.templates))),
				subtleStyle.Render("Press c to clear the query, or / to edit it."),
			)
		}
		return strings.Join(lines, "\n")
	}

	scroll := m.prompts.scroll
	if scroll < 0 {
		scroll = 0
	}
	if scroll >= len(filtered) {
		scroll = len(filtered) - 1
	}

	for i, t := range filtered[scroll:] {
		selected := (scroll + i) == m.prompts.scroll
		lines = append(lines, formatPromptRow(t, selected, width-2))
	}

	// Preview pane for the selected row (only when explicitly loaded).
	if m.prompts.previewID != "" && m.prompts.scroll >= 0 && m.prompts.scroll < len(filtered) {
		if filtered[m.prompts.scroll].ID == m.prompts.previewID {
			lines = append(lines, "", subtleStyle.Render("preview · "+m.prompts.previewID))
			lines = append(lines, formatPromptPreview(filtered[m.prompts.scroll], width-2)...)
		}
	}

	lines = append(lines, "", subtleStyle.Render(fmt.Sprintf(
		"%d shown · %d loaded",
		len(filtered), len(m.prompts.templates),
	)))
	out := strings.Join(lines, "\n")
	if m.actionMenu.open && m.actionMenu.owner == "Prompts" {
		out += "\n\n" + m.renderActionMenu(width)
	}
	return out
}

// promptsTopBanner — title + count chip + state chip.
func (m Model) promptsTopBanner(width int) string {
	title := titleStyle.Bold(true).Render("✎ PROMPTS")
	chipText, chipStyle := " HEALTHY ", okStyle
	switch {
	case m.prompts.err != "":
		chipText, chipStyle = " ERROR ", warnStyle
	case m.prompts.loading:
		chipText, chipStyle = " LOADING ", infoStyle
	case len(m.prompts.templates) == 0:
		chipText, chipStyle = " EMPTY ", subtleStyle
	}
	chip := chipStyle.Render(chipText)
	countChip := subtleStyle.Render(fmt.Sprintf(" %d ", len(m.prompts.templates)))
	chipStrip := countChip + " " + chip
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chipStrip)-4, 1)
	return title + strings.Repeat(" ", gap) + chipStrip
}
