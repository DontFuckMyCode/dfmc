package tui

// engine_events_provider.go — handlers for provider:* events:
// stream lifecycle, throttle / retry / fallback, race outcomes, circuit
// breaker. Registered on the package event registry; called via dispatch.

import (
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

func registerProviderEventHandlers(r *engineEventRegistry) {
	r.register("provider:complete", handleProviderComplete)
	r.register("provider:stream:start", handleProviderStreamStart)
	r.register("provider:throttle:retry", handleProviderThrottleRetry)
	r.register("provider:circuit:open", handleProviderCircuitOpen)
	r.register("provider:circuit:closed", handleProviderCircuitClosed)
	r.register("provider:stream:recovered", handleProviderStreamRecovered)
	r.register("provider:race:complete", handleProviderRaceComplete)
	r.register("provider:race:failed", handleProviderRaceFailed)
	r.register("provider:fallback", handleProviderFallback)
}

func handleProviderComplete(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	tokens := payloadIntAny(payload, 0, "total_tokens", "tokens")
	inputTokens := payloadInt(payload, "input_tokens", 0)
	outputTokens := payloadInt(payload, "output_tokens", 0)
	if tokens <= 0 {
		tokens = inputTokens + outputTokens
	}
	if inputTokens > 0 || outputTokens > 0 || tokens > 0 {
		m.telemetry.lastInputTokens = inputTokens
		m.telemetry.lastOutputTokens = outputTokens
		m.telemetry.lastTotalTokens = tokens
		m.telemetry.sessionInputTokens += inputTokens
		m.telemetry.sessionOutputTokens += outputTokens
		m.telemetry.sessionTotalTokens += tokens
	}
	// Phase I item 2 — per-provider usage history. Append the
	// provider:complete event to the per-name ring buffer so the
	// Providers panel detail view can show the last-N completions
	// without a second round trip through the engine event log.
	providerHist := payloadString(payload, "provider", "")
	modelHist := payloadString(payload, "model", "")
	if strings.TrimSpace(providerHist) != "" {
		m = m.recordProviderUsage(providerUsageEntry{
			At:           time.Now(),
			Provider:     providerHist,
			Model:        modelHist,
			InputTokens:  inputTokens,
			OutputTokens: outputTokens,
			TotalTokens:  tokens,
		})
	}
	line := ""
	if m.agentLoop.active {
		m.agentLoop.phase = "complete"
		m.agentLoop.active = false
		providerName := payloadString(payload, "provider", m.agentLoop.provider)
		modelName := payloadString(payload, "model", m.agentLoop.model)
		detail := ""
		if tokens > 0 {
			detail = "total " + compactMetric(tokens) + " tok"
		}
		if inputTokens > 0 || outputTokens > 0 {
			detail = fmt.Sprintf("in %s | out %s | total %s tok", compactMetric(inputTokens), compactMetric(outputTokens), compactMetric(tokens))
		}
		if providerName != "" || modelName != "" {
			if detail != "" {
				detail += " | "
			}
			detail += strings.Trim(strings.TrimSpace(providerName+"/"+modelName), "/")
		}
		m.upsertStreamingChatEvent(chatEventLine{
			Key:    "provider:complete",
			Kind:   "provider",
			Status: "ok",
			Title:  "provider complete",
			Detail: detail,
		})
		line = fmt.Sprintf("Provider complete: %s/%s (%dtok)", providerName, modelName, tokens)
	}
	return m, line
}

func handleProviderStreamStart(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	inputTokens := payloadIntAny(payload, 0, "input_tokens", "tokens")
	if inputTokens > 0 {
		m.chat.streamInputTokens = inputTokens
		m.telemetry.lastInputTokens = inputTokens
	}
	return m, ""
}

func handleProviderThrottleRetry(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	providerName := payloadString(payload, "provider", "?")
	attempt := payloadInt(payload, "attempt", 0)
	waitMs := payloadInt(payload, "wait_ms", 0)
	streaming := payloadBool(payload, "stream", false)
	label := "request"
	if streaming {
		label = "stream"
	}
	waitText := "immediately"
	if waitMs > 0 {
		waitText = fmt.Sprintf("in %s", (time.Duration(waitMs) * time.Millisecond).Round(100*time.Millisecond))
	}
	// Throttle events fire on the engine bus regardless of whether
	// there is an active chat turn — only mirror into the transcript
	// while the user is waiting on a response, otherwise we pollute
	// the chat history with provider chatter the user never asked for.
	if m.chat.sending {
		m.upsertStreamingChatEvent(chatEventLine{
			Key:    fmt.Sprintf("provider:throttle:%s:%d", providerName, attempt),
			Kind:   "provider",
			Status: "warn",
			Title:  "provider throttle",
			Detail: fmt.Sprintf("%s %s retry #%d %s", providerName, label, attempt, waitText),
		})
	}
	line := fmt.Sprintf("Provider throttled: %s %s retry #%d %s.", providerName, label, attempt, waitText)
	return m, line
}

func handleProviderCircuitOpen(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	providerName := payloadString(payload, "provider", "?")
	cooldownMs := payloadInt(payload, "cooldown_ms", 0)
	detail := providerName + " falling back"
	line := ""
	if cooldownMs > 0 {
		cooldown := (time.Duration(cooldownMs) * time.Millisecond).Round(time.Second)
		detail = fmt.Sprintf("%s skip for %s | falling back", providerName, cooldown)
		line = fmt.Sprintf("Provider %s circuit open — skipping for %s, falling back.", providerName, cooldown)
	} else {
		line = fmt.Sprintf("Provider %s circuit open — falling back.", providerName)
	}
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "provider:circuit:" + providerName,
		Kind:   "provider",
		Status: "warn",
		Title:  "provider circuit",
		Detail: detail,
	})
	return m, line
}

