// render_workflow.go — the Workflow tab (Drive cockpit) rendering +
// keyboard surface. A two-column panel: run selector on the left, TODO
// tree on the right, with an overlaid routing editor for configuring
// drive.Config.Routing (ProviderTag -> profile name). Nothing here
// starts drive runs — it just displays state supplied by workflow
// events and hands edits back via persistDriveRoutingProjectConfig.
//
//   - renderWorkflowView / selectedRunForWorkflow / renderWorkflowTreeRows /
//     renderWorkflowTodoDetail: the display.
//   - todoStatusIcon / renderRunStatusChip: tiny status glyphs shared
//     between the list and the tree.
//   - handleWorkflowKey / handleRoutingEditorKey /
//     cycleWorkflowTodoExpand: keyboard surface, including the
//     routing-editor submodal (enter / + / d / esc).
//   - renderRoutingEditor / workflowRouting: routing-editor overlay
//     and its draft map accessor.

package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

// renderWorkflowView shows the Drive TODO tree panel. Two-column layout:
// LEFT (30%): run selector list — ID, task truncated, status chip, age
// RIGHT (70%): TODO tree for the selected run — hierarchical view of
// pending/running/done/blocked/skipped TODOs with expand/collapse detail.
func (m Model) renderWorkflowView(width int) string {
	listWidth := width * 30 / 100
	if listWidth < 28 {
		listWidth = 28
	}
	if listWidth > width-28 {
		listWidth = width / 2
	}
	detailWidth := width - listWidth - 3

	runs := m.workflow.runs
	listLines := []string{
		sectionHeader("\u26a1", "Workflow"),
		subtleStyle.Render("enter select \u00b7 j/k move \u00b7 r routing"),
		renderDivider(listWidth - 2),
		"",
	}
	if len(runs) == 0 {
		listLines = append(listLines,
			subtleStyle.Render("No drive runs yet."),
			"",
			subtleStyle.Render("Start one from Chat:"),
			subtleStyle.Render("  /drive <task description>"),
		)
	} else {
		for i, r := range runs {
			prefix := "  "
			label := truncateForLine(r.ID, 8) + "  " + truncateForLine(r.Task, listWidth-14)
			if r.ID == m.workflow.selectedRunID {
				prefix = "> "
				label = titleStyle.Render(label)
			} else if i == m.workflow.selectedIndex {
				prefix = "> "
			}
			statusChip := renderRunStatusChip(r.Status)
			listLines = append(listLines, prefix+label+"  "+statusChip)
		}
	}

	// Show routing summary in the header when routing draft has entries
	if routing := m.workflow.routingDraft; len(routing) > 0 {
		var parts []string
		keys := make([]string, 0, len(routing))
		for k := range routing {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, tag := range keys {
			parts = append(parts, tag+"\u2192"+routing[tag])
		}
		if len(parts) > 0 {
			listLines = append(listLines, "", subtleStyle.Render("routing: "+strings.Join(parts, " \u00b7 ")))
		}
	}

	var detailLines []string
	selectedRun := m.selectedRunForWorkflow()
	if selectedRun == nil {
		detailLines = []string{
			sectionHeader("\u25c9", "Run Details"),
			renderDivider(detailWidth - 2),
			"",
			subtleStyle.Render("Select a run from the list to inspect its TODO tree."),
		}
	} else {
		_done, _blocked, _skipped, _pending := selectedRun.Counts()
		running := 0
		for _, t := range selectedRun.Todos {
			if t.Status == drive.TodoRunning {
				running++
			}
		}
		done, blocked, skipped, pending := _done, _blocked, _skipped, _pending
		detailLines = []string{
			sectionHeader("\u26a1", truncateForLine(selectedRun.Task, detailWidth-20)),
			fmt.Sprintf("%s  %s  %s  %s  %s  %s",
				renderRunStatusChip(selectedRun.Status),
				doneStyle.Render(fmt.Sprintf("%d done", done)),
				pendingStyle.Render(fmt.Sprintf("%d pending", pending)),
				runningStyle.Render(fmt.Sprintf("%d running", running)),
				blockedStyle.Render(fmt.Sprintf("%d blocked", blocked)),
				skippedStyle.Render(fmt.Sprintf("%d skipped", skipped)),
			),
			renderDivider(detailWidth - 2),
			"",
		}
		rows := m.renderWorkflowTreeRows(selectedRun, detailWidth)
		detailLines = append(detailLines, rows...)

		// Show TODO detail below tree when a TODO is selected
		if m.workflow.selectedTodoID != "" {
			detailLines = append(detailLines, "", renderDivider(detailWidth-2), "")
			detailLines = append(detailLines, m.renderWorkflowTodoDetail(selectedRun, detailWidth)...)
		}
	}

	left := lipgloss.NewStyle().Width(listWidth).Render(strings.Join(listLines, "\n"))
	right := lipgloss.NewStyle().Width(detailWidth).Render(strings.Join(detailLines, "\n"))

	// Show routing editor overlay when active
	if m.workflow.showRoutingEditor {
		routingPanel := m.renderRoutingEditor(width)
		return routingPanel
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "   ", right)
}

func (m Model) selectedRunForWorkflow() *drive.Run {
	if m.workflow.selectedRunID == "" {
		return nil
	}
	for _, r := range m.workflow.runs {
		if r.ID == m.workflow.selectedRunID {
			return r
		}
	}
	return nil
}

func (m Model) renderWorkflowTreeRows(run *drive.Run, width int) []string {
	if run == nil || len(run.Todos) == 0 {
		return []string{subtleStyle.Render("(no TODOs \u2014 run may still be planning)")}
	}

	kids := make(map[string][]*drive.Todo)
	var roots []*drive.Todo
	todoMap := make(map[string]*drive.Todo)
	for i := range run.Todos {
		t := &run.Todos[i]
		todoMap[t.ID] = t
	}
	for i := range run.Todos {
		t := &run.Todos[i]
		if t.ParentID == "" || todoMap[t.ParentID] == nil {
			roots = append(roots, t)
		} else {
			kids[t.ParentID] = append(kids[t.ParentID], t)
		}
	}

	var rows []string
	var walk func(t *drive.Todo, depth int)
	walk = func(t *drive.Todo, depth int) {
		prefix := strings.Repeat("  ", depth)
		icon := todoStatusIcon(t.Status)
		expanded := m.workflow.expandedTodo[t.ID]
		expandMark := " "
		if _, hasKids := kids[t.ID]; hasKids {
			if expanded {
				expandMark = "\u25bc"
			} else {
				expandMark = "\u25b6"
			}
		}
		title := truncateForLine(t.Title, width-depth*2-8)
		line := prefix + icon + expandMark + " " + title
		tagStr := ""
		if t.ProviderTag != "" {
			tagStr += subtleStyle.Render("[" + t.ProviderTag + "]")
		}
		if t.WorkerClass != "" {
			tagStr += subtleStyle.Render("[" + t.WorkerClass + "]")
		}
		if tagStr != "" {
			line += "  " + tagStr
		}
		rows = append(rows, line)
		if expanded {
			for _, child := range kids[t.ID] {
				walk(child, depth+1)
			}
		}
	}
	for _, root := range roots {
		walk(root, 0)
	}
	return rows
}

func todoStatusIcon(s drive.TodoStatus) string {
	switch s {
	case drive.TodoPending:
		return "\u23f3"
	case drive.TodoRunning:
		return "\U0001f504"
	case drive.TodoDone:
		return "\u2705"
	case drive.TodoBlocked:
		return "\u274c"
	case drive.TodoSkipped:
		return "\u23ed"
	default:
		return "\u25cb"
	}
}

func renderRunStatusChip(s drive.RunStatus) string {
	switch s {
	case drive.RunPlanning:
		return subtleStyle.Render("\u25a1 planning")
	case drive.RunRunning:
		return accentStyle.Render("\u25b8 running")
	case drive.RunDone:
		return doneStyle.Render("\u2713 done")
	case drive.RunStopped:
		return subtleStyle.Render("\u25a0 stopped")
	case drive.RunFailed:
		return blockedStyle.Render("\u2717 failed")
	default:
		return subtleStyle.Render(string(s))
	}
}

// renderWorkflowTodoDetail shows the expanded detail of a selected TODO:
// ID, status, ProviderTag, WorkerClass, Brief, Detail, and the routed
// profile name from the drive.Config.Routing map.
func (m Model) renderWorkflowTodoDetail(run *drive.Run, width int) []string {
	if run == nil || m.workflow.selectedTodoID == "" {
		return nil
	}
	var todo *drive.Todo
	for i := range run.Todos {
		if run.Todos[i].ID == m.workflow.selectedTodoID {
			todo = &run.Todos[i]
			break
		}
	}
	if todo == nil {
		return nil
	}

	lines := []string{
		titleStyle.Render("TODO Detail"),
		"",
		fmt.Sprintf("  ID:       %s", subtleStyle.Render(todo.ID)),
		fmt.Sprintf("  Status:   %s", todoStatusIcon(todo.Status)+" "+subtleStyle.Render(string(todo.Status))),
	}
	if todo.ProviderTag != "" {
		lines = append(lines, fmt.Sprintf("  Tag:      %s", accentStyle.Render(todo.ProviderTag)))
		// Show which profile this tag routes to
		if m.eng != nil && m.eng.Config != nil {
			routing := m.workflow.routingDraft
			if profile, ok := routing[todo.ProviderTag]; ok {
				lines = append(lines, fmt.Sprintf("  Routed:   %s \u2192 %s", subtleStyle.Render(todo.ProviderTag), accentStyle.Render(profile)))
			} else {
				lines = append(lines, fmt.Sprintf("  Routed:   %s \u2192 %s", subtleStyle.Render(todo.ProviderTag), subtleStyle.Render("(default)")))
			}
		}
	}
	if todo.WorkerClass != "" {
		lines = append(lines, fmt.Sprintf("  Worker:   %s", subtleStyle.Render(todo.WorkerClass)))
	}
	if todo.Brief != "" {
		lines = append(lines, fmt.Sprintf("  Brief:    %s", truncateForPanel(todo.Brief, width)))
	}
	if todo.Detail != "" {
		lines = append(lines, "")
		lines = append(lines, subtleStyle.Render("  Detail:"))
		for _, detailLine := range strings.Split(todo.Detail, "\n") {
			lines = append(lines, "  "+subtleStyle.Render(truncateForPanel(detailLine, width-2)))
		}
	}
	if len(todo.FileScope) > 0 {
		lines = append(lines, fmt.Sprintf("  Scope:    %s", subtleStyle.Render(strings.Join(todo.FileScope, ", "))))
	}
	if len(todo.DependsOn) > 0 {
		lines = append(lines, fmt.Sprintf("  Depends:  %s", subtleStyle.Render(strings.Join(todo.DependsOn, ", "))))
	}
	if todo.Error != "" {
		lines = append(lines, "")
		lines = append(lines, failStyle.Render("  Error:   ")+failStyle.Render(truncateForPanel(todo.Error, width-8)))
	}
	lines = append(lines, "", subtleStyle.Render("esc deselect"))
	return lines
}

// handleWorkflowKey keyboard handler for the Workflow tab.
// Two-level navigation: run selector (left column, no run selected) →
// TODO tree (right column, run selected). 'r' opens the routing editor
// overlay when no run is selected, letting the user configure the
// drive.Config.Routing map (ProviderTag → profile name).
func (m Model) handleWorkflowKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Routing editor has its own key handler
	if m.workflow.showRoutingEditor {
		return m.handleRoutingEditorKey(msg)
	}

	total := len(m.workflow.runs)
	step := 1

	switch msg.String() {
	case "j", "down":
		if m.workflow.selectedRunID == "" {
			// run selector: move selectedIndex down
			if m.workflow.selectedIndex+step < total {
				m.workflow.selectedIndex += step
			}
		} else {
			// TODO tree: scroll down
			m.workflow.scrollY += step
		}
	case "k", "up":
		if m.workflow.selectedRunID == "" {
			if m.workflow.selectedIndex >= step {
				m.workflow.selectedIndex -= step
			} else {
				m.workflow.selectedIndex = 0
			}
		} else {
			if m.workflow.scrollY >= step {
				m.workflow.scrollY -= step
			} else {
				m.workflow.scrollY = 0
			}
		}
	case "g":
		if m.workflow.selectedRunID == "" {
			m.workflow.selectedIndex = 0
		} else {
			m.workflow.scrollY = 0
		}
	case "G":
		if m.workflow.selectedRunID == "" {
			if total > 0 {
				m.workflow.selectedIndex = total - 1
			}
		}
	case "enter", "o":
		if m.workflow.selectedRunID == "" {
			// select a run from the selector list
			if m.workflow.selectedIndex >= 0 && m.workflow.selectedIndex < total {
				run := m.workflow.runs[m.workflow.selectedIndex]
				m.workflow.selectedRunID = run.ID
				m.workflow.scrollY = 0
			}
		} else {
			// toggle TODO expand + set selectedTodoID
			m = m.cycleWorkflowTodoExpand()
			// Set selectedTodoID to the TODO at current scroll position
			run := m.selectedRunForWorkflow()
			if run != nil {
				visible := 0
				for _, t := range run.Todos {
					if t.ParentID == "" || m.workflow.expandedTodo[t.ParentID] {
						if visible == m.workflow.scrollY {
							m.workflow.selectedTodoID = t.ID
							break
						}
						visible++
					}
				}
			}
		}
	case "r":
		// routing editor: only when no run is selected
		if m.workflow.selectedRunID == "" {
			m.workflow.showRoutingEditor = true
			m.workflow.routingEditTag = ""
			m.workflow.routingEditProfile = ""
			m.workflow.routingEditIndex = 0
			m.workflow.routingEditMode = false
			if m.workflow.routingDraft == nil {
				m.workflow.routingDraft = m.loadDriveRoutingFromProjectConfig()
				if m.workflow.routingDraft == nil {
					m.workflow.routingDraft = make(map[string]string)
				}
			}
		}
	case "esc":
		if m.workflow.showRoutingEditor {
			m.workflow.showRoutingEditor = false
		} else if m.workflow.selectedTodoID != "" {
			// deselect TODO — hide detail
			m.workflow.selectedTodoID = ""
		} else if m.workflow.selectedRunID != "" {
			// deselect run — back to run selector
			m.workflow.selectedRunID = ""
			m.workflow.scrollY = 0
		}
	}
	return m, nil
}

