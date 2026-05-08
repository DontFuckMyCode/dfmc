package tui

// render_workflow_routing.go — provider-tag → profile routing editor for
// the Workflow tab. Owns the editor's keyboard surface
// (handleRoutingEditorKey), the overlay's renderer (renderRoutingEditor),
// and the small accessor for the editor's draft map (workflowRouting).
// All other Workflow-tab rendering and keyboard logic lives in
// render_workflow.go and render_workflow_keys.go.

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

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
				if m.workflow.routingDraft == nil {
					m.workflow.routingDraft = make(map[string]string)
				}
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
			// Add to draft with current profile as default. routingDraft
			// is nil-by-default on a fresh model, so lazy-init before
			// the first write to avoid a "nil map" panic.
			if m.workflow.routingDraft == nil {
				m.workflow.routingDraft = make(map[string]string)
			}
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

// renderRoutingEditor shows the drive.Config.Routing editor overlay.
// The routing map controls which provider profile is used for each
// TODO ProviderTag (plan/code/review/test/etc.) when starting a drive run.
func (m Model) renderRoutingEditor(width int) string {
	if m.eng == nil || m.eng.Config == nil {
		return subtleStyle.Render("(engine not available)")
	}

	lines := []string{
		sectionHeader("⚙", "Routing Editor"),
		subtleStyle.Render("enter edit · + add · d delete · esc close"),
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
		lines = append(lines, subtleStyle.Render("Press + to add a tag→profile mapping."))
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
			lines = append(lines, prefix+titleStyle.Render(tag)+subtleStyle.Render(" → ")+accentStyle.Render(profile))
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
