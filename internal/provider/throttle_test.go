package provider

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestIsThrottleStatus(t *testing.T) {
	if !isThrottleStatus(http.StatusTooManyRequests) {
		t.Fatal("429 must be throttle")
	}
	if !isThrottleStatus(http.StatusServiceUnavailable) {
		t.Fatal("503 must be throttle")
	}
	for _, code := range []int{400, 401, 403, 404, 500, 502, 504} {
		if isThrottleStatus(code) {
			t.Fatalf("%d must NOT be treated as throttle", code)
		}
	}
}

func TestParseRetryAfter_Seconds(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "42")
	d, ok := parseRetryAfter(h)
	if !ok || d != 42*time.Second {
		t.Fatalf("expected 42s, got (%s, %v)", d, ok)
	}
}

func TestParseRetryAfter_ClampsHuge(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "99999")
	d, ok := parseRetryAfter(h)
	if !ok {
		t.Fatal("expected parse ok")
	}
	if d > 5*time.Minute {
		t.Fatalf("Retry-After must be clamped to 5m, got %s", d)
	}
}

func TestParseRetryAfter_Missing(t *testing.T) {
	if _, ok := parseRetryAfter(http.Header{}); ok {
		t.Fatal("missing Retry-After should report ok=false")
	}
}

func TestBackoffForAttempt_ExponentialCapped(t *testing.T) {
	// 0→1s, 1→2s, 2→4s, 3→8s; beyond that caps at 30s.
	cases := map[int]time.Duration{
		0:  1 * time.Second,
		1:  2 * time.Second,
		2:  4 * time.Second,
		3:  8 * time.Second,
		4:  16 * time.Second,
		5:  30 * time.Second,
		10: 30 * time.Second,
	}
	for attempt, want := range cases {
		if got := backoffForAttempt(attempt); got != want {
			t.Errorf("attempt=%d: got %s, want %s", attempt, got, want)
		}
	}
}

func TestThrottledError_ImplementsErrProviderThrottled(t *testing.T) {
	te := &ThrottledError{Provider: "anthropic", StatusCode: 429, RetryAfter: 2 * time.Second, Detail: "rate limited"}
	if !errors.Is(te, ErrProviderThrottled) {
		t.Fatal("ThrottledError must errors.Is(ErrProviderThrottled)")
	}
	var extracted *ThrottledError
	if !errors.As(te, &extracted) || extracted.Provider != "anthropic" {
		t.Fatal("errors.As must unwrap back to the typed ThrottledError")
	}
}

// flakyThrottleProvider succeeds on the Nth call and throttles before.
// Counter is atomic so concurrent streams / races don't scramble it.
type flakyThrottleProvider struct {
	name       string
	calls      int32
	succeedOn  int32
	retryAfter time.Duration
}

func (p *flakyThrottleProvider) Name() string  { return p.name }
func (p *flakyThrottleProvider) Model() string { return "test-model" }
func (p *flakyThrottleProvider) Hints() ProviderHints {
	return ProviderHints{SupportsTools: true}
}
func (p *flakyThrottleProvider) CountTokens(text string) int { return len(text) / 4 }
func (p *flakyThrottleProvider) MaxContext() int             { return 100_000 }
func (p *flakyThrottleProvider) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	n := atomic.AddInt32(&p.calls, 1)
	if n < p.succeedOn {
		return nil, &ThrottledError{
			Provider:   p.name,
			StatusCode: 429,
			RetryAfter: p.retryAfter,
			Detail:     p.name + " throttled",
		}
	}
	return &CompletionResponse{Text: "ok after " + p.name}, nil
}
func (p *flakyThrottleProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	n := atomic.AddInt32(&p.calls, 1)
	if n < p.succeedOn {
		return nil, &ThrottledError{
			Provider:   p.name,
			StatusCode: 429,
			RetryAfter: p.retryAfter,
			Detail:     p.name + " throttled",
		}
	}
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Type: StreamDelta, Delta: "ok"}
	close(ch)
	return ch, nil
}

