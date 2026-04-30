package provider

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

// multiModelStub implements Provider with a configurable model chain so
// completeWithProviderRetry's per-model fallback logic can be exercised
// independently of the cross-provider cascade. perModelErr keys on
// req.Model so each model in the chain can return a different error.
type multiModelStub struct {
	name        string
	models      []string
	perModelErr map[string]error
	calls       int32
	supports    bool
}

func (m *multiModelStub) Name() string     { return m.name }
func (m *multiModelStub) Model() string    { return m.models[0] }
func (m *multiModelStub) Models() []string { return append([]string(nil), m.models...) }
func (m *multiModelStub) Complete(_ context.Context, req CompletionRequest) (*CompletionResponse, error) {
	atomic.AddInt32(&m.calls, 1)
	if err, ok := m.perModelErr[req.Model]; ok && err != nil {
		return nil, err
	}
	return &CompletionResponse{Text: "ok from " + req.Model, Model: req.Model}, nil
}
func (m *multiModelStub) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	atomic.AddInt32(&m.calls, 1)
	ch := make(chan StreamEvent, 1)
	if err, ok := m.perModelErr[req.Model]; ok && err != nil {
		ch <- StreamEvent{Type: StreamError, Err: err}
		close(ch)
		return ch, err
	}
	ch <- StreamEvent{Type: StreamDone, Model: req.Model}
	close(ch)
	return ch, nil
}
func (m *multiModelStub) CountTokens(text string) int { return len(text) / 4 }
func (m *multiModelStub) MaxContext() int             { return 100_000 }
func (m *multiModelStub) Hints() ProviderHints        { return ProviderHints{SupportsTools: m.supports} }

// TestIsTransient_KnownPatterns pins the classifier behaviour against
// representative error strings produced by the providers and the network
// stack. Drift here is exactly what would silently break the model
// fallback chain.
func TestIsTransient_KnownPatterns(t *testing.T) {
	transient := []error{
		errors.New("anthropic error status 503: server overloaded"),
		errors.New("openai error status 502: bad gateway"),
		errors.New("google error status 504: timeout"),
		errors.New("dial tcp 1.2.3.4:443: connection refused"),
		errors.New("read tcp: connection reset by peer"),
		errors.New("dial tcp: lookup api.example.com: no such host"),
		errors.New("Get \"...\": net/http: TLS handshake timeout"),
		errors.New("read: i/o timeout"),
		errors.New("unexpected EOF"),
		ErrProviderUnavailable,
	}
	for _, e := range transient {
		if !isTransient(e) {
			t.Errorf("expected transient: %v", e)
		}
	}

	deterministic := []error{
		errors.New("anthropic error status 401: invalid api key"),
		errors.New("anthropic error status 400: bad request"),
		errors.New("openai error status 404: model not found"),
		errors.New("validation error: messages must not be empty"),
		context.Canceled,
		context.DeadlineExceeded,
		nil,
	}
	for _, e := range deterministic {
		if isTransient(e) {
			t.Errorf("expected NOT transient: %v", e)
		}
	}
}

// TestModelChain_TransientErrorContinues verifies that when the primary
// model returns a 503 the router moves to the next model in the chain
// instead of breaking out — that's what FallbackModels exists for.
func TestModelChain_TransientErrorContinues(t *testing.T) {
	stub := &multiModelStub{
		name:   "primary",
		models: []string{"smart", "cheap"},
		perModelErr: map[string]error{
			"smart": errors.New("primary error status 503: overloaded"),
		},
		supports: true,
	}
	r := newRouterWith(stub)
	r.primary = "primary"

	resp, used, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatalf("expected fallback to cheap to succeed, got: %v", err)
	}
	if used != "primary" {
		t.Fatalf("expected provider name 'primary', got %q", used)
	}
	if resp.Model != "cheap" {
		t.Fatalf("expected cheap model to answer, got %q", resp.Model)
	}
	if got := atomic.LoadInt32(&stub.calls); got != 2 {
		t.Fatalf("expected 2 model attempts (smart fail + cheap success), got %d", got)
	}
}

// TestModelChain_AuthErrorBreaks verifies the inverse: a 401 stops the
// chain immediately so a misconfigured key doesn't burn through every
// fallback model in the profile.
func TestModelChain_AuthErrorBreaks(t *testing.T) {
	stub := &multiModelStub{
		name:   "primary",
		models: []string{"smart", "cheap"},
		perModelErr: map[string]error{
			"smart": errors.New("primary error status 401: invalid api key"),
			"cheap": errors.New("primary error status 401: invalid api key"),
		},
		supports: true,
	}
	r := newRouterWith(stub)
	r.primary = "primary"

	_, _, err := r.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: "user", Content: "ping"}},
	})
	if err == nil {
		t.Fatal("expected auth error to surface")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected error to mention 401, got: %v", err)
	}
	// Only one model attempted: 401 on smart breaks before cheap is tried.
	// (The provider is then dropped from the cascade and "offline" is
	// tried instead, but that adds 0 calls to this stub.)
	if got := atomic.LoadInt32(&stub.calls); got != 1 {
		t.Fatalf("expected 1 model attempt (auth break), got %d", got)
	}
}
