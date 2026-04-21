package provider

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// fakeRaceProvider is a controllable Provider used to shape race outcomes.
// sleep simulates upstream latency; err simulates upstream failure. The
// provider's Name/Model/Hints satisfy the interface but aren't exercised.
type fakeRaceProvider struct {
	name  string
	text  string
	sleep time.Duration
	err   error
	// calls counts Complete invocations so tests can assert losers were
	// actually dispatched (not just short-circuited).
	calls int32
	// cancelled counts the number of Complete calls that exited via
	// context cancellation. Losers should bump this.
	cancelled int32
}

func (f *fakeRaceProvider) Name() string  { return f.name }
func (f *fakeRaceProvider) Model() string { return f.name + "-model" }
func (f *fakeRaceProvider) Models() []string { return []string{f.name + "-model"} }
func (f *fakeRaceProvider) Complete(ctx context.Context, _ CompletionRequest) (*CompletionResponse, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.sleep > 0 {
		select {
		case <-time.After(f.sleep):
		case <-ctx.Done():
			atomic.AddInt32(&f.cancelled, 1)
			return nil, ctx.Err()
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return &CompletionResponse{Text: f.text, Model: f.Model()}, nil
}
func (f *fakeRaceProvider) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, 1)
	resp, err := f.Complete(ctx, req)
	if err != nil {
		ch <- StreamEvent{Type: StreamError, Err: err}
		close(ch)
		return ch, nil
	}
	ch <- StreamEvent{Type: StreamDone, Model: resp.Model}
	close(ch)
	return ch, nil
}
func (f *fakeRaceProvider) CountTokens(text string) int { return len(text) / 4 }
func (f *fakeRaceProvider) MaxContext() int             { return 100_000 }
func (f *fakeRaceProvider) Hints() ProviderHints {
	return ProviderHints{MaxContext: 100_000, SupportsTools: false}
}

func newRouterWith(providers ...Provider) *Router {
	r := &Router{providers: map[string]Provider{}}
	for _, p := range providers {
		r.Register(p)
	}
	return r
}

func newRaceReq() CompletionRequest {
	return CompletionRequest{
		Messages: []Message{{Role: types.RoleUser, Content: "ping"}},
	}
}

