// engine_ask.go — public Ask surface for the Engine. Composes the request
// builder (engine_ask_request.go), the history-budget machinery
// (engine_ask_history.go), and the provider router into the four entry
// points the rest of the codebase calls: Ask (default), AskRaced (concurrent
// candidates), AskWithMetadata (the canonical path with intent routing),
// and StreamAsk (SSE for the web UI / TUI streaming chat — lives in
// engine_ask_stream.go).
//
// The split keeps this file focused on lifecycle and intent routing; every
// "how is the request shaped" or "how do we trim history" detail lives in a
// sibling. Adding a new synchronous completion entry point should land
// here; adjusting trim/summary heuristics belongs in engine_ask_history.go;
// persisting a completed turn belongs in engine_ask_request.go; SSE
// pumping + leak-prevention belongs in engine_ask_stream.go.

package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/provider"
)

func (e *Engine) Ask(ctx context.Context, question string) (string, error) {
	return e.AskWithMetadata(ctx, question)
}

// AskRaced issues the same completion request against every candidate
// provider concurrently and returns the first successful response. When
// candidates is nil/empty the router derives candidates from ResolveOrder
// (stripping the offline stub).
//
// Race mode always goes through the non-tool-loop path: racing N provider-
// native tool loops would have them trying to edit files concurrently with
// no coordination. For multi-turn tool work, use Ask/Chat normally; race is
// for single-shot Q&A where latency or reliability matters more than cost.
func (e *Engine) AskRaced(ctx context.Context, question string, candidates []string) (string, string, error) {
	if err := e.requireReady("ask"); err != nil {
		return "", "", err
	}
	if err := e.maybeAutoReloadProjectConfig(); err != nil {
		return "", "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(question) == "" {
		return "", "", errors.New("question cannot be empty")
	}
	if e.Providers == nil {
		return "", "", errors.New("provider router is not initialized")
	}
	e.maybeAutoHandoff(question)
	e.ensureIndexed(ctx)

	chunks := e.buildContextChunks(question)
	systemPrompt, systemBlocks := e.buildSystemPrompt(question, chunks)
	req := provider.CompletionRequest{
		Provider:     e.provider(),
		Model:        e.model(),
		Messages:     e.buildRequestMessages(question, chunks, systemPrompt),
		Context:      chunks,
		System:       systemPrompt,
		SystemBlocks: systemBlocks,
	}
	start := time.Now()
	resp, winner, err := e.Providers.CompleteRaced(ctx, req, candidates)
	durMs := time.Since(start).Milliseconds()
	if err != nil {
		e.EventBus.Publish(Event{
			Type:   "provider:race:failed",
			Source: "engine",
			Payload: map[string]any{
				"candidates":  candidates,
				"duration_ms": durMs,
				"error":       err.Error(),
			},
		})
		return "", "", err
	}
	e.recordInteraction(question, resp.Text, winner, resp.Model, resp.Usage.TotalTokens, chunks)
	e.EventBus.Publish(Event{
		Type:   "provider:race:complete",
		Source: "engine",
		Payload: map[string]any{
			"winner":      winner,
			"candidates":  candidates,
			"model":       resp.Model,
			"tokens":      resp.Usage.TotalTokens,
			"duration_ms": durMs,
		},
	})
	return resp.Text, winner, nil
}

func (e *Engine) AskWithMetadata(ctx context.Context, question string) (string, error) {
	if err := e.requireReady("ask"); err != nil {
		return "", err
	}
	if err := e.maybeAutoReloadProjectConfig(); err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if question == "" {
		return "", fmt.Errorf("question cannot be empty")
	}
	if e.Providers == nil {
		return "", fmt.Errorf("provider router is not initialized")
	}

	// Intent layer normalizes vague follow-ups ("devam et", "fix it") into
	// self-contained instructions and decides whether this turn is a
	// resume of a parked agent or a fresh ask. Fail-open: on any layer
	// failure the raw question passes through and the path below is
	// unchanged. Resume + clarify paths short-circuit to dedicated
	// handlers so the main model isn't called when it doesn't need to be.
	decision := e.routeIntent(ctx, question)
	if decision.Intent == "resume" && e.HasParkedAgent() {
		completion, err := e.ResumeAgent(ctx, question)
		if err != nil {
			return "", err
		}
		return completion.Answer, nil
	}
	if decision.Intent == "clarify" && decision.FollowUpQuestion != "" {
		// Record the exchange so subsequent turns see what was asked.
		// Provider/model are tagged "intent-layer" to make these turns
		// distinguishable in conversation history (they didn't cost a
		// main-model call).
		e.recordInteraction(question, decision.FollowUpQuestion, "intent-layer", "clarify", 0, nil)
		return decision.FollowUpQuestion, nil
	}
	prompt := decision.EnrichedRequest
	if prompt == "" {
		prompt = question
	}

	e.maybeAutoHandoff(prompt)
	if e.shouldUseNativeToolLoop() {
		completion, err := e.askWithNativeToolsAutoContinue(ctx, prompt)
		if err != nil {
			return "", err
		}
		return completion.Answer, nil
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

	resp, usedProvider, err := e.Providers.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	e.recordInteraction(prompt, resp.Text, usedProvider, resp.Model, resp.Usage.TotalTokens, chunks)
	e.EventBus.Publish(Event{
		Type:   "provider:complete",
		Source: "engine",
		Payload: map[string]any{
			"provider":      usedProvider,
			"model":         resp.Model,
			"tokens":        resp.Usage.TotalTokens,
			"input_tokens":  resp.Usage.InputTokens,
			"output_tokens": resp.Usage.OutputTokens,
			"total_tokens":  resp.Usage.TotalTokens,
		},
	})
	return resp.Text, nil
}

// StreamAsk lives in engine_ask_stream.go.