func handleProviderCircuitClosed(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	providerName := payloadString(payload, "provider", "?")
	return m, fmt.Sprintf("Provider %s circuit closed — recovered.", providerName)
}

func handleProviderStreamRecovered(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	from := payloadString(payload, "from", "?")
	to := payloadString(payload, "to", "?")
	return m, fmt.Sprintf("↻ Stream resumed on %s after %s blip.", to, from)
}

func handleProviderRaceComplete(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	winner := payloadString(payload, "winner", "?")
	tokens := payloadInt(payload, "tokens", 0)
	duration := payloadInt(payload, "duration_ms", 0)
	candidates, _ := payload["candidates"].([]any)
	var names []string
	for _, c := range candidates {
		if s, ok := c.(string); ok && strings.TrimSpace(s) != "" {
			names = append(names, s)
		}
	}
	m.pushToolChip(toolChip{
		Name:       "race",
		Status:     "race-ok",
		Preview:    fmt.Sprintf("won by %s", winner),
		DurationMs: duration,
	})
	line := ""
	if len(names) > 0 {
		line = fmt.Sprintf("Provider race: %s won [%s] (%dtok, %dms).", winner, strings.Join(names, ","), tokens, duration)
	} else {
		line = fmt.Sprintf("Provider race: %s won (%dtok, %dms).", winner, tokens, duration)
	}
	return m, line
}

func handleProviderRaceFailed(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	errText := payloadString(payload, "error", "all candidates errored")
	duration := payloadInt(payload, "duration_ms", 0)
	m.pushToolChip(toolChip{
		Name:       "race",
		Status:     "race-failed",
		Preview:    truncateSingleLine(errText, 72),
		DurationMs: duration,
	})
	return m, fmt.Sprintf("Provider race failed (%dms): %s", duration, truncateSingleLine(errText, 140))
}

func handleProviderFallback(m Model, eventType string, event engine.Event, payload map[string]any) (Model, string) {
	from := payloadString(payload, "from", "")
	to := payloadString(payload, "to", "")
	errText := strings.TrimSpace(payloadString(payload, "error", ""))
	attempt := payloadInt(payload, "attempt", 0)
	detail := fmt.Sprintf("%s → %s (attempt %d)", from, to, attempt)
	if errText != "" {
		detail += " · " + truncateSingleLine(errText, 80)
	}
	m.upsertStreamingChatEvent(chatEventLine{
		Key:    "provider:fallback:" + from + "-" + to,
		Kind:   "system",
		Status: "warn",
		Title:  "provider fallback",
		Detail: detail,
	})
	return m, "Provider fallback: " + detail
}
