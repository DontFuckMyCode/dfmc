// Ask / StreamAsk / history-management methods for the Engine.
// Extracted from engine.go. Covers the end-to-end completion paths
// (single, raced, metadata, streaming), the history budget + summary
// machinery that keeps the conversation under the model's context
// window, and the initial codebase indexer that primes AST/CodeMap.

package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/dontfuckmycode/dfmc/internal/hooks"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

const (
	historySummaryBudgetDivisor = 6
	historyBudgetDivisor        = 16
)

func (e *Engine) buildRequestMessages(question string, chunks []types.ContextChunk, systemPrompt string) []provider.Message {
	historyBudget := e.historyBudgetForRequest(question, chunks, systemPrompt)
	summaryBudget := 0
	if historyBudget >= 64 {
		summaryBudget = clampInt(historyBudget/historySummaryBudgetDivisor, minHistorySummaryTokens, maxHistorySummaryTokens)
	}
	mainBudget := historyBudget - summaryBudget
	if mainBudget < minHistorySummaryTokens {
		mainBudget = historyBudget
		summaryBudget = 0
	}

	msgs, omitted := e.trimmedConversationMessages(mainBudget)
	if summaryBudget > 0 && len(omitted) > 0 {
		summary := buildHistorySummary(omitted, summaryBudget)
		if strings.TrimSpace(summary) != "" {
			msgs = append([]provider.Message{
				{Role: types.RoleAssistant, Content: summary},
			}, msgs...)
		}
	}
	msgs = append(msgs, provider.Message{
		Role:    types.RoleUser,
		Content: question,
	})
	return msgs
}

func (e *Engine) conversationHistoryBudget() int {
	budget := e.Config.Context.MaxHistoryTokens
	if budget <= 0 {
		limit := e.providerMaxContext()
		if limit <= 0 {
			limit = defaultProviderContextTokens
		}
		budget = limit / historyBudgetDivisor
		if budget <= 0 {
			budget = defaultHistoryBudgetTokens
		}
	}
	if budget < minContextPerFileTokens {
		budget = minContextPerFileTokens
	}
	if budget > maxHistoryBudgetTokens {
		budget = maxHistoryBudgetTokens
	}
	return budget
}

func (e *Engine) trimmedConversationMessages(budget int) ([]provider.Message, []types.Message) {
	if e.Conversation == nil {
		return nil, nil
	}
	active := e.Conversation.Active()
	if active == nil {
		return nil, nil
	}
	rawHistory := active.Messages()
	if len(rawHistory) == 0 {
		return nil, nil
	}
	if budget <= 0 {
		return nil, nil
	}

	history := make([]types.Message, 0, len(rawHistory))
	for _, msg := range rawHistory {
		if msg.Role != types.RoleUser && msg.Role != types.RoleAssistant {
			continue
		}
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		history = append(history, msg)
	}
	if len(history) == 0 {
		return nil, nil
	}

	out := make([]provider.Message, 0, minInt(maxHistoryMessages, len(history)))
	used := 0
	cutoff := -1

	for i := len(history) - 1; i >= 0; i-- {
		if len(out) >= maxHistoryMessages || used >= budget {
			cutoff = i
			break
		}
		msg := history[i]
		content := strings.TrimSpace(msg.Content)
		tok := estimateTokens(content)
		if tok <= 0 {
			continue
		}
		if used+tok > budget {
			remaining := budget - used
			if remaining < minHistorySummaryTokens {
				cutoff = i
				break
			}
			content = trimToTokenBudget(content, remaining)
			tok = estimateTokens(content)
			if tok <= 0 {
				cutoff = i
				break
			}
		}
		out = append(out, provider.Message{
			Role:    msg.Role,
			Content: content,
		})
		used += tok
	}

	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if cutoff < 0 {
		return out, nil
	}
	omitted := make([]types.Message, cutoff+1)
	copy(omitted, history[:cutoff+1])
	return out, omitted
}

func (e *Engine) historyBudgetForRequest(question string, chunks []types.ContextChunk, systemPrompt string) int {
	providerLimit := e.providerMaxContext()
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}
	responseReserve := defaultResponseReserveTokens
	if prof, ok := e.Config.Providers.Profiles[e.provider()]; ok && prof.MaxTokens > 0 {
		responseReserve = prof.MaxTokens
	}
	if responseReserve > maxResponseReserveTokens {
		responseReserve = maxResponseReserveTokens
	}
	if responseReserve < minContextPerFileTokens {
		responseReserve = minContextPerFileTokens
	}

	usedByRequest := estimateTokens(question) + estimateTokens(systemPrompt) + baseToolReserveTokens
	for _, ch := range chunks {
		usedByRequest += ch.TokenCount
	}
	available := providerLimit - responseReserve - usedByRequest
	if available <= 0 {
		return 0
	}

	maxHistory := e.conversationHistoryBudget()
	return minInt(maxHistory, available)
}

