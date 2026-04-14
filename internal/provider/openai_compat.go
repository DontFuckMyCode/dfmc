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

func NewOpenAICompatibleProvider(name, model, apiKey, baseURL string, maxTokens, maxContext int) *OpenAICompatibleProvider {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL(name)
	}
	return &OpenAICompatibleProvider{
		name:       name,
		model:      model,
		apiKey:     apiKey,
		baseURL:    strings.TrimRight(baseURL, "/"),
		maxTokens:  maxTokens,
		maxContext: maxContext,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (p *OpenAICompatibleProvider) Name() string  { return p.name }
func (p *OpenAICompatibleProvider) Model() string { return p.model }

func (p *OpenAICompatibleProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if strings.TrimSpace(p.baseURL) == "" {
		return nil, fmt.Errorf("%w: %s base_url missing", ErrProviderUnavailable, p.name)
	}
	// Most OpenAI-compatible providers require API keys; generic may be keyless.
	if p.name != "generic" && strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("%w: %s api key missing", ErrProviderUnavailable, p.name)
	}

	messages := make([]map[string]string, 0, len(req.Messages)+2)
	if strings.TrimSpace(req.System) != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": req.System,
		})
	}
	for _, m := range req.Messages {
		role := string(m.Role)
		if role == "" {
			role = "user"
		}
		messages = append(messages, map[string]string{
			"role":    role,
			"content": m.Content,
		})
	}
	if len(req.Context) > 0 {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": renderContextChunks(req.Context),
		})
	}

	model := nonEmpty(req.Model, p.model)
	body := map[string]any{
		"model":    model,
		"messages": messages,
	}
	if p.maxTokens > 0 {
		body["max_tokens"] = p.maxTokens
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

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s error status %d: %s", p.name, resp.StatusCode, string(raw))
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
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", p.name, err)
	}

	text := ""
	if len(parsed.Choices) > 0 {
		text = parsed.Choices[0].Message.Content
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
		Text:  text,
		Model: nonEmpty(parsed.Model, model),
		Usage: usage,
	}, nil
}

func (p *OpenAICompatibleProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	if strings.TrimSpace(p.baseURL) == "" {
		return nil, fmt.Errorf("%w: %s base_url missing", ErrProviderUnavailable, p.name)
	}
	if p.name != "generic" && strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("%w: %s api key missing", ErrProviderUnavailable, p.name)
	}

	messages := make([]map[string]string, 0, len(req.Messages)+2)
	if strings.TrimSpace(req.System) != "" {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": req.System,
		})
	}
	for _, m := range req.Messages {
		role := string(m.Role)
		if role == "" {
			role = "user"
		}
		messages = append(messages, map[string]string{
			"role":    role,
			"content": m.Content,
		})
	}
	if len(req.Context) > 0 {
		messages = append(messages, map[string]string{
			"role":    "system",
			"content": renderContextChunks(req.Context),
		})
	}
	model := nonEmpty(req.Model, p.model)
	body := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   true,
	}
	if p.maxTokens > 0 {
		body["max_tokens"] = p.maxTokens
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
		return nil, fmt.Errorf("%s error status %d: %s", p.name, resp.StatusCode, string(raw))
	}

	ch := make(chan StreamEvent, 32)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

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
				ch <- StreamEvent{Type: StreamDone}
				return
			}
			if strings.Contains(payload, "\"success\":false") {
				ch <- StreamEvent{Type: StreamError, Err: fmt.Errorf("%s provider error: %s", p.name, payload)}
				return
			}

			var evt struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(payload), &evt); err != nil {
				continue
			}
			if len(evt.Choices) == 0 {
				continue
			}
			delta := evt.Choices[0].Delta.Content
			if delta == "" {
				continue
			}
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
		ch <- StreamEvent{Type: StreamDone}
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
		ToolStyle:   "function-calling",
		Cache:       false,
		LowLatency:  false,
		BestFor:     []string{"general", "code"},
		MaxContext:  p.MaxContext(),
		DefaultMode: "balanced",
	}
}

func defaultOpenAIBaseURL(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "openai":
		return "https://api.openai.com/v1"
	case "deepseek":
		return "https://api.deepseek.com/v1"
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
