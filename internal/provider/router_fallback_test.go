package provider

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// stubProvider is a minimal Provider implementation used to verify cascade order.
type stubProvider struct {
	name          string
	text          string
	err           error
	supportsTools bool
	calls         int32
}

func (p *stubProvider) Name() string                            { return p.name }
func (p *stubProvider) Model() string                            { return p.name + "-model" }
func (p *stubProvider) Models() []string                        { return []string{p.name + "-model"} }
func (p *stubProvider) Complete(_ context.Context, _ CompletionRequest) (*CompletionResponse, error) {
	atomic.AddInt32(&p.calls, 1)
	if p.err != nil {
		return nil, p.err
	}
	return &CompletionResponse{Text: p.text, Model: p.Model()}, nil
}
func (p *stubProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	atomic.AddInt32(&p.calls, 1)
	ch := make(chan StreamEvent, 1)
	resp, err := p.Complete(ctx, req)
	if err != nil {
		ch <- StreamEvent{Type: StreamError, Err: err}
		close(ch)
		return ch, nil
	}
	ch <- StreamEvent{Type: StreamDone, Model: resp.Model}
	close(ch)
	return ch, nil
}
func (p *stubProvider) CountTokens(text string) int { return len(text) / 4 }
func (p *stubProvider) MaxContext() int             { return 100_000 }
func (p *stubProvider) Hints() ProviderHints {
	return ProviderHints{SupportsTools: p.supportsTools}
}

// TestRouterSelectProvider_PrimaryFailsFallbackSucceeds verifies that when the
// primary provider errors, the router transparently falls through to the
// configured fallback and returns its answer.
func TestRouterSelectProvider_PrimaryFailsFallbackSucceeds(t *testing.T) {
	primary := &stubProvider{name: "primary", err: errors.New("primary unavailable"), supportsTools: true}
	fallback := &stubProvider{name: "fallback", text: "fallback answer", supportsTools: true}
	r := newRouterWith(primary, fallback)
	r.primary = "primary"
	r.fallback = []string{"fallback"}

	resp, used, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("expected fallback to succeed after primary error, got err: %v", err)
	}
	if used != "fallback" {
		t.Errorf("used=%q, want fallback", used)
	}
	if resp.Text != "fallback answer" {
		t.Errorf("resp.Text=%q, want \"fallback answer\"", resp.Text)
	}
	if n := atomic.LoadInt32(&primary.calls); n != 1 {
		t.Errorf("primary calls=%d, want 1", n)
	}
	if n := atomic.LoadInt32(&fallback.calls); n != 1 {
		t.Errorf("fallback calls=%d, want 1", n)
	}
}

// TestRouterSelectProvider_PrimaryFailsOfflineSucceeds verifies that when both
// primary and configured fallback fail, the router falls through to the
// always-available offline stub.
func TestRouterSelectProvider_PrimaryFailsOfflineSucceeds(t *testing.T) {
	primary := &stubProvider{name: "primary", err: errors.New("primary boom"), supportsTools: true}
	cfgFallback := &stubProvider{name: "cfg-fallback", err: errors.New("fallback also boom"), supportsTools: true}
	r := newRouterWith(primary, cfgFallback)
	r.Register(NewOfflineProvider()) // offline is always available
	r.primary = "primary"
	r.fallback = []string{"cfg-fallback"}

	resp, used, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("expected offline fallback after primary+cfg-fallback fail, got err: %v", err)
	}
	if used != "offline" {
		t.Errorf("used=%q, want offline", used)
	}
	if resp == nil || resp.Text == "" {
		t.Fatal("expected non-empty response from offline")
	}
}

// TestRouterSelectProvider_CascadeExhaustedError verifies that when every
// provider in the cascade errors, the joined error surfaces all failures.
func TestRouterSelectProvider_CascadeExhaustedError(t *testing.T) {
	primary := &stubProvider{name: "primary", err: errors.New("primary error"), supportsTools: true}
	fallback := &stubProvider{name: "fallback", err: errors.New("fallback error"), supportsTools: true}
	r := newRouterWith(primary, fallback)
	r.primary = "primary"
	r.fallback = []string{"fallback"}

	_, _, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected joined error after cascade exhaustion, got nil")
	}
	msg := err.Error()
	for _, needle := range []string{"primary error", "fallback error", "offline"} {
		if !strings.Contains(msg, needle) {
			t.Errorf("joined error missing %q; got: %s", needle, msg)
		}
	}
}

