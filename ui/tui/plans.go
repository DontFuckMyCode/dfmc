package tui

// plans.go — the Plans panel exposes the deterministic task decomposer
// in internal/planning. It's a "would this query fan out?" diagnostic:
// the user types a task, we run SplitTask against it, and render the
// subtasks with their split hint, the parallel/serial verdict, and a
// confidence meter.
//
// Shape: a query string, a cached Plan, a scroll offset into the subtask
// list, and an edit-mode flag that routes keypresses into the input.
// Computation is offline (pattern matching in planning/splitter.go), so
// there's no async load — Enter commits the query and renders in-place.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/planning"
)

const (
	// plansDescriptionChars caps the preview body so a pasted multi-line
	// query can't push the rest of the view off-screen.
	plansDescriptionChars = 600
)

// plansConfidenceLabel maps a [0,1] score to a short qualitative tag.
// Thresholds mirror the docstrings in task_split.go's Prompt field so
// the panel teaches the same "when to fan out" heuristic.
func plansConfidenceLabel(c float64) string {
	switch {
	case c >= 0.7:
		return "strong"
	case c >= 0.4:
		return "weak"
	default:
		return "none"
	}
}

// plansConfidenceBar renders a 10-wide unicode meter for the confidence
// score so the eye catches "strong/weak/none" without reading the
// number. 0.0 → empty, 1.0 → full.
func plansConfidenceBar(c float64) string {
	total := 10
	filled := int(c * float64(total))
	if filled < 0 {
		filled = 0
	}
	if filled > total {
		filled = total
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", total-filled)
	return bar
}

// formatPlansSubtaskRow renders one subtask line. Shape:
// `▶ 2. [stage] title line here`.
func formatPlansSubtaskRow(i int, s planning.Subtask, selected bool, width int) string {
	marker := "  "
	if selected {
		marker = accentStyle.Render("▶ ")
	}
	head := fmt.Sprintf("%d.", i+1)
	hint := ""
	if s.Hint != "" {
		hint = subtleStyle.Render(" [" + s.Hint + "]")
	}
	title := s.Title
	if title == "" {
		title = s.Description
	}
	line := marker + head + hint + " " + title
	if width > 0 {
		line = truncateSingleLine(line, width)
	}
	return line
}

// formatPlansPreview renders the highlighted subtask's full description
// under the list so the user can see what the agent loop would actually
// send if this subtask were fanned out.
func formatPlansPreview(s planning.Subtask, width int) []string {
	body := s.Description
	if len(body) > plansDescriptionChars {
		body = body[:plansDescriptionChars-1] + "…"
	}
	out := []string{"  " + subtleStyle.Render("description")}
	for _, line := range wrapPromptLines(body, width) {
		out = append(out, "    "+line)
	}
	return out
}

func (m Model) renderPlansView(width int) string {
	width = clampInt(width, 24, 1000)
	hint := subtleStyle.Render("e edit · enter run · esc cancel edit · j/k scroll · c clear")
	header := sectionHeader("⇄", "Plans")

	// Query line: mirror the search-style "typing" badge from other panels.
	queryLine := subtleStyle.Render("task: ")
	if strings.TrimSpace(m.plans.query) != "" {
		queryLine += m.plans.query
	} else {
		queryLine += subtleStyle.Render("(none — press e to enter a task)")
	}
	if m.plans.inputActive {
		queryLine += subtleStyle.Render("  · typing, enter to run")
	}

	lines := []string{header, hint, queryLine, renderDivider(width - 2)}

	if m.plans.err != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.plans.err))
		return strings.Join(lines, "\n")
	}

	if m.plans.plan == nil {
		lines = append(lines,
			"",
			subtleStyle.Render("Offline task decomposer. Paste a query and press enter."),
			subtleStyle.Render("Detects numbered lists, stage markers ('first/then'), and multi-conjunctions."),
			subtleStyle.Render("High confidence + parallel=true → candidate for tool_batch_call(delegate_task)."),
		)
		return strings.Join(lines, "\n")
	}

	plan := m.plans.plan
	parallelTag := "serial"
	if plan.Parallel {
		parallelTag = "parallel"
	}
	if len(plan.Subtasks) <= 1 {
		parallelTag = "single"
	}

	summary := fmt.Sprintf(
		"%d subtasks · %s · confidence %s %.2f (%s)",
		len(plan.Subtasks),
		parallelTag,
		plansConfidenceBar(plan.Confidence),
		plan.Confidence,
		plansConfidenceLabel(plan.Confidence),
	)
	lines = append(lines, subtleStyle.Render(summary), "")

	if len(plan.Subtasks) == 0 {
		lines = append(lines, subtleStyle.Render("Splitter declined — treat the query as a single task."))
		return strings.Join(lines, "\n")
	}

	scroll := clampScroll(m.plans.scroll, len(plan.Subtasks))
	for i, s := range plan.Subtasks[scroll:] {
		idx := scroll + i
		selected := idx == m.plans.scroll
		lines = append(lines, formatPlansSubtaskRow(idx, s, selected, width-2))
	}

	if m.plans.scroll >= 0 && m.plans.scroll < len(plan.Subtasks) {
		lines = append(lines, "", subtleStyle.Render(fmt.Sprintf("subtask #%d", m.plans.scroll+1)))
		lines = append(lines, formatPlansPreview(plan.Subtasks[m.plans.scroll], width-2)...)
	}

	if !plan.Parallel && len(plan.Subtasks) > 1 {
		lines = append(lines, "", subtleStyle.Render("▸ Serial plan — run subtasks one after another, feeding results forward."))
	} else if plan.Parallel && plan.Confidence >= 0.7 {
		lines = append(lines, "", subtleStyle.Render("▸ Strong parallel split — candidate for tool_batch_call(delegate_task)."))
	}

	return strings.Join(lines, "\n")
}

