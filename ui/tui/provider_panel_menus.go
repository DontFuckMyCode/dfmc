// provider_panel_menus.go — context-menu builders for the Providers
// tab. Each builder returns parallel slices (labels / actions /
// disabled flags / reason text) so the render layer can draw rows and
// the key layer can dispatch on the action string without both caring
// about item indices. Three surfaces:
//
//   - buildListMenu: the main providers list (per-row actions when a
//     provider is selected + global actions like Sync / New / Refresh).
//   - buildDetailMenu: the single-provider detail view (profile/
//     models/routing/persistence/navigation groups).
//   - buildPipelineMenu: the pipeline browser (activate/edit/delete on
//     the selected pipeline + New / Back).
//
// nextEnabledMenuIndex is the cursor-movement helper both the list
// and detail key handlers use to skip disabled rows in a given
// direction — keeps arrow-key nav predictable when an item is greyed
// out because it's already the primary / already active / etc.

package tui

import (
	"strings"
)

func (m Model) buildListMenu() ([]string, []string, []bool, []string) {
	var labels, actions []string
	var disabled []bool
	var reasons []string

	scroll := clampScroll(m.providers.scroll, len(m.providers.rows))
	selectedName := ""
	if scroll >= 0 && scroll < len(m.providers.rows) {
		selectedName = m.providers.rows[scroll].Name
	}

	// --- Provider-specific actions ---
	if selectedName != "" {
		labels = append(labels, "View Detail")
		actions = append(actions, "detail")
		disabled = append(disabled, false)
		reasons = append(reasons, "")

		// Set Primary — context-aware label
		isPrimary := false
		if m.eng != nil && strings.EqualFold(m.eng.Config.Providers.Primary, selectedName) {
			isPrimary = true
		}
		if isPrimary {
			labels = append(labels, "Already Primary")
		} else {
			labels = append(labels, "Set as Primary")
		}
		actions = append(actions, "set_primary")
		disabled = append(disabled, isPrimary)
		if isPrimary {
			reasons = append(reasons, "already the primary provider")
		} else {
			reasons = append(reasons, "")
		}

		// Toggle Fallback — context-aware label
		inFallback := false
		if m.eng != nil {
			for _, fb := range m.eng.Config.Providers.Fallback {
				if strings.EqualFold(fb, selectedName) {
					inFallback = true
					break
				}
			}
		}
		if inFallback {
			labels = append(labels, "Remove from Fallback")
		} else {
			labels = append(labels, "Add to Fallback")
		}
		actions = append(actions, "toggle_fallback")
		disabled = append(disabled, false)
		reasons = append(reasons, "")

		labels = append(labels, "Cycle Model")
		actions = append(actions, "cycle_model")
		disabled = append(disabled, false)
		reasons = append(reasons, "")
		labels = append(labels, "Save Config")
		actions = append(actions, "save_config")
		disabled = append(disabled, false)
		reasons = append(reasons, "")
		labels = append(labels, "Delete Provider")
		actions = append(actions, "delete_provider")
		disabled = append(disabled, false)
		reasons = append(reasons, "")
	}

	// --- Global actions ---
	labels = append(labels, "Sync Models")
	actions = append(actions, "sync_models")
	disabled = append(disabled, false)
	reasons = append(reasons, "")
	labels = append(labels, "Pipelines")
	actions = append(actions, "pipelines")
	disabled = append(disabled, false)
	reasons = append(reasons, "")
	labels = append(labels, "New Provider")
	actions = append(actions, "new_provider")
	disabled = append(disabled, false)
	reasons = append(reasons, "")
	labels = append(labels, "Refresh")
	actions = append(actions, "refresh")
	disabled = append(disabled, false)
	reasons = append(reasons, "")

	return labels, actions, disabled, reasons
}

func (m Model) buildDetailMenu() ([]string, []string, []bool, []string) {
	var labels, actions []string
	var disabled []bool
	var reasons []string
	name := m.providers.detailProvider

	// --- Profile ---
	labels = append(labels, "Edit Profile")
	actions = append(actions, "edit_profile")
	disabled = append(disabled, false)
	reasons = append(reasons, "")

	// --- Models ---
	labels = append(labels, "Add Model")
	actions = append(actions, "add_model")
	disabled = append(disabled, false)
	reasons = append(reasons, "")
	labels = append(labels, "Set Active Model")
	actions = append(actions, "set_active_model")
	disabled = append(disabled, false)
	reasons = append(reasons, "")
	labels = append(labels, "Delete Selected Model")
	actions = append(actions, "delete_model")
	disabled = append(disabled, false)
	reasons = append(reasons, "")

	// --- Routing ---
	isPrimary := false
	if m.eng != nil && strings.EqualFold(m.eng.Config.Providers.Primary, name) {
		isPrimary = true
	}
	if isPrimary {
		labels = append(labels, "Already Primary")
	} else {
		labels = append(labels, "Set as Primary")
	}
	actions = append(actions, "set_primary")
	disabled = append(disabled, isPrimary)
	if isPrimary {
		reasons = append(reasons, "already the primary provider")
	} else {
		reasons = append(reasons, "")
	}

	inFallback := false
	if m.eng != nil {
		for _, fb := range m.eng.Config.Providers.Fallback {
			if strings.EqualFold(fb, name) {
				inFallback = true
				break
			}
		}
	}
	if inFallback {
		labels = append(labels, "Remove from Fallback")
	} else {
		labels = append(labels, "Add to Fallback")
	}
	actions = append(actions, "toggle_fallback")
	disabled = append(disabled, false)
	reasons = append(reasons, "")

	// --- Persistence ---
	labels = append(labels, "Save Config")
	actions = append(actions, "save_config")
	disabled = append(disabled, false)
	reasons = append(reasons, "")

	// --- Navigation ---
	labels = append(labels, "Back to List")
	actions = append(actions, "back")
	disabled = append(disabled, false)
	reasons = append(reasons, "")

	return labels, actions, disabled, reasons
}

func (m Model) buildPipelineMenu() ([]string, []string, []bool, []string) {
	var labels, actions []string
	var disabled []bool
	var reasons []string

	scroll := clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
	if scroll >= 0 && scroll < len(m.providers.pipelineNames) {
		name := m.providers.pipelineNames[scroll]
		if name == m.providers.activePipeline {
			labels = append(labels, "Already Active")
		} else {
			labels = append(labels, "Activate Pipeline")
		}
		actions = append(actions, "activate")
		disabled = append(disabled, name == m.providers.activePipeline)
		if name == m.providers.activePipeline {
			reasons = append(reasons, "already the active pipeline")
		} else {
			reasons = append(reasons, "")
		}
		labels = append(labels, "Edit Pipeline")
		actions = append(actions, "edit")
		disabled = append(disabled, false)
		reasons = append(reasons, "")
		labels = append(labels, "Delete Pipeline")
		actions = append(actions, "delete")
		disabled = append(disabled, false)
		reasons = append(reasons, "")
	}

	labels = append(labels, "New Pipeline")
	actions = append(actions, "new")
	disabled = append(disabled, false)
	reasons = append(reasons, "")
	labels = append(labels, "Back to List")
	actions = append(actions, "back")
	disabled = append(disabled, false)
	reasons = append(reasons, "")

	return labels, actions, disabled, reasons
}

func nextEnabledMenuIndex(disabled []bool, start, total, dir int) int {
	if total == 0 {
		return 0
	}
	idx := start + dir
	for idx >= 0 && idx < total {
		if idx < len(disabled) && disabled[idx] {
			idx += dir
			continue
		}
		return idx
	}
	// All items in this direction are disabled — stay where we are.
	return start
}
