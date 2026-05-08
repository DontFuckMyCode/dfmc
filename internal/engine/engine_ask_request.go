package engine

// engine_ask_request.go — request-shaping helpers extracted from
// engine_ask.go. Builds the provider request's messages slice (history
// trim + summary + new user turn), runs the initial codebase index
// that primes AST/CodeMap, and persists each turn into Conversation +
// Memory after the provider returns. The public Ask surface
// (Ask/AskRaced/AskWithMetadata/StreamAsk) lives in engine_ask.go and
// composes these helpers.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
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
	var summary string
	if summaryBudget > 0 && len(omitted) > 0 {
		summary = strings.TrimSpace(buildHistorySummary(omitted, summaryBudget))
	}
	msgs = append(msgs, provider.Message{
		Role:    types.RoleUser,
		Content: question,
	})
	// Merge the summary into the oldest kept user turn instead of
	// prepending it as its own message. Prepending-as-assistant left
	// msgs[0].Role == assistant (Anthropic/compat reject user-first
	// violations); prepending-as-user doubled up with the following
	// user turn (same rejection). Merging keeps the alternation valid
	// and preserves the [History summary] marker that downstream
	// prompts rely on.
	if summary != "" {
		for i := range msgs {
			if msgs[i].Role == types.RoleUser {
				msgs[i].Content = summary + "\n\n---\n\n" + msgs[i].Content
				break
			}
		}
	}
	if len(omitted) > 0 {
		e.publishHistoryTrimmedEvent(msgs, omitted, summary, historyBudget, summaryBudget)
	}
	return msgs
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
		// Best-effort async persist: non-blocking, failures logged.
		// This guards against crash-before-Shutdown losing the turn.
		e.Conversation.SaveActiveAsync()
	}
	if e.Memory != nil {
		e.Memory.SetWorkingQuestionAnswer(question, answer)
		for _, ch := range chunks {
			e.Memory.TouchFile(ch.Path)
		}
		_ = e.Memory.AddEpisodicInteraction(e.ProjectRoot, question, answer, 0.7)
	}
}
