// agent_loop_autocontinue.go — self-resume on `[done: false]` (or
// missing `[done:]`). Wraps askWithNativeTools so a single user turn
// can chain into multiple agent loops without the user typing each
// follow-up manually. The model declares "I'm done" by emitting
// `[done: true]` in the final-answer tail; absent or `[done: false]`
// triggers another loop seeded with the first `[next:]` action as the
// new user prompt.
//
// User-stated invariant (the load-bearing prompt):
//   "kendine tekrar promptu kendi atmalı devam etmeli ne bileyim
//    işte … görev ne ise bitene kadar gerekirse sürekli devam etmeli"
//
// Capped by Config.Agent.MaxAutoContinueIterations so a runaway model
// can't burn the whole budget on its own. Set Config.Agent.AutoContinue
// to "off" to revert to the legacy "stop after every final answer and
// wait for the user" behaviour (useful in CI, scripted Asks, tests).
//
// Each auto-continue iteration is a full askWithNativeTools call with
// its own conversation record + memory write, so a crash or panic
// between iterations leaves a coherent transcript on disk. The wrapper
// concatenates the per-iteration stripped answers with separator
// banners ("--- auto-continue 2/5: <prompt> ---") before returning to
// the caller, so the user sees one continuous answer instead of having
// to scroll through N turns.

package engine

import (
	"context"
	"fmt"
	"strings"
)

// autoContinueConfig is the resolved knob set for the wrapper. Pulled
// from Config.Agent and normalised so callers don't have to repeat
// the "auto vs off" parsing logic.
type autoContinueConfig struct {
	Enabled       bool
	MaxIterations int
}

func (e *Engine) autoContinueConfig() autoContinueConfig {
	cfg := autoContinueConfig{Enabled: true, MaxIterations: 5}
	if e == nil || e.Config == nil {
		return cfg
	}
	mode := strings.ToLower(strings.TrimSpace(e.Config.Agent.AutoContinue))
	switch mode {
	case "off", "false", "no", "0", "manual", "disabled":
		cfg.Enabled = false
	}
	if n := e.Config.Agent.MaxAutoContinueIterations; n > 0 {
		cfg.MaxIterations = n
	}
	return cfg
}

// askWithNativeToolsAutoContinue runs askWithNativeTools and, while the
// final answer says "more work to do", re-enters the loop with the
// first [next:] action as the user prompt. The returned
// nativeToolCompletion's Answer is the concatenated, marker-stripped
// view of every iteration; ToolTraces accumulates across iterations
// so coach hints / event consumers see the full trajectory.
func (e *Engine) askWithNativeToolsAutoContinue(ctx context.Context, question string) (nativeToolCompletion, error) {
	first, err := e.askWithNativeTools(ctx, question)
	if err != nil {
		return first, err
	}
	cfg := e.autoContinueConfig()
	if !cfg.Enabled {
		return first, nil
	}
	parts := []string{}
	if _, _, stripped := parseAssistantHints(first.Answer); stripped != "" {
		parts = append(parts, stripped)
	}
	last := first
	for iter := 1; iter <= cfg.MaxIterations; iter++ {
		if ctx.Err() != nil {
			break
		}
		hints := parseAssistantHintsFull(last.Answer)
		// Stop on explicit done OR when the model gave us nothing
		// concrete to continue with. Empty NextActions means we'd be
		// guessing — better to stop cleanly than loop on a vague
		// "verify the result" stub.
		if hints.Done {
			break
		}
		if len(hints.NextActions) == 0 {
			break
		}
		nextPrompt := strings.TrimSpace(hints.NextActions[0])
		if nextPrompt == "" {
			break
		}
		if e.EventBus != nil {
			e.EventBus.Publish(Event{
				Type:   "assistant:auto_continue",
				Source: "engine",
				Payload: map[string]any{
					"iteration":      iter,
					"max_iterations": cfg.MaxIterations,
					"prompt":         nextPrompt,
					"reason":         autoContinueReason(hints),
				},
			})
		}
		next, err := e.askWithNativeTools(ctx, nextPrompt)
		if err != nil {
			// Surface the error but keep the partial chain so the
			// user sees what got done before the failure. The caller
			// (AskWithMetadata) only returns the answer when err is
			// nil, so this matters only for callers that look at both.
			last.Answer = joinAutoContinueParts(parts)
			return last, err
		}
		if _, _, stripped := parseAssistantHints(next.Answer); stripped != "" {
			parts = append(parts, fmt.Sprintf(
				"--- auto-continue %d/%d · %s ---\n\n%s",
				iter, cfg.MaxIterations, truncateRunesWithMarker(nextPrompt, 80, "…"), stripped,
			))
		}
		last.ToolTraces = append(last.ToolTraces, next.ToolTraces...)
		last.TokenCount += next.TokenCount
		last.Answer = next.Answer
	}
	if len(parts) > 0 {
		last.Answer = joinAutoContinueParts(parts)
	}
	return last, nil
}

// joinAutoContinueParts concatenates per-iteration stripped answers
// with double newlines between them. Single-iteration runs (the common
// case when the model emits [done: true] right away) skip the join
// overhead and return the lone part as-is.
func joinAutoContinueParts(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts, "\n\n")
}

// autoContinueReason names why the wrapper decided to keep going,
// surfaced on the assistant:auto_continue event so the TUI badge can
// label the iteration ("model said done=false" vs "no done marker").
func autoContinueReason(h AssistantHints) string {
	if h.DoneSet {
		return "done_false"
	}
	return "no_done_marker"
}
