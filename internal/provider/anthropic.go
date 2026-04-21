package provider

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

type AnthropicProvider struct {
	name       string
	model      string
	apiKey     string
	baseURL    string
	maxTokens  int
	maxContext int
	httpClient *http.Client
}

func NewAnthropicProvider(model, apiKey, baseURL string, maxTokens, maxContext int) *AnthropicProvider {
	return NewNamedAnthropicProvider("anthropic", model, apiKey, baseURL, maxTokens, maxContext)
}

func NewNamedAnthropicProvider(name, model, apiKey, baseURL string, maxTokens, maxContext int) *AnthropicProvider {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	baseURL = normalizeAnthropicBaseURL(baseURL)
	return &AnthropicProvider{
		name:       strings.TrimSpace(name),
		model:      model,
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		maxTokens:  maxTokens,
		maxContext: maxContext,
		httpClient: newProviderHTTPClient(),
	}
}

func (p *AnthropicProvider) Name() string {
	if strings.TrimSpace(p.name) == "" {
		return "anthropic"
	}
	return p.name
}
func (p *AnthropicProvider) Model() string   { return p.model }
func (p *AnthropicProvider) Models() []string { return []string{p.model} }

func (p *AnthropicProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("%w: anthropic api key missing", ErrProviderUnavailable)
	}
	model := nonEmpty(req.Model, p.model)

	messages := buildAnthropicMessages(req)

	payload := map[string]any{
		"model":      model,
		"max_tokens": p.requestMaxTokens(),
		"messages":   messages,
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
	defer resp.Body.Close()

	raw, truncated, err := readBoundedBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, fmt.Errorf("anthropic response exceeded %d bytes — refusing to decode (likely a misbehaving proxy or hostile endpoint)", maxProviderResponseBytes)
	}
	if resp.StatusCode >= 400 {
		if isThrottleStatus(resp.StatusCode) {
			return nil, newThrottledErrorFromResponse("anthropic", resp, string(raw))
		}
		return nil, fmt.Errorf("anthropic error status %d: %s", resp.StatusCode, string(raw))
	}
	if errMsg := parseCommonProviderError(raw); errMsg != "" {
		return nil, fmt.Errorf("anthropic provider error: %s", errMsg)
	}

	var parsed struct {
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Content []anthropicContentBlock `json:"content"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}

	text, toolCalls := splitAnthropicContent(parsed.Content)
	usage := Usage{
		InputTokens:  parsed.Usage.InputTokens,
		OutputTokens: parsed.Usage.OutputTokens,
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	if usage.TotalTokens == 0 {
		usage.InputTokens = p.CountTokens(renderMessagesForCount(req.Messages))
		usage.OutputTokens = p.CountTokens(text)
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	return &CompletionResponse{
		Text:       text,
		Model:      nonEmpty(parsed.Model, model),
		Usage:      usage,
		ToolCalls:  toolCalls,
		StopReason: anthropicStopReason(parsed.StopReason),
	}, nil
}

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
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if isThrottleStatus(resp.StatusCode) {
			return nil, newThrottledErrorFromResponse("anthropic", resp, string(raw))
		}
		return nil, fmt.Errorf("anthropic error status %d: %s", resp.StatusCode, string(raw))
	}

	ch := make(chan StreamEvent, 32)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

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

func normalizeAnthropicBaseURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

// maxProviderResponseBytes caps how much we read off ANY provider HTTP
// body. Real Claude/OpenAI/Gemini completions max out in the low MB
// range; anything larger is a misbehaving proxy or a malicious endpoint
// trying to OOM the host. Pre-fix every provider used a bare
// io.ReadAll(resp.Body) — a 4 GB body would crash the process before
// JSON decode even fired. 32 MiB sits comfortably above legitimate
// envelopes (full reasoning trace + tool_use blocks) but well under any
// practical OOM threshold. Apply via readBoundedBody (below).
const maxProviderResponseBytes = 32 << 20 // 32 MiB

// readBoundedBody is the standard "read the whole body but stop at
// maxProviderResponseBytes" helper. Returns the truncated bytes plus an
// indicator the caller can use to surface a clearer error than
// "unexpected end of JSON input". Use everywhere a provider read
// happens.
func readBoundedBody(body io.Reader) ([]byte, bool, error) {
	limited := io.LimitReader(body, maxProviderResponseBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return raw, false, err
	}
	if len(raw) > maxProviderResponseBytes {
		return raw[:maxProviderResponseBytes], true, nil
	}
	return raw, false, nil
}

func parseCommonProviderError(raw []byte) string {
	var e struct {
		Success *bool  `json:"success"`
		Code    any    `json:"code"`
		Msg     string `json:"msg"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return ""
	}
	if e.Success != nil && !*e.Success {
		if strings.TrimSpace(e.Msg) != "" {
			return e.Msg
		}
		if strings.TrimSpace(e.Message) != "" {
			return e.Message
		}
		if e.Code != nil {
			return fmt.Sprintf("code=%v", e.Code)
		}
		return "unknown error"
	}
	if strings.TrimSpace(e.Msg) != "" && strings.TrimSpace(string(raw)) != "" && strings.Contains(string(raw), `"success":false`) {
		return e.Msg
	}
	return ""
}

func (p *AnthropicProvider) CountTokens(text string) int {
	return len(strings.Fields(text))
}

func (p *AnthropicProvider) MaxContext() int {
	if p.maxContext > 0 {
		return p.maxContext
	}
	return 1000000
}

func (p *AnthropicProvider) Hints() ProviderHints {
	return ProviderHints{
		ToolStyle:     "tool_use",
		Cache:         true,
		LowLatency:    false,
		BestFor:       []string{"review", "reasoning", "long-context"},
		MaxContext:    p.MaxContext(),
		DefaultMode:   "high",
		SupportsTools: true,
	}
}

func (p *AnthropicProvider) requestMaxTokens() int {
	if p.maxTokens > 0 {
		return p.maxTokens
	}
	return 2048
}
