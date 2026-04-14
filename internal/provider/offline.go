package provider

import (
	"context"
	"fmt"
	"strings"
)

type OfflineProvider struct{}

func NewOfflineProvider() *OfflineProvider {
	return &OfflineProvider{}
}

func (p *OfflineProvider) Name() string {
	return "offline"
}

func (p *OfflineProvider) Model() string {
	return "offline-analyzer-v1"
}

func (p *OfflineProvider) Complete(_ context.Context, req CompletionRequest) (*CompletionResponse, error) {
	question := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			question = strings.TrimSpace(req.Messages[i].Content)
			break
		}
	}

	var b strings.Builder
	if question == "" {
		b.WriteString("Offline mode is active. No user question was provided.")
	} else {
		b.WriteString("Offline mode is active. I analyzed your local code context and prepared a best-effort response.")
		b.WriteString("\n\nQuestion:\n")
		b.WriteString(question)
	}

	if len(req.Context) > 0 {
		b.WriteString("\n\nRelevant files:")
		limit := len(req.Context)
		if limit > 6 {
			limit = 6
		}
		for i := 0; i < limit; i++ {
			ch := req.Context[i]
			b.WriteString(fmt.Sprintf("\n- %s (score %.2f)", ch.Path, ch.Score))
		}
	}

	b.WriteString("\n\nTip: configure provider API keys to enable full LLM generation.")

	out := b.String()
	usage := Usage{
		InputTokens:  p.CountTokens(question),
		OutputTokens: p.CountTokens(out),
	}
	usage.TotalTokens = usage.InputTokens + usage.OutputTokens

	return &CompletionResponse{
		Text:  out,
		Model: p.Model(),
		Usage: usage,
	}, nil
}

func (p *OfflineProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 16)
	go func() {
		defer close(ch)
		resp, err := p.Complete(ctx, req)
		if err != nil {
			ch <- StreamEvent{Type: StreamError, Err: err}
			return
		}
		lines := strings.Split(resp.Text, "\n")
		for _, line := range lines {
			select {
			case <-ctx.Done():
				ch <- StreamEvent{Type: StreamError, Err: ctx.Err()}
				return
			case ch <- StreamEvent{Type: StreamDelta, Delta: line + "\n"}:
			}
		}
		ch <- StreamEvent{Type: StreamDone}
	}()
	return ch, nil
}

func (p *OfflineProvider) CountTokens(text string) int {
	return len(strings.Fields(text))
}

func (p *OfflineProvider) MaxContext() int {
	return 12000
}

func (p *OfflineProvider) Hints() ProviderHints {
	return ProviderHints{
		ToolStyle:   "none",
		Cache:       false,
		LowLatency:  true,
		BestFor:     []string{"offline-analysis", "fallback"},
		MaxContext:  p.MaxContext(),
		DefaultMode: "standard",
	}
}
