package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
)

// fakeTextProvider is a minimal Provider stub for AskRaced. Unlike
// scriptedProvider it can simulate slow responses and static errors so the
// race outcome is deterministic. hints.SupportsTools is intentionally false
// — AskRaced must skip the native-tool-loop path in every case so this stub
// doesn't need to implement tool calling.
type fakeTextProvider struct {
	name  string
	text  string
	sleep time.Duration
	err   error
}

func (f *fakeTextProvider) Name() string                  { return f.name }
func (f *fakeTextProvider) Model() string                 { return f.name + "-m" }
func (f *fakeTextProvider) CountTokens(s string) int      { return len(s) / 4 }
func (f *fakeTextProvider) MaxContext() int               { return 64000 }
func (f *fakeTextProvider) Hints() provider.ProviderHints { return provider.ProviderHints{MaxContext: 64000} }
func (f *fakeTextProvider) Complete(ctx context.Context, _ provider.CompletionRequest) (*provider.CompletionResponse, error) {
	if f.sleep > 0 {
		select {
		case <-time.After(f.sleep):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return &provider.CompletionResponse{
		Text:  f.text,
		Model: f.Model(),
		Usage: provider.Usage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3},
	}, nil
}
func (f *fakeTextProvider) Stream(context.Context, provider.CompletionRequest) (<-chan provider.StreamEvent, error) {
	return nil, errors.New("stream not used in race tests")
}

func newRaceEngine(t *testing.T, providers ...provider.Provider) *Engine {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Providers.Primary = providers[0].Name()
	for _, p := range providers {
		cfg.Providers.Profiles[p.Name()] = config.ModelConfig{Model: p.Model(), MaxContext: 64000}
	}
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	for _, p := range providers {
		router.Register(p)
	}
	return &Engine{
		Config:    cfg,
		Providers: router,
		EventBus:  NewEventBus(),
	}
}

// TestAskRacedReturnsWinnerAndEmitsEvent: the faster candidate's text must
// come back AND the engine must publish a provider:race:complete event
// whose payload identifies the winner. This proves both that AskRaced
// reached CompleteRaced and that it emits observability correctly.
func TestAskRacedReturnsWinnerAndEmitsEvent(t *testing.T) {
	fast := &fakeTextProvider{name: "fast", text: "from fast", sleep: 10 * time.Millisecond}
	slow := &fakeTextProvider{name: "slow", text: "from slow", sleep: 200 * time.Millisecond}
	eng := newRaceEngine(t, fast, slow)

	evCh := eng.EventBus.Subscribe("provider:race:complete")

	answer, winner, err := eng.AskRaced(context.Background(), "hello", []string{"fast", "slow"})
	if err != nil {
		t.Fatalf("AskRaced: %v", err)
	}
	if winner != "fast" {
		t.Fatalf("expected fast winner, got %q", winner)
	}
	if !strings.Contains(answer, "from fast") {
		t.Fatalf("expected fast answer, got %q", answer)
	}

	select {
	case ev := <-evCh:
		payload, _ := ev.Payload.(map[string]any)
		gotWinner, _ := payload["winner"].(string)
		if gotWinner != "fast" {
			t.Fatalf("event winner=%q, want fast (payload=%+v)", gotWinner, payload)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("race:complete event not published")
	}
}

// TestAskRacedPublishesFailureEvent: when every candidate errors, the
// engine must emit provider:race:failed so consumers can surface the
// outage instead of silently getting a joined-error log line.
func TestAskRacedPublishesFailureEvent(t *testing.T) {
	a := &fakeTextProvider{name: "a", err: errors.New("boom-a")}
	b := &fakeTextProvider{name: "b", err: errors.New("boom-b")}
	eng := newRaceEngine(t, a, b)

	evCh := eng.EventBus.Subscribe("provider:race:failed")

	_, _, err := eng.AskRaced(context.Background(), "hello", []string{"a", "b"})
	if err == nil {
		t.Fatalf("expected joined error")
	}

	select {
	case ev := <-evCh:
		payload, _ := ev.Payload.(map[string]any)
		if _, ok := payload["error"].(string); !ok {
			t.Fatalf("race:failed payload missing error: %+v", payload)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("race:failed event not published")
	}
}

// TestAskRacedEmptyCandidatesDerivesFromRouter: passing nil candidates must
// let the router derive them via ResolveOrder (primary+fallbacks, stripping
// offline). The winner comes back from the derived set without the caller
// naming it.
func TestAskRacedEmptyCandidatesDerivesFromRouter(t *testing.T) {
	primary := &fakeTextProvider{name: "primary", text: "hello", sleep: 5 * time.Millisecond}
	eng := newRaceEngine(t, primary)

	answer, winner, err := eng.AskRaced(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("AskRaced empty candidates: %v", err)
	}
	if winner != "primary" {
		t.Fatalf("expected primary winner when candidates nil, got %q", winner)
	}
	if !strings.Contains(answer, "hello") {
		t.Fatalf("unexpected answer %q", answer)
	}
}

// TestAskRacedEmptyQuestionRejected: invalid input must be refused before
// any provider is dispatched — otherwise N providers get billed for a
// blank prompt.
func TestAskRacedEmptyQuestionRejected(t *testing.T) {
	p := &fakeTextProvider{name: "p", text: "x"}
	eng := newRaceEngine(t, p)

	_, _, err := eng.AskRaced(context.Background(), "   ", []string{"p"})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty-question error, got %v", err)
	}
}
