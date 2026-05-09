package provider

// google_stream.go — SSE streaming for the Gemini provider.
// Sibling of google.go which keeps the GoogleProvider struct,
// constructor, Complete sync entry point, the shared buildRequestBody
// payload assembler used by both paths, statusError classifier (with
// the context-overflow path that the router auto-compacts on),
// CountTokens fallback, MaxContext, and Hints.
//
// Splitting Stream out keeps google.go scoped to "what does one
// generateContent call look like" while this file owns "how do we
// pump streamGenerateContent?alt=sse SSE chunks into the engine's
// StreamEvent channel without leaking the upstream HTTP body when
// the consumer walks away mid-stream."

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
		defer func() { _ = resp.Body.Close() }()
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
		defer func() { _ = resp.Body.Close() }()

		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

		startAnnounced := false
		var usage Usage
		usageSet := false
		stopReason := StopUnknown
		var toolCalls []ToolCall

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
			done := StreamEvent{
				Type:       StreamDone,
				Provider:   providerName,
				Model:      model,
				StopReason: stopReason,
				ToolCalls:  toolCalls,
			}
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
					input := map[string]any{}
					if len(part.FunctionCall.Args) > 0 {
						_ = json.Unmarshal(part.FunctionCall.Args, &input)
					}
					toolCalls = append(toolCalls, ToolCall{
						ID:    fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, len(toolCalls)),
						Name:  part.FunctionCall.Name,
						Input: input,
					})
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
				stopReason = googleStopReason(fr, len(toolCalls) > 0)
			}
		}
		if err := sc.Err(); err != nil {
			ch <- StreamEvent{Type: StreamError, Err: err}
			return
		}
		if len(toolCalls) > 0 && stopReason == StopUnknown {
			stopReason = StopTool
		}
		finish()
	}()

	return ch, nil
}
