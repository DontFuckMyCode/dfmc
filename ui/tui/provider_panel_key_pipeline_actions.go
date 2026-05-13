package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/dontfuckmycode/dfmc/internal/config"
)

func (m Model) openProvidersPipelineActionMenu() Model {
	scroll := clampScroll(m.providers.pipelineScroll, len(m.providers.pipelineNames))
	selected := ""
	if scroll >= 0 && scroll < len(m.providers.pipelineNames) {
		selected = m.providers.pipelineNames[scroll]
	}
	actions := []panelAction{}
	if selected != "" {
		actions = append(actions,
			panelAction{Label: "Activate pipeline " + selected, Handler: func(m Model) (Model, tea.Cmd) {
				if m.eng == nil {
					m.notice = "engine not ready"
					return m, nil
				}
				if err := m.eng.ActivatePipeline(selected); err != nil {
					m.notice = "pipeline failed: " + err.Error()
				} else {
					m.providers.activePipeline = selected
					m.status = m.eng.Status()
					m.notice = "activated pipeline: " + selected
				}
				return m, nil
			}},
			panelAction{Label: "Edit pipeline " + selected, Handler: func(m Model) (Model, tea.Cmd) {
				if m.eng != nil {
					if pipe, ok := m.eng.Pipeline(selected); ok {
						m.providers.pipelineEditMode = true
						m.providers.pipelineDraftName = selected
						m.providers.pipelineDraftSteps = append([]config.PipelineStep(nil), pipe.Steps...)
						m.providers.pipelineEditStep = -1
						m.providers.pipelineEditField = 0
						m.providers.pipelineDraftBuf = ""
					}
				}
				return m, nil
			}},
			panelAction{Label: "Delete pipeline " + selected, Handler: func(m Model) (Model, tea.Cmd) {
				m.providers.confirmAction = "delete_pipeline"
				m.providers.confirmTarget = selected
				return m, nil
			}},
		)
	}
	actions = append(actions,
		panelAction{Label: "New pipeline", Handler: func(m Model) (Model, tea.Cmd) {
			m.providers.pipelineEditMode = true
			m.providers.pipelineDraftName = ""
			m.providers.pipelineDraftSteps = nil
			m.providers.pipelineEditStep = -1
			m.providers.pipelineEditField = 0
			m.providers.pipelineDraftBuf = ""
			return m, nil
		}},
		panelAction{Label: "Back to providers", Handler: func(m Model) (Model, tea.Cmd) {
			m.providers.viewMode = "list"
			return m, nil
		}},
	)
	return m.openActionMenu("Providers", "Pipelines", actions)
}
