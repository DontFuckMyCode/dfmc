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
