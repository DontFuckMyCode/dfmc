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
		// Keep the most-recent round verbatim; older rounds become a
		// one-line summary. Was 3 — dropped to 2 after real sessions
		// showed per-round tool_result payloads dominating the budget.
		// Two is still enough for the model to see what it just did
		// plus the setup round before it.
		KeepRecentRounds:          2,
		HandoffBriefMaxTokens:     500,
		AutoHandoffThresholdRatio: 0.9,
	}
	if e == nil || e.Config == nil {
		return out
	}
	cfg := e.Config.Agent.ContextLifecycle
	// Asymmetry note (REPORT.md #8): numeric fields use `> 0` as the
	// "unset" sentinel so a default-zero stays defaulted, but Enabled
	// is a bool — Go can't distinguish "unset" from "explicit false".
	// We rely on DefaultConfig() pre-seeding Enabled=true and YAML's
	// merge semantics preserving untouched fields, so the only paths
	// that yield cfg.Enabled==false are:
	//   (a) the user explicitly wrote `enabled: false` in YAML, or
	//   (b) a caller constructed ContextLifecycleConfig from a literal
	//       without copying defaults — a programmer error we won't
	//       paper over silently.
	// In both cases honouring cfg.Enabled is the correct behaviour.
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
	return e.maybeCompactNativeLoopHistoryForBudget(msgs, systemPrompt, chunks, 0)
}

// maybeCompactNativeLoopHistoryForBudget is the budget-aware variant.
// The compact threshold must sit BELOW the ceiling that actually parks
// the loop; otherwise we silently let the history drift until the
// park gate trips and it's too late. Callers pass the effective tool
// budget (cfg.Agent.MaxToolTokens or the elastic-scaled equivalent);
// compaction then fires at 0.7 × min(providerLimit, budget) — the
// binding constraint, not just the provider's hard window.
func (e *Engine) maybeCompactNativeLoopHistoryForBudget(
	msgs []provider.Message,
	systemPrompt string,
	chunks []types.ContextChunk,
	budgetTokens int,
) ([]provider.Message, *compactionReport) {
	lifecycle := e.resolveContextLifecycle()
	if !lifecycle.Enabled {
		return msgs, nil
	}

	providerLimit := e.providerMaxContext()
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}
	// Reference is the smaller of the provider window and the tool
	// budget. With defaults (128k provider, 120k budget) this barely
	// shifts the threshold, but on a 1M-window provider with a 120k
	// tool budget the threshold was firing at 700k — past parking —
	// before this change.
	reference := providerLimit
	if budgetTokens > 0 && budgetTokens < reference {
		reference = budgetTokens
	}
	threshold := int(float64(reference) * lifecycle.AutoCompactThresholdRatio)
	if threshold <= 0 {
		return msgs, nil
	}

	current := estimateRequestTokens(systemPrompt, chunks, msgs)
	if current < threshold {
		return msgs, nil
	}
	return e.compactNativeLoopHistory(msgs, systemPrompt, chunks, current, lifecycle)
}

// proactiveCompactRatio is the budget ratio at which the proactive
// step-boundary compactor fires once the loop is past the soft round
// cap. Lower than the reactive AutoCompactThresholdRatio (default 0.7)
// because we'd rather collapse old rounds preemptively than wait for
// the budget to tip into emergency-park territory. 0.5 keeps headroom
// stable through long sustained loops without compacting unnecessarily
// early.
const proactiveCompactRatio = 0.5

