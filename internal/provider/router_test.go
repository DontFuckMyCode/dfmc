package provider

import (
	"context"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestRouterFallsBackToOffline(t *testing.T) {
	cfg := config.ProvidersConfig{
		Primary:  "anthropic",
		Fallback: []string{"openai"},
		Profiles: map[string]config.ModelConfig{
			"anthropic": {Model: "claude-sonnet-4-6"},
			"openai":    {Model: "gpt-5.4"},
		},
	}
	router, err := NewRouter(cfg)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	resp, name, err := router.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "Explain auth module"}},
	})
	if err != nil {
		t.Fatalf("router complete: %v", err)
	}
	if name != "offline" {
		t.Fatalf("expected offline provider, got %s", name)
	}
	if resp == nil || resp.Text == "" {
		t.Fatal("expected non-empty response")
	}
}

// TestFallbackObserver_FiresOnCascade pins the new fallback wire:
// when Complete walks past failing providers in the resolved order
// to a working one, the observer fires once per transition with
// from/to/attempt/err. Without this signal the cascade was invisible
// — the user saw the offline provider's response with no clue that
// anthropic and openai had errored along the way.
func TestFallbackObserver_FiresOnCascade(t *testing.T) {
	cfg := config.ProvidersConfig{
		Primary:  "anthropic",
		Fallback: []string{"openai"},
		Profiles: map[string]config.ModelConfig{
			"anthropic": {Model: "claude-sonnet-4-6"},
			"openai":    {Model: "gpt-5.4"},
		},
	}
	router, err := NewRouter(cfg)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	type call struct {
		from    string
		to      string
		attempt int
	}
	calls := make([]call, 0, 4)
	router.SetFallbackObserver(func(ev FallbackEvent) {
		calls = append(calls, call{from: ev.From, to: ev.To, attempt: ev.Attempt})
	})

	// Both anthropic and openai are placeholder providers (no API key
	// in test env), so they fail and the cascade falls through to
	// offline. Expect TWO observer fires: anthropic→openai and
	// openai→offline.
	if _, _, err := router.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatalf("router complete: %v", err)
	}

	if len(calls) < 2 {
		t.Fatalf("expected at least 2 fallback events, got %d: %#v", len(calls), calls)
	}
	// First transition: primary → first fallback
	if calls[0].from != "anthropic" || calls[0].to != "openai" {
		t.Errorf("first transition: got %s→%s, want anthropic→openai", calls[0].from, calls[0].to)
	}
	if calls[0].attempt != 0 {
		t.Errorf("first transition attempt: got %d, want 0", calls[0].attempt)
	}
	// Second transition: first fallback → next (offline)
	if calls[1].from != "openai" || calls[1].to != "offline" {
		t.Errorf("second transition: got %s→%s, want openai→offline", calls[1].from, calls[1].to)
	}
	// No third event — offline succeeded, last attempt is terminal
	// not a fallback transition.
	if len(calls) > 2 {
		t.Errorf("expected NO transition past the successful offline call, got %d extra: %#v", len(calls)-2, calls[2:])
	}
}

func TestOfflineStream(t *testing.T) {
	p := NewOfflineProvider()
	stream, err := p.Stream(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("offline stream: %v", err)
	}
	gotDone := false
	for ev := range stream {
		if ev.Type == StreamDone {
			gotDone = true
		}
	}
	if !gotDone {
		t.Fatal("expected stream done event")
	}
}

