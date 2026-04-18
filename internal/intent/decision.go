// Package intent runs a small, state-aware sub-LLM before every user Ask.
// Given a compact snapshot of the engine's current state (parked agent?
// last tool? last assistant turn? recent tools?) and the user's raw input,
// the intent classifier returns a routing decision (resume vs new vs
// clarify) and an enriched prompt that the main model can act on without
// having to re-resolve coreferences ("fix it", "do that for the others",
// "devam et").
//
// Design tenets:
//
//   - Fail-open: any timeout/error/missing provider → raw input passes
//     through, the engine never blocks waiting for the intent layer.
//   - Cheap: targets ~$0.001/turn on Haiku-class models. Don't reach for
//     this for Opus.
//   - Authentic transcripts: the conversation log stores the user's
//     original message; only the *current turn's* prompt to the main
//     model is the enriched version. Future turns see the original text.
//   - State is plumbed in, not introspected: callers build a Snapshot
//     once and pass it. This package never reaches into Engine internals.
package intent

import "time"

// Intent classifies what the user wants the engine to do with this turn.
type Intent string

const (
	// IntentResume means the user is continuing a parked agent loop. The
	// engine should call ResumeAgent with the (possibly empty) note,
	// preserving cumulative tool budgets and the in-flight message stack.
	IntentResume Intent = "resume"
	// IntentNew is a fresh question or instruction. The engine should
	// take the standard Ask path. Any parked state is unrelated and
	// should be cleared.
	IntentNew Intent = "new"
	// IntentClarify means the input is too ambiguous to act on (e.g.
	// "fix it" with no recent error in context). The engine should
	// echo FollowUpQuestion to the user without calling the main model
	// — saves a round-trip and avoids hallucinated guesses.
	IntentClarify Intent = "clarify"
)

// Decision is the intent layer's full output. EnrichedRequest is what
// the main model should see for this turn (never empty when Intent is
// resume or new). Reasoning is a short trace of why the classifier
// chose this routing — surfaced to the TUI for transparency, not
// consumed by the engine. FollowUpQuestion is populated only for
// IntentClarify and is shown verbatim to the user.
type Decision struct {
	Intent           Intent
	EnrichedRequest  string
	Reasoning        string
	FollowUpQuestion string
	// Source identifies who produced the decision. "llm" means a real
	// classifier call; "fallback" means the layer failed open and the
	// raw input was passed through untouched. The TUI uses this to
	// decide whether to surface the "intent: ✓" chip.
	Source string
	// Latency captures end-to-end time spent in the router (LLM call +
	// snapshot build + JSON parsing). Zero when Source == "fallback"
	// short-circuited before any provider call.
	Latency time.Duration
}

// Fallback returns a Decision that passes the raw user input through
// untouched. Used when the intent layer is disabled, when the chosen
// provider is missing, or when the LLM call errors out under fail-open.
func Fallback(raw string) Decision {
	return Decision{
		Intent:          IntentNew,
		EnrichedRequest: raw,
		Reasoning:       "intent layer disabled or unavailable; raw input passed through",
		Source:          "fallback",
	}
}
