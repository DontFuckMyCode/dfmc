package engine

// engine_ask_history.go — history-budget machinery extracted from
// engine_ask.go. Owns the rules for how many tokens of conversation
// tail get sent on each Ask, how the trimmed window is built, and the
// "trim happened" event surface. The summary that replaces older
// turns lives in engine_ask_history_summary.go and the per-turn
// tool-call recap lives in engine_ask_history_tail.go. Pure-function
// helpers are file-private; everything that touches Engine state lives
// on Engine receivers so the public Ask surface in engine_ask.go can
// call into it without a circular dependency.

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

const (
	historySummaryBudgetDivisor = 6
	historyBudgetDivisor        = 16
)

// publishHistoryTrimmedEvent surfaces the history-trim decision so the
// TUI / web can render a "we kept N turns and summarized M older ones"
// hint instead of silently losing context. Without this event the
// trim is invisible — the user assumes the assistant simply forgot.
func (e *Engine) publishHistoryTrimmedEvent(kept []provider.Message, omitted []types.Message, summary string, historyBudget, summaryBudget int) {
	if e == nil || e.EventBus == nil {
		return
	}
	keptCount := 0
	keptTokens := 0
	for _, m := range kept {
		if m.Role != types.RoleUser && m.Role != types.RoleAssistant {
			continue
		}
		keptCount++
		keptTokens += tokens.Estimate(m.Content)
	}
	preview := summary
	const maxPreview = 240
	if len(preview) > maxPreview {
		preview = preview[:maxPreview] + "…"
	}
	e.EventBus.Publish(Event{
		Type:   "history:trimmed",
		Source: "engine",
		Payload: map[string]any{
			"kept_messages":    keptCount,
			"kept_tokens":      keptTokens,
			"omitted_messages": len(omitted),
			"summary_tokens":   tokens.Estimate(summary),
			"summary_budget":   summaryBudget,
			"history_budget":   historyBudget,
			"summary_preview":  preview,
		},
	})
}

func (e *Engine) conversationHistoryBudget() int {
	// User-set Context.MaxHistoryTokens is authoritative — bypass the
	// auto-compute cap so a user on a 1M-window Opus can extend memory
	// well beyond the safe-floor default. The cap exists to keep the
	// auto-compute path from overshooting on huge windows; once the
	// user has explicitly chosen a number, trust it.
	if userSet := e.Config.Context.MaxHistoryTokens; userSet > 0 {
		if userSet < minContextPerFileTokens {
			return minContextPerFileTokens
		}
		return userSet
	}
	limit := e.providerMaxContext()
	if limit <= 0 {
		limit = defaultProviderContextTokens
	}
	budget := limit / historyBudgetDivisor
	if budget <= 0 {
		budget = defaultHistoryBudgetTokens
	}
	if budget < minContextPerFileTokens {
		budget = minContextPerFileTokens
	}
	if budget > maxHistoryBudgetTokens {
		budget = maxHistoryBudgetTokens
	}
	return budget
}

// conversationHistoryMaxMessages resolves the message-count cap for the
// trim window. User-set values pass through; zero falls back to the
// engine's compiled-in floor. Floor is the same constant the trim
// loop uses directly when this returns its default — keeping a single
// source of truth.
func (e *Engine) conversationHistoryMaxMessages() int {
	if e == nil || e.Config == nil {
		return maxHistoryMessages
	}
	if n := e.Config.Context.MaxHistoryMessages; n > 0 {
		return n
	}
	return maxHistoryMessages
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
		// Keep tool-only assistant turns (Content empty but ToolCalls/
		// Results present) — historicaly these were dropped, which
		// erased entire rounds of agentic work from the model's view.
		if strings.TrimSpace(msg.Content) == "" && len(msg.ToolCalls) == 0 && len(msg.Results) == 0 {
			continue
		}
		history = append(history, msg)
	}
	if len(history) == 0 {
		return nil, nil
	}

	maxMsgs := e.conversationHistoryMaxMessages()
	out := make([]provider.Message, 0, minInt(maxMsgs, len(history)))
	used := 0
	cutoff := -1

	for i := len(history) - 1; i >= 0; i-- {
		if len(out) >= maxMsgs || used >= budget {
			cutoff = i
			break
		}
		msg := history[i]
		content := strings.TrimSpace(msg.Content)
		// Append a compact tool-work tail to assistant turns so the
		// model sees what tools it called previously instead of just
		// the prose answer. Without this, every new user turn starts
		// blind to prior tool history — the model rediscovers files
		// and re-runs commands it already executed last turn. The
		// tail is text-only (no raw tool output, no params blob), so
		// it costs ~30-50 tokens per turn instead of the kilobytes a
		// real tool result would.
		if msg.Role == types.RoleAssistant {
			if tail := renderHistoricalToolTail(msg); tail != "" {
				if content == "" {
					content = tail
				} else {
					content = content + "\n\n" + tail
				}
			}
		}
		// Prefix every history turn with its message ID so the LLM
		// can name pruning candidates in its [cleanup: ...] hint.
		// IDs are short (~9 chars) so the per-turn overhead is
		// negligible vs the unlock it provides for pruning.
		if id := strings.TrimSpace(msg.ID); id != "" && content != "" {
			content = "[id:" + id + "] " + content
		}
		tok := tokens.Estimate(content)
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
			tok = tokens.Estimate(content)
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

	// The backward walk above can leave an assistant turn at the head of
	// the kept window when the budget cuts mid-pair. Anthropic's
	// /messages API (and the Anthropic-compat paths in kimi/zai/minimax)
	// hard-reject a request whose messages array starts with an
	// assistant turn - the operator would see a generic 400 with no tie
	// back to the trim decision. Peel leading assistants into the
	// omitted slice so the kept window always opens on a user turn; the
	// dropped entries still contribute to the history summary.
	firstKept := 0
	if cutoff >= 0 {
		firstKept = cutoff + 1
	}
	for len(out) > 0 && out[0].Role == types.RoleAssistant {
		out = out[1:]
		firstKept++
	}

	if firstKept == 0 {
		return out, nil
	}
	omitted := make([]types.Message, firstKept)
	copy(omitted, history[:firstKept])
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

	usedByRequest := tokens.Estimate(question) + tokens.Estimate(systemPrompt) + baseToolReserveTokens
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
