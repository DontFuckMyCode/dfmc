package tui

import "strings"

func (m Model) currentCostPer1kTokens() float64 {
	if m.status.ProviderProfile.CostPer1kTokens > 0 {
		return m.status.ProviderProfile.CostPer1kTokens
	}
	if m.eng == nil || m.eng.Config == nil {
		return 0
	}
	provider := strings.TrimSpace(m.currentProvider())
	if provider == "" {
		provider = strings.TrimSpace(m.eng.Config.Providers.Primary)
	}
	if profile, ok := m.eng.Config.Providers.Profiles[provider]; ok {
		return profile.CostPer1kTokens
	}
	return 0
}

func (m Model) sessionTokenTotals() (int, int, int) {
	transcriptInput, transcriptOutput := transcriptTokenTotals(m.chat.transcript)
	input := max(m.telemetry.sessionInputTokens, transcriptInput)
	output := max(m.telemetry.sessionOutputTokens, transcriptOutput)
	total := m.telemetry.sessionTotalTokens
	if total <= 0 {
		total = m.telemetry.sessionInputTokens + m.telemetry.sessionOutputTokens
	}
	total = max(total, input+output)
	return input, output, total
}
