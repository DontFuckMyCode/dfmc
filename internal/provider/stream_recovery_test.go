package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// flakyStreamProvider opens a stream that immediately emits a transient
// StreamError before any content. This simulates a TLS reset / proxy
// timeout / brief upstream 5xx — the case the recovery wrapper is built
// to mask.
type flakyStreamProvider struct {
	name string
	err  error
}

func (p *flakyStreamProvider) Name() string                { return p.name }
func (p *flakyStreamProvider) Model() string               { return p.name + "-model" }
func (p *flakyStreamProvider) Models() []string            { return []string{p.name + "-model"} }
func (p *flakyStreamProvider) CountTokens(text string) int { return len(text) / 4 }
func (p *flakyStreamProvider) MaxContext() int             { return 100_000 }
func (p *flakyStreamProvider) Hints() ProviderHints        { return ProviderHints{SupportsTools: true} }
func (p *flakyStreamProvider) Complete(_ context.Context, _ CompletionRequest) (*CompletionResponse, error) {
	return nil, p.err
}
func (p *flakyStreamProvider) Stream(_ context.Context, _ CompletionRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 1)
	ch <- StreamEvent{Type: StreamError, Err: p.err}
	close(ch)
	return ch, nil
}

// healthyStreamProvider streams a single delta + done. Used as the
// fallback target so the recovery path has a reachable downstream.
type healthyStreamProvider struct {
	name string
	text string
}

func (p *healthyStreamProvider) Name() string                { return p.name }
func (p *healthyStreamProvider) Model() string               { return p.name + "-model" }
func (p *healthyStreamProvider) Models() []string            { return []string{p.name + "-model"} }
func (p *healthyStreamProvider) CountTokens(text string) int { return len(text) / 4 }
func (p *healthyStreamProvider) MaxContext() int             { return 100_000 }
func (p *healthyStreamProvider) Hints() ProviderHints        { return ProviderHints{SupportsTools: true} }
func (p *healthyStreamProvider) Complete(_ context.Context, _ CompletionRequest) (*CompletionResponse, error) {
	return &CompletionResponse{Text: p.text, Model: p.Model()}, nil
}
func (p *healthyStreamProvider) Stream(_ context.Context, _ CompletionRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 3)
	ch <- StreamEvent{Type: StreamStart, Provider: p.name, Model: p.Model()}
	ch <- StreamEvent{Type: StreamDelta, Delta: p.text}
	ch <- StreamEvent{Type: StreamDone, Provider: p.name, Model: p.Model()}
	close(ch)
	return ch, nil
}

// TestStreamRecoveryFromTransientPreContentError asserts that when the
// primary opens a stream then immediately fails with a transient error
// before delivering any content, the wrapper transparently retries the
// next provider and the caller sees the recovered content as if nothing
// went wrong.
func TestStreamRecoveryFromTransientPreContentError(t *testing.T) {
	primary := &flakyStreamProvider{
		name: "primary",
		err:  errors.New("upstream returned status 503"),
	}
	fallback := &healthyStreamProvider{
		name: "fallback",
		text: "recovered answer",
	}
	r := newRouterWith(primary, fallback)
	r.primary = "primary"
	r.fallback = []string{"fallback"}

	stream, _, err := r.Stream(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream open: %v", err)
	}

	var got strings.Builder
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				goto done
			}
			if ev.Type == StreamDelta {
				got.WriteString(ev.Delta)
			}
			if ev.Type == StreamDone {
				goto done
			}
			if ev.Type == StreamError {
				t.Fatalf("expected silent recovery, but got StreamError: %v", ev.Err)
			}
		case <-timeout:
			t.Fatal("timed out waiting for stream events")
		}
	}
done:
	if got.String() != "recovered answer" {
		t.Errorf("expected recovered answer, got %q", got.String())
	}
}

// TestStreamNoRecoveryWhenContentDelivered asserts that once we have
// forwarded any content to the caller, a subsequent stream error
// surfaces directly. We don't try to splice partials across providers
// because the resulting duplication / broken tool_use IDs are worse
// than letting the caller decide what to do.
func TestStreamNoRecoveryWhenContentDelivered(t *testing.T) {
	mid := &midStreamFailProvider{
		name:       "primary",
		preContent: "partial answer",
		failAfter:  true,
		failureErr: errors.New("upstream returned status 503 after partial"),
	}
	fallback := &healthyStreamProvider{name: "fallback", text: "should-not-see-this"}
	r := newRouterWith(mid, fallback)
	r.primary = "primary"
	r.fallback = []string{"fallback"}

	stream, _, err := r.Stream(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream open: %v", err)
	}

	var got strings.Builder
	var sawError bool
	timeout := time.After(2 * time.Second)
loop:
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				break loop
			}
			if ev.Type == StreamDelta {
				got.WriteString(ev.Delta)
			}
			if ev.Type == StreamError {
				sawError = true
				break loop
			}
		case <-timeout:
			t.Fatal("timed out waiting for stream events")
		}
	}

	if got.String() != "partial answer" {
		t.Errorf("expected partial only, got %q", got.String())
	}
	if !sawError {
		t.Error("expected StreamError to surface to caller after content was delivered")
	}
	if strings.Contains(got.String(), "should-not-see") {
		t.Error("recovery must not splice fallback content after partial was delivered")
	}
}

