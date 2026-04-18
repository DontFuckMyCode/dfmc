package engine

import (
	"context"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
)

type terminalAfterCancelProvider struct {
	done chan struct{}
}

func (p *terminalAfterCancelProvider) Name() string  { return "cancel-drain" }
func (p *terminalAfterCancelProvider) Model() string { return "cancel-drain-model" }
func (p *terminalAfterCancelProvider) Complete(context.Context, provider.CompletionRequest) (*provider.CompletionResponse, error) {
	return &provider.CompletionResponse{Text: "unused", Model: p.Model()}, nil
}
func (p *terminalAfterCancelProvider) Stream(ctx context.Context, _ provider.CompletionRequest) (<-chan provider.StreamEvent, error) {
	ch := make(chan provider.StreamEvent)
	go func() {
		defer close(ch)
		defer close(p.done)
		<-ctx.Done()
		ch <- provider.StreamEvent{Type: provider.StreamDone, Provider: p.Name(), Model: p.Model()}
	}()
	return ch, nil
}
func (p *terminalAfterCancelProvider) CountTokens(string) int { return 0 }
func (p *terminalAfterCancelProvider) MaxContext() int        { return 12000 }
func (p *terminalAfterCancelProvider) Hints() provider.ProviderHints {
	return provider.ProviderHints{MaxContext: 12000}
}

func TestStreamAskCancellationDrainsUpstreamStream(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "cancel-drain"
	cfg.Providers.Profiles["cancel-drain"] = config.ModelConfig{Model: "cancel-drain-model", MaxContext: 12000}
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	stub := &terminalAfterCancelProvider{done: make(chan struct{})}
	router.Register(stub)

	eng := &Engine{
		Config:      cfg,
		EventBus:    NewEventBus(),
		ProjectRoot: t.TempDir(),
		Providers:   router,
		state:       StateReady,
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := eng.StreamAsk(ctx, "hello")
	if err != nil {
		t.Fatalf("StreamAsk: %v", err)
	}
	_ = stream
	cancel()

	select {
	case <-stub.done:
	case <-time.After(1 * time.Second):
		t.Fatal("provider stream goroutine did not exit after StreamAsk cancellation")
	}
}
