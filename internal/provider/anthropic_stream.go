package provider

// anthropic_stream.go — SSE streaming entry point for the Anthropic
// provider. Sibling of anthropic.go which keeps the constructor +
// Name/Model/Models trio + Complete sync entry + the bounded-body
// reader + parseCommonProviderError + CountTokens/MaxContext/Hints
// metadata + requestMaxTokens floor.
//
// Splitting Stream out keeps anthropic.go scoped to "what does a
// non-streaming Complete look like" while this file owns the SSE
// pump: the message_start/content_block_delta/message_delta/
// message_stop/error frame state machine, the running model+usage+
// stop-reason snapshot we promote on StreamDone, the ctx-aware
// emitStart announce so a cancelled consumer doesn't block the
// goroutine, and the bufio scanner buffer floor (10MB cap so a
// pathological stream still terminates).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func (p *AnthropicProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("%w: anthropic api key missing", ErrProviderUnavailable)
	}
	model := nonEmpty(req.Model, p.model)

	messages := buildAnthropicMessages(req)

	payload := map[string]any{
		"model":      model,
		"max_tokens": p.requestMaxTokens(),
		"messages":   messages,
		"stream":     true,
	}
	if sys := anthropicSystemPayload(req); sys != nil {
		payload["system"] = sys
	}
	if len(req.Tools) > 0 {
		payload["tools"] = anthropicToolDescriptors(req.Tools)
		if choice := anthropicToolChoice(req.ToolChoice); choice != nil {
			payload["tool_choice"] = choice
		}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	endpoint := p.baseURL + "/messages"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		if isThrottleStatus(resp.StatusCode) {
			return nil, newThrottledErrorFromResponse("anthropic", resp, string(raw))
		}
		return nil, &StatusError{Provider: "anthropic", StatusCode: resp.StatusCode, Body: string(raw)}
	}

	ch := make(chan StreamEvent, 32)
	go func() {
		defer close(ch)
		defer func() { _ = resp.Body.Close() }()

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

		// Running model/usage/stop_reason snapshot assembled from Anthropic's
		// message_start / message_delta frames. Populated on StreamDone so
		// downstream consumers don't need a separate Complete call.
		resolvedModel := model
		startAnnounced := false
		usage := Usage{}
		usageSet := false
		stopReason := StopUnknown

		emitStart := func() {
			if startAnnounced {
				return
			}
			startAnnounced = true
			select {
			case <-ctx.Done():
				return
			case ch <- StreamEvent{Type: StreamStart, Provider: "anthropic", Model: resolvedModel}:
			}
		}

		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				continue
			}
			if strings.Contains(payload, "\"success\":false") {
				ch <- StreamEvent{Type: StreamError, Err: fmt.Errorf("anthropic provider error: %s", payload)}
				return
			}

			var evt struct {
				Type  string `json:"type"`
				Delta struct {
					Type       string `json:"type"`
					Text       string `json:"text"`
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Message struct {
					Model string `json:"model"`
					Usage struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
					} `json:"usage"`
				} `json:"message"`
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(payload), &evt); err != nil {
				continue
			}
			switch evt.Type {
			case "message_start":
				if strings.TrimSpace(evt.Message.Model) != "" {
					resolvedModel = evt.Message.Model
				}
				if evt.Message.Usage.InputTokens > 0 {
					usage.InputTokens = evt.Message.Usage.InputTokens
					usageSet = true
				}
				if evt.Message.Usage.OutputTokens > 0 {
					usage.OutputTokens = evt.Message.Usage.OutputTokens
					usageSet = true
				}
				emitStart()
			case "content_block_delta":
				emitStart()
				if evt.Delta.Text != "" {
					select {
					case <-ctx.Done():
						ch <- StreamEvent{Type: StreamError, Err: ctx.Err()}
						return
					case ch <- StreamEvent{Type: StreamDelta, Delta: evt.Delta.Text}:
					}
				}
			case "message_delta":
				if strings.TrimSpace(evt.Delta.StopReason) != "" {
					stopReason = anthropicStopReason(evt.Delta.StopReason)
				}
				if evt.Usage.OutputTokens > 0 {
					usage.OutputTokens = evt.Usage.OutputTokens
					usageSet = true
				}
			case "message_stop":
				emitStart()
				done := StreamEvent{Type: StreamDone, Provider: "anthropic", Model: resolvedModel, StopReason: stopReason}
				if usageSet {
					u := usage
					u.TotalTokens = u.InputTokens + u.OutputTokens
					done.Usage = &u
				}
				ch <- done
				return
			case "error":
				msg := strings.TrimSpace(evt.Error.Message)
				if msg == "" {
					msg = "anthropic stream error"
				}
				ch <- StreamEvent{Type: StreamError, Err: errors.New(msg)}
				return
			}
		}
		if err := sc.Err(); err != nil {
			ch <- StreamEvent{Type: StreamError, Err: err}
			return
		}
		emitStart()
		done := StreamEvent{Type: StreamDone, Provider: "anthropic", Model: resolvedModel, StopReason: stopReason}
		if usageSet {
			u := usage
			u.TotalTokens = u.InputTokens + u.OutputTokens
			done.Usage = &u
		}
		ch <- done
	}()

	return ch, nil
}
