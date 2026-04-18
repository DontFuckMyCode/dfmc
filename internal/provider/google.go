package provider

// google.go — Gemini (Google Generative Language API) provider.
//
// Endpoint: https://generativelanguage.googleapis.com/v1beta
//   POST /models/{model}:generateContent            — non-streaming
//   POST /models/{model}:streamGenerateContent?alt=sse — SSE streaming
//
// The request shape differs from OpenAI in three important ways:
//   1. System prompt lives in a top-level `systemInstruction`, not the
//      messages array.
//   2. Roles are {"user","model"} (no "assistant"); see google_tools.go.
//   3. Tool calls are JSON objects inline with text parts, not a separate
//      list on the message — see parseGoogleCandidate.

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

const defaultGoogleBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// GoogleProvider implements Provider against the Gemini REST API.
type GoogleProvider struct {
	name       string
	model      string
	apiKey     string
	baseURL    string
	maxTokens  int
	maxContext int
	httpClient *http.Client
}

// NewGoogleProvider builds a Gemini client. `model` is e.g. "gemini-1.5-pro"
// or "gemini-2.0-flash". `baseURL` overrides the API host for testing or
// self-hosted gateways; empty uses the public endpoint.
func NewGoogleProvider(model, apiKey, baseURL string, maxTokens, maxContext int) *GoogleProvider {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultGoogleBaseURL
	}
	return &GoogleProvider{
		name:       "google",
		model:      model,
		apiKey:     apiKey,
		baseURL:    baseURL,
		maxTokens:  maxTokens,
		maxContext: maxContext,
		httpClient: newProviderHTTPClient(),
	}
}

func (p *GoogleProvider) Name() string  { return p.name }
func (p *GoogleProvider) Model() string { return p.model }

