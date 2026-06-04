package tui

// plans_render.go — pure render layer for the Plans panel: the small
// visual primitives (confidence label/bar, subtask row, preview body),
// the top banner, and the renderPlansView / renderPlansViewInner
// orchestrator. Sibling of plans.go which keeps the panel state
// shape, the SplitTask runner, the arrow-only action menu, and the
// keyboard router.
//
// Splitting the render side out keeps plans.go scoped to "what is
// the panel state and how does the user drive it" while this file
// owns "how does that state turn into glyphs on the screen" — the
// two-axis confidence meter, the parallel/serial summary, the
// per-subtask row, the description preview, and the strong-parallel
// vs serial-walk hint at the bottom.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/planning"
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
	body := truncateStr(s.Description, plansDescriptionChars)
	out := []string{"  " + subtleStyle.Render("description")}
	for _, line := range wrapPromptLines(body, width) {
		out = append(out, "    "+line)
	}
	return out
}

// plansTopBanner — title + state chip. EMPTY (no plan), TYPING (input
// active), READY (plan loaded), ERROR.
func (m Model) plansTopBanner(width int) string {
	title := titleStyle.Bold(true).Render("⇄ PLANS")
	chipText, chipStyle := " EMPTY ", subtleStyle
	switch {
	case m.plans.err != "":
		chipText, chipStyle = " ERROR ", warnStyle
	case m.plans.inputActive:
		chipText, chipStyle = " TYPING ", infoStyle
	case m.plans.plan != nil:
		chipText, chipStyle = " READY ", okStyle
	}
	chip := chipStyle.Render(chipText)
	gap := max(width-lipgloss.Width(title)-lipgloss.Width(chip)-4, 1)
	return title + strings.Repeat(" ", gap) + chip
}

func (m Model) renderPlansView(width int) string {
	return m.renderPlansViewSized(width, 24)
}

func (m Model) renderPlansViewSized(width, height int) string {
	out := m.renderPlansViewInnerSized(width, height)
	if m.actionMenu.open && m.actionMenu.owner == "Plans" {
		out += "\n\n" + m.renderActionMenu(width)
	}
	return out
}

func (m Model) renderPlansViewInnerSized(width, height int) string {
	width = clampInt(width, 24, 1000)
	height = max(height, 8)
	banner := m.plansTopBanner(width)
	// Plans uses `e` (edit task) instead of `/` (search) because the
	// query here is a SplitTask input, not a row filter. panelIdleHint
	// would mis-claim `/ search` so we hand-roll the hint to match
	// what the key handler actually accepts.
	hint := subtleStyle.Render("↑↓ scroll · e edit task · enter re-run · c clear · → action menu")

	queryLine := subtleStyle.Render("task ")
	if strings.TrimSpace(m.plans.query) != "" {
		queryLine += boldStyle.Render(m.plans.query)
	} else {
		queryLine += subtleStyle.Render("(none — press e to enter a task)")
	}
	if m.plans.inputActive {
		queryLine += subtleStyle.Render("  · typing, enter to run")
	}

	lines := []string{banner, queryLine, hint, renderDivider(width - 2)}

	if m.plans.err != "" {
		lines = append(lines, "", warnStyle.Render("error · "+m.plans.err),
			subtleStyle.Render("Press e to edit the task and try again."))
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

	cursor := clampScroll(m.plans.scroll, len(plan.Subtasks))
	rowBudget := max(height-len(lines)-8, 1)
	start, end := scrollWindow(cursor, len(plan.Subtasks), rowBudget)
	for idx := start; idx < end; idx++ {
		s := plan.Subtasks[idx]
		selected := idx == cursor
		lines = append(lines, formatPlansSubtaskRow(idx, s, selected, width-2))
	}

	if cursor >= 0 && cursor < len(plan.Subtasks) {
		lines = append(lines, "", subtleStyle.Render(fmt.Sprintf("subtask #%d", cursor+1)))
		lines = append(lines, formatPlansPreview(plan.Subtasks[cursor], width-2)...)
	}

	if !plan.Parallel && len(plan.Subtasks) > 1 {
		lines = append(lines, "", subtleStyle.Render("▸ Serial plan — run subtasks one after another, feeding results forward."))
	} else if plan.Parallel && plan.Confidence >= 0.7 {
		lines = append(lines, "", subtleStyle.Render("▸ Strong parallel split — candidate for tool_batch_call(delegate_task)."))
	}

	return strings.Join(lines, "\n")
}
