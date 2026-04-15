package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

const (
	maxLocalToolSteps        = 6
	maxToolResultOutputChars = 3200
	maxToolResultDataChars   = 1200
)

var localToolBlockPattern = regexp.MustCompile("(?s)```(?:dfmc-tool|json)\\s*\\n(.*?)\\n```")

type localToolCall struct {
	Tool   string         `json:"tool"`
	Params map[string]any `json:"params"`
}

type localToolTrace struct {
	Call       localToolCall
	Result     tools.Result
	Err        string
	Provider   string
	Model      string
	Step       int
	OccurredAt time.Time
}

type localToolCompletion struct {
	Answer       string
	Provider     string
	Model        string
	TokenCount   int
	Context      []types.ContextChunk
	ToolTraces   []localToolTrace
	SystemPrompt string
}

func (e *Engine) shouldUseLocalToolLoop() bool {
	if e == nil || e.Tools == nil {
		return false
	}
	if len(e.ListTools()) == 0 {
		return false
	}
	runtime := e.promptRuntime()
	return !strings.EqualFold(strings.TrimSpace(runtime.ToolStyle), "none")
}

func (e *Engine) askWithLocalTools(ctx context.Context, question string) (localToolCompletion, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	e.ensureIndexed(ctx)

	chunks := e.buildContextChunks(question)
	systemPrompt := e.buildLocalToolSystemPrompt(question, chunks)
	baseReq := provider.CompletionRequest{
		Provider: e.provider(),
		Model:    e.model(),
		Context:  chunks,
		System:   systemPrompt,
	}

	tail := make([]provider.Message, 0, maxLocalToolSteps*2)
	traces := make([]localToolTrace, 0, maxLocalToolSteps)
	totalTokens := 0
	contextTokens := 0
	for _, chunk := range chunks {
		contextTokens += chunk.TokenCount
	}
	lastProvider := strings.TrimSpace(baseReq.Provider)
	lastModel := strings.TrimSpace(baseReq.Model)
	e.publishAgentLoopEvent("agent:loop:start", map[string]any{
		"provider":       lastProvider,
		"model":          lastModel,
		"max_tool_steps": maxLocalToolSteps,
		"context_files":  len(chunks),
		"context_tokens": contextTokens,
	})
	e.publishAgentLoopEvent("agent:loop:contract", map[string]any{
		"provider":         lastProvider,
		"model":            lastModel,
		"max_tool_steps":   maxLocalToolSteps,
		"pre_tool":         preToolContextInstruction(),
		"post_tool":        postToolContextInstruction(),
		"context_snapshot": formatToolLoopContextSnapshot(chunks, 4),
		"tools":            append([]string(nil), e.ListTools()...),
	})

	for step := 0; step <= maxLocalToolSteps; step++ {
		e.publishAgentLoopEvent("agent:loop:thinking", map[string]any{
			"step":           step + 1,
			"max_tool_steps": maxLocalToolSteps,
			"tool_rounds":    len(traces),
		})
		e.publishAgentLoopEvent("agent:loop:context_enter", map[string]any{
			"step":             step + 1,
			"max_tool_steps":   maxLocalToolSteps,
			"tool_rounds":      len(traces),
			"instruction":      buildTurnContextEnterInstruction(step+1, maxLocalToolSteps, traces),
			"context_snapshot": formatToolLoopContextSnapshot(chunks, 3),
		})
		req := baseReq
		req.Messages = e.buildToolLoopRequestMessages(question, chunks, systemPrompt, tail)
		req.Messages = append(req.Messages, provider.Message{
			Role: types.RoleUser,
			Content: buildToolLoopTurnInstruction(
				question,
				step+1,
				maxLocalToolSteps,
				traces,
				chunks,
			),
		})

		resp, usedProvider, err := e.Providers.Complete(ctx, req)
		if err != nil {
			e.publishAgentLoopEvent("agent:loop:error", map[string]any{
				"step":           step + 1,
				"max_tool_steps": maxLocalToolSteps,
				"error":          err.Error(),
			})
			return localToolCompletion{}, err
		}
		totalTokens += resp.Usage.TotalTokens
		if strings.TrimSpace(usedProvider) != "" {
			lastProvider = usedProvider
		}
		if strings.TrimSpace(resp.Model) != "" {
			lastModel = resp.Model
		}

		call, ok := parseLocalToolCall(resp.Text)
		if !ok {
			completion := localToolCompletion{
				Answer:       resp.Text,
				Provider:     lastProvider,
				Model:        lastModel,
				TokenCount:   totalTokens,
				Context:      chunks,
				ToolTraces:   traces,
				SystemPrompt: systemPrompt,
			}
			e.recordAgentInteraction(question, completion)
			e.publishAgentLoopEvent("agent:loop:final", map[string]any{
				"step":           step + 1,
				"max_tool_steps": maxLocalToolSteps,
				"tool_rounds":    len(traces),
				"provider":       completion.Provider,
				"model":          completion.Model,
			})
			e.publishProviderComplete(completion.Provider, completion.Model, completion.TokenCount)
			return completion, nil
		}
		if step == maxLocalToolSteps {
			e.publishAgentLoopEvent("agent:loop:max_steps", map[string]any{
				"step":           step + 1,
				"max_tool_steps": maxLocalToolSteps,
				"tool_rounds":    len(traces),
				"tool":           call.Tool,
			})
			return localToolCompletion{}, fmt.Errorf("agent tool loop exceeded %d steps", maxLocalToolSteps)
		}

		trace := localToolTrace{
			Call:       call,
			Provider:   lastProvider,
			Model:      lastModel,
			Step:       step + 1,
			OccurredAt: time.Now(),
		}
		e.publishToolCall(trace)
		res, toolErr := e.CallTool(ctx, call.Tool, call.Params)
		if toolErr != nil {
			trace.Err = toolErr.Error()
		} else {
			trace.Result = res
		}
		e.publishToolResult(trace)
		e.publishAgentLoopEvent("agent:loop:context_exit", map[string]any{
			"step":        trace.Step,
			"tool_rounds": len(traces) + 1,
			"tool":        trace.Call.Tool,
			"success":     trace.Err == "",
			"instruction": buildTurnContextExitInstruction(trace),
		})
		traces = append(traces, trace)

		tail = append(tail,
			provider.Message{Role: types.RoleAssistant, Content: strings.TrimSpace(resp.Text)},
			provider.Message{Role: types.RoleUser, Content: formatLocalToolResultPrompt(trace)},
		)
	}

	e.publishAgentLoopEvent("agent:loop:error", map[string]any{
		"max_tool_steps": maxLocalToolSteps,
		"error":          "agent tool loop ended unexpectedly",
	})
	return localToolCompletion{}, fmt.Errorf("agent tool loop ended unexpectedly")
}

