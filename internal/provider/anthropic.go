package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type AnthropicProvider struct {
	model      string
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewAnthropicProvider(model, apiKey, baseURL string) *AnthropicProvider {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}
	baseURL = normalizeAnthropicBaseURL(baseURL)
	return &AnthropicProvider{
		model:   model,
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (p *AnthropicProvider) Name() string  { return "anthropic" }
func (p *AnthropicProvider) Model() string { return p.model }

func (p *AnthropicProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("%w: anthropic api key missing", ErrProviderUnavailable)
	}
	model := nonEmpty(req.Model, p.model)

	type textBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type message struct {
		Role    string      `json:"role"`
		Content []textBlock `json:"content"`
	}

	messages := make([]message, 0, len(req.Messages)+1)
	for _, m := range req.Messages {
		role := string(m.Role)
		if role == "" {
			role = "user"
		}
		if role == "system" {
			// System prompt goes to top-level `system` field in Anthropic API.
			continue
		}
		messages = append(messages, message{
			Role: role,
			Content: []textBlock{
				{Type: "text", Text: m.Content},
			},
		})
	}
	if len(req.Context) > 0 {
		messages = append(messages, message{
			Role: "user",
			Content: []textBlock{
				{Type: "text", Text: renderContextChunks(req.Context)},
			},
		})
	}

	payload := map[string]any{
		"model":      model,
		"max_tokens": 2048,
		"messages":   messages,
	}
	if strings.TrimSpace(req.System) != "" {
		payload["system"] = req.System
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

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("anthropic error status %d: %s", resp.StatusCode, string(raw))
	}
	if errMsg := parseCommonProviderError(raw); errMsg != "" {
		return nil, fmt.Errorf("anthropic provider error: %s", errMsg)
	}

	var parsed struct {
		Model string `json:"model"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode anthropic response: %w", err)
	}

	var b strings.Builder
	for _, c := range parsed.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	text := b.String()
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
		Text:  text,
		Model: nonEmpty(parsed.Model, model),
		Usage: usage,
	}, nil
}

func (p *AnthropicProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("%w: anthropic api key missing", ErrProviderUnavailable)
	}
	model := nonEmpty(req.Model, p.model)

	type textBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type message struct {
		Role    string      `json:"role"`
		Content []textBlock `json:"content"`
	}

	messages := make([]message, 0, len(req.Messages)+1)
	for _, m := range req.Messages {
		role := string(m.Role)
		if role == "" {
			role = "user"
		}
		if role == "system" {
			continue
		}
		messages = append(messages, message{
			Role: role,
			Content: []textBlock{
				{Type: "text", Text: m.Content},
			},
		})
	}
	if len(req.Context) > 0 {
		messages = append(messages, message{
			Role: "user",
			Content: []textBlock{
				{Type: "text", Text: renderContextChunks(req.Context)},
			},
		})
	}

	payload := map[string]any{
		"model":      model,
		"max_tokens": 2048,
		"messages":   messages,
		"stream":     true,
	}
	if strings.TrimSpace(req.System) != "" {
		payload["system"] = req.System
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
		return nil, fmt.Errorf("anthropic error status %d: %s", resp.StatusCode, string(raw))
	}

	ch := make(chan StreamEvent, 32)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

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
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(payload), &evt); err != nil {
				continue
			}
			switch evt.Type {
			case "content_block_delta":
				if evt.Delta.Text != "" {
					select {
					case <-ctx.Done():
						ch <- StreamEvent{Type: StreamError, Err: ctx.Err()}
						return
					case ch <- StreamEvent{Type: StreamDelta, Delta: evt.Delta.Text}:
					}
				}
			case "message_stop":
				ch <- StreamEvent{Type: StreamDone}
				return
			case "error":
				msg := strings.TrimSpace(evt.Error.Message)
				if msg == "" {
					msg = "anthropic stream error"
				}
				ch <- StreamEvent{Type: StreamError, Err: fmt.Errorf(msg)}
				return
			}
		}
		if err := sc.Err(); err != nil {
			ch <- StreamEvent{Type: StreamError, Err: err}
			return
		}
		ch <- StreamEvent{Type: StreamDone}
	}()

	return ch, nil
}

func normalizeAnthropicBaseURL(baseURL string) string {
	b := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(b, "/api/anthropic") {
		return b + "/v1"
	}
	return b
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
	return 1000000
}

func (p *AnthropicProvider) Hints() ProviderHints {
	return ProviderHints{
		ToolStyle:   "tool_use",
		Cache:       true,
		LowLatency:  false,
		BestFor:     []string{"review", "reasoning", "long-context"},
		MaxContext:  p.MaxContext(),
		DefaultMode: "high",
	}
}