// handleRoutingEditorKey handles keystrokes within the routing editor overlay.
// 'routingDraft' holds the tag→profile map being edited.
func (m Model) handleRoutingEditorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	routing := m.workflow.routingDraft
	keys := make([]string, 0, len(routing))
	for k := range routing {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if keys == nil {
		keys = []string{}
	}
	step := 1

	switch msg.String() {
	case "j", "down":
		if m.workflow.routingEditMode {
			// cycling through available profiles
			profiles := m.availableProviders()
			if len(profiles) > 0 {
				cur := m.workflow.routingEditProfile
				idx := 0
				for i, p := range profiles {
					if p == cur {
						idx = i
						break
					}
				}
				idx = (idx + 1) % len(profiles)
				m.workflow.routingEditProfile = profiles[idx]
			}
		} else {
			if m.workflow.routingEditIndex+step < len(keys) {
				m.workflow.routingEditIndex += step
			}
		}
	case "k", "up":
		if m.workflow.routingEditMode {
			profiles := m.availableProviders()
			if len(profiles) > 0 {
				cur := m.workflow.routingEditProfile
				idx := len(profiles) - 1
				for i, p := range profiles {
					if p == cur {
						idx = i
						break
					}
				}
				idx = (idx - 1 + len(profiles)) % len(profiles)
				m.workflow.routingEditProfile = profiles[idx]
			}
		} else {
			if m.workflow.routingEditIndex >= step {
				m.workflow.routingEditIndex -= step
			} else {
				m.workflow.routingEditIndex = 0
			}
		}
	case "enter":
		if m.workflow.routingEditMode {
			// commit the edit
			tag := m.workflow.routingEditTag
			if tag != "" && m.workflow.routingEditProfile != "" {
				m.workflow.routingDraft[tag] = m.workflow.routingEditProfile
			}
			m.workflow.routingEditMode = false
			m.workflow.routingEditTag = ""
			m.workflow.routingEditProfile = ""
		} else {
			// start editing the selected row's profile
			if m.workflow.routingEditIndex >= 0 && m.workflow.routingEditIndex < len(keys) {
				tag := keys[m.workflow.routingEditIndex]
				m.workflow.routingEditTag = tag
				m.workflow.routingEditProfile = routing[tag]
				m.workflow.routingEditMode = true
			}
		}
	case "+":
		// add a new entry (cycle through available profiles as tag)
		profiles := m.availableProviders()
		if len(profiles) > 0 {
			// Use the first profile as a starting point for a new tag
			tag := profiles[0]
			// Make tag name from profile
			m.workflow.routingEditTag = tag
			m.workflow.routingEditProfile = profiles[0]
			m.workflow.routingEditMode = true
			// Add to draft with current profile as default
			m.workflow.routingDraft[tag] = profiles[0]
			// Select the new row
			for i, k := range keys {
				if k == tag {
					m.workflow.routingEditIndex = i
					break
				}
			}
		}
	case "d":
		// delete the selected entry
		if m.workflow.routingEditIndex >= 0 && m.workflow.routingEditIndex < len(keys) {
			tag := keys[m.workflow.routingEditIndex]
			delete(m.workflow.routingDraft, tag)
			if m.workflow.routingEditIndex >= len(m.workflow.routingDraft) {
				m.workflow.routingEditIndex = max(0, len(m.workflow.routingDraft)-1)
			}
		}
	case "esc":
		if path, err := m.persistDriveRoutingProjectConfig(m.workflow.routingDraft); err == nil {
			m.notice = "routing saved: " + path
		}
		m.workflow.showRoutingEditor = false
		m.workflow.routingEditMode = false
	}
	return m, nil
}