func (e *Engine) buildLocalToolSystemPrompt(question string, chunks []types.ContextChunk) string {
	systemPrompt := ""
	if e.Context != nil {
		systemPrompt = e.Context.BuildSystemPromptWithRuntime(
			e.ProjectRoot,
			question,
			chunks,
			e.ListTools(),
			e.promptRuntime(),
		)
	}
	bridge := buildLocalToolBridgeInstructions(e.ListTools(), e.promptRuntime(), chunks)
	if strings.TrimSpace(bridge) == "" {
		return systemPrompt
	}
	if strings.TrimSpace(systemPrompt) == "" {
		return bridge
	}
	return strings.TrimSpace(systemPrompt) + "\n\n[DFMC local tool bridge]\n" + bridge
}

func buildLocalToolBridgeInstructions(toolNames []string, runtime ctxmgr.PromptRuntime, chunks []types.ContextChunk) string {
	if len(toolNames) == 0 {
		return ""
	}
	lines := []string{
		"You can use DFMC local tools through a strict text bridge.",
		"When a tool is needed, reply with ONLY one fenced code block tagged dfmc-tool.",
		"Inside the block emit strict JSON: {\"tool\":\"read_file\",\"params\":{\"path\":\"README.md\"}}",
		"Do not mix prose with a dfmc-tool block.",
		"After you receive a tool result, either emit another dfmc-tool block or provide the final answer in plain text.",
		preToolContextInstruction(),
		postToolContextInstruction(),
		"Before editing an existing file, read it first with read_file.",
		"Keep tool calls narrow: focused paths, ranges, and filters.",
		"Never dump full raw tool output to the user; summarize only relevant evidence.",
	}
	style := strings.TrimSpace(runtime.ToolStyle)
	if style != "" {
		lines = append(lines, "Runtime hint: provider tool style is "+style+", but DFMC local bridge format takes priority here.")
	}
	if len(toolNames) > 0 {
		lines = append(lines, "Available tools: "+strings.Join(toolNames, ", "))
	}
	if snapshot := formatToolLoopContextSnapshot(chunks, 4); snapshot != "" {
		lines = append(lines, "Context snapshot: "+snapshot)
	}
	return strings.Join(lines, "\n")
}

