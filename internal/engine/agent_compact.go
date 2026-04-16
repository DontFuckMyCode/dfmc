package engine

// agent_compact.go — offline (LLM-free) auto-compaction for the native tool
// loop's in-flight message list. The goal is token-miser behaviour: when the
// running conversation plus tool rounds approach the provider's context
// window, collapse the oldest completed rounds into a single summary
// message so subsequent provider calls stay cheap.
//
// Honours cfg.Agent.ContextLifecycle: fires only above the configured ratio,
// keeps the last N rounds verbatim (so the model still sees recent tool
// evidence), and never splits an assistant+tool_result pair (splitting would
// break Anthropic/OpenAI tool-turn invariants).

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// compactionReport captures what maybeCompactNativeLoopHistory did so the
// caller can emit a telemetry event and so tests can assert the behaviour.
type compactionReport struct {
	BeforeTokens     int
	AfterTokens      int
	RoundsCollapsed  int
	MessagesRemoved  int
	ThresholdRatio   float64
	KeepRecentRounds int
}

// resolveContextLifecycle returns the effective lifecycle config for this
// engine, substituting safe defaults for zero values so yaml-missing fields
// behave predictably.
func (e *Engine) resolveContextLifecycle() config.ContextLifecycleConfig {
	out := config.ContextLifecycleConfig{
		Enabled:                   true,
		AutoCompactThresholdRatio: 0.7,
		KeepRecentRounds:          3,
		HandoffBriefMaxTokens:     500,
		AutoHandoffThresholdRatio: 0.9,
	}
	if e == nil || e.Config == nil {
		return out
	}
	cfg := e.Config.Agent.ContextLifecycle
	out.Enabled = cfg.Enabled
	if cfg.AutoCompactThresholdRatio > 0 {
		out.AutoCompactThresholdRatio = cfg.AutoCompactThresholdRatio
	}
	if cfg.KeepRecentRounds > 0 {
		out.KeepRecentRounds = cfg.KeepRecentRounds
	}
	if cfg.HandoffBriefMaxTokens > 0 {
		out.HandoffBriefMaxTokens = cfg.HandoffBriefMaxTokens
	}
	if cfg.AutoHandoffThresholdRatio > 0 {
		out.AutoHandoffThresholdRatio = cfg.AutoHandoffThresholdRatio
	}
	return out
}

// maybeCompactNativeLoopHistory checks whether the current in-loop message
// list plus the static context is approaching the provider's context window
// and, if so, collapses the oldest complete tool rounds into a summary
// message. Returns the (possibly rewritten) msgs and — when compaction
// fired — a report for event emission. Pure function otherwise: no side
// effects, no provider calls.
func (e *Engine) maybeCompactNativeLoopHistory(
	msgs []provider.Message,
	systemPrompt string,
	chunks []types.ContextChunk,
) ([]provider.Message, *compactionReport) {
	lifecycle := e.resolveContextLifecycle()
	if !lifecycle.Enabled {
		return msgs, nil
	}

	providerLimit := e.providerMaxContext()
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}
	threshold := int(float64(providerLimit) * lifecycle.AutoCompactThresholdRatio)
	if threshold <= 0 {
		return msgs, nil
	}

	current := estimateRequestTokens(systemPrompt, chunks, msgs)
	if current < threshold {
		return msgs, nil
	}
	return e.compactNativeLoopHistory(msgs, systemPrompt, chunks, current, lifecycle)
}

// forceCompactNativeLoopHistory runs compaction unconditionally (no threshold
// gate). Used on the resume path where we already know the parked history is
// fat — the next provider call will trip budget unless we collapse first.
// Still honours KeepRecentRounds and the "compaction saved nothing" early-out.
func (e *Engine) forceCompactNativeLoopHistory(
	msgs []provider.Message,
	systemPrompt string,
	chunks []types.ContextChunk,
) ([]provider.Message, *compactionReport) {
	lifecycle := e.resolveContextLifecycle()
	if !lifecycle.Enabled {
		return msgs, nil
	}
	current := estimateRequestTokens(systemPrompt, chunks, msgs)
	return e.compactNativeLoopHistory(msgs, systemPrompt, chunks, current, lifecycle)
}

// compactNativeLoopHistory is the shared collapse routine: splits the
// post-prefix messages into tool rounds, keeps the last KeepRecentRounds
// verbatim, and replaces the older rounds with a single summary message.
func (e *Engine) compactNativeLoopHistory(
	msgs []provider.Message,
	systemPrompt string,
	chunks []types.ContextChunk,
	current int,
	lifecycle config.ContextLifecycleConfig,
) ([]provider.Message, *compactionReport) {
	prefixEnd := findNativeLoopPrefixEnd(msgs)
	rounds := splitNativeLoopRounds(msgs[prefixEnd:])
	if len(rounds) <= lifecycle.KeepRecentRounds {
		return msgs, nil
	}
	collapseCount := len(rounds) - lifecycle.KeepRecentRounds
	if collapseCount <= 0 {
		return msgs, nil
	}

	toCollapse := rounds[:collapseCount]
	keep := rounds[collapseCount:]

	summary := summariseCollapsedRounds(toCollapse, 220)
	if strings.TrimSpace(summary) == "" {
		return msgs, nil
	}

	rebuilt := make([]provider.Message, 0, prefixEnd+1+totalRoundMessages(keep))
	rebuilt = append(rebuilt, msgs[:prefixEnd]...)
	rebuilt = append(rebuilt, provider.Message{
		Role:    types.RoleAssistant,
		Content: "[auto-compacted prior tool context]\n" + summary,
	})
	for _, r := range keep {
		rebuilt = append(rebuilt, r.Messages...)
	}

	after := estimateRequestTokens(systemPrompt, chunks, rebuilt)
	removed := len(msgs) - len(rebuilt)
	if removed <= 0 || after >= current {
		return msgs, nil
	}

	return rebuilt, &compactionReport{
		BeforeTokens:     current,
		AfterTokens:      after,
		RoundsCollapsed:  collapseCount,
		MessagesRemoved:  removed,
		ThresholdRatio:   lifecycle.AutoCompactThresholdRatio,
		KeepRecentRounds: lifecycle.KeepRecentRounds,
	}
}

