package engine

// agent_loop.go — shared helpers for the agent loop.
//
// The text-bridge flow (dfmc-tool fenced JSON) has been retired in favour of
// the provider-native loop in agent_loop_native.go. The helpers that survive
// here are the ones both the native loop and the streaming wrapper still use:
// request message assembly, token-budgeted history, payload trimming, event
// publishing, and the streamAnswerText fallback for non-streaming providers.

import (
	"context"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (e *Engine) buildToolLoopRequestMessages(question string, chunks []types.ContextChunk, systemPrompt string, tail []provider.Message) []provider.Message {
	historyBudget := e.historyBudgetForRequestWithTail(question, chunks, systemPrompt, tail)
	summaryBudget := 0
	if historyBudget >= 64 {
		summaryBudget = clampInt(historyBudget/6, minHistorySummaryTokens, maxHistorySummaryTokens)
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
	if len(tail) > 0 {
		msgs = append(msgs, tail...)
	}
	return msgs
}

func (e *Engine) historyBudgetForRequestWithTail(question string, chunks []types.ContextChunk, systemPrompt string, tail []provider.Message) int {
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

	usedByRequest := tokens.Estimate(question) + tokens.Estimate(systemPrompt) + baseToolReserveTokens
	for _, ch := range chunks {
		usedByRequest += ch.TokenCount
	}
	for _, msg := range tail {
		usedByRequest += tokens.Estimate(msg.Content)
	}
	available := providerLimit - responseReserve - usedByRequest
	if available <= 0 {
		return 0
	}

	maxHistory := e.conversationHistoryBudget()
	return minInt(maxHistory, available)
}

func trimToolPayload(raw string, maxChars int) string {
	trimmed := strings.TrimSpace(raw)
	if maxChars <= 0 {
		return trimmed
	}
	// Rune-based slicing — the parameter is "max characters" but the
	// previous implementation byte-sliced, which split multi-byte
	// UTF-8 sequences (CJK, emoji, accented Latin) at the boundary
	// and produced invalid UTF-8 that downstream JSON serializers
	// silently mangled. compactToolPayload in the same file always
	// did this correctly; trimToolPayload was the inconsistent one.
	return truncateRunesWithMarker(trimmed, maxChars, "\n...[truncated]")
}

// trimToolPayloadDetail is the same as trimToolPayload but returns
// whether truncation actually fired AND how many runes were dropped.
// Used by the result formatter to distinguish "ANSI/noise compression"
// (the model still has the full meaning) from "hard truncation" (the
// model is missing real bytes) so the TUI can surface a distinct
// "✂ truncated" badge instead of conflating both into a single
// compression-% number.
func trimToolPayloadDetail(raw string, maxChars int) (out string, hardTruncated bool, droppedRunes int) {
	trimmed := strings.TrimSpace(raw)
	if maxChars <= 0 {
		return trimmed, false, 0
	}
	r := []rune(trimmed)
	if len(r) <= maxChars {
		return trimmed, false, 0
	}
	out = truncateRunesWithMarker(trimmed, maxChars, "\n...[truncated]")
	droppedRunes = len(r) - len([]rune(out))
	return out, true, droppedRunes
}

// truncateRunesWithMarker caps `s` at `maxRunes` runes, appending the
// trailing marker (e.g. "...") when truncation actually fires. The
// marker is reserved out of the budget so the final output stays
// within `maxRunes` runes — this is what makes it safe to feed into
// downstream length-bounded buffers.
func truncateRunesWithMarker(s string, maxRunes int, marker string) string {
	if maxRunes <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	mr := []rune(marker)
	// Tiny budget: not enough room for marker — fall back to a hard
	// rune cap so we never expand beyond the requested cap.
	if maxRunes <= len(mr) {
		return string(r[:maxRunes])
	}
	cut := maxRunes - len(mr)
	return strings.TrimSpace(string(r[:cut])) + marker
}

// publishProviderCompleteWithSource is the richer variant used when the
// caller has the original prompt + assistant response in hand. The
// providerlog archive consumes the previews and the source label so a
// later "/log" view can show "agent_loop round 3 / Sonnet 4.6 / 1.2K
// tokens / preview…". When previews are empty the payload still flows;
// the archive simply omits those fields.
func (e *Engine) publishProviderCompleteWithSource(providerName, model string, tokenCount int, source, userPreview, assistantPreview string, usageParts ...provider.Usage) {
	if e.EventBus == nil {
		return
	}
	payload := map[string]any{
		"provider": providerName,
		"model":    model,
		"tokens":   tokenCount,
	}
	if strings.TrimSpace(source) != "" {
		payload["source"] = source
	}
	if strings.TrimSpace(userPreview) != "" {
		payload["user_preview"] = truncatePreview(userPreview, 240)
	}
	if strings.TrimSpace(assistantPreview) != "" {
		payload["assistant_preview"] = truncatePreview(assistantPreview, 240)
	}
	if len(usageParts) > 0 {
		usage := usageParts[0]
		if usage.InputTokens > 0 {
			payload["input_tokens"] = usage.InputTokens
		}
		if usage.OutputTokens > 0 {
			payload["output_tokens"] = usage.OutputTokens
		}
		if usage.TotalTokens > 0 {
			payload["total_tokens"] = usage.TotalTokens
			payload["tokens"] = usage.TotalTokens
		}
	}
	e.EventBus.Publish(Event{
		Type:    "provider:complete",
		Source:  "engine",
		Payload: payload,
	})
}

func (e *Engine) publishAgentLoopEvent(eventType string, payload map[string]any) {
	if e == nil || e.EventBus == nil {
		return
	}
	if payload == nil {
		payload = map[string]any{}
	}
	if _, ok := payload["provider"]; !ok {
		payload["provider"] = e.provider()
	}
	if _, ok := payload["model"]; !ok {
		payload["model"] = e.model()
	}
	e.EventBus.Publish(Event{
		Type:    strings.TrimSpace(eventType),
		Source:  "engine",
		Payload: payload,
	})
}

// streamAnswerText replays a complete answer string as line-granular
// StreamDelta events on a fresh channel, then a final StreamDone.
//
// LIFECYCLE CONTRACT — IMPORTANT for callers:
//   - The producer goroutine writes to a buffered channel (cap 16) and
//     only escapes on `ctx.Done()`. There is NO `default:` drop arm —
//     dropping deltas silently was strictly worse than blocking (the
//     user got a truncated answer with no error, M1 review caught this).
//   - Consumers MUST cancel `ctx` if they stop reading before draining
//     the channel. Closing the SSE connection / TUI tab / web client
//     without canceling will leak the goroutine forever once the buffer
//     fills (one big answer ≈ a handful of lines past 16).
//   - For HTTP/SSE handlers, derive `ctx` from the request context so
//     the runtime cancels automatically on client disconnect.
//   - For TUI consumers, cancel when navigating away or starting a new
//     stream so the prior one's goroutine exits.
func streamAnswerText(ctx context.Context, answer string) <-chan provider.StreamEvent {
	ch := make(chan provider.StreamEvent, 16)
	go func() {
		defer close(ch)
		if strings.TrimSpace(answer) == "" {
			ch <- provider.StreamEvent{Type: provider.StreamDone}
			return
		}
		lines := strings.Split(answer, "\n")
		for _, line := range lines {
			delta := line
			if !strings.HasSuffix(delta, "\n") {
				delta += "\n"
			}
			select {
			case <-ctx.Done():
				ch <- provider.StreamEvent{Type: provider.StreamError, Err: ctx.Err()}
				return
			case ch <- provider.StreamEvent{Type: provider.StreamDelta, Delta: delta}:
			}
		}
		select {
		case <-ctx.Done():
			ch <- provider.StreamEvent{Type: provider.StreamError, Err: ctx.Err()}
			return
		case ch <- provider.StreamEvent{Type: provider.StreamDone}:
		}
	}()
	return ch
}
