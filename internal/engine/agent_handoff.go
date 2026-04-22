package engine

// agent_handoff.go — offline (LLM-free) auto-new-session handoff. Complements
// agent_compact.go: when the accumulated conversation history plus the next
// user question will blow past AutoHandoffThresholdRatio of the provider's
// context window, we close the current conversation and start a fresh one
// seeded with a terse "handoff brief". The brief is produced without any
// provider call — we scan the outgoing conversation for user intent, tool
// activity, and the last assistant answer, and render a compact summary.
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

	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/internal/tokens"
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
		e.publishAgentLoopEvent("conversation:save_error", payload)
	}
}

// buildHandoffBrief renders a terse, LLM-free summary of an outgoing
// conversation so the new session has just enough context to keep going
// without paying for the whole transcript. Ordering:
//  1. Original user intent (first user turn, truncated).
//  2. Subsequent user asks (one-line each, up to a few).
//  3. Tool activity summary — count per tool name, ok/fail split.
//  4. Open todos (pending + in_progress from todo_write), if any.
//  5. Last assistant answer (truncated).
//
// Bounded by maxTokens (~4 chars per token). Deterministic: identical inputs
// produce identical output.
func buildHandoffBrief(convID string, history []types.Message, openTodos []tools.TodoItem, maxTokens int) string {
	if len(history) == 0 {
		return ""
	}
	var userTurns []string
	var lastAssistant string
	toolCounts := map[string]int{}
	toolSuccess := map[string]int{}
	toolFailure := map[string]int{}

	for _, msg := range history {
		switch msg.Role {
		case types.RoleUser:
			text := strings.TrimSpace(msg.Content)
			if text != "" {
				userTurns = append(userTurns, truncateRunes(text, 160))
			}
		case types.RoleAssistant:
			if text := strings.TrimSpace(msg.Content); text != "" {
				lastAssistant = text
			}
			for _, call := range msg.ToolCalls {
				name := strings.TrimSpace(call.Name)
				if name == "" {
					continue
				}
				toolCounts[name]++
			}
			for _, r := range msg.Results {
				name := strings.TrimSpace(r.Name)
				if name == "" {
					continue
				}
				if r.Success {
					toolSuccess[name]++
				} else {
					toolFailure[name]++
				}
			}
		}
	}

	lines := []string{fmt.Sprintf("[handoff brief · prior session %s]", convID)}
	if len(userTurns) > 0 {
		lines = append(lines, "original request: "+userTurns[0])
		if len(userTurns) > 1 {
			tail := userTurns[1:]
			if len(tail) > 3 {
				tail = tail[len(tail)-3:]
			}
			for _, t := range tail {
				lines = append(lines, "follow-up: "+t)
			}
		}
	}
	if len(toolCounts) > 0 {
		parts := make([]string, 0, len(toolCounts))
		for _, name := range sortedStringKeys(toolCounts) {
			count := toolCounts[name]
			ok := toolSuccess[name]
			fail := toolFailure[name]
			parts = append(parts, fmt.Sprintf("%s×%d ok=%d fail=%d", name, count, ok, fail))
		}
		lines = append(lines, "tool activity: "+strings.Join(parts, "; "))
	}
	if openLines := renderOpenTodos(openTodos); len(openLines) > 0 {
		lines = append(lines, openLines...)
	}
	if lastAssistant != "" {
		lines = append(lines, "last answer: "+truncateRunes(lastAssistant, 320))
	}

	body := strings.Join(lines, "\n")
	if maxTokens > 0 {
		budgetChars := maxTokens * 4
		if budgetChars > 0 && len(body) > budgetChars {
			body = body[:budgetChars] + "\n...[truncated]"
		}
	}
	return body
}

// renderOpenTodos emits brief lines for todo_write items still in-flight.
// Completed items are dropped — the handoff brief is about "what's left",
// not a status report. Caps at 8 lines to keep the brief bounded; overflow
// is represented as "+N more".
func renderOpenTodos(items []tools.TodoItem) []string {
	if len(items) == 0 {
		return nil
	}
	const maxLines = 8
	var pending, active []tools.TodoItem
	for _, it := range items {
		switch strings.ToLower(strings.TrimSpace(it.Status)) {
		case "completed", "done":
			continue
		case "in_progress", "active", "doing":
			active = append(active, it)
		default:
			pending = append(pending, it)
		}
	}
	if len(pending)+len(active) == 0 {
		return nil
	}
	header := fmt.Sprintf("open todos: %d pending, %d in_progress", len(pending), len(active))
	out := []string{header}
	ordered := append(active, pending...)
	shown := 0
	for _, it := range ordered {
		if shown >= maxLines {
			out = append(out, fmt.Sprintf("  (+%d more)", len(ordered)-shown))
			break
		}
		mark := "[ ]"
		if strings.EqualFold(strings.TrimSpace(it.Status), "in_progress") {
			mark = "[~]"
		}
		out = append(out, fmt.Sprintf("  %s %s", mark, truncateRunes(strings.TrimSpace(it.Content), 140)))
		shown++
	}
	return out
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "..."
}

func sortedStringKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// tiny inlined sort — avoids pulling in sort for one caller in hot path.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
