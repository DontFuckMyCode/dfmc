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
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/provider"
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

	usedByRequest := estimateTokens(question) + estimateTokens(systemPrompt) + baseToolReserveTokens
	for _, ch := range chunks {
		usedByRequest += ch.TokenCount
	}
	for _, msg := range tail {
		usedByRequest += estimateTokens(msg.Content)
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

func (e *Engine) publishProviderComplete(providerName, model string, tokenCount int) {
	if e.EventBus == nil {
		return
	}
	e.EventBus.Publish(Event{
		Type:   "provider:complete",
		Source: "engine",
		Payload: map[string]any{
			"provider": providerName,
			"model":    model,
			"tokens":   tokenCount,
		},
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

// formatToolParamsPreview renders a human-friendly one-liner for the
// `params_preview` event field that the TUI hangs under each tool chip.
// Pre-fix this dumped Go's map-stringification:
//
//	args="map[args:[build ./...] command:go timeout_ms:60000]" name=run_command
//
// — readable to nobody. Post-fix it picks a domain verb + target so a
// glance at the chat reads like a Claude-Code-style transcript:
//
//	$ go build ./...
//	Read internal/config/config.go (lines 1-80)
//	Edit internal/engine/engine.go
//	Search "loadDotEnv"
//	Glob **/*_test.go
//	Batch [5: read_file ×3, grep_codebase, glob]
//
// Falls back to the legacy "key=value" dump when the params shape
// doesn't match a known verb so we never silently lose information.
func formatToolParamsPreview(params map[string]any, limit int) string {
	if len(params) == 0 {
		return ""
	}
	if pretty := formatToolParamsVerb(params); pretty != "" {
		return compactToolPayload(pretty, limit)
	}
	return compactToolPayload(formatToolParamsKVDump(params), limit)
}

// formatToolParamsVerb is the verb-style branch. Returns "" when the
// params don't match any known shape so the caller can fall through
// to the kv-dump.
func formatToolParamsVerb(params map[string]any) string {
	// Meta-tool wrappers (tool_call, tool_batch_call) — unwrap so the
	// preview names the underlying file/command, not "tool_call".
	if calls, ok := params["calls"].([]any); ok && len(calls) > 0 {
		return formatToolParamsBatchVerb(calls)
	}
	if name, ok := params["name"].(string); ok && strings.TrimSpace(name) != "" {
		inner, _ := params["args"].(map[string]any)
		if inner == nil {
			inner = map[string]any{}
		}
		return formatToolParamsVerbFor(name, inner)
	}
	return ""
}

// formatToolParamsVerbFor renders the verb line for a single backend
// tool given its name + already-unwrapped args.
func formatToolParamsVerbFor(name string, args map[string]any) string {
	switch name {
	case "run_command":
		cmd := strings.TrimSpace(fmt.Sprint(args["command"]))
		if cmd == "" {
			return ""
		}
		rest := formatArgsList(args["args"])
		if rest != "" {
			return "$ " + cmd + " " + rest
		}
		return "$ " + cmd
	case "read_file":
		path := strings.TrimSpace(fmt.Sprint(args["path"]))
		if path == "" {
			return ""
		}
		if start, ok := pickAnyInt(args["line_start"]); ok {
			if end, ok := pickAnyInt(args["line_end"]); ok && end > 0 {
				return fmt.Sprintf("Read %s (lines %d-%d)", path, start, end)
			}
		}
		return "Read " + path
	case "edit_file":
		path := strings.TrimSpace(fmt.Sprint(args["path"]))
		if path == "" {
			return "Edit"
		}
		return "Edit " + path
	case "write_file":
		path := strings.TrimSpace(fmt.Sprint(args["path"]))
		if path == "" {
			return "Write"
		}
		return "Write " + path
	case "list_dir":
		path := strings.TrimSpace(fmt.Sprint(args["path"]))
		if path == "" {
			return "List ."
		}
		return "List " + path
	case "grep_codebase":
		pattern := strings.TrimSpace(fmt.Sprint(args["pattern"]))
		if pattern == "" {
			return ""
		}
		return `Search "` + pattern + `"`
	case "glob":
		pattern := strings.TrimSpace(fmt.Sprint(args["pattern"]))
		if pattern == "" {
			return ""
		}
		return "Glob " + pattern
	case "tool_search":
		query := strings.TrimSpace(fmt.Sprint(args["query"]))
		if query == "" {
			return ""
		}
		return "Lookup " + query
	case "tool_help":
		target := strings.TrimSpace(fmt.Sprint(args["name"]))
		if target == "" {
			return ""
		}
		return "Help " + target
	}
	// Generic fallback for unknown tools — name + first identifying arg.
	for _, key := range []string{"path", "pattern", "query", "command", "url"} {
		if raw, ok := args[key]; ok {
			value := strings.TrimSpace(fmt.Sprint(raw))
			if value != "" {
				return name + " " + value
			}
		}
	}
	return ""
}

// formatToolParamsBatchVerb renders the verb line for tool_batch_call.
// Counts repeats of the same tool so a "5x read_file" batch reads as
// "Batch [5: read_file ×5]" instead of listing the same name five times.
func formatToolParamsBatchVerb(calls []any) string {
	if len(calls) == 0 {
		return "Batch []"
	}
	counts := make(map[string]int)
	order := make([]string, 0, len(calls))
	for _, raw := range calls {
		obj, _ := raw.(map[string]any)
		name := strings.TrimSpace(fmt.Sprint(obj["name"]))
		if name == "" {
			name = "?"
		}
		if _, seen := counts[name]; !seen {
			order = append(order, name)
		}
		counts[name]++
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		if c := counts[name]; c > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", name, c))
		} else {
			parts = append(parts, name)
		}
	}
	return fmt.Sprintf("Batch [%d: %s]", len(calls), strings.Join(parts, ", "))
}

// formatArgsList renders a short whitespace-joined preview of the
// JSON shapes commandArgs() accepts (string / []string / []any).
func formatArgsList(raw any) string {
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

// pickAnyInt extracts an int from JSON-derived loose shapes
// (json.Number marshals through float64; some paths preserve int).
func pickAnyInt(raw any) (int, bool) {
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

// formatToolParamsKVDump is the legacy "key=value" form, kept as a
// last-resort fallback for tools we don't have a verb for. Should be
// rare in practice once formatToolParamsVerbFor covers the registry.
func formatToolParamsKVDump(params map[string]any) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(fmt.Sprint(params[key]))
		if value == "" {
			continue
		}
		if strings.ContainsAny(value, " \t\r\n") {
			value = strconvQuote(value)
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, value))
	}
	return strings.Join(parts, " ")
}

func compactToolPayload(raw string, maxChars int) string {
	text := strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		first := strings.TrimSpace(text[:idx])
		if first == "" {
			first = "[multiline]"
		}
		text = first + " ..."
	}
	return truncateRunesWithMarker(text, maxChars, "...")
}

// strconvQuote is a thin alias over strconv.Quote so call sites read
// in the agent-loop file's local vocabulary. The previous hand-rolled
// version only escaped backslash and double quote, which produced
// broken JSON / TUI-line previews for any tool param containing
// newlines, tabs, or other control characters (the model often emits
// multi-line param values for write_file / edit_file). strconv.Quote
// handles every C-style escape plus all `< 0x20` control bytes.
func strconvQuote(s string) string {
	return strconv.Quote(s)
}

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
		ch <- provider.StreamEvent{Type: provider.StreamDone}
	}()
	return ch
}
