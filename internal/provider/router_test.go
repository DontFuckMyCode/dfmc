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