// cycleWorkflowTodoExpand finds the TODO at the current scroll position
// and toggles its expanded state.
func (m Model) cycleWorkflowTodoExpand() Model {
	run := m.selectedRunForWorkflow()
	if run == nil || len(run.Todos) == 0 {
		return m
	}
	// find Nth visible TODO at current scroll
	visible := 0
	var targetID string
	for _, t := range run.Todos {
		if t.ParentID == "" || m.workflow.expandedTodo[t.ParentID] {
			if visible == m.workflow.scrollY {
				targetID = t.ID
				break
			}
			visible++
		}
	}
	if targetID != "" {
		m.workflow.expandedTodo[targetID] = !m.workflow.expandedTodo[targetID]
	}
	return m
}

// renderRoutingEditor shows the drive.Config.Routing editor overlay.
// The routing map controls which provider profile is used for each
// TODO ProviderTag (plan/code/review/test/etc.) when starting a drive run.
func (m Model) renderRoutingEditor(width int) string {
	if m.eng == nil || m.eng.Config == nil {
		return subtleStyle.Render("(engine not available)")
	}

	lines := []string{
		sectionHeader("\u2699", "Routing Editor"),
		subtleStyle.Render("enter edit \u00b7 + add \u00b7 d delete \u00b7 esc close"),
		renderDivider(width - 4),
		"",
	}

	profiles := m.availableProviders()
	if len(profiles) == 0 {
		lines = append(lines, subtleStyle.Render("(no provider profiles configured)"))
		return strings.Join(lines, "\n")
	}

	routing := m.workflowRouting()
	if len(routing) == 0 {
		lines = append(lines, subtleStyle.Render("No routing entries yet."))
		lines = append(lines, "")
		lines = append(lines, subtleStyle.Render("Press + to add a tag\u2192profile mapping."))
	} else {
		keys := make([]string, 0, len(routing))
		for k := range routing {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		for i, tag := range keys {
			profile := routing[tag]
			prefix := "  "
			if i == m.workflow.routingEditIndex {
				prefix = "> "
			}
			// If this row is being edited
			if m.workflow.routingEditMode && i == m.workflow.routingEditIndex {
				profile = codeStyle.Render(m.workflow.routingEditProfile) + subtleStyle.Render("|")
			}
			lines = append(lines, prefix+titleStyle.Render(tag)+subtleStyle.Render(" \u2192 ")+accentStyle.Render(profile))
		}
	}

	lines = append(lines, "")
	lines = append(lines, subtleStyle.Render("Profiles: ")+strings.Join(profiles, ", "))

	return lipgloss.NewStyle().Width(width).Render(strings.Join(lines, "\n"))
}

// workflowRouting returns the current routing map from the workflow state.
// This is stored on the model so it persists while the editor is open.
func (m Model) workflowRouting() map[string]string {
	// m.workflow.routing is not persisted — it's built from the engine config
	// on each render call.
	if m.eng == nil || m.eng.Config == nil {
		return nil
	}
	// Build a routing map from the engine's drive config
	// For now, return an empty map — the user can add entries via the editor.
	// A future enhancement would load this from a saved config.
	return m.workflow.routingDraft
}
