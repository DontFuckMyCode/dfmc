package tui

// Sub-agent branch of the engine-event router. Owns the three
// agent:subagent:* cases (start/fallback/done) so the chip ribbon,
// the active-subagent counter, and the notice line share one home.
// engine_events.go dispatches prefix-matched events here.

import "fmt"

func (m Model) handleSubagentEvent(eventType string, payload map[string]any) (Model, string) {
	line := ""
	switch eventType {
	case "agent:subagent:start":
		m.autoActivateStatsPanelMode(statsPanelModeSubagents, "subagents")
		task := payloadString(payload, "task", "task")
		role := payloadString(payload, "role", "")
		candidates := payloadStringSlice(payload, "provider_candidates")
		targetLabel := subagentProviderLabel(
			payloadString(payload, "provider", ""),
			payloadString(payload, "model", ""),
		)
		chainLabel := subagentProfileChain(candidates)
		m.telemetry.activeSubagentCount++
		chipName := "subagent"
		if role != "" {
			chipName = "subagent/" + role
		}
		preview := truncateSingleLine(task, 72)
		chip := toolChip{
			Name:    chipName,
			Status:  "subagent-running",
			Preview: preview,
		}
		if chainLabel != "" {
			chip.Verb = chainLabel
		} else if targetLabel != "" {
			chip.Verb = targetLabel
		}
		m.pushToolChip(chip)
		m.pushStreamingMessageToolChip(chip)
		if role != "" {
			line = fmt.Sprintf("Subagent (%s) started: %s", role, preview)
		} else {
			line = "Subagent started: " + preview
		}
		if chainLabel != "" {
			line += " [" + chainLabel + "]"
		} else if targetLabel != "" {
			line += " [" + targetLabel + "]"
		}
	case "agent:subagent:fallback":
		role := payloadString(payload, "role", "")
		attempt := payloadInt(payload, "attempt", 0)
		fromProfile := payloadString(payload, "from_profile", "")
		toProfile := payloadString(payload, "to_profile", "")
		errText := payloadString(payload, "error", "")
		reasons := payloadStringSlice(payload, "fallback_reasons")
		if errText == "" && len(reasons) > 0 {
			errText = reasons[len(reasons)-1]
		}
		chipName := "subagent"
		if role != "" {
			chipName = "subagent/" + role
		}
		preview := subagentProfileTransition(fromProfile, toProfile)
		if preview == "" {
			preview = "provider fallback"
		}
		chip := toolChip{
			Name:    chipName,
			Status:  "subagent-fallback",
			Preview: preview,
		}
		if errText != "" {
			chip.Verb = truncateSingleLine(errText, 72)
		}
		m.pushToolChip(chip)
		if attempt > 0 {
			line = fmt.Sprintf("Subagent fallback #%d: %s", attempt, preview)
		} else {
			line = "Subagent fallback: " + preview
		}
		if errText != "" {
			line += " - " + truncateSingleLine(errText, 120)
		}
	case "agent:subagent:done":
		if m.telemetry.activeSubagentCount > 0 {
			m.telemetry.activeSubagentCount--
		}
		duration := payloadInt(payload, "duration_ms", 0)
		rounds := payloadInt(payload, "tool_rounds", 0)
		parked := payloadBool(payload, "parked", false)
		errText := payloadString(payload, "err", "")
		role := payloadString(payload, "role", "")
		attempts := payloadInt(payload, "attempts", 0)
		fallbackUsed := payloadBool(payload, "fallback_used", false)
		providerLabel := subagentProviderLabel(
			payloadString(payload, "provider", ""),
			payloadString(payload, "model", ""),
		)
		status := "subagent-ok"
		chipPreview := fmt.Sprintf("%d rounds", rounds)
		if providerLabel != "" {
			chipPreview += " · " + providerLabel
		}
		if attempts > 1 {
			chipPreview += fmt.Sprintf(" · %d attempts", attempts)
		}
		if fallbackUsed {
			chipPreview += " · fallback"
		}
		if parked {
			chipPreview += " · parked"
		}
		if errText != "" {
			status = "subagent-failed"
			chipPreview = truncateSingleLine(errText, 72)
		}
		chipName := "subagent"
		if role != "" {
			chipName = "subagent/" + role
		}
		finished := toolChip{
			Name:       chipName,
			Status:     status,
			DurationMs: duration,
			Preview:    chipPreview,
		}
		m.finishToolChip(finished)
		m.finishStreamingMessageToolChip(finished)
		switch {
		case errText != "":
			line = fmt.Sprintf("Subagent failed (%dms): %s", duration, truncateSingleLine(errText, 120))
		case parked:
			line = fmt.Sprintf("Subagent parked after %d rounds (%dms).", rounds, duration)
		default:
			line = fmt.Sprintf("Subagent done: %d rounds (%dms).", rounds, duration)
		}
		if errText == "" && providerLabel != "" {
			line += " via " + providerLabel
		}
		if errText == "" && fallbackUsed && attempts > 1 {
			line += fmt.Sprintf(" after %d attempts", attempts)
		}
	}
	return m, line
}
