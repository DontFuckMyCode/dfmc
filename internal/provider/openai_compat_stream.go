package provider

// openai_compat_stream.go — SSE streaming entry point for the
// OpenAI-compatible provider (deepseek/kimi/zai/alibaba/generic/
// ollama). Sibling of openai_compat.go which keeps the constructor
// + Name/Model/Models trio + Complete sync entry + the bounded-body
// reader and metadata trio (CountTokens / MaxContext / Hints).
//
// Splitting Stream out keeps openai_compat.go scoped to "what does
// a non-streaming Complete look like" while this file owns the SSE
// pump: data:[DONE] sentinel detection, per-event Choices[0].Delta
// content extraction, finish_reason→stop-reason promotion, the
// running model+usage snapshot we promote on StreamDone, and the
// finish closure that emits the terminal event.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (p *OpenAICompatibleProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	if strings.TrimSpace(p.baseURL) == "" {
		return nil, fmt.Errorf("%w: %s base_url missing", ErrProviderUnavailable, p.name)
	}
	if p.name != "generic" && strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("%w: %s api key missing", ErrProviderUnavailable, p.name)
	}

	messages := buildOpenAIMessages(req)
	model := nonEmpty(req.Model, p.model)
	body := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   true,
	}
	if p.maxTokens > 0 {
		body["max_tokens"] = p.maxTokens
	}
	if len(req.Tools) > 0 {
		body["tools"] = openaiToolDescriptors(req.Tools)
		if choice := openaiToolChoice(req.ToolChoice); choice != nil {
			body["tool_choice"] = choice
		}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	endpoint := p.baseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.apiKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		if isThrottleStatus(resp.StatusCode) {
			return nil, newThrottledErrorFromResponse(p.name, resp, string(raw))
		}
		return nil, &StatusError{Provider: p.name, StatusCode: resp.StatusCode, Body: string(raw)}
	}

	ch := make(chan StreamEvent, 32)
	providerName := p.name
	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

		resolvedModel := model
		startAnnounced := false
		usage := Usage{}
		usageSet := false
		stopReason := StopUnknown
		var currentCalls map[int]*ToolCall
		var currentArgs map[int]*strings.Builder

		emitStart := func() {
			emitStreamStartOnce(ctx, ch, &startAnnounced, providerName, resolvedModel)
		}

		finish := func() {
			emitStart()
			ch <- streamDoneEvent(providerName, resolvedModel, stopReason, finalizeOpenAIStreamToolCalls(currentCalls, currentArgs), usage, usageSet)
		}

		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				continue
			}
			if payload == "[DONE]" {
				finish()
				return
			}
			if strings.Contains(payload, "\"success\":false") {
				ch <- StreamEvent{Type: StreamError, Err: fmt.Errorf("%s provider error: %s", providerName, payload)}
				return
			}

			var evt struct {
				Model   string `json:"model"`
				Choices []struct {
					Delta struct {
						Content   string `json:"content"`
						ToolCalls []struct {
							Index    int    `json:"index"`
							ID       string `json:"id"`
							Type     string `json:"type"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
					TotalTokens      int `json:"total_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(payload), &evt); err != nil {
				continue
			}
			if strings.TrimSpace(evt.Model) != "" {
				resolvedModel = evt.Model
			}
			if evt.Usage != nil {
				if evt.Usage.PromptTokens > 0 {
					usage.InputTokens = evt.Usage.PromptTokens
					usageSet = true
				}
				if evt.Usage.CompletionTokens > 0 {
					usage.OutputTokens = evt.Usage.CompletionTokens
					usageSet = true
				}
				if evt.Usage.TotalTokens > 0 {
					usage.TotalTokens = evt.Usage.TotalTokens
					usageSet = true
				}
			}
			if len(evt.Choices) == 0 {
				continue
			}
			if fr := strings.TrimSpace(evt.Choices[0].FinishReason); fr != "" {
				stopReason = openaiStopReason(fr)
			}

			// Process Tool Calls
			for _, tcDelta := range evt.Choices[0].Delta.ToolCalls {
				if currentCalls == nil {
					currentCalls = make(map[int]*ToolCall)
					currentArgs = make(map[int]*strings.Builder)
				}
				if _, ok := currentCalls[tcDelta.Index]; !ok {
					currentCalls[tcDelta.Index] = &ToolCall{
						ID:   tcDelta.ID,
						Name: tcDelta.Function.Name,
					}
					currentArgs[tcDelta.Index] = &strings.Builder{}
				}
				if tcDelta.Function.Arguments != "" {
					currentArgs[tcDelta.Index].WriteString(tcDelta.Function.Arguments)
				}
			}

			delta := evt.Choices[0].Delta.Content
			if delta == "" {
				continue
			}
			emitStart()
			select {
			case <-ctx.Done():
				ch <- StreamEvent{Type: StreamError, Err: ctx.Err()}
				return
			case ch <- StreamEvent{Type: StreamDelta, Delta: delta}:
			}
		}
		if err := sc.Err(); err != nil {
			ch <- StreamEvent{Type: StreamError, Err: err}
			return
		}
		finish()
	}()

	return ch, nil
}