func preToolContextInstruction() string {
	return "Pre-tool instruction (context enter): use current context snapshot + prior tool evidence to choose ONE narrow tool call."
}

func postToolContextInstruction() string {
	return "Post-tool instruction (context exit): after each tool result, decide: one next tool call OR final answer. Do not stay in tool-call loop without progress."
}

func buildTurnContextEnterInstruction(step, maxToolSteps int, traces []localToolTrace) string {
	if len(traces) == 0 {
		return fmt.Sprintf("Step %d/%d context-enter: pick the first narrow tool call from retrieved context.", step, maxToolSteps)
	}
	last := traces[len(traces)-1]
	toolName := strings.TrimSpace(last.Call.Tool)
	if toolName == "" {
		toolName = "tool"
	}
	if last.Err != "" {
		return fmt.Sprintf("Step %d/%d context-enter: previous %s call failed; pick a narrower recovery call or finalize with limits.", step, maxToolSteps, toolName)
	}
	return fmt.Sprintf("Step %d/%d context-enter: use %s evidence to decide one next call or final answer.", step, maxToolSteps, toolName)
}

func buildTurnContextExitInstruction(trace localToolTrace) string {
	toolName := strings.TrimSpace(trace.Call.Tool)
	if toolName == "" {
		toolName = "tool"
	}
	if trace.Err != "" {
		return fmt.Sprintf("Context-exit after %s: failed call; adjust strategy with a narrower or alternative tool.", toolName)
	}
	if trace.Result.Truncated {
		return fmt.Sprintf("Context-exit after %s: output truncated; if needed follow with narrower scoped retrieval.", toolName)
	}
	return fmt.Sprintf("Context-exit after %s: keep only relevant evidence, then choose one next call or final answer.", toolName)
}

func formatToolLoopContextSnapshot(chunks []types.ContextChunk, limit int) string {
	if len(chunks) == 0 {
		return "no retrieved context chunks"
	}
	total := 0
	parts := make([]string, 0, limit)
	for i, chunk := range chunks {
		total += chunk.TokenCount
		if i >= limit {
			continue
		}
		path := strings.TrimSpace(chunk.Path)
		if path == "" {
			path = "(unknown)"
		}
		parts = append(parts, fmt.Sprintf("%s#L%d-L%d(%dtok)", path, chunk.LineStart, chunk.LineEnd, chunk.TokenCount))
	}
	summary := fmt.Sprintf("files=%d tokens=%d", len(chunks), total)
	if len(parts) > 0 {
		summary += " top=" + strings.Join(parts, "; ")
	}
	if len(chunks) > limit {
		summary += fmt.Sprintf("; +%d more", len(chunks)-limit)
	}
	return summary
}

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

func parseLocalToolCall(raw string) (localToolCall, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return localToolCall{}, false
	}
	matches := localToolBlockPattern.FindAllStringSubmatch(trimmed, -1)
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		call, ok := parseLocalToolCallJSON(match[1])
		if ok {
			return call, true
		}
	}
	call, ok := parseLocalToolCallJSON(trimmed)
	if ok {
		return call, true
	}
	return localToolCall{}, false
}