func TestCompleteWithThrottleRetry_RecoversAfterBackoff(t *testing.T) {
	p := &flakyThrottleProvider{name: "flaky", succeedOn: 3, retryAfter: 10 * time.Millisecond}
	cfg := config.DefaultConfig()
	r, err := NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	r.providers[p.name] = p

	start := time.Now()
	resp, err := r.completeWithThrottleRetry(context.Background(), p, CompletionRequest{})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("expected eventual success, got: %v", err)
	}
	if resp.Text != "ok after flaky" {
		t.Fatalf("unexpected response: %q", resp.Text)
	}
	if atomic.LoadInt32(&p.calls) != 3 {
		t.Fatalf("expected 3 calls (2 throttles + 1 success), got %d", p.calls)
	}
	// Retry-After was 10ms per attempt × 2 failed attempts = at least
	// 20ms total wait. Allow generous ceiling for CI.
	if elapsed < 15*time.Millisecond {
		t.Fatalf("retry didn't honour Retry-After (elapsed %s)", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("retry slept unexpectedly long: %s", elapsed)
	}
}

func TestCompleteWithThrottleRetry_GivesUpAfterMax(t *testing.T) {
	// Never succeeds — after maxThrottleRetries+1 attempts, surfaces the
	// last throttle error for the fallback cascade to handle.
	p := &flakyThrottleProvider{name: "stuck", succeedOn: 100, retryAfter: time.Millisecond}
	cfg := config.DefaultConfig()
	r, err := NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	r.providers[p.name] = p

	_, err = r.completeWithThrottleRetry(context.Background(), p, CompletionRequest{})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if !errors.Is(err, ErrProviderThrottled) {
		t.Fatalf("error should wrap ErrProviderThrottled, got: %v", err)
	}
	if atomic.LoadInt32(&p.calls) != int32(maxThrottleRetries+1) {
		t.Fatalf("expected %d attempts, got %d", maxThrottleRetries+1, p.calls)
	}
}

func TestCompleteWithThrottleRetry_RespectsContext(t *testing.T) {
	p := &flakyThrottleProvider{name: "busy", succeedOn: 100, retryAfter: 5 * time.Second}
	cfg := config.DefaultConfig()
	r, err := NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	r.providers[p.name] = p

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err = r.completeWithThrottleRetry(ctx, p, CompletionRequest{})
	if err == nil {
		t.Fatal("expected context error")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected ctx error, got: %v", err)
	}
}

func TestCompleteWithThrottleRetry_NonThrottleErrorNoRetry(t *testing.T) {
	// Non-throttle errors bubble up immediately so the fallback
	// cascade can try the next provider without wasting time backing
	// off on a 401.
	p := &errorOnceProvider{name: "err", err: errors.New("401 unauthorized")}
	cfg := config.DefaultConfig()
	r, err := NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	r.providers[p.name] = p

	_, err = r.completeWithThrottleRetry(context.Background(), p, CompletionRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if p.calls != 1 {
		t.Fatalf("non-throttle error must not retry, got %d calls", p.calls)
	}
}

type errorOnceProvider struct {
	name  string
	err   error
	calls int
}

func (p *errorOnceProvider) Name() string  { return p.name }
func (p *errorOnceProvider) Model() string { return "test-model" }
func (p *errorOnceProvider) Hints() ProviderHints {
	return ProviderHints{SupportsTools: true}
}
func (p *errorOnceProvider) CountTokens(text string) int { return len(text) / 4 }
func (p *errorOnceProvider) MaxContext() int             { return 100_000 }
func (p *errorOnceProvider) Complete(_ context.Context, _ CompletionRequest) (*CompletionResponse, error) {
	p.calls++
	return nil, p.err
}
func (p *errorOnceProvider) Stream(_ context.Context, _ CompletionRequest) (<-chan StreamEvent, error) {
	p.calls++
	return nil, p.err
}