// TestRegisterProvider_NilProfileFromConfig tests providerFromProfile with
// a zero-value ModelConfig (no api key, no base URL) — should produce a
// PlaceholderProvider.
func TestRegisterProvider_NilProfileFromConfig(t *testing.T) {
	// providerFromProfile with an empty config should return a PlaceholderProvider
	// because no live client can be constructed without credentials.
	p := providerFromProfile("someprovider", config.ModelConfig{})
	if pp, ok := p.(*PlaceholderProvider); !ok {
		t.Fatalf("providerFromProfile(emptyConfig) = %T, want *PlaceholderProvider", p)
	} else {
		if pp.Name() != "someprovider" {
			t.Errorf("placeholder name=%q, want someprovider", pp.Name())
		}
	}
}

// TestRegisterProvider_EmptyNameNormalization verifies that Register
// normalizes provider names before storing, so lookups work case-insensitively.
func TestRegisterProvider_EmptyNameNormalization(t *testing.T) {
	r := &Router{providers: map[string]Provider{}}
	r.Register(NewOfflineProvider())

	// Lookup with different casing should find the same provider.
	p, ok := r.Get("OFFLINE")
	if !ok {
		t.Error("offline lookup by uppercase failed")
	}
	if p.Name() != "offline" {
		t.Errorf("offline.Name() = %q, want offline", p.Name())
	}

	p, ok = r.Get("Offline")
	if !ok {
		t.Error("offline lookup by MixedCase failed")
	}

	// Whitespace-padded name should also work.
	p, ok = r.Get("  offline  ")
	if !ok {
		t.Error("offline lookup by whitespace-padded name failed")
	}
}

// TestRegisterProvider_EmptyNameSkippedByResolveOrder verifies that an empty
// string in the fallback list is skipped during ResolveOrder without causing
// an error or producing empty entries.
func TestRegisterProvider_EmptyNameSkippedByResolveOrder(t *testing.T) {
	r := newRouterWith(&stubProvider{name: "real", text: "ok", supportsTools: true})
	r.primary = "real"
	r.fallback = []string{"", "  ", "also-real"}
	r.Register(NewOfflineProvider())

	order := r.ResolveOrder("")
	// Empty and whitespace entries should not appear.
	for _, name := range order {
		if strings.TrimSpace(name) == "" {
			t.Fatalf("ResolveOrder produced empty entry: %v", order)
		}
	}
}

// TestThrottleFastRequests triggers throttle behavior by making multiple
// rapid requests to a provider that returns 429 immediately. The router's
// throttle observer should receive notices for each throttled attempt.
func TestThrottleFastRequests(t *testing.T) {
	p := &flakyThrottleProvider{name: "fast-throttle", succeedOn: 10, retryAfter: 1 * time.Millisecond}
	cfg := config.DefaultConfig()
	r, err := NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	r.providers[p.name] = p

	var notices []ThrottleNotice
	r.SetThrottleObserver(func(n ThrottleNotice) {
		notices = append(notices, n)
	})

	// Make 5 rapid requests; each should eventually succeed after retries.
	for i := 0; i < 5; i++ {
		_, _, err := r.Complete(context.Background(), CompletionRequest{Provider: "fast-throttle"})
		if err != nil {
			t.Fatalf("request %d: expected eventual success, got err: %v", i, err)
		}
	}

	if len(notices) == 0 {
		t.Fatal("expected at least one throttle notice, got none")
	}
}

// TestPlaceholderProvider_WhenAllProvidersArePlaceholder verifies the cascade
// behaves correctly when only placeholder providers are available (no real API
// keys configured). The router should still try each one and surface the
// ErrProviderUnavailable from the first placeholder.
func TestPlaceholderProvider_WhenAllProvidersArePlaceholder(t *testing.T) {
	// All registered providers are placeholders with configured=false.
	primary := NewPlaceholderProvider("anthropic", "claude-sonnet-4-6", false, 100000)
	fallback := NewPlaceholderProvider("openai", "gpt-5.4", false, 100000)
	r := newRouterWith(primary, fallback)
	r.primary = "anthropic"
	r.fallback = []string{"openai"}

	_, _, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hello"}},
	})
	// Should surface an ErrProviderUnavailable from the first placeholder tried.
	if err == nil {
		t.Fatal("expected error when all providers are placeholders, got nil")
	}
	if !errors.Is(err, ErrProviderUnavailable) {
		t.Fatalf("expected ErrProviderUnavailable, got: %v", err)
	}
}

// TestOfflineProviderFallbackChain verifies the full fallback chain:
// requested → primary → fallback[] → offline. When primary and all fallbacks
// fail but offline succeeds, the answer comes from offline.
func TestOfflineProviderFallbackChain(t *testing.T) {
	primary := &stubProvider{name: "primary", err: errors.New("primary fail"), supportsTools: true}
	fb1 := &stubProvider{name: "fb1", err: errors.New("fb1 fail"), supportsTools: true}
	fb2 := &stubProvider{name: "fb2", err: errors.New("fb2 fail"), supportsTools: true}
	r := newRouterWith(primary, fb1, fb2)
	r.Register(NewOfflineProvider()) // offline is always available
	r.primary = "primary"
	r.fallback = []string{"fb1", "fb2"}

	resp, used, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("expected offline answer after cascade failure, got err: %v", err)
	}
	if used != "offline" {
		t.Errorf("used=%q, want offline", used)
	}
	if resp == nil || resp.Text == "" {
		t.Fatal("offline response was empty")
	}
}