// runPlansSplit computes the plan for the current query and stamps it
// into the model. Pure function — no engine round-trip.
func (m Model) runPlansSplit() Model {
	q := strings.TrimSpace(m.plans.query)
	if q == "" {
		m.plans.plan = nil
		m.plans.err = "task is empty"
		return m
	}
	m.plans.err = ""
	p := planning.SplitTask(q)
	m.plans.plan = &p
	m.plans.scroll = 0
	return m
}

func (m Model) handlePlansKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.plans.inputActive {
		return m.handlePlansInputKey(msg)
	}
	total := 0
	if m.plans.plan != nil {
		total = len(m.plans.plan.Subtasks)
	}
	step := 1
	pageStep := 10
	switch msg.String() {
	case "e":
		m.plans.inputActive = true
		return m, nil
	case "j", "down":
		if m.plans.scroll+step < total {
			m.plans.scroll += step
		}
	case "k", "up":
		if m.plans.scroll >= step {
			m.plans.scroll -= step
		} else {
			m.plans.scroll = 0
		}
	case "pgdown":
		if m.plans.scroll+pageStep < total {
			m.plans.scroll += pageStep
		} else if total > 0 {
			m.plans.scroll = total - 1
		}
	case "pgup":
		if m.plans.scroll >= pageStep {
			m.plans.scroll -= pageStep
		} else {
			m.plans.scroll = 0
		}
	case "g":
		m.plans.scroll = 0
	case "G":
		if total > 0 {
			m.plans.scroll = total - 1
		}
	case "c":
		m.plans.query = ""
		m.plans.plan = nil
		m.plans.err = ""
		m.plans.scroll = 0
	case "enter":
		// Re-run with the current query — cheap, and gives the user a
		// way to reload without editing.
		if strings.TrimSpace(m.plans.query) != "" {
			m = m.runPlansSplit()
		}
	}
	return m, nil
}

func (m Model) handlePlansInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.plans.inputActive = false
		m = m.runPlansSplit()
		return m, nil
	case tea.KeyEsc:
		m.plans.inputActive = false
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.plans.query); len(r) > 0 {
			m.plans.query = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.plans.query += msg.String()
		return m, nil
	}
	return m, nil
}