// proactiveCompactNativeLoopHistory is a step-boundary trigger meant for
// long-running loops. Same compactor body as the reactive variant but
// the threshold is gentler (proactiveCompactRatio) so headroom never
// gets a chance to crash. The loop body calls this only after the soft
// round cap so short Q&A turns don't pay the (small) compaction cost.
func (e *Engine) proactiveCompactNativeLoopHistory(
	msgs []provider.Message,
	systemPrompt string,
	chunks []types.ContextChunk,
	budgetTokens int,
) ([]provider.Message, *compactionReport) {
	lifecycle := e.resolveContextLifecycle()
	if !lifecycle.Enabled {
		return msgs, nil
	}
	providerLimit := e.providerMaxContext()
	if providerLimit <= 0 {
		providerLimit = defaultProviderContextTokens
	}
	reference := providerLimit
	if budgetTokens > 0 && budgetTokens < reference {
		reference = budgetTokens
	}
	threshold := int(float64(reference) * proactiveCompactRatio)
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

// summariseSingleRound collapses one round (assistant turn + its
// tool_results) into a multi-line block that PRESERVES enough signal
// for the model to keep reasoning about earlier work after compaction.
//
// Pre-fix the summary was the bare shape "round 5 · tools=read_file
// · ok=2 fail=0" — the actual file contents the model had read, the
// commands it ran, the errors it saw were all destroyed. After 5+
// rounds the model knew it "did something" but not what, so it
// re-discovered the same files / re-ran the same commands and burned
// budget rediscovering its own past. The user noticed: budget caps
// don't matter if tool results don't keep doing work in the model's
// reasoning context.
//
// Post-fix each round emits:
//   - the model's own narration line (head.Content) when present
//   - one indented line per tool_call: "↳ <tool> <target> → <result-head>"
//   - failures get the error tail trimmed for the same reason
//
// Result excerpts cap at ~120 chars per call so a 12-call round still
// fits in a few hundred tokens.
func summariseSingleRound(index int, round toolRound) string {
	if len(round.Messages) == 0 {
		return ""
	}
	head := round.Messages[0]
	if head.Role != types.RoleAssistant {
		return ""
	}

	// Pair each tool_call with its tool_result by ToolCallID so we can
	// emit the target+excerpt together. Map keyed by ID; calls without
	// a matching result (race, missing pair) still surface as "(no
	// result)" so the round count stays accurate.
	resultByID := make(map[string]provider.Message, len(round.Messages)-1)
	for _, m := range round.Messages[1:] {
		if m.ToolCallID != "" {
			resultByID[m.ToolCallID] = m
		}
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

	header := fmt.Sprintf("round %d", index)
	if len(head.ToolCalls) > 0 {
		header += fmt.Sprintf(" · %d call(s) · ok=%d fail=%d", len(head.ToolCalls), successes, failures)
	}
	text := strings.TrimSpace(head.Content)
	if runes := []rune(text); len(runes) > 120 {
		text = string(runes[:120]) + "..."
	}
	if text != "" {
		header += " · note=" + text
	}

	out := []string{"- " + header}
	for _, call := range head.ToolCalls {
		line := summariseSingleToolCall(call, resultByID[call.ID])
		if line != "" {
			out = append(out, "  "+line)
		}
	}
	return strings.Join(out, "\n")
}

// summariseSingleToolCall renders one indented line per tool_call
// inside a collapsed round. Format:
//
//	↳ read_file path=foo.go (lines 1-80) → "package config\n..."
//	↳ run_command go build ./... → exit 1: ./foo.go:12: undefined
//	↳ grep_codebase pattern=loadDotEnv → 3 hits across 2 files
//
// Keeps enough signal that the model can recall "I read foo.go and
// saw a Config struct" even after the round is collapsed. Result
// excerpts trimmed to ~120 chars; errors get the first error line.
func summariseSingleToolCall(call provider.ToolCall, result provider.Message) string {
	name := strings.TrimSpace(call.Name)
	if name == "" {
		return ""
	}
	target := summariseToolCallTarget(call)
	body := "↳ " + name
	if target != "" {
		body += " " + target
	}
	excerpt := summariseToolResultExcerpt(result)
	if excerpt != "" {
		body += " → " + excerpt
	}
	return body
}

// summariseToolCallTarget pulls the most identifying input args out
// of a ToolCall — path / pattern / command / dir — so the collapsed
// round line names the file, command, or query the model worked with.
// Mirrors the same priority order as the live TUI batch-inner preview
// (tools.previewBatchTarget) so the user sees consistent identifiers
// across "live chip" and "collapsed history" surfaces.
func summariseToolCallTarget(call provider.ToolCall) string {
	input := call.Input
	// Meta-tool wrappers (tool_call, tool_batch_call) carry the real
	// target one level down inside `args`. Unwrap so the summary still
	// names the underlying file/command rather than a meaningless "tool_call".
	if name, ok := input["name"].(string); ok && strings.TrimSpace(name) != "" {
		if inner, ok := input["args"].(map[string]any); ok {
			input = inner
			// Rename the line to point at the real backend tool, not
			// the wrapper.
			_ = name // keep go vet happy; we intentionally don't override `name` here
		}
	}
	for _, key := range []string{"path", "pattern", "query", "command", "dir", "url"} {
		if raw, ok := input[key]; ok {
			value := strings.TrimSpace(fmt.Sprint(raw))
			if value == "" {
				continue
			}
			if key == "command" {
				if rest := summariseAnyArgsList(input["args"]); rest != "" {
					value += " " + rest
				}
			}
			if key == "path" {
				if start, ok := pickInt(input["line_start"]); ok {
					if end, ok := pickInt(input["line_end"]); ok && end > 0 {
						value += fmt.Sprintf(" (lines %d-%d)", start, end)
					}
				}
			}
			if len(value) > 80 {
				value = value[:77] + "..."
			}
			return key + "=" + value
		}
	}
	return ""
}

// summariseToolResultExcerpt returns a one-line snippet of the
// tool_result content so the model can glance at "what came back"
// after the round was collapsed. Failures get the first error line
// (or the wrapped exit message) which is usually the actionable bit;
// successes get the first non-empty line of the output, capped to
// ~120 chars. Empty when the result is missing — caller skips the
// "→" so the line stays clean.
func summariseToolResultExcerpt(result provider.Message) string {
	body := strings.TrimSpace(result.Content)
	if body == "" {
		return ""
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if result.ToolError && strings.HasPrefix(line, "ERROR:") {
			// "ERROR: command exited with code 1" → strip the prefix
			// so the excerpt reads naturally with the failure marker
			// downstream.
			line = strings.TrimSpace(strings.TrimPrefix(line, "ERROR:"))
		}
		if len(line) > 120 {
			line = line[:117] + "..."
		}
		if result.ToolError {
			line = "FAIL " + line
		}
		return line
	}
	return ""
}

// summariseAnyArgsList renders a short whitespace-joined preview of
// run_command args. Tolerates the JSON shapes commandArgs() accepts.
func summariseAnyArgsList(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case []string:
		return strings.Join(v, " ")
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// pickInt extracts an int from the loose JSON-derived shapes the
// agent's input map can hold (json.Number, float64, int, int64).
func pickInt(raw any) (int, bool) {
	switch v := raw.(type) {
	case nil:
		return 0, false
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	}
	return 0, false
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
