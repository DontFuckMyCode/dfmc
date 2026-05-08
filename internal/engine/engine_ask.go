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

	"github.com/dontfuckmycode/dfmc/internal/drive"
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

	// Auto-decompose: prompt scoring ile çok adımlı mı tek adımlı mı
	// karar ver. True ise Drive'a yönlendir (plan + adım adım exec).
	if e.shouldDecomposePrompt(prompt) {
		result, err := e.runViaDrive(ctx, prompt)
		if err != nil {
			return "", err
		}
		return e.formatDriveResult(result), nil
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
			"provider":          usedProvider,
			"model":             resp.Model,
			"tokens":            resp.Usage.TotalTokens,
			"input_tokens":      resp.Usage.InputTokens,
			"output_tokens":     resp.Usage.OutputTokens,
			"total_tokens":      resp.Usage.TotalTokens,
			"source":            "ask",
			"user_preview":      truncatePreview(prompt, 240),
			"assistant_preview": truncatePreview(resp.Text, 240),
		},
	})
	return resp.Text, nil
}

// truncatePreview clamps a string to max runes with an ellipsis so the
// archive doesn't carry full prompts/answers (those live in the
// conversation JSONL — provider_calls is the rollup index).
func truncatePreview(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max < 4 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// StreamAsk lives in engine_ask_stream.go.

// shouldDecomposePrompt returns true when the prompt looks multi-step
// enough to benefit from Drive's TODO decomposition and adım-adım
// execution. Scoring is intentionally cheap (regex + heuristics) so
// it runs on every Ask without adding noticeable latency.
func (e *Engine) shouldDecomposePrompt(prompt string) bool {
	if e == nil || e.Config == nil {
		return false
	}
	if !e.Config.Agent.ChatAutoDecompose {
		return false
	}
	if prompt == "" {
		return false
	}

	// Low score = likely single-turn Q&A → stay in Ask.
	// High score = multi-step work → route to Drive.
	score := 0
	lower := strings.ToLower(prompt)

	// Sequential action signals
	if strings.Contains(lower, "and then") || strings.Contains(lower, "after that") {
		score += 3
	}
	if strings.Contains(lower, "also") || strings.Contains(lower, "bunun yanında") || strings.Contains(lower, "ek olarak") {
		score += 2
	}
	if strings.Contains(lower, "once ") || strings.Contains(lower, "first, ") || strings.Contains(lower, "önce ") {
		score += 2
	}

	// Bulk operation signals
	if strings.Contains(lower, "all ") && (strings.Contains(lower, "files") || strings.Contains(lower, "functions") || strings.Contains(lower, "tests")) {
		score += 4
	}
	if strings.Contains(lower, "every ") || strings.Contains(lower, "tüm ") {
		score += 3
	}
	if strings.Contains(lower, "multiple ") || strings.Contains(lower, "birden fazla") {
		score += 2
	}

	// Complex task signals
	if strings.Contains(lower, "create a project") || strings.Contains(lower, "set up") || strings.Contains(lower, "migrate") {
		score += 5
	}
	if strings.Contains(lower, "refactor") || strings.Contains(lower, "rewrite") {
		score += 4
	}
	if strings.Contains(lower, "fix all") || strings.Contains(lower, "update all") || strings.Contains(lower, "tümünü düzelt") {
		score += 4
	}
	if strings.Contains(lower, "implement ") || strings.Contains(lower, "write ") {
		score += 2
	}
	if strings.Contains(lower, "build ") || strings.Contains(lower, "create ") {
		score += 1
	}

	// Single-step Q&A signals (reduce score)
	if strings.HasPrefix(lower, "what is") || strings.HasPrefix(lower, "what's") {
		score -= 3
	}
	if strings.HasPrefix(lower, "how do i") || strings.HasPrefix(lower, "how can i") {
		score -= 2
	}
	if strings.HasPrefix(lower, "why ") || strings.HasPrefix(lower, "neden") {
		score -= 3
	}
	if strings.HasPrefix(lower, "explain ") {
		score -= 3
	}
	if strings.HasPrefix(lower, "show me") || strings.HasPrefix(lower, "bana göster") {
		score -= 2
	}
	if strings.HasPrefix(lower, "list ") {
		score -= 1
	}

	// Threshold: score >= 4 → Drive, else Ask
	return score >= 4
}

// runViaDrive executes the prompt through Drive's plan → execute loop.
// It creates a Driver from the engine's own components and blocks
// until the run finishes (or ctx cancels). Returns the final Run record.
func (e *Engine) runViaDrive(ctx context.Context, prompt string) (*drive.Run, error) {
	runner := e.NewDriveRunner()
	if runner == nil {
		return nil, fmt.Errorf("drive runner not available: providers not initialized")
	}

	dcfg := drive.DefaultConfig()
	// 60 dakika max — uzun refactor/migration işleri için yeterli.
	dcfg.MaxWallTime = 60 * time.Minute
	dcfg.MaxFailedTodos = 5 // 3 yerine 5 — karmaşık işlerde daha toleranslı
	dcfg.MaxParallel = 3
	dcfg.AutoApprove = []string{
		"read_file", "grep_codebase", "glob", "ast_query",
		"find_symbol", "list_dir", "web_fetch", "web_search",
	}

	// Adapter: engine.EventBus.Publish(func(Event)) → drive.Publisher(func(string, map))
	publisher := func(eventType string, payload map[string]any) {
		e.EventBus.Publish(Event{Type: eventType, Payload: payload})
	}

	d := drive.NewDriver(runner, nil, publisher, dcfg)
	return d.Run(ctx, prompt)
}

// formatDriveResult extracts a human-readable summary from the completed
// Drive run for the chat response. Returns "" on nil run.
func (e *Engine) formatDriveResult(run *drive.Run) string {
	if run == nil {
		return ""
	}
	done, blocked, skipped, _ := run.Counts()
	msg := fmt.Sprintf("[Drive] %s — %d done, %d blocked, %d skipped",
		run.Status, done, blocked, skipped)
	if run.Reason != "" {
		msg += " | " + run.Reason
	}
	return msg
}