func TestProviderFromProfileSelectsLiveClients(t *testing.T) {
	p1 := providerFromProfile("openai", config.ModelConfig{
		Model:      "gpt-5.4",
		APIKey:     "k-test",
		MaxContext: 1050000,
		Protocol:   "openai",
	})
	if _, ok := p1.(*OpenAICompatibleProvider); !ok {
		t.Fatalf("expected OpenAICompatibleProvider, got %T", p1)
	}
	if got := p1.MaxContext(); got != 1050000 {
		t.Fatalf("expected openai max context 1050000, got %d", got)
	}

	p2 := providerFromProfile("anthropic", config.ModelConfig{
		Model:      "claude-sonnet-4-6",
		APIKey:     "k-test",
		MaxContext: 1000000,
		Protocol:   "anthropic",
	})
	ap2, ok := p2.(*AnthropicProvider)
	if !ok {
		t.Fatalf("expected AnthropicProvider, got %T", p2)
	}
	if got := ap2.Name(); got != "anthropic" {
		t.Fatalf("expected anthropic provider name preserved, got %q", got)
	}

	p3 := providerFromProfile("generic", config.ModelConfig{
		Model:      "qwen",
		BaseURL:    "http://localhost:11434/v1",
		MaxContext: 128000,
		Protocol:   "openai-compatible",
	})
	if _, ok := p3.(*OpenAICompatibleProvider); !ok {
		t.Fatalf("expected OpenAICompatibleProvider for generic, got %T", p3)
	}

	p4 := providerFromProfile("kimi", config.ModelConfig{
		Model:      "kimi-k2.5",
		APIKey:     "k-test",
		BaseURL:    "https://api.moonshot.ai/v1",
		MaxContext: 262144,
		Protocol:   "openai-compatible",
	})
	if _, ok := p4.(*OpenAICompatibleProvider); !ok {
		t.Fatalf("expected OpenAICompatibleProvider for kimi, got %T", p4)
	}

	p5 := providerFromProfile("minimax", config.ModelConfig{
		Model:      "MiniMax-M2.7",
		APIKey:     "k-test",
		BaseURL:    "https://api.minimax.io/anthropic/v1",
		MaxContext: 204800,
		Protocol:   "anthropic",
	})
	ap5, ok := p5.(*AnthropicProvider)
	if !ok {
		t.Fatalf("expected AnthropicProvider for minimax, got %T", p5)
	}
	if got := ap5.Name(); got != "minimax" {
		t.Fatalf("expected minimax provider name preserved, got %q", got)
	}

	p6 := providerFromProfile("zai", config.ModelConfig{
		Model:      "glm-5.1",
		APIKey:     "k-test",
		BaseURL:    "https://api.z.ai/api/anthropic",
		MaxContext: 200000,
		Protocol:   "anthropic",
	})
	ap6, ok := p6.(*OpenAICompatibleProvider)
	if !ok {
		t.Fatalf("expected OpenAICompatibleProvider for zai anthropic-style profile, got %T", p6)
	}
	if got := ap6.Name(); got != "zai" {
		t.Fatalf("expected zai provider name preserved, got %q", got)
	}
	if got := ap6.baseURL; got != "https://api.z.ai/api/paas/v4" {
		t.Fatalf("expected zai anthropic-style config to remap to paas endpoint, got %q", got)
	}
}

// T5: Router.Get must never return (nil, true) for a disabled/placeholder
// provider. A caller that receives (nil, true) will panic on the next
// method call. The test verifies that a provider registered with no API
// key (placeholder) is either absent from the map OR returns (provider, true)
// with a non-nil provider.
func TestRouter_Get_DisabledProvider_ReturnsSafeValue(t *testing.T) {
	cfg := config.ProvidersConfig{
		Primary:  "disabled",
		Fallback: []string{},
		Profiles: map[string]config.ModelConfig{
			// Empty API key → placeholder provider registered
			"disabled": {Model: "claude-sonnet", APIKey: ""},
		},
	}
	router, err := NewRouter(cfg)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	p, ok := router.Get("disabled")
	if ok && p == nil {
		t.Fatal("Get returned (nil, true) for disabled provider — caller will panic")
	}
	// It's acceptable for ok=false (placeholder not in map) OR
	// ok=true with a non-nil provider (placeholder is present).
	// Both are safe. The forbidden case is (nil, true).
}
