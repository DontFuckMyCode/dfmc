package provider

// anthropic.go — non-streaming Complete entry, constructor, name/
// model accessors, the bounded-body reader shared by every provider,
// the parseCommonProviderError convenience, and the metadata trio
// (CountTokens / MaxContext / Hints). Sibling: anthropic_stream.go
// owns the SSE Stream pump.

import (
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
	name       string
	model      string
	models     []string
	apiKey     string
	baseURL    string
	maxTokens  int
	maxContext int
	httpClient *http.Client
}

func NewAnthropicProvider(model, apiKey, baseURL string, maxTokens, maxContext int) *AnthropicProvider {
	return NewNamedAnthropicProvider("anthropic", model, apiKey, baseURL, maxTokens, maxContext, 0)
}

func NewNamedAnthropicProvider(name, model, apiKey, baseURL string, maxTokens, maxContext int, httpTimeout time.Duration) *AnthropicProvider {
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
		httpClient: newProviderHTTPClient(httpTimeout, baseURL),
	}
}

func (p *AnthropicProvider) Name() string {
	if strings.TrimSpace(p.name) == "" {
		return "anthropic"
	}
	return p.name
}
func (p *AnthropicProvider) Model() string { return p.model }
func (p *AnthropicProvider) Models() []string {
	if len(p.models) > 0 {
		return append([]string(nil), p.models...)
	}
	return []string{p.model}
}

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
	defer func() { _ = resp.Body.Close() }()

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
		return nil, &StatusError{Provider: "anthropic", StatusCode: resp.StatusCode, Body: string(raw)}
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

// Stream lives in anthropic_stream.go.

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