func parseLocalToolCallJSON(raw string) (localToolCall, bool) {
	var call localToolCall
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &call); err != nil {
		return localToolCall{}, false
	}
	call.Tool = strings.TrimSpace(call.Tool)
	if call.Tool == "" {
		return localToolCall{}, false
	}
	if call.Params == nil {
		call.Params = map[string]any{}
	}
	return call, true
}

func formatLocalToolResultPrompt(trace localToolTrace) string {
	var b strings.Builder
	b.WriteString("[DFMC tool result]\n")
	b.WriteString("tool: ")
	b.WriteString(trace.Call.Tool)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("step: %d\n", trace.Step))
	if trace.Err != "" {
		b.WriteString("success: false\n")
		b.WriteString("error: ")
		b.WriteString(trace.Err)
		b.WriteString("\n")
	} else {
		b.WriteString("success: true\n")
	}
	b.WriteString(fmt.Sprintf("duration_ms: %d\n", trace.Result.DurationMs))
	if out := strings.TrimSpace(trace.Result.Output); out != "" {
		b.WriteString("output:\n")
		b.WriteString(trimToolPayload(out, maxToolResultOutputChars))
		b.WriteString("\n")
	}
	if len(trace.Result.Data) > 0 {
		if raw, err := json.MarshalIndent(trace.Result.Data, "", "  "); err == nil {
			b.WriteString("data:\n")
			b.WriteString(trimToolPayload(string(raw), maxToolResultDataChars))
			b.WriteString("\n")
		}
	}
	if trace.Result.Truncated {
		b.WriteString("truncated: true\n")
	}
	b.WriteString("\n[DFMC post-tool context contract]\n")
	b.WriteString("- Enter context: treat this tool result as temporary evidence for the next decision.\n")
	b.WriteString("- Exit context: before final answer, keep only relevant evidence and avoid dumping raw tool payload.\n")
	b.WriteString("- Next action: emit either exactly ONE dfmc-tool block OR the final answer.\n")
	b.WriteString("- If this tool failed, adjust strategy with a narrower/fixer tool call.\n")
	b.WriteString("If you need another tool, reply only with a dfmc-tool block. Otherwise answer the user directly.")
	return strings.TrimSpace(b.String())
}

func buildToolLoopTurnInstruction(question string, step, maxToolSteps int, traces []localToolTrace, chunks []types.ContextChunk) string {
	var b strings.Builder
	b.WriteString("[DFMC tool loop turn state]\n")
	b.WriteString(fmt.Sprintf("step: %d\n", step))
	b.WriteString(fmt.Sprintf("max_tool_steps: %d\n", maxToolSteps))
	b.WriteString(fmt.Sprintf("tool_rounds_completed: %d\n", len(traces)))
	b.WriteString("original_user_request: ")
	b.WriteString(strings.TrimSpace(question))
	b.WriteString("\n")
	b.WriteString("context_scope: ")
	b.WriteString(formatToolLoopContextSnapshot(chunks, 3))
	b.WriteString("\n")
	if len(traces) > 0 {
		last := traces[len(traces)-1]
		b.WriteString("last_tool: ")
		b.WriteString(strings.TrimSpace(last.Call.Tool))
		b.WriteString("\n")
		if last.Err != "" {
			b.WriteString("last_tool_status: failed\n")
			b.WriteString("last_tool_error: ")
			b.WriteString(trimToolPayload(last.Err, 200))
			b.WriteString("\n")
		} else {
			b.WriteString("last_tool_status: ok\n")
			if preview := trimToolPayload(strings.TrimSpace(last.Result.Output), 220); preview != "" {
				b.WriteString("last_tool_output_preview:\n")
				b.WriteString(preview)
				b.WriteString("\n")
			}
		}
	}
	b.WriteString("instruction: choose next action with progress. Either one dfmc-tool block or final answer.")
	return strings.TrimSpace(b.String())
}

func trimToolPayload(raw string, maxChars int) string {
	trimmed := strings.TrimSpace(raw)
	if maxChars <= 0 || len(trimmed) <= maxChars {
		return trimmed
	}
	if maxChars <= 12 {
		return trimmed[:maxChars]
	}
	return strings.TrimSpace(trimmed[:maxChars-12]) + "\n...[truncated]"
}