// TestRouterComplete_ContextDeadlineExceeded short-circuits mid-cascade when
// the context deadline fires — no further providers should be attempted.
func TestRouterComplete_ContextDeadlineExceeded(t *testing.T) {
	primary := &stubProvider{name: "primary", err: errors.New("primary fail"), supportsTools: true}
	fb1 := &stubProvider{name: "fb1", text: "fb1 answer", supportsTools: true}
	r := newRouterWith(primary, fb1)
	r.primary = "primary"
	r.fallback = []string{"fb1"}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Give the context a moment to expire before we call.
	time.Sleep(5 * time.Millisecond)

	_, _, err := r.Complete(ctx, CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hello"}},
	})
	// Should return context deadline exceeded, not a cascade error.
	if err == nil {
		t.Fatal("expected context error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected ctx error, got: %v", err)
	}
}

// TestRouterResolveOrder_Deduplication ensures each provider name appears at
// most once in the resolve order even when it appears in multiple slots.
func TestRouterResolveOrder_Deduplication(t *testing.T) {
	r := newRouterWith(
		&stubProvider{name: "primary", text: "p", supportsTools: true},
		&stubProvider{name: "fallback1", text: "f1", supportsTools: true},
		&stubProvider{name: "fallback2", text: "f2", supportsTools: true},
	)
	r.primary = "primary"
	r.fallback = []string{"fallback1", "fallback2"}

	order := r.ResolveOrder("primary")
	seen := map[string]int{}
	for _, name := range order {
		seen[name]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("provider %q appears %d times in ResolveOrder: %v", name, count, order)
		}
	}
	// offline must always be last.
	if len(order) > 0 && order[len(order)-1] != "offline" {
		t.Errorf("offline should be last in order, got: %v", order)
	}
}

// TestProviderFromProfile_GenericNoBaseURL returns PlaceholderProvider.
func TestProviderFromProfile_GenericNoBaseURL(t *testing.T) {
	p := providerFromProfile("generic", config.ModelConfig{
		Model:      "qwen",
		APIKey:     "",
		BaseURL:    "",
		MaxContext: 128000,
	})
	if _, ok := p.(*PlaceholderProvider); !ok {
		t.Fatalf("generic with no api key and no baseURL should be PlaceholderProvider, got %T", p)
	}
}

// TestProviderFromProfile_DeepseekMapping verifies that deepseek maps to
// openai-compatible protocol.
func TestProviderFromProfile_DeepseekMapping(t *testing.T) {
	p := providerFromProfile("deepseek", config.ModelConfig{
		Model:      "deepseek-v3",
		APIKey:     "test-key",
		MaxContext: 64000,
	})
	if _, ok := p.(*OpenAICompatibleProvider); !ok {
		t.Fatalf("deepseek should produce OpenAICompatibleProvider, got %T", p)
	}
}

// TestProviderFromProfile_GoogleMapping verifies that google/gemini map to
// google protocol and produce a GoogleProvider.
func TestProviderFromProfile_GoogleMapping(t *testing.T) {
	p := providerFromProfile("gemini", config.ModelConfig{
		Model:      "gemini-2.5-pro",
		APIKey:     "test-key",
		MaxContext: 1000000,
	})
	if _, ok := p.(*GoogleProvider); !ok {
		t.Fatalf("gemini should produce GoogleProvider, got %T", p)
	}
}

// TestProviderFromProfile_ZAIAnthropicRemap verifies the Z.AI anthropic
// endpoint remapping to the stable OpenAI-compatible paas/v4 endpoint.
func TestProviderFromProfile_ZAIAnthropicRemap(t *testing.T) {
	p := providerFromProfile("zai", config.ModelConfig{
		Model:      "glm-5.1",
		APIKey:     "test-key",
		BaseURL:    "https://api.z.ai/api/anthropic",
		MaxContext: 200000,
		Protocol:   "anthropic",
	})
	cp, ok := p.(*OpenAICompatibleProvider)
	if !ok {
		t.Fatalf("zai with anthropic-style URL should produce OpenAICompatibleProvider, got %T", p)
	}
	if cp.baseURL != "https://api.z.ai/api/paas/v4" {
		t.Errorf("zai anthropic-style baseURL should be remapped to paas/v4, got %q", cp.baseURL)
	}
}
