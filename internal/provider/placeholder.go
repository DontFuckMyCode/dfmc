package provider

import (
	"context"
	"fmt"
	"strings"
)

type PlaceholderProvider struct {
	name       string
	model      string
	configured bool
}

func NewPlaceholderProvider(name, model string, configured bool) *PlaceholderProvider {
	return &PlaceholderProvider{
		name:       name,
		model:      model,
		configured: configured,
	}
}

func (p *PlaceholderProvider) Name() string {
	return p.name
}

func (p *PlaceholderProvider) Model() string {
	return p.model
}

func (p *PlaceholderProvider) Complete(_ context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if !p.configured {
		return nil, fmt.Errorf("%w: %s api key missing", ErrProviderUnavailable, p.name)
	}
	text := fmt.Sprintf("%s provider is configured, but network client implementation is pending. Falling back is recommended for now.", p.name)
	return &CompletionResponse{
		Text:  text,
		Model: nonEmpty(req.Model, p.model),
		Usage: Usage{
			InputTokens:  p.CountTokens(strings.TrimSpace(text)),
			OutputTokens: p.CountTokens(strings.TrimSpace(text)),
			TotalTokens:  p.CountTokens(strings.TrimSpace(text)) * 2,
		},
	}, nil
}

func (p *PlaceholderProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 2)
	go func() {
		defer close(ch)
		resp, err := p.Complete(ctx, req)
		if err != nil {
			ch <- StreamEvent{Type: StreamError, Err: err}
			return
		}
		ch <- StreamEvent{Type: StreamDelta, Delta: resp.Text}
		ch <- StreamEvent{Type: StreamDone}
	}()
	return ch, nil
}

func (p *PlaceholderProvider) CountTokens(text string) int {
	return len(strings.Fields(text))
}

func (p *PlaceholderProvider) MaxContext() int {
	return 128000
}

func (p *PlaceholderProvider) Hints() ProviderHints {
	return ProviderHints{
		ToolStyle:   "provider-native",
		Cache:       true,
		LowLatency:  false,
		BestFor:     []string{"general"},
		MaxContext:  p.MaxContext(),
		DefaultMode: "balanced",
	}
}

func nonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