func (e *Engine) recordAgentInteraction(question string, completion localToolCompletion) {
	now := time.Now()
	assistantMsg := types.Message{
		Role:      types.RoleAssistant,
		Content:   completion.Answer,
		Timestamp: now,
		TokenCnt:  completion.TokenCount,
		Metadata: map[string]string{
			"provider":    completion.Provider,
			"model":       completion.Model,
			"tool_rounds": fmt.Sprintf("%d", len(completion.ToolTraces)),
		},
	}
	for _, trace := range completion.ToolTraces {
		callMetadata := map[string]string{
			"provider": trace.Provider,
			"model":    trace.Model,
			"step":     fmt.Sprintf("%d", trace.Step),
		}
		resultMetadata := map[string]string{
			"provider": trace.Provider,
			"model":    trace.Model,
			"step":     fmt.Sprintf("%d", trace.Step),
		}
		if trace.Err != "" {
			resultMetadata["error"] = trace.Err
		}
		assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, types.ToolCallRecord{
			Name:      trace.Call.Tool,
			Params:    trace.Call.Params,
			Timestamp: trace.OccurredAt,
			Metadata:  callMetadata,
		})
		assistantMsg.Results = append(assistantMsg.Results, types.ToolResultRecord{
			Name:      trace.Call.Tool,
			Output:    strings.TrimSpace(trace.Result.Output),
			Success:   trace.Err == "",
			Timestamp: trace.OccurredAt,
			Metadata:  resultMetadata,
		})
	}

	if e.Conversation != nil {
		e.Conversation.AddMessage(completion.Provider, completion.Model, types.Message{
			Role:      types.RoleUser,
			Content:   question,
			Timestamp: now,
		})
		e.Conversation.AddMessage(completion.Provider, completion.Model, assistantMsg)
	}
	if e.Memory != nil {
		e.Memory.SetWorkingQuestionAnswer(question, completion.Answer)
		for _, ch := range completion.Context {
			e.Memory.TouchFile(ch.Path)
		}
		_ = e.Memory.AddEpisodicInteraction(e.ProjectRoot, question, completion.Answer, 0.75)
	}
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

func (e *Engine) publishToolCall(trace localToolTrace) {
	if e.EventBus == nil {
		return
	}
	e.EventBus.Publish(Event{
		Type:   "tool:call",
		Source: "engine",
		Payload: map[string]any{
			"tool":           trace.Call.Tool,
			"params":         trace.Call.Params,
			"params_preview": formatToolParamsPreview(trace.Call.Params, 180),
			"step":           trace.Step,
			"provider":       trace.Provider,
			"model":          trace.Model,
		},
	})
}

func (e *Engine) publishToolResult(trace localToolTrace) {
	if e.EventBus == nil {
		return
	}
	payload := map[string]any{
		"tool":           trace.Call.Tool,
		"success":        trace.Err == "",
		"durationMs":     trace.Result.DurationMs,
		"step":           trace.Step,
		"provider":       trace.Provider,
		"model":          trace.Model,
		"truncated":      trace.Result.Truncated,
		"output_preview": compactToolPayload(trace.Result.Output, 180),
	}
	if trace.Err != "" {
		payload["error"] = trace.Err
	}
	e.EventBus.Publish(Event{
		Type:    "tool:result",
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

func formatToolParamsPreview(params map[string]any, limit int) string {
	if len(params) == 0 {
		return ""
	}
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
	if len(parts) == 0 {
		return ""
	}
	out := strings.Join(parts, " ")
	return compactToolPayload(out, limit)
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
	if maxChars <= 0 {
		return text
	}
	if len([]rune(text)) <= maxChars {
		return text
	}
	runes := []rune(text)
	if maxChars <= 3 {
		return string(runes[:maxChars])
	}
	return string(runes[:maxChars-3]) + "..."
}

func strconvQuote(s string) string {
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	return "\"" + escaped + "\""
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
