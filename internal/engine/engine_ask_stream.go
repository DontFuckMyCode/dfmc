// engine_ask_stream.go — StreamAsk SSE entry point. Sibling of
// engine_ask.go which keeps the synchronous Ask / AskRaced /
// AskWithMetadata trio and the package-level doc-comment listing
// the request-builder + history-budget split.
//
// Splitting StreamAsk out keeps the synchronous and streaming surfaces
// next to each other while isolating the goroutine pump (the
// ctx.Done()-guarded forwarder that prevents a leaked upstream HTTP
// connection when an SSE consumer walks away mid-stream) into its own
// file. Adjusting the SSE protocol or the leak-prevention pattern
// belongs here; lifecycle/intent routing for non-streaming asks
// stays in engine_ask.go.

package engine

import (
	"context"
	"errors"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/hooks"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
)

func (e *Engine) StreamAsk(ctx context.Context, question string) (<-chan provider.StreamEvent, error) {
	if err := e.requireReady("stream ask"); err != nil {
		return nil, err
	}
	if err := e.maybeAutoReloadProjectConfig(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(question) == "" {
		return nil, errors.New("question cannot be empty")
	}
	if e.Providers == nil {
		return nil, errors.New("provider router is not initialized")
	}
	// user_prompt_submit fires before we commit to a provider round-trip
	// so hooks can observe every turn that leaves the UI, regardless of
	// whether the engine routes through the native-tool loop or plain
	// streaming. Hooks are best-effort — we don't block the ask if a
	// hook fails or times out.
	if e.Hooks != nil && e.Hooks.Count(hooks.EventUserPromptSubmit) > 0 {
		e.Hooks.Fire(ctx, hooks.EventUserPromptSubmit, hooks.Payload{
			"prompt":       question,
			"provider":     e.provider(),
			"model":        e.model(),
			"project_root": e.ProjectRoot,
		})
	}

	// Intent layer (mirrors AskWithMetadata). Resume short-circuits to
	// ResumeAgent (whose answer is then streamed to the consumer as a
	// single chunk to keep the SSE protocol identical for callers).
	// Clarify short-circuits to streaming the follow-up question text.
	decision := e.routeIntent(ctx, question)
	if decision.Intent == "resume" && e.HasParkedAgent() {
		completion, err := e.ResumeAgent(ctx, question)
		if err != nil {
			return nil, err
		}
		return streamAnswerText(ctx, completion.Answer), nil
	}
	if decision.Intent == "clarify" && decision.FollowUpQuestion != "" {
		e.recordInteraction(question, decision.FollowUpQuestion, "intent-layer", "clarify", 0, nil)
		return streamAnswerText(ctx, decision.FollowUpQuestion), nil
	}
	prompt := decision.EnrichedRequest
	if prompt == "" {
		prompt = question
	}

	e.maybeAutoHandoff(prompt)
	if e.shouldUseNativeToolLoop() {
		completion, err := e.askWithNativeToolsAutoContinue(ctx, prompt)
		if err != nil {
			return nil, err
		}
		return streamAnswerText(ctx, completion.Answer), nil
	}
	e.ensureIndexed(ctx)

	chunks := e.buildContextChunks(prompt)

	systemPrompt, systemBlocks := e.buildSystemPrompt(prompt, chunks)
	req := provider.CompletionRequest{
		Provider:     e.provider(),
		Model:        e.model(),
		Messages:     e.buildRequestMessages(prompt, chunks, systemPrompt),
		Context:      chunks,
		System:       systemPrompt,
		SystemBlocks: systemBlocks,
	}
	requestInputTokens := estimateRequestTokens(systemPrompt, chunks, req.Messages)
	if e.EventBus != nil {
		e.EventBus.Publish(Event{
			Type:   "provider:stream:start",
			Source: "engine",
			Payload: map[string]any{
				"provider":     req.Provider,
				"model":        req.Model,
				"input_tokens": requestInputTokens,
				"tokens":       requestInputTokens,
			},
		})
	}

	stream, usedProvider, err := e.Providers.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	out := make(chan provider.StreamEvent, 32)
	go func() {
		defer close(out)
		var acc strings.Builder
		draining := false
		for ev := range stream {
			if ev.Type == provider.StreamDelta {
				acc.WriteString(ev.Delta)
			}
			if draining {
				if ev.Type == provider.StreamError || ev.Type == provider.StreamDone {
					return
				}
				continue
			}
			// Pre-fix this was a bare `out <- ev` with no ctx.Done()
			// guard — if the HTTP/SSE consumer walked away mid-stream,
			// the upstream provider channel kept producing, this
			// goroutine kept blocking on a full buffered out chan, and
			// the upstream stream never got drained. Result: leaked
			// goroutine + upstream HTTP connection held open until the
			// provider's own timeout fired.
			select {
			case out <- ev:
			case <-ctx.Done():
				draining = true
				if ev.Type == provider.StreamError || ev.Type == provider.StreamDone {
					return
				}
				continue
			}
			if ev.Type == provider.StreamError {
				return
			}
			if ev.Type == provider.StreamDone {
				answer := acc.String()
				if strings.TrimSpace(answer) != "" {
					usage := provider.Usage{}
					if ev.Usage != nil {
						usage = *ev.Usage
					}
					if usage.InputTokens <= 0 {
						usage.InputTokens = requestInputTokens
					}
					if usage.OutputTokens <= 0 {
						usage.OutputTokens = tokens.Estimate(answer)
					}
					if usage.TotalTokens <= 0 {
						usage.TotalTokens = usage.InputTokens + usage.OutputTokens
					}
					e.recordInteraction(prompt, answer, usedProvider, req.Model, usage.TotalTokens, chunks)
					e.EventBus.Publish(Event{
						Type:   "provider:complete",
						Source: "engine",
						Payload: map[string]any{
							"provider":      usedProvider,
							"model":         req.Model,
							"tokens":        usage.TotalTokens,
							"input_tokens":  usage.InputTokens,
							"output_tokens": usage.OutputTokens,
							"total_tokens":  usage.TotalTokens,
						},
					})
				}
				return
			}
		}
	}()
	return out, nil
}
