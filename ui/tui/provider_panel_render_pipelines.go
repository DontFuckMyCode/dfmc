package tui

// Pipelines views. Split from provider_panel_render.go so the
// read-only listing and the inline editor live next to each other,
// away from the provider detail view and the raw row formatters.

import (
	"fmt"
	"strings"
)

func (m Model) renderPipelinesView(width int) string {
	width = clampInt(width, 24, 1000)
	header := sectionHeader("⚑", "Pipelines")

	if m.providers.pipelineEditMode {
		return m.renderPipelineEditView(width, header)
	}

	hint := subtleStyle.Render("j/k scroll · g/G/home/end top/bottom · pgup/pgdown page · enter menu · esc/q back")
	lines := []string{header, hint, renderDivider(width - 2)}

	names := m.providers.pipelineNames
	if len(names) == 0 {
		lines = append(lines, "",
			warnStyle.Render("No pipelines configured"),
			"",
			subtleStyle.Render("Press Enter → New Pipeline to create one."),
		)
		return strings.Join(lines, "\n")
	}

	lines = append(lines, subtleStyle.Render(fmt.Sprintf("%d pipelines configured", len(names))), "")

	for i, name := range names {
		selected := i == m.providers.pipelineScroll
		prefix := "  "
		num := fmt.Sprintf("%d.", i+1)
		label := name
		if selected {
			prefix = accentStyle.Render("▶ ")
			num = accentStyle.Render(num)
			label = accentStyle.Render(label)
		} else {
			num = subtleStyle.Render(num)
			label = subtleStyle.Render(label)
		}
		if name == m.providers.activePipeline {
			label += accentStyle.Render(" · active")
		}
		if m.eng != nil {
			if pipe, ok := m.eng.Pipeline(name); ok {
				if len(pipe.Steps) > 0 {
					if selected {
						lines = append(lines, prefix+num+" "+label)
						for j, step := range pipe.Steps {
							stepNum := fmt.Sprintf("%d.", j+1)
							var stepLabel string
							if j == 0 {
								stepLabel = subtleStyle.Render(stepNum+" ") + accentStyle.Render(step.Provider) + subtleStyle.Render(" / ") + accentStyle.Render(step.Model)
								stepLabel += subtleStyle.Render(" ← primary")
							} else {
								stepLabel = subtleStyle.Render(stepNum+" ") + subtleStyle.Render(step.Provider) + subtleStyle.Render(" / ") + subtleStyle.Render(step.Model)
								stepLabel += subtleStyle.Render(fmt.Sprintf(" ← fallback %d", j))
							}
							lines = append(lines, "    "+stepLabel)
						}
					} else {
						label += subtleStyle.Render(fmt.Sprintf(" · %d steps", len(pipe.Steps)))
						lines = append(lines, prefix+num+" "+label)
					}
				} else {
					lines = append(lines, prefix+num+" "+label)
				}
			} else {
				lines = append(lines, prefix+num+" "+label)
			}
		} else {
			lines = append(lines, prefix+num+" "+label)
		}
	}

	lines = append(lines, m.renderProvidersMenu(width-2)...)
	return strings.Join(lines, "\n")
}

func (m Model) renderPipelineEditView(width int, header string) string {
	hint := subtleStyle.Render("j/k step · tab field · enter commit · d delete step · esc cancel")
	if m.providers.pipelineEditStep == -1 {
		hint = subtleStyle.Render("type name · tab next · enter save · esc cancel")
	} else if m.providers.pipelineEditStep == len(m.providers.pipelineDraftSteps) {
		hint = subtleStyle.Render("enter add step · k back · esc cancel")
	}
	lines := []string{header, hint, renderDivider(width - 2), ""}

	nameLabel := "name: "
	if m.providers.pipelineEditStep == -1 {
		nameLabel = accentStyle.Render("▶ name: ") + accentStyle.Render(m.providers.pipelineDraftName)
	} else {
		nameLabel += subtleStyle.Render(m.providers.pipelineDraftName)
	}
	lines = append(lines, "  "+nameLabel)
	lines = append(lines, "")

	steps := m.providers.pipelineDraftSteps
	if len(steps) > 0 {
		lines = append(lines, "  "+sectionTitleStyle.Render(fmt.Sprintf("Steps (%d)", len(steps))))
	}

	for i, step := range steps {
		selected := i == m.providers.pipelineEditStep
		prefix := "    "
		stepLabel := fmt.Sprintf("%d. ", i+1)
		if selected {
			prefix = "  " + accentStyle.Render("▶ ")
			stepLabel = accentStyle.Render(stepLabel)
		}
		provider := step.Provider
		model := step.Model
		if selected {
			if m.providers.pipelineEditField == 0 {
				provider = accentStyle.Render(provider)
				model = subtleStyle.Render(model)
			} else {
				provider = subtleStyle.Render(provider)
				model = accentStyle.Render(model)
			}
			if m.providers.pipelineDraftBuf != "" {
				if m.providers.pipelineEditField == 0 {
					provider = accentStyle.Render(m.providers.pipelineDraftBuf)
				} else {
					model = accentStyle.Render(m.providers.pipelineDraftBuf)
				}
			}
		} else {
			provider = subtleStyle.Render(provider)
			model = subtleStyle.Render(model)
		}
		lines = append(lines, prefix+stepLabel+provider+" / "+model)
	}
	// "+ Add Step" pseudo-row
	if m.providers.pipelineEditStep == len(steps) {
		lines = append(lines, "  "+accentStyle.Render("▶ + Add Step"))
	} else {
		lines = append(lines, "    "+subtleStyle.Render("+ Add Step"))
	}
	lines = append(lines, m.renderProvidersMenu(width-2)...)
	return strings.Join(lines, "\n")
}
