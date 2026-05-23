package engine

// agent_handoff.go — offline (LLM-free) auto-new-session handoff. Complements
// agent_compact.go: when the accumulated conversation history plus the next
// user question will blow past AutoHandoffThresholdRatio of the provider's
// context window, we close the current conversation and start a fresh one
// seeded with a terse "handoff brief". The brief generator (buildHandoffBrief
// + readPathFromCall + renderOpenTodos + truncateRunes + sortedStringKeys)
// lives in agent_handoff_brief.go; this file owns the rotation trigger
// (maybeAutoHandoff) and the conversation-save warning helper.
//
// Trip order:
//   1. Below AutoCompactThresholdRatio      → loop runs raw.
//   2. Above compact, below handoff         → mid-loop compaction (see
//                                             agent_compact.go).
//   3. Above AutoHandoffThresholdRatio      → this file: rotate conversation
//                                             before the next turn even
//                                             begins.

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/tokens"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// handoffReport captures what maybeAutoHandoff did so the caller can emit a
// telemetry event and so tests can assert the behaviour.
type handoffReport struct {
	OldConversationID string
	NewConversationID string
	HistoryTokens     int
	BriefTokens       int
	MessagesSealed    int
	ThresholdRatio    float64
	MaxBriefTokens    int
}

// maybeAutoHandoff checks whether the current conversation plus the pending
// question is over AutoHandoffThresholdRatio. If so, it seals the active
// conversation, starts a new one seeded with a handoff brief assistant turn,
// and publishes context:lifecycle:handoff. Returns the report so the caller
// can surface the event payload.
//
// This function only ever rotates the *conversation manager* — it does not
// touch the in-flight request state because rotation must happen before any
// provider call is made for the new question.
func (e *Engine) maybeAutoHandoff(question string) *handoffReport {
	if e == nil || e.Conversation == nil {
		return nil
	}
	lifecycle := e.resolveContextLifecycle()
	if !lifecycle.Enabled {
		return nil
	}
	ratio := lifecycle.AutoHandoffThresholdRatio
	if ratio <= 0 {
		return nil
	}
	// Guard against misconfigurations that would make compaction pointless.
	if ratio <= lifecycle.AutoCompactThresholdRatio {
		return nil
	}

	providerLimit := e.providerMaxContext()
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}
	threshold := int(float64(providerLimit) * ratio)
	if threshold <= 0 {
		return nil
	}

	active := e.Conversation.Active()
	if active == nil {
		return nil
	}
	history := active.Messages()
	if len(history) == 0 {
		return nil
	}

	pending := tokens.Estimate(question)
	for _, msg := range history {
		pending += tokens.Estimate(msg.Content)
		for _, call := range msg.ToolCalls {
			pending += tokens.Estimate(call.Name)
			for k, v := range call.Params {
				pending += tokens.Estimate(k) + tokens.Estimate(fmt.Sprint(v))
			}
		}
		for _, r := range msg.Results {
			pending += tokens.Estimate(r.Output)
		}
	}
	if pending < threshold {
		return nil
	}

	briefBudget := lifecycle.HandoffBriefMaxTokens
	if briefBudget <= 0 {
		briefBudget = 500
	}
	var openTodos []tools.TodoItem
	if e.Tools != nil {
		openTodos = e.Tools.TodoSnapshot()
	}
	brief := buildHandoffBrief(active.ID, history, openTodos, briefBudget)
	if strings.TrimSpace(brief) == "" {
		return nil
	}

	oldID := active.ID
	provider := strings.TrimSpace(active.Provider)
	if provider == "" {
		provider = e.provider()
	}
	model := strings.TrimSpace(active.Model)
	if model == "" {
		model = e.model()
	}

	newConv := e.Conversation.Start(provider, model)
	if newConv == nil {
		return nil
	}
	e.Conversation.AddMessage(provider, model, types.Message{
		Role:    types.RoleAssistant,
		Content: brief,
		Metadata: map[string]string{
			"auto_handoff":        "true",
			"source_conversation": oldID,
		},
	})
	// New conversation starts with a non-trivial brief — flush
	// immediately so the handoff state is durable. Same rationale
	// as the per-turn save in the agent loop.
	e.saveActiveConversationWithWarning("handoff", map[string]any{
		"old_conversation": oldID,
		"new_conversation": newConv.ID,
	})

	report := &handoffReport{
		OldConversationID: oldID,
		NewConversationID: newConv.ID,
		HistoryTokens:     pending,
		BriefTokens:       tokens.Estimate(brief),
		MessagesSealed:    len(history),
		ThresholdRatio:    ratio,
		MaxBriefTokens:    briefBudget,
	}
	e.publishAgentLoopEvent("context:lifecycle:handoff", map[string]any{
		"old_conversation": oldID,
		"new_conversation": newConv.ID,
		"history_tokens":   report.HistoryTokens,
		"brief_tokens":     report.BriefTokens,
		"messages_sealed":  report.MessagesSealed,
		"threshold_ratio":  report.ThresholdRatio,
		"max_brief_tokens": report.MaxBriefTokens,
		"surface":          "conversation",
	})
	return report
}

func (e *Engine) saveActiveConversationWithWarning(surface string, payload map[string]any) {
	if e == nil || e.Conversation == nil {
		return
	}
	if err := e.Conversation.SaveActive(); err != nil {
		if payload == nil {
			payload = map[string]any{}
		}
		payload["surface"] = strings.TrimSpace(surface)
		payload["error"] = err.Error()
		e.publishAgentLoopEvent("conversation:save:error", payload)
	}
}