// toolRound groups one assistant turn (optionally carrying tool_calls) with
// the user tool_result messages that immediately follow it.
type toolRound struct {
	Messages []provider.Message
}

// findNativeLoopPrefixEnd returns the index where the provider-injected
// prefix (history + original user question) ends and tool rounds begin. The
// prefix ends after the last user message that carries no ToolCallID — i.e.
// the organic user turn, not a tool_result turn.
func findNativeLoopPrefixEnd(msgs []provider.Message) int {
	end := 0
	for i, m := range msgs {
		if m.Role == types.RoleUser && strings.TrimSpace(m.ToolCallID) == "" {
			end = i + 1
		}
	}
	return end
}

// splitNativeLoopRounds walks the post-prefix slice and groups each assistant
// message with any consecutive user tool_result messages that follow. An
// assistant message with no trailing tool_results still forms a lone round
// (e.g. an interim reasoning turn).
func splitNativeLoopRounds(msgs []provider.Message) []toolRound {
	out := make([]toolRound, 0, len(msgs)/2+1)
	i := 0
	for i < len(msgs) {
		if msgs[i].Role != types.RoleAssistant {
			// Stray non-assistant start — attach to previous round if present,
			// otherwise start a new degenerate round. Either way, keep things
			// ordered so we don't lose messages.
			if len(out) > 0 {
				out[len(out)-1].Messages = append(out[len(out)-1].Messages, msgs[i])
				i++
				continue
			}
			out = append(out, toolRound{Messages: []provider.Message{msgs[i]}})
			i++
			continue
		}
		round := toolRound{Messages: []provider.Message{msgs[i]}}
		i++
		for i < len(msgs) && msgs[i].Role == types.RoleUser && strings.TrimSpace(msgs[i].ToolCallID) != "" {
			round.Messages = append(round.Messages, msgs[i])
			i++
		}
		out = append(out, round)
	}
	return out
}

func totalRoundMessages(rounds []toolRound) int {
	n := 0
	for _, r := range rounds {
		n += len(r.Messages)
	}
	return n
}

// summariseCollapsedRounds builds a terse textual summary of the rounds being
// dropped. Offline only: no LLM call. Each round contributes one line listing
// the tool names invoked and a short result-success/error tag.
func summariseCollapsedRounds(rounds []toolRound, maxTokens int) string {
	if len(rounds) == 0 {
		return ""
	}
	lines := make([]string, 0, len(rounds))
	for i, r := range rounds {
		if line := summariseSingleRound(i+1, r); line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	body := strings.Join(lines, "\n")
	budget := maxTokens * 4 // ~4 chars per token rough guide
	if budget > 0 && len(body) > budget {
		body = body[:budget] + "\n...[truncated]"
	}
	return body
}

func summariseSingleRound(index int, round toolRound) string {
	if len(round.Messages) == 0 {
		return ""
	}
	head := round.Messages[0]
	if head.Role != types.RoleAssistant {
		return ""
	}
	toolNames := make([]string, 0, len(head.ToolCalls))
	for _, call := range head.ToolCalls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		toolNames = append(toolNames, name)
	}
	successes := 0
	failures := 0
	for _, m := range round.Messages[1:] {
		if m.ToolError {
			failures++
		} else {
			successes++
		}
	}
	text := strings.TrimSpace(head.Content)
	if runes := []rune(text); len(runes) > 80 {
		text = string(runes[:80]) + "..."
	}

	parts := []string{fmt.Sprintf("round %d", index)}
	if len(toolNames) > 0 {
		parts = append(parts, "tools="+strings.Join(toolNames, ","))
	}
	if successes+failures > 0 {
		parts = append(parts, fmt.Sprintf("ok=%d fail=%d", successes, failures))
	}
	if text != "" {
		parts = append(parts, "note="+text)
	}
	return "- " + strings.Join(parts, " · ")
}

// estimateRequestTokens gives a consistent token estimate used both by the
// compaction decision and the post-compaction delta so the report reflects a
// real saving.
func estimateRequestTokens(systemPrompt string, chunks []types.ContextChunk, msgs []provider.Message) int {
	total := estimateTokens(systemPrompt)
	for _, ch := range chunks {
		total += ch.TokenCount
	}
	for _, m := range msgs {
		total += estimateTokens(m.Content)
		for _, call := range m.ToolCalls {
			if call.Name != "" {
				total += estimateTokens(call.Name)
			}
			for k, v := range call.Input {
				total += estimateTokens(k) + estimateTokens(fmt.Sprint(v))
			}
		}
	}
	return total
}
