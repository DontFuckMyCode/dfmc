package tui

import (
	"fmt"
	"time"
)

func (m Model) handleSubagentStart(payload map[string]any, now time.Time) (Model, string) {
	m.autoActivateStatsPanelMode(statsPanelModeSubagents, "subagents")
	m.startSubagentRuntime(payload, now)
	task := payloadString(payload, "task", "task")
	role := payloadString(payload, "role", "")
	candidates := payloadStringSlice(payload, "provider_candidates")
	targetLabel := subagentProviderLabel(
		payloadString(payload, "provider", ""),
		payloadString(payload, "model", ""),
	)
	chainLabel := subagentProfileChain(candidates)
	m.telemetry.activeSubagentCount++

	preview := truncateSingleLine(task, 72)
	chip := toolChip{
		Name:    subagentChipName(role),
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

	line := "Subagent started: " + preview
	if role != "" {
		line = fmt.Sprintf("Subagent (%s) started: %s", role, preview)
	}
	if chainLabel != "" {
		line += " [" + chainLabel + "]"
	} else if targetLabel != "" {
		line += " [" + targetLabel + "]"
	}
	return m, line
}

func (m Model) handleSubagentFallback(payload map[string]any, now time.Time) (Model, string) {
	m.fallbackSubagentRuntime(payload, now)
	role := payloadString(payload, "role", "")
	attempt := payloadInt(payload, "attempt", 0)
	fromProfile := payloadString(payload, "from_profile", "")
	toProfile := payloadString(payload, "to_profile", "")
	errText := payloadString(payload, "error", "")
	reasons := payloadStringSlice(payload, "fallback_reasons")
	if errText == "" && len(reasons) > 0 {
		errText = reasons[len(reasons)-1]
	}

	preview := subagentProfileTransition(fromProfile, toProfile)
	if preview == "" {
		preview = "provider fallback"
	}
	chip := toolChip{
		Name:    subagentChipName(role),
		Status:  "subagent-fallback",
		Preview: preview,
	}
	if errText != "" {
		chip.Verb = truncateSingleLine(errText, 72)
	}
	m.pushToolChip(chip)

	line := "Subagent fallback: " + preview
	if attempt > 0 {
		line = fmt.Sprintf("Subagent fallback #%d: %s", attempt, preview)
	}
	if errText != "" {
		line += " - " + truncateSingleLine(errText, 120)
	}
	return m, line
}

func (m Model) handleSubagentDone(payload map[string]any, now time.Time) (Model, string) {
	m.finishSubagentRuntime(payload, now)
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
	chipPreview := subagentDonePreview(rounds, providerLabel, attempts, fallbackUsed, parked)
	if errText != "" {
		status = "subagent-failed"
		chipPreview = truncateSingleLine(errText, 72)
	}
	finished := toolChip{
		Name:       subagentChipName(role),
		Status:     status,
		DurationMs: duration,
		Preview:    chipPreview,
	}
	m.finishToolChip(finished)
	m.finishStreamingMessageToolChip(finished)
	return m, subagentDoneLine(duration, rounds, parked, errText, providerLabel, attempts, fallbackUsed)
}

func (m Model) handleSubagentInterrupted(payload map[string]any, now time.Time) (Model, string) {
	m.finishSubagentRuntime(payload, now)
	if m.telemetry.activeSubagentCount > 0 {
		m.telemetry.activeSubagentCount--
	}

	role := payloadString(payload, "role", "")
	duration := payloadInt(payload, "duration_ms", 0)

	status := "subagent-interrupted"
	chip := toolChip{
		Name:       subagentChipName(role),
		Status:     status,
		DurationMs: duration,
		Preview:    "interrupted",
	}
	m.finishToolChip(chip)
	m.finishStreamingMessageToolChip(chip)

	line := "Subagent interrupted"
	if role != "" {
		line = fmt.Sprintf("Subagent (%s) interrupted", role)
	}
	if duration > 0 {
		line += fmt.Sprintf(" after %dms", duration)
	}
	return m, line
}

func subagentChipName(role string) string {
	if role == "" {
		return "subagent"
	}
	return "subagent/" + role
}

func subagentDonePreview(rounds int, providerLabel string, attempts int, fallbackUsed, parked bool) string {
	preview := fmt.Sprintf("%d rounds", rounds)
	if providerLabel != "" {
		preview += " \u00b7 " + providerLabel
	}
	if attempts > 1 {
		preview += fmt.Sprintf(" \u00b7 %d attempts", attempts)
	}
	if fallbackUsed {
		preview += " \u00b7 fallback"
	}
	if parked {
		preview += " \u00b7 parked"
	}
	return preview
}

func subagentDoneLine(duration, rounds int, parked bool, errText, providerLabel string, attempts int, fallbackUsed bool) string {
	switch {
	case errText != "":
		return fmt.Sprintf("Subagent failed (%dms): %s", duration, truncateSingleLine(errText, 120))
	case parked:
		return fmt.Sprintf("Subagent parked after %d rounds (%dms).", rounds, duration)
	}
	line := fmt.Sprintf("Subagent done: %d rounds (%dms).", rounds, duration)
	if providerLabel != "" {
		line += " via " + providerLabel
	}
	if fallbackUsed && attempts > 1 {
		line += fmt.Sprintf(" after %d attempts", attempts)
	}
	return line
}