func trimToTokenBudget(content string, maxTokens int) string {
	return tokens.TrimToBudget(content, maxTokens, "")
}

func buildHistorySummary(omitted []types.Message, maxTokens int) string {
	if maxTokens <= 0 || len(omitted) == 0 {
		return ""
	}
	userN := 0
	assistantN := 0
	for _, m := range omitted {
		if m.Role == types.RoleUser {
			userN++
		}
		if m.Role == types.RoleAssistant {
			assistantN++
		}
	}
	terms := topTermsFromMessages(omitted, 3)
	files := topFileMentions(omitted, 2)
	primary := latestOmittedByRole(omitted, types.RoleUser, 12)
	progress := latestOmittedByRole(omitted, types.RoleAssistant, 12)
	openItems := recentUserQuestions(omitted, 1, 10)

	var b strings.Builder
	fmt.Fprintf(&b, "[History summary] Scope=%d msgs (%dU/%dA).", len(omitted), userN, assistantN)
	if primary != "" {
		b.WriteString(" Primary=")
		b.WriteString(primary)
		b.WriteString(".")
	}
	if progress != "" {
		b.WriteString(" Progress=")
		b.WriteString(progress)
		b.WriteString(".")
	}
	if len(terms) > 0 {
		b.WriteString(" Topics=")
		b.WriteString(strings.Join(terms, ", "))
		b.WriteString(".")
	}
	if len(files) > 0 {
		b.WriteString(" Files=")
		b.WriteString(strings.Join(files, ", "))
		b.WriteString(".")
	}
	if len(openItems) > 0 {
		b.WriteString(" Open=")
		b.WriteString(strings.Join(openItems, " | "))
		b.WriteString(".")
	}
	return trimToTokenBudget(b.String(), maxTokens)
}

func latestOmittedByRole(messages []types.Message, role types.MessageRole, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != role {
			continue
		}
		s := trimToTokenBudget(strings.TrimSpace(messages[i].Content), maxTokens)
		if s != "" {
			return s
		}
	}
	return ""
}

func recentUserQuestions(messages []types.Message, maxItems, maxTokensPerItem int) []string {
	if maxItems <= 0 {
		return nil
	}
	out := make([]string, 0, maxItems)
	for i := len(messages) - 1; i >= 0 && len(out) < maxItems; i-- {
		msg := messages[i]
		if msg.Role != types.RoleUser {
			continue
		}
		text := strings.TrimSpace(msg.Content)
		if !strings.Contains(text, "?") {
			continue
		}
		s := trimToTokenBudget(text, maxTokensPerItem)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func topTermsFromMessages(messages []types.Message, limit int) []string {
	if limit <= 0 {
		return nil
	}
	stop := map[string]struct{}{
		"the": {}, "and": {}, "for": {}, "with": {}, "this": {}, "that": {}, "from": {}, "into": {}, "your": {}, "you": {},
		"about": {}, "also": {}, "just": {}, "when": {}, "then": {}, "than": {}, "what": {}, "which": {}, "where": {}, "while": {},
		"code": {}, "file": {}, "line": {}, "tool": {}, "message": {}, "messages": {}, "user": {}, "assistant": {},
	}
	counts := map[string]int{}
	for _, msg := range messages {
		for _, tok := range tokenizeForSummary(msg.Content) {
			if _, blocked := stop[tok]; blocked {
				continue
			}
			counts[tok]++
		}
	}
	type kv struct {
		Key   string
		Count int
	}
	ranked := make([]kv, 0, len(counts))
	for k, c := range counts {
		ranked = append(ranked, kv{Key: k, Count: c})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Count == ranked[j].Count {
			return ranked[i].Key < ranked[j].Key
		}
		return ranked[i].Count > ranked[j].Count
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]string, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.Key)
	}
	return out
}

func tokenizeForSummary(text string) []string {
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(text)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) < 3 {
			continue
		}
		out = append(out, p)
	}
	return out
}

