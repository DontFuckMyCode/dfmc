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
		hint = subtleStyle.Render(" ["+s.Hint+"]")
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
	if strings.TrimSpace(m.plansQuery) != "" {
		queryLine += m.plansQuery
	} else {
		queryLine += subtleStyle.Render("(none — press e to enter a task)")
	}
	if m.plansInputActive {
		queryLine += subtleStyle.Render("  · typing, enter to run")
	}

	lines := []string{header, hint, queryLine, renderDivider(width - 2)}

	if m.plansErr != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.plansErr))
		return strings.Join(lines, "\n")
	}

	if m.plansPlan == nil {
		lines = append(lines,
			"",
			subtleStyle.Render("Offline task decomposer. Paste a query and press enter."),
			subtleStyle.Render("Detects numbered lists, stage markers ('first/then'), and multi-conjunctions."),
			subtleStyle.Render("High confidence + parallel=true → candidate for tool_batch_call(delegate_task)."),
		)
		return strings.Join(lines, "\n")
	}

	plan := m.plansPlan
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

	scroll := clampScroll(m.plansScroll, len(plan.Subtasks))
	for i, s := range plan.Subtasks[scroll:] {
		idx := scroll + i
		selected := idx == m.plansScroll
		lines = append(lines, formatPlansSubtaskRow(idx, s, selected, width-2))
	}

	if m.plansScroll >= 0 && m.plansScroll < len(plan.Subtasks) {
		lines = append(lines, "", subtleStyle.Render(fmt.Sprintf("subtask #%d", m.plansScroll+1)))
		lines = append(lines, formatPlansPreview(plan.Subtasks[m.plansScroll], width-2)...)
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
	q := strings.TrimSpace(m.plansQuery)
	if q == "" {
		m.plansPlan = nil
		m.plansErr = "task is empty"
		return m
	}
	m.plansErr = ""
	p := planning.SplitTask(q)
	m.plansPlan = &p
	m.plansScroll = 0
	return m
}

func (m Model) handlePlansKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.plansInputActive {
		return m.handlePlansInputKey(msg)
	}
	total := 0
	if m.plansPlan != nil {
		total = len(m.plansPlan.Subtasks)
	}
	step := 1
	pageStep := 10
	switch msg.String() {
	case "e":
		m.plansInputActive = true
		return m, nil
	case "j", "down":
		if m.plansScroll+step < total {
			m.plansScroll += step
		}
	case "k", "up":
		if m.plansScroll >= step {
			m.plansScroll -= step
		} else {
			m.plansScroll = 0
		}
	case "pgdown":
		if m.plansScroll+pageStep < total {
			m.plansScroll += pageStep
		} else if total > 0 {
			m.plansScroll = total - 1
		}
	case "pgup":
		if m.plansScroll >= pageStep {
			m.plansScroll -= pageStep
		} else {
			m.plansScroll = 0
		}
	case "g":
		m.plansScroll = 0
	case "G":
		if total > 0 {
			m.plansScroll = total - 1
		}
	case "c":
		m.plansQuery = ""
		m.plansPlan = nil
		m.plansErr = ""
		m.plansScroll = 0
	case "enter":
		// Re-run with the current query — cheap, and gives the user a
		// way to reload without editing.
		if strings.TrimSpace(m.plansQuery) != "" {
			m = m.runPlansSplit()
		}
	}
	return m, nil
}

func (m Model) handlePlansInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		m.plansInputActive = false
		m = m.runPlansSplit()
		return m, nil
	case tea.KeyEsc:
		m.plansInputActive = false
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.plansQuery); len(r) > 0 {
			m.plansQuery = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.plansQuery += msg.String()
		return m, nil
	}
	return m, nil
}
