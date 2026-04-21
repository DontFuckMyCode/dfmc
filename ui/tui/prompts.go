package tui

// prompts.go — the Prompts panel is a read view over internal/promptlib:
// the full template library after defaults, ~/.dfmc/prompts, and
// .dfmc/prompts overrides are merged. The goal is debug-by-inspection —
// users can see which template would win for a task/role combo and why,
// without having to re-run an Ask to watch the bundle rendering code.
//
// Shape: a list of promptlib.Template, a search query, a scroll offset,
// and an optional preview of the currently highlighted body. Because
// promptlib instances are built ad-hoc at every call site in DFMC (the
// CLI and Web do the same), this panel also builds its own on refresh —
// which means it always reflects what a *fresh* render would see.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
)

const (
	// promptsPreviewChars caps the rendered body length so one monster
	// template can't eat the whole panel.
	promptsPreviewChars = 4000
)

type promptsLoadedMsg struct {
	templates []promptlib.Template
	err       error
}

func loadPromptsCmd(eng *engine.Engine) tea.Cmd {
	return func() tea.Msg {
		lib := promptlib.New()
		// Project root may be blank in degraded-startup mode — that's fine,
		// LoadOverrides just skips the empty root.
		root := ""
		if eng != nil {
			root = eng.ProjectRoot
		}
		if err := lib.LoadOverrides(root); err != nil {
			return promptsLoadedMsg{err: err}
		}
		return promptsLoadedMsg{templates: lib.List()}
	}
}

// filteredPrompts narrows the list with a case-insensitive substring
// match over ID / Type / Task / Role / Language / Profile / Description.
// Body is deliberately excluded — it's long and matching it would make
// every "what do I have?" query look like a full-text search.
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
	hint := subtleStyle.Render("j/k scroll · enter preview · / search · r refresh · c clear search")
	header := sectionHeader("✎", "Prompts")
	queryLine := subtleStyle.Render("query: ")
	if strings.TrimSpace(m.prompts.query) != "" {
		queryLine += m.prompts.query
	} else {
		queryLine += subtleStyle.Render("(none)")
	}
	if m.prompts.searchActive {
		queryLine += subtleStyle.Render("  · typing, enter to commit")
	}
	lines := []string{header, hint, queryLine, renderDivider(width - 2)}

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
		lines = append(lines, "",
			subtleStyle.Render("No prompt templates loaded."),
			subtleStyle.Render("Add YAML/JSON/MD files to ~/.dfmc/prompts or .dfmc/prompts."),
		)
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
	return strings.Join(lines, "\n")
}

func (m Model) handlePromptsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.prompts.searchActive {
		return m.handlePromptsSearchKey(msg)
	}
	total := len(filteredPrompts(m.prompts.templates, m.prompts.query))
	step := 1
	pageStep := 10
	switch msg.String() {
	case "j", "down":
		if m.prompts.scroll+step < total {
			m.prompts.scroll += step
		}
	case "k", "up":
		if m.prompts.scroll >= step {
			m.prompts.scroll -= step
		} else {
			m.prompts.scroll = 0
		}
	case "pgdown":
		if m.prompts.scroll+pageStep < total {
			m.prompts.scroll += pageStep
		} else if total > 0 {
			m.prompts.scroll = total - 1
		}
	case "pgup":
		if m.prompts.scroll >= pageStep {
			m.prompts.scroll -= pageStep
		} else {
			m.prompts.scroll = 0
		}
	case "g":
		m.prompts.scroll = 0
	case "G":
		if total > 0 {
			m.prompts.scroll = total - 1
		}
	case "enter":
		filtered := filteredPrompts(m.prompts.templates, m.prompts.query)
		if len(filtered) == 0 || m.prompts.scroll < 0 || m.prompts.scroll >= len(filtered) {
			return m, nil
		}
		m.prompts.previewID = filtered[m.prompts.scroll].ID
		return m, nil
	case "r":
		m.prompts.loading = true
		m.prompts.err = ""
		return m, loadPromptsCmd(m.eng)
	case "/":
		m.prompts.searchActive = true
		return m, nil
	case "c":
		m.prompts.query = ""
		m.prompts.scroll = 0
	}
	return m, nil
}

func (m Model) handlePromptsSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.prompts.searchActive = false
		m.prompts.scroll = 0
		return m, nil
	case tea.KeyEsc:
		m.prompts.searchActive = false
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.prompts.query); len(r) > 0 {
			m.prompts.query = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.prompts.query += msg.String()
		return m, nil
	}
	return m, nil
}