func topFileMentions(messages []types.Message, limit int) []string {
	if limit <= 0 {
		return nil
	}
	counts := map[string]int{}
	for _, msg := range messages {
		matches := fileMentionRe.FindAllString(strings.TrimSpace(msg.Content), -1)
		for _, m := range matches {
			key := strings.ToLower(strings.TrimSpace(strings.Trim(m, ".,;:()[]{}\"'`")))
			if key == "" {
				continue
			}
			counts[key]++
		}
	}
	type kv struct {
		Key   string
		Count int
	}
	ranked := make([]kv, 0, len(counts))
	for k, c := range counts {
		ranked = append(ranked, kv{Key: k, Count: c})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Count == ranked[j].Count {
			return ranked[i].Key < ranked[j].Key
		}
		return ranked[i].Count > ranked[j].Count
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	out := make([]string, 0, len(ranked))
	for _, item := range ranked {
		out = append(out, item.Key)
	}
	return out
}
func (e *Engine) indexCodebase(ctx context.Context) {
	start := time.Now()
	e.EventBus.Publish(Event{Type: "index:start", Source: "engine", Payload: e.ProjectRoot})
	paths, err := e.collectSourceFiles(e.ProjectRoot)
	if err != nil {
		e.EventBus.Publish(Event{Type: "index:error", Source: "engine", Payload: err.Error()})
		return
	}

	if e.CodeMap != nil {
		if err := e.CodeMap.BuildFromFiles(ctx, paths, func(processed, total int) {
			e.EventBus.Publish(Event{
				Type:   "index:progress",
				Source: "engine",
				Payload: map[string]any{
					"processed": processed,
					"total":     total,
				},
			})
		}); err != nil {
			if errors.Is(err, context.Canceled) {
				e.EventBus.Publish(Event{Type: "index:cancelled", Source: "engine"})
			} else {
				e.EventBus.Publish(Event{Type: "index:error", Source: "engine", Payload: err.Error()})
			}
			return
		}
	}

	select {
	case <-ctx.Done():
		e.EventBus.Publish(Event{Type: "index:cancelled", Source: "engine"})
		return
	default:
	}
	e.EventBus.Publish(Event{
		Type:   "index:done",
		Source: "engine",
		Payload: map[string]any{
			"duration_ms": time.Since(start).Milliseconds(),
			"files":       len(paths),
		},
	})
}
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
		return "", "", fmt.Errorf("question cannot be empty")
	}
	if e.Providers == nil {
		return "", "", fmt.Errorf("provider router is not initialized")
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

// buildSystemPrompt renders the system prompt bundle via the context manager
// and returns both the flat text form (for providers that ignore caching)
// and the structured SystemBlocks (for Anthropic's prompt caching). Returns
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
		completion, err := e.askWithNativeTools(ctx, prompt)
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
			"provider": usedProvider,
			"model":    resp.Model,
			"tokens":   resp.Usage.TotalTokens,
		},
	})
	return resp.Text, nil
}

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
		return nil, fmt.Errorf("question cannot be empty")
	}
	if e.Providers == nil {
		return nil, fmt.Errorf("provider router is not initialized")
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
		completion, err := e.askWithNativeTools(ctx, prompt)
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
					tokenEstimate := estimateTokens(prompt) + estimateTokens(answer)
					e.recordInteraction(prompt, answer, usedProvider, req.Model, tokenEstimate, chunks)
					e.EventBus.Publish(Event{
						Type:   "provider:complete",
						Source: "engine",
						Payload: map[string]any{
							"provider": usedProvider,
							"model":    req.Model,
							"tokens":   tokenEstimate,
						},
					})
				}
				return
			}
		}
	}()
	return out, nil
}
func (e *Engine) recordInteraction(question, answer, providerName, model string, tokenCount int, chunks []types.ContextChunk) {
	if e.Conversation != nil {
		e.Conversation.AddMessage(providerName, model, types.Message{
			Role:      types.RoleUser,
			Content:   question,
			Timestamp: time.Now(),
		})
		e.Conversation.AddMessage(providerName, model, types.Message{
			Role:      types.RoleAssistant,
			Content:   answer,
			Timestamp: time.Now(),
			TokenCnt:  tokenCount,
			Metadata: map[string]string{
				"provider": providerName,
				"model":    model,
			},
		})
	}
	if e.Memory != nil {
		e.Memory.SetWorkingQuestionAnswer(question, answer)
		for _, ch := range chunks {
			e.Memory.TouchFile(ch.Path)
		}
		_ = e.Memory.AddEpisodicInteraction(e.ProjectRoot, question, answer, 0.7)
	}
}
