package provider

// router.go — provider lookup, primary/fallback ordering, observer
// wiring, top-level Complete and Stream entry points. The retry
// machinery splits across siblings:
//
//   retry_throttle.go  — same-provider Retry-After / backoff loop
//                        shared by Complete and Stream paths.
//   retry_chain.go     — per-provider model-chain walk + transient-
//                        error fallthrough; calls the throttle layer
//                        for each model attempt.
//   retry_context.go   — context-overflow detection + message
//                        compaction; called from the chain layer on
//                        ErrContextOverflow before moving providers.
//   race.go            — CompleteRaced and its target resolver.
//   stream_recovery.go — mid-stream provider swap on transient drops.
//
// Adding a new retry concern:
//
//   1. Add a new sibling file rather than expanding any existing one.
//   2. The chain and throttle helpers are generic on the return type
//      (T any) so a wrapper can compose them without copying logic.
//   3. Don't touch ResolveOrder unless you're changing the lookup
//      cascade — it's the single source of truth for provider order.

import (
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

type Router struct {
	mu               sync.RWMutex
	primary          string
	fallback         []string
	providers        map[string]Provider
	throttleObserver func(ThrottleNotice)

	// Circuit breaker state. Separate mutex so a long-running call against
	// one provider doesn't block health checks/updates for another.
	healthMu        sync.Mutex
	health          map[string]*providerHealth
	circuitObserver func(CircuitEvent)

	// streamRecoveredObserver fires when streamForwardWithRecovery
	// successfully resumed a stream on a fallback provider. Optional;
	// nil = no telemetry. Engine wires this to its EventBus so the
	// TUI/web/CLI can render a "↻ stream resumed on <fallback>" chip
	// instead of letting the recovery be invisible.
	streamRecoveredObserver func(StreamRecoveredEvent)

	// fallbackObserver fires when Complete / Stream walks past a failing
	// provider in the resolved order to the next one. Optional; nil = no
	// telemetry. Engine wires this to publish a provider:fallback event
	// so the TUI / web / CLI -v stream show "primary failed, retrying on
	// <next>" instead of the cascade being invisible.
	fallbackObserver func(FallbackEvent)
}

// FallbackEvent describes a single provider→provider transition during
// fallback cascade. From is the provider that just failed (with Err);
// To is the next provider the router will try. Attempt is 0-indexed in
// the resolved order — Attempt=0 means primary failed → first fallback.
type FallbackEvent struct {
	From    string
	To      string
	Err     error
	Attempt int
}

// StreamRecoveredEvent describes a successful mid-stream provider swap.
// From is the provider that errored; To is the provider that served
// the rest of the stream. Err is the original transient error that
// triggered the swap.
type StreamRecoveredEvent struct {
	From string
	To   string
	Err  error
}

type ThrottleNotice struct {
	Provider string
	Attempt  int
	Wait     time.Duration
	Stream   bool
	Err      error
}

func NewRouter(cfg config.ProvidersConfig) (*Router, error) {
	r := &Router{
		primary:   cfg.Primary,
		fallback:  append([]string(nil), cfg.Fallback...),
		providers: map[string]Provider{},
		health:    map[string]*providerHealth{},
	}

	// Always available fallback.
	r.Register(NewOfflineProvider())

	for name, profile := range cfg.Profiles {
		r.Register(providerFromProfile(name, profile))
	}

	if strings.TrimSpace(r.primary) == "" {
		r.primary = "offline"
	}
	return r, nil
}

// providerFromProfile + httpTimeout + normalizedProtocol live in
// router_profile.go.

func (r *Router) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[normalizeProviderName(p.Name())] = p
}

func (r *Router) Primary() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.primary
}

func (r *Router) SetPrimary(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.primary = normalizeProviderName(name)
}

func (r *Router) Fallback() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.fallback...)
}

func (r *Router) SetFallback(names []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, n := range names {
		n = normalizeProviderName(n)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		out = append(out, n)
	}
	r.fallback = out
}

func (r *Router) SetThrottleObserver(fn func(ThrottleNotice)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.throttleObserver = fn
}

func (r *Router) SetFallbackObserver(fn func(FallbackEvent)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fallbackObserver = fn
}

func (r *Router) emitFallback(from, to string, err error, attempt int) {
	r.mu.RLock()
	fn := r.fallbackObserver
	r.mu.RUnlock()
	if fn == nil {
		return
	}
	fn(FallbackEvent{From: from, To: to, Err: err, Attempt: attempt})
}

func (r *Router) SetStreamRecoveredObserver(fn func(StreamRecoveredEvent)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.streamRecoveredObserver = fn
}

func (r *Router) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[normalizeProviderName(name)]
	return p, ok
}

func (r *Router) emitThrottleNotice(n ThrottleNotice) {
	r.mu.RLock()
	fn := r.throttleObserver
	r.mu.RUnlock()
	if fn != nil {
		fn(n)
	}
}

func (r *Router) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.providers))
	for name := range r.providers {
		out = append(out, name)
	}
	return out
}

// ResolveOrder returns the provider lookup order for a request targeting
// `requested`. The order is: requested (if any) → primary → fallback[]
// → "offline". Deduplication is applied so each name appears at most once.
// "offline" is always last because it always has an answer and racing it
// would waste tokens.
//
// The returned slice is the order Complete/Stream iterate when handling
// a request. On ErrContextOverflow the SAME provider is retried once after
// compacting history before moving to the next provider — compacting and
// moving to a different provider wouldn't help because the new provider
// still sees the same conversation.
func (r *Router) ResolveOrder(requested string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	add := func(name string) {
		name = normalizeProviderName(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}

	add(requested)
	add(r.primary)
	for _, fb := range r.fallback {
		add(fb)
	}
	add("offline")
	return out
}

func normalizeProviderName(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}
