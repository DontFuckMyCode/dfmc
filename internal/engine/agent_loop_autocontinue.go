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
	"regexp"
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
func (e *Engine) askWithNativeToolsAutoContinue(ctx context.Context, question string, onDelta ...func(string)) (nativeToolCompletion, error) {
	var deltaFn func(string)
	if len(onDelta) > 0 {
		deltaFn = onDelta[0]
	}

	first, err := e.askWithNativeTools(ctx, question, deltaFn)
	if err != nil {
		return first, err
	}
	cfg := e.autoContinueConfig()
	if !cfg.Enabled {
		return first, nil
	}
	// Parked completions (budget/step-cap/shutdown/interrupted) are not
	// auto-continue candidates — the park notice is an engine signal to
	// the user, not a model answer to chain from. When the loop parks and
	// autonomous resume is off (or the cumulative ceiling was hit), the
	// wrapper must surface the park as-is instead of treating the notice
	// as a regular answer and trying to continue past it.
	if first.Parked {
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
		choiceGate := looksLikeUserChoiceGate(last.Answer)
		if hints.Done && !choiceGate {
			break
		}
		if len(hints.NextActions) == 0 {
			nextPrompt := autonomousFallbackPrompt(last.Answer)
			if e.EventBus != nil {
				e.EventBus.Publish(Event{
					Type:   "assistant:auto_continue",
					Source: "engine",
					Payload: map[string]any{
						"iteration":      iter,
						"max_iterations": cfg.MaxIterations,
						"prompt":         nextPrompt,
						"reason":         "missing_next_action",
						"choice_gate":    choiceGate,
					},
				})
			}

			banner := fmt.Sprintf("\n\n--- auto-continue %d/%d - self-select next step ---\n\n",
				iter, cfg.MaxIterations)
			if deltaFn != nil {
				deltaFn(banner)
			}

			next, err := e.askWithNativeTools(ctx, nextPrompt, deltaFn)
			if err != nil {
				last.Answer = joinAutoContinueParts(parts)
				return last, err
			}
			if _, _, stripped := parseAssistantHints(next.Answer); stripped != "" {
				parts = append(parts, fmt.Sprintf(
					"--- auto-continue %d/%d - self-select next step ---\n\n%s",
					iter, cfg.MaxIterations, stripped,
				))
			}
			last.ToolTraces = append(last.ToolTraces, next.ToolTraces...)
			last.TokenCount += next.TokenCount
			last.Answer = next.Answer
			continue
		}
		nextPrompt := strings.TrimSpace(hints.NextActions[0])
		if nextPrompt == "" {
			if e.EventBus != nil {
				e.EventBus.Publish(Event{
					Type:   "assistant:auto_continue:clarify",
					Source: "engine",
					Payload: map[string]any{
						"iteration":      iter,
						"max_iterations": cfg.MaxIterations,
						"reason":         "blank_next_action",
					},
				})
			}
			nudge := "\n*— engine paused: `[next:]` was empty. Reply with the concrete next step (or `/cancel`).*"
			parts = append(parts, nudge)
			if deltaFn != nil {
				deltaFn("\n\n" + nudge)
			}
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

		banner := fmt.Sprintf("\n\n--- auto-continue %d/%d · %s ---\n\n",
			iter, cfg.MaxIterations, truncateRunesWithMarker(nextPrompt, 80, "…"))
		if deltaFn != nil {
			deltaFn(banner)
		}

		next, err := e.askWithNativeTools(ctx, nextPrompt, deltaFn)
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

var numberedChoiceLinePattern = regexp.MustCompile(`(?m)^\s*(?:[1-9][0-9]*[.)]|[-*]\s*(?:option|secenek|seçenek)\s*[1-9])\s+\S`)

func looksLikeUserChoiceGate(answer string) bool {
	text := strings.TrimSpace(stripped_(answer, AssistantHints{}))
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"choose one",
		"choose an option",
		"select one",
		"select an option",
		"pick one",
		"press 1",
		"press 2",
		"press 3",
		"type 1",
		"type 2",
		"type 3",
		"reply with 1",
		"reply with 2",
		"reply with 3",
		"option 1",
		"option 2",
		"option 3",
		"birini sec",
		"birini seç",
		"secenek sec",
		"seçenek seç",
		"seçenek",
		"secenek",
		"1'e bas",
		"2'ye bas",
		"3'e bas",
		"1 yaz",
		"2 yaz",
		"3 yaz",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return len(numberedChoiceLinePattern.FindAllStringIndex(text, 3)) >= 2 &&
		(strings.Contains(lower, "which") ||
			strings.Contains(lower, "choose") ||
			strings.Contains(lower, "select") ||
			strings.Contains(lower, "pick") ||
			strings.Contains(lower, "hang") ||
			strings.Contains(lower, "seç") ||
			strings.Contains(lower, "sec") ||
			strings.Contains(lower, "bas") ||
			strings.Contains(lower, "yaz"))
}

func autonomousFallbackPrompt(answer string) string {
	_, _, stripped := parseAssistantHints(answer)
	stripped = strings.TrimSpace(stripped)
	if stripped == "" {
		stripped = strings.TrimSpace(answer)
	}
	if stripped != "" {
		stripped = truncateRunesWithMarker(stripped, 1200, "...")
	}
	var b strings.Builder
	b.WriteString("[DFMC autonomous continuation]\n")
	b.WriteString("The previous assistant turn stopped without a usable [next:] action, or it asked the user to choose between options. Do not wait for the user.\n")
	b.WriteString("Pick the safest, highest-value next step yourself from the original goal and current evidence, then continue using tools as needed. If the work is actually complete, return a concise final summary and end with [done: true].\n")
	b.WriteString("Do not ask the user to press 1/2/3 or choose an option; make the operational choice yourself and proceed.\n")
	if stripped != "" {
		b.WriteString("\nPrevious stalled answer:\n")
		b.WriteString(stripped)
	}
	return b.String()
}