// TestStreamRecoveredObserver_FiresOnSuccessfulSwap pins the telemetry
// hook: the observer must fire exactly once with the From/To pair when
// recovery succeeded, and NOT fire for failed-recovery or
// content-already-delivered paths (those would mislead operators about
// the recovery layer's effectiveness).
func TestStreamRecoveredObserver_FiresOnSuccessfulSwap(t *testing.T) {
	primary := &flakyStreamProvider{
		name: "primary",
		err:  errors.New("upstream returned status 503"),
	}
	fallback := &healthyStreamProvider{name: "fallback", text: "ok"}
	r := newRouterWith(primary, fallback)
	r.primary = "primary"
	r.fallback = []string{"fallback"}

	var (
		gotFrom string
		gotTo   string
		fires   int
	)
	r.SetStreamRecoveredObserver(func(ev StreamRecoveredEvent) {
		gotFrom = ev.From
		gotTo = ev.To
		fires++
	})

	stream, _, err := r.Stream(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream open: %v", err)
	}
	// Drain the recovered stream.
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				goto done
			}
			if ev.Type == StreamDone {
				goto done
			}
		case <-timeout:
			t.Fatal("stream timed out")
		}
	}
done:
	if fires != 1 {
		t.Fatalf("observer should fire exactly once, got %d", fires)
	}
	if gotFrom != "primary" || gotTo != "fallback" {
		t.Errorf("expected From=primary To=fallback, got From=%q To=%q", gotFrom, gotTo)
	}
}

// TestStreamRecoveredObserver_SkipsWhenContentAlreadyDelivered asserts
// that the observer does NOT fire when the wrapper refuses to recover
// (because partial content was already streamed). A misfire here would
// tell operators "recovery worked" when in fact the user saw an error.
func TestStreamRecoveredObserver_SkipsWhenContentAlreadyDelivered(t *testing.T) {
	mid := &midStreamFailProvider{
		name:       "primary",
		preContent: "partial",
		failAfter:  true,
		failureErr: errors.New("status 503 mid-stream"),
	}
	fallback := &healthyStreamProvider{name: "fallback", text: "unused"}
	r := newRouterWith(mid, fallback)
	r.primary = "primary"
	r.fallback = []string{"fallback"}

	var fires int
	r.SetStreamRecoveredObserver(func(ev StreamRecoveredEvent) {
		fires++
	})

	stream, _, err := r.Stream(context.Background(), CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Stream open: %v", err)
	}
	// Drain until the channel closes.
	for ev := range stream {
		_ = ev
	}
	if fires != 0 {
		t.Fatalf("observer must not fire when recovery is refused, got %d", fires)
	}
}

// midStreamFailProvider streams some content then fails — used to pin
// the "no recovery after content" rule.
type midStreamFailProvider struct {
	name       string
	preContent string
	failAfter  bool
	failureErr error
}

func (p *midStreamFailProvider) Name() string                { return p.name }
func (p *midStreamFailProvider) Model() string               { return p.name + "-model" }
func (p *midStreamFailProvider) Models() []string            { return []string{p.name + "-model"} }
func (p *midStreamFailProvider) CountTokens(text string) int { return len(text) / 4 }
func (p *midStreamFailProvider) MaxContext() int             { return 100_000 }
func (p *midStreamFailProvider) Hints() ProviderHints        { return ProviderHints{SupportsTools: true} }
func (p *midStreamFailProvider) Complete(_ context.Context, _ CompletionRequest) (*CompletionResponse, error) {
	return nil, errors.New("not used in this test")
}
func (p *midStreamFailProvider) Stream(_ context.Context, _ CompletionRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 3)
	ch <- StreamEvent{Type: StreamStart, Provider: p.name, Model: p.Model()}
	if p.preContent != "" {
		ch <- StreamEvent{Type: StreamDelta, Delta: p.preContent}
	}
	if p.failAfter {
		ch <- StreamEvent{Type: StreamError, Err: p.failureErr}
	} else {
		ch <- StreamEvent{Type: StreamDone, Provider: p.name, Model: p.Model()}
	}
	close(ch)
	return ch, nil
}
