// Engine glue for the intent layer. Builds the Snapshot from current
// engine state and runs the intent.Router to decide how each user submit
// should be routed (resume parked agent vs. start fresh vs. ask for
// clarification) and how to phrase the prompt the main model sees.
//
// Kept in its own file so the integration can grow (more state in the
// snapshot, structured emit of intent decisions to the EventBus, etc.)
// without bloating engine_ask.go.

package engine

import (
	"context"

	"github.com/dontfuckmycode/dfmc/internal/intent"
)

// buildIntentSnapshot assembles the compact engine-state view the intent
// classifier sees. Every accessor here is cheap (in-memory reads, no
// disk I/O) so it's safe to call on every user submit.
func (e *Engine) buildIntentSnapshot() intent.Snapshot {
	snap := intent.Snapshot{
		Provider: e.provider(),
		Model:    e.model(),
	}
	if details, ok := e.ParkedAgentDetails(); ok && details != nil {
		snap.Parked = true
		snap.ParkedSummary = e.ParkedAgentSummary()
		snap.ParkedStep = details.Step
		snap.ParkedToolName = details.LastToolName
		snap.ParkedAt = details.ParkedAt
		snap.CumulativeSteps = details.CumulativeSteps
		snap.CumulativeTokens = details.CumulativeTokens
	}
	recent := e.RecentConversationContext(500, 5)
	snap.LastAssistant = recent.LastAssistant
	snap.RecentToolNames = recent.RecentToolNames
	snap.UserTurnCount = recent.UserTurnCount
	return snap
}

// routeIntent runs the intent classifier and publishes a telemetry
// event so the TUI / Web UI / remote clients can show "intent: ✓" and
// surface the rewrite for transparency. Always returns a usable
// Decision — Fallback(raw) on any failure when FailOpen is set.
//
// Callers receive the Decision but should NOT consume EnrichedRequest
// for a `resume` intent; that path goes through ResumeAgent which
// expects the user's note as-is. For `new`, EnrichedRequest is what
// the main model should see this turn (the original raw message stays
// in the conversation log).
func (e *Engine) routeIntent(ctx context.Context, raw string) intent.Decision {
	if e == nil || e.Intent == nil {
		return intent.Fallback(raw)
	}
	snap := e.buildIntentSnapshot()
	dec, _ := e.Intent.Evaluate(ctx, raw, snap)
	if e.EventBus != nil {
		e.EventBus.Publish(Event{
			Type:   "intent:decision",
			Source: "intent",
			Payload: map[string]any{
				"intent":     string(dec.Intent),
				"source":     dec.Source,
				"latency_ms": dec.Latency.Milliseconds(),
				"reasoning":  dec.Reasoning,
				"raw":        raw,
				"enriched":   dec.EnrichedRequest,
				"follow_up":  dec.FollowUpQuestion,
			},
		})
	}
	return dec
}
