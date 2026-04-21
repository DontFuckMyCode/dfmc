package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

// notConfiguredError builds the error returned when a placeholder has no
// API key. Includes the expected env var name so the user can fix it in
// one try instead of hunting through config docs.
func notConfiguredError(name string) error {
	env := config.EnvVarForProvider(name)
	if env != "" {
		return fmt.Errorf("%w: %s api key missing — set %s in .env or providers.profiles.%s.api_key", ErrProviderUnavailable, name, env, name)
	}
	return fmt.Errorf("%w: %s api key missing — set providers.profiles.%s.api_key in config.yaml", ErrProviderUnavailable, name, name)
}

type PlaceholderProvider struct {
	name       string
	model      string
	configured bool
	maxContext int
}

func NewPlaceholderProvider(name, model string, configured bool, maxContext int) *PlaceholderProvider {
	return &PlaceholderProvider{
		name:       name,
		model:      model,
		configured: configured,
		maxContext: maxContext,
	}
}

func (p *PlaceholderProvider) Name() string {
	return p.name
}

func (p *PlaceholderProvider) Model() string   { return p.model }
func (p *PlaceholderProvider) Models() []string { return []string{p.model} }

func (p *PlaceholderProvider) Complete(_ context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if !p.configured {
		return nil, notConfiguredError(p.name)
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
	if !p.configured {
		return nil, notConfiguredError(p.name)
	}
	ch := make(chan StreamEvent, 3)
	go func() {
		defer close(ch)
		resp, err := p.Complete(ctx, req)
		if err != nil {
			ch <- StreamEvent{Type: StreamError, Err: err}
			return
		}
		ch <- StreamEvent{Type: StreamStart, Provider: p.name, Model: resp.Model}
		ch <- StreamEvent{Type: StreamDelta, Delta: resp.Text}
		usage := resp.Usage
		ch <- StreamEvent{
			Type:       StreamDone,
			Provider:   p.name,
			Model:      resp.Model,
			Usage:      &usage,
			StopReason: resp.StopReason,
		}
	}()
	return ch, nil
}

func (p *PlaceholderProvider) CountTokens(text string) int {
	return len(strings.Fields(text))
}

func (p *PlaceholderProvider) MaxContext() int {
	if p.maxContext > 0 {
		return p.maxContext
	}
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
