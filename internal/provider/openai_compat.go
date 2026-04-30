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

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type OpenAICompatibleProvider struct {
	name       string
	model      string
	apiKey     string
	baseURL    string
	maxTokens  int
	maxContext int
	httpClient *http.Client
}

func NewOpenAICompatibleProvider(name, model, apiKey, baseURL string, maxTokens, maxContext int, httpTimeout time.Duration) *OpenAICompatibleProvider {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL(name)
	}
	return &OpenAICompatibleProvider{
		name:       name,
		model:      model,
		apiKey:     apiKey,
		baseURL:    normalizeOpenAIBaseURL(name, baseURL),
		maxTokens:  maxTokens,
		maxContext: maxContext,
		httpClient: newProviderHTTPClient(httpTimeout),
	}
}

// normalizeOpenAIBaseURL trims the trailing slash and appends a sensible
// version segment when the URL points at a known host's root (e.g.
// https://api.deepseek.com without /v1). Users often paste the bare host
// from provider docs; appending /chat/completions to a bare host yields a
// 404 because the real endpoint lives under /v1. For hosts we recognize
// we fix it; unknown hosts are left alone to avoid guessing wrong.
func normalizeOpenAIBaseURL(name, raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	// Already contains a version or path segment — leave it.
	for _, seg := range []string{"/v1", "/v2", "/v3", "/v4", "/openai", "/compatible-mode", "/paas", "/anthropic", "/api/"} {
		if strings.Contains(lower, seg) {
			return trimmed
		}
	}
	// Bare host for a provider we know needs /v1.
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "openai", "deepseek", "kimi", "moonshot", "generic":
		return trimmed + "/v1"
	}
	return trimmed
}

func (p *OpenAICompatibleProvider) Name() string     { return p.name }
func (p *OpenAICompatibleProvider) Model() string    { return p.model }
func (p *OpenAICompatibleProvider) Models() []string { return []string{p.model} }

func (p *OpenAICompatibleProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if strings.TrimSpace(p.baseURL) == "" {
		return nil, fmt.Errorf("%w: %s base_url missing", ErrProviderUnavailable, p.name)
	}
	// Most OpenAI-compatible providers require API keys; generic may be keyless.
	if p.name != "generic" && strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("%w: %s api key missing", ErrProviderUnavailable, p.name)
	}

	messages := buildOpenAIMessages(req)

	model := nonEmpty(req.Model, p.model)
	body := map[string]any{
		"model":    model,
		"messages": messages,
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
	defer resp.Body.Close()

	raw, truncated, err := readBoundedBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if truncated {
		return nil, fmt.Errorf("%s response exceeded %d bytes — refusing to decode (likely a misbehaving proxy or hostile endpoint)", p.name, maxProviderResponseBytes)
	}
	if resp.StatusCode >= 400 {
		if isThrottleStatus(resp.StatusCode) {
			return nil, newThrottledErrorFromResponse(p.name, resp, string(raw))
		}
		return nil, &StatusError{Provider: p.name, StatusCode: resp.StatusCode, Body: string(raw)}
	}
	if errMsg := parseCommonProviderError(raw); errMsg != "" {
		return nil, fmt.Errorf("%s provider error: %s", p.name, errMsg)
	}

	var parsed struct {
		Model string `json:"model"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Choices []struct {
			FinishReason string            `json:"finish_reason"`
			Message      openaiChatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", p.name, err)
	}

	text := ""
	var toolCalls []ToolCall
	stop := StopUnknown
	if len(parsed.Choices) > 0 {
		text = parsed.Choices[0].Message.Content
		toolCalls = parseOpenAIToolCalls(parsed.Choices[0].Message.ToolCalls)
		stop = openaiStopReason(parsed.Choices[0].FinishReason)
	}
	usage := Usage{
		InputTokens:  parsed.Usage.PromptTokens,
		OutputTokens: parsed.Usage.CompletionTokens,
		TotalTokens:  parsed.Usage.TotalTokens,
	}
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
		StopReason: stop,
	}, nil
}

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
		defer resp.Body.Close()
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
		defer resp.Body.Close()

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

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
			case ch <- StreamEvent{Type: StreamStart, Provider: providerName, Model: resolvedModel}:
			}
		}

		finish := func() {
			emitStart()
			done := StreamEvent{Type: StreamDone, Provider: providerName, Model: resolvedModel, StopReason: stopReason}
			if usageSet {
				u := usage
				u.TotalTokens = u.InputTokens + u.OutputTokens
				done.Usage = &u
			}
			ch <- done
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
						Content string `json:"content"`
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

func (p *OpenAICompatibleProvider) CountTokens(text string) int {
	return len(strings.Fields(text))
}

func (p *OpenAICompatibleProvider) MaxContext() int {
	if p.maxContext > 0 {
		return p.maxContext
	}
	return 128000
}

func (p *OpenAICompatibleProvider) Hints() ProviderHints {
	return ProviderHints{
		ToolStyle:     "function-calling",
		Cache:         false,
		LowLatency:    false,
		BestFor:       []string{"general", "code"},
		MaxContext:    p.MaxContext(),
		DefaultMode:   "balanced",
		SupportsTools: true,
	}
}

func defaultOpenAIBaseURL(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "openai":
		return "https://api.openai.com/v1"
	case "deepseek":
		return "https://api.deepseek.com/v1"
	case "kimi", "moonshot":
		return "https://api.moonshot.ai/v1"
	case "zai":
		return "https://api.z.ai/api/paas/v4"
	case "alibaba":
		return "https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
	default:
		return ""
	}
}

func renderMessagesForCount(messages []Message) string {
	var b strings.Builder
	for _, m := range messages {
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func renderContextChunks(chunks []types.ContextChunk) string {
	var b strings.Builder
	b.WriteString("Relevant code context:\n")
	for i, ch := range chunks {
		if i >= 8 {
			break
		}
		b.WriteString(fmt.Sprintf("\n[%d] %s (score %.2f)\n", i+1, ch.Path, ch.Score))
		b.WriteString(ch.Content)
		b.WriteString("\n")
	}
	return b.String()
}