// TestCompleteRacedFastestWins: the candidate with the shortest simulated
// latency returns first and wins the race. Losers must be cancelled, not left
// to finish their full sleep.
func TestCompleteRacedFastestWins(t *testing.T) {
	fast := &fakeRaceProvider{name: "fast", text: "hello from fast", sleep: 10 * time.Millisecond}
	slow := &fakeRaceProvider{name: "slow", text: "hello from slow", sleep: 500 * time.Millisecond}
	r := newRouterWith(fast, slow)

	start := time.Now()
	resp, name, err := r.CompleteRaced(context.Background(), newRaceReq(), []string{"fast", "slow"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if name != "fast" || resp.Text != "hello from fast" {
		t.Fatalf("expected fast win, got name=%s text=%q", name, resp.Text)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("elapsed %s suggests slow was awaited — race did not cancel losers", elapsed)
	}
	// Let the cancellation actually propagate before we assert.
	time.Sleep(40 * time.Millisecond)
	if atomic.LoadInt32(&slow.cancelled) == 0 {
		t.Fatalf("slow candidate should have been cancelled, got cancelled=%d", slow.cancelled)
	}
}

// TestCompleteRacedErrorThenSuccess: an erroring candidate must not kill the
// race — the next successful return wins even if it arrives later.
func TestCompleteRacedErrorThenSuccess(t *testing.T) {
	flaky := &fakeRaceProvider{name: "flaky", err: errors.New("transient 500"), sleep: 5 * time.Millisecond}
	steady := &fakeRaceProvider{name: "steady", text: "ok", sleep: 40 * time.Millisecond}
	r := newRouterWith(flaky, steady)

	resp, name, err := r.CompleteRaced(context.Background(), newRaceReq(), []string{"flaky", "steady"})
	if err != nil {
		t.Fatalf("expected success from steady, got err: %v", err)
	}
	if name != "steady" || resp.Text != "ok" {
		t.Fatalf("expected steady win, got name=%s text=%q", name, resp.Text)
	}
}

// TestCompleteRacedAllFail: every candidate erroring should surface a joined
// error that mentions each failing provider by name.
func TestCompleteRacedAllFail(t *testing.T) {
	a := &fakeRaceProvider{name: "a", err: errors.New("boom-a")}
	b := &fakeRaceProvider{name: "b", err: errors.New("boom-b")}
	r := newRouterWith(a, b)

	_, _, err := r.CompleteRaced(context.Background(), newRaceReq(), []string{"a", "b"})
	if err == nil {
		t.Fatalf("expected joined error, got nil")
	}
	msg := err.Error()
	for _, needle := range []string{"a:", "b:", "boom-a", "boom-b"} {
		if !strings.Contains(msg, needle) {
			t.Fatalf("joined error missing %q: %s", needle, msg)
		}
	}
}

// TestCompleteRacedSingleCandidateSkipsRace: a race of one should call
// Complete directly (no goroutine fan-out). The test doesn't assert on
// goroutines, just that a single-candidate call works and propagates errors.
func TestCompleteRacedSingleCandidateSkipsRace(t *testing.T) {
	solo := &fakeRaceProvider{name: "solo", text: "alone"}
	r := newRouterWith(solo)

	resp, name, err := r.CompleteRaced(context.Background(), newRaceReq(), []string{"solo"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if name != "solo" || resp.Text != "alone" {
		t.Fatalf("expected solo win, got name=%s text=%q", name, resp.Text)
	}
	if got := atomic.LoadInt32(&solo.calls); got != 1 {
		t.Fatalf("expected 1 call, got %d", got)
	}
}

// TestCompleteRacedUnknownCandidatesDropped: typos in the candidate list must
// not abort the race. The known candidates still race and one of them wins.
func TestCompleteRacedUnknownCandidatesDropped(t *testing.T) {
	good := &fakeRaceProvider{name: "good", text: "ok"}
	r := newRouterWith(good)

	_, name, err := r.CompleteRaced(context.Background(), newRaceReq(), []string{"ghost", "good", "phantom"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if name != "good" {
		t.Fatalf("expected good win, got %s", name)
	}
}

// TestCompleteRacedNoCandidatesAtAll: an empty candidate list plus an empty
// router produces a clear error rather than a hang.
func TestCompleteRacedNoCandidatesAtAll(t *testing.T) {
	r := newRouterWith() // empty router, no offline
	_, _, err := r.CompleteRaced(context.Background(), newRaceReq(), nil)
	if err == nil || !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("expected ErrProviderNotFound, got %v", err)
	}
}

// TestCompleteRacedContextCancellation: cancelling the parent context must
// return ctx.Err() instead of waiting for candidates to finish.
func TestCompleteRacedContextCancellation(t *testing.T) {
	a := &fakeRaceProvider{name: "a", text: "late", sleep: 500 * time.Millisecond}
	b := &fakeRaceProvider{name: "b", text: "late", sleep: 500 * time.Millisecond}
	r := newRouterWith(a, b)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, _, err := r.CompleteRaced(ctx, newRaceReq(), []string{"a", "b"})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected cancellation error, got nil")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("cancellation took too long: %s", elapsed)
	}
}

// TestCompleteRacedEmptyCandidatesStripsOffline: empty candidates should
// derive targets from ResolveOrder but skip the offline stub so the race is
// actually meaningful. When offline is the only thing configured, though, it
// stays in so the caller still gets an answer.
func TestCompleteRacedEmptyCandidatesStripsOffline(t *testing.T) {
	real1 := &fakeRaceProvider{name: "real1", text: "r1"}
	real2 := &fakeRaceProvider{name: "real2", text: "r2"}
	r := &Router{
		primary:   "real1",
		fallback:  []string{"real2"},
		providers: map[string]Provider{},
	}
	r.Register(NewOfflineProvider())
	r.Register(real1)
	r.Register(real2)

	targets := r.resolveRaceTargets("", nil)
	if len(targets) == 0 {
		t.Fatalf("expected non-empty targets")
	}
	for _, tgt := range targets {
		if tgt.name == "offline" {
			t.Fatalf("offline should have been stripped when real providers exist, got %v", targets)
		}
	}

	// But when ONLY offline is configured, it must survive the strip.
	only := &Router{primary: "offline", providers: map[string]Provider{}}
	only.Register(NewOfflineProvider())
	targets = only.resolveRaceTargets("", nil)
	if len(targets) != 1 || targets[0].name != "offline" {
		t.Fatalf("offline-only race should keep offline, got %v", targets)
	}
}
