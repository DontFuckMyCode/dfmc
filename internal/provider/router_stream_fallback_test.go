package provider

import (
	"context"
	"sync"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// TestFallbackObserver_FiresOnStreamCascade is the streaming counterpart to
// TestFallbackObserver_FiresOnCascade. router.Stream is the main chat path
// (engine.StreamAsk goes through it), but its dispatch loop used to emit no
// FallbackEvent at all — so the TUI fallback badge / telemetry went dark on
// streaming fallbacks while non-streaming Complete reported them. This pins
// that Stream now mirrors Complete's per-transition emission.
func TestFallbackObserver_FiresOnStreamCascade(t *testing.T) {
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
		from, to string
		attempt  int
	}
	var mu sync.Mutex
	var calls []call
	router.SetFallbackObserver(func(ev FallbackEvent) {
		mu.Lock()
		calls = append(calls, call{ev.From, ev.To, ev.Attempt})
		mu.Unlock()
	})

	// anthropic + openai are unconfigured placeholders (no API key in the
	// test env), so their Stream fails at open with notConfiguredError and
	// the cascade falls through to offline, which streams successfully.
	stream, _, serr := router.Stream(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hi"}},
	})
	if serr != nil {
		t.Fatalf("router stream: %v", serr)
	}
	// Drain the returned (offline) stream so the recovery goroutine finishes
	// before the test ends.
	if stream != nil {
		for range stream {
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) < 2 {
		t.Fatalf("streaming cascade emitted %d fallback events, want >= 2 (Complete emits these; Stream must too): %#v", len(calls), calls)
	}
	if calls[0].from != "anthropic" || calls[0].to != "openai" {
		t.Errorf("first transition: got %s→%s, want anthropic→openai", calls[0].from, calls[0].to)
	}
	if calls[0].attempt != 0 {
		t.Errorf("first transition attempt: got %d, want 0", calls[0].attempt)
	}
	if calls[1].from != "openai" || calls[1].to != "offline" {
		t.Errorf("second transition: got %s→%s, want openai→offline", calls[1].from, calls[1].to)
	}
}