func (p *GoogleProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("%w: google api key missing", ErrProviderUnavailable)
	}
	model := nonEmpty(req.Model, p.model)

	payload, err := json.Marshal(p.buildRequestBody(req))
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s/models/%s:generateContent", p.baseURL, model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.apiKey)

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
		return nil, fmt.Errorf("google response exceeded %d bytes — refusing to decode (likely a misbehaving proxy or hostile endpoint)", maxProviderResponseBytes)
	}
	if resp.StatusCode >= 400 {
		if isThrottleStatus(resp.StatusCode) {
			return nil, newThrottledErrorFromResponse("google", resp, string(raw))
		}
		return nil, p.statusError(resp.StatusCode, raw)
	}

	var parsed struct {
		Candidates     []googleCandidate   `json:"candidates"`
		UsageMetadata  googleUsageMetadata `json:"usageMetadata"`
		PromptFeedback struct {
			BlockReason string `json:"blockReason"`
		} `json:"promptFeedback"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode google response: %w", err)
	}
	if len(parsed.Candidates) == 0 {
		if parsed.PromptFeedback.BlockReason != "" {
			return nil, fmt.Errorf("google blocked request: %s", parsed.PromptFeedback.BlockReason)
		}
		return nil, fmt.Errorf("google returned no candidates")
	}
	text, calls, stop := parseGoogleCandidate(parsed.Candidates[0])
	usage := Usage{
		InputTokens:  parsed.UsageMetadata.PromptTokenCount,
		OutputTokens: parsed.UsageMetadata.CandidatesTokenCount,
		TotalTokens:  parsed.UsageMetadata.TotalTokenCount,
	}
	if usage.TotalTokens == 0 {
		usage.InputTokens = p.CountTokens(renderMessagesForCount(req.Messages))
		usage.OutputTokens = p.CountTokens(text)
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return &CompletionResponse{
		Text:       text,
		Model:      model,
		Usage:      usage,
		ToolCalls:  calls,
		StopReason: stop,
	}, nil
}

func (p *GoogleProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	if strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("%w: google api key missing", ErrProviderUnavailable)
	}
	model := nonEmpty(req.Model, p.model)
	payload, err := json.Marshal(p.buildRequestBody(req))
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", p.baseURL, model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if isThrottleStatus(resp.StatusCode) {
			return nil, newThrottledErrorFromResponse("google", resp, string(raw))
		}
		return nil, p.statusError(resp.StatusCode, raw)
	}

	ch := make(chan StreamEvent, 32)
	providerName := p.name
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

		startAnnounced := false
		var usage Usage
		usageSet := false
		stopReason := StopUnknown
		emittedToolCall := false

		emitStart := func() {
			if startAnnounced {
				return
			}
			startAnnounced = true
			select {
			case <-ctx.Done():
			case ch <- StreamEvent{Type: StreamStart, Provider: providerName, Model: model}:
			}
		}

		finish := func() {
			emitStart()
			done := StreamEvent{Type: StreamDone, Provider: providerName, Model: model, StopReason: stopReason}
			if usageSet {
				u := usage
				if u.TotalTokens == 0 {
					u.TotalTokens = u.InputTokens + u.OutputTokens
				}
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
			body := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if body == "" || body == "[DONE]" {
				continue
			}
			var evt struct {
				Candidates    []googleCandidate   `json:"candidates"`
				UsageMetadata googleUsageMetadata `json:"usageMetadata"`
			}
			if err := json.Unmarshal([]byte(body), &evt); err != nil {
				continue
			}
			if evt.UsageMetadata.TotalTokenCount > 0 || evt.UsageMetadata.PromptTokenCount > 0 {
				usage.InputTokens = evt.UsageMetadata.PromptTokenCount
				usage.OutputTokens = evt.UsageMetadata.CandidatesTokenCount
				usage.TotalTokens = evt.UsageMetadata.TotalTokenCount
				usageSet = true
			}
			if len(evt.Candidates) == 0 {
				continue
			}
			cand := evt.Candidates[0]
			for _, part := range cand.Content.Parts {
				if part.FunctionCall != nil {
					emittedToolCall = true
				}
				if strings.TrimSpace(part.Text) == "" {
					continue
				}
				emitStart()
				select {
				case <-ctx.Done():
					ch <- StreamEvent{Type: StreamError, Err: ctx.Err()}
					return
				case ch <- StreamEvent{Type: StreamDelta, Delta: part.Text}:
				}
			}
			if fr := strings.TrimSpace(cand.FinishReason); fr != "" {
				stopReason = googleStopReason(fr, emittedToolCall)
			}
		}
		if err := sc.Err(); err != nil {
			ch <- StreamEvent{Type: StreamError, Err: err}
			return
		}
		if emittedToolCall && stopReason == StopUnknown {
			stopReason = StopTool
		}
		finish()
	}()

	return ch, nil
}

// buildRequestBody assembles the generateContent payload. Shared between
// Complete and Stream so the two stay in sync.
func (p *GoogleProvider) buildRequestBody(req CompletionRequest) map[string]any {
	body := map[string]any{
		"contents": buildGoogleContents(req),
	}
	if sys := googleSystemInstruction(req); sys != nil {
		body["systemInstruction"] = sys
	}
	gen := map[string]any{}
	if p.maxTokens > 0 {
		gen["maxOutputTokens"] = p.maxTokens
	}
	if len(gen) > 0 {
		body["generationConfig"] = gen
	}
	if decls := googleToolDeclarations(req.Tools); len(decls) > 0 {
		body["tools"] = decls
		if cfg := googleToolChoice(req.ToolChoice); cfg != nil {
			body["toolConfig"] = cfg
		}
	}
	return body
}

// statusError turns a non-2xx HTTP response into an error, flagging context
// overflow so the router can auto-compact. Gemini surfaces overflow as 400
// with "exceeds the maximum number of tokens" or similar language; the
// string match in router.isContextOverflow also catches variants.
func (p *GoogleProvider) statusError(code int, raw []byte) error {
	msg := strings.ToLower(string(raw))
	if strings.Contains(msg, "exceeds the maximum") ||
		strings.Contains(msg, "input token count") ||
		strings.Contains(msg, "context window") {
		return fmt.Errorf("%w: google status %d: %s", ErrContextOverflow, code, string(raw))
	}
	return fmt.Errorf("google error status %d: %s", code, string(raw))
}

func (p *GoogleProvider) CountTokens(text string) int {
	// Gemini doesn't expose a cheap local tokenizer; fields-based estimate
	// matches what other providers use for their fallback path.
	return len(strings.Fields(text))
}

func (p *GoogleProvider) MaxContext() int {
	if p.maxContext > 0 {
		return p.maxContext
	}
	// Gemini 1.5/2.x default ceilings are 1M+ tokens. Picking 1M keeps the
	// number defensible when the profile doesn't specify one.
	return 1_000_000
}

func (p *GoogleProvider) Hints() ProviderHints {
	return ProviderHints{
		ToolStyle:     "function-calling",
		Cache:         false,
		LowLatency:    false,
		BestFor:       []string{"long-context", "code", "multimodal"},
		MaxContext:    p.MaxContext(),
		DefaultMode:   "balanced",
		SupportsTools: true,
	}
}
