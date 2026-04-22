package provider

import (
	"context"
	"errors"
	"fmt"
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

func providerFromProfile(name string, profile config.ModelConfig) Provider {
	name = normalizeProviderName(name)
	model := profile.Model
	apiKey := strings.TrimSpace(profile.APIKey)
	baseURL := strings.TrimSpace(profile.BaseURL)
	protocol := normalizedProtocol(name, profile.Protocol)
	if name == "zai" && (protocol == "anthropic" || strings.Contains(strings.ToLower(baseURL), "/api/anthropic")) {
		// Z.AI documents an Anthropic-compatible endpoint for Claude Code style
		// clients, but DFMC's runtime behaves more reliably against Z.AI's
		// OpenAI-compatible `/api/paas/v4` surface. Users often paste the
		// Claude-style base URL into DFMC and hit 404_NOT_FOUND; remap that
		// configuration onto the stable OpenAI-compatible endpoint so the
		// profile self-heals instead of failing at runtime.
		protocol = "openai-compatible"
		baseURL = defaultOpenAIBaseURL(name)
	}

	switch protocol {
	case "anthropic":
		if apiKey == "" {
			return NewPlaceholderProvider(name, model, false, profile.MaxContext)
		}
		return NewNamedAnthropicProvider(name, model, apiKey, baseURL, profile.MaxTokens, profile.MaxContext)
	case "google", "gemini":
		if apiKey == "" {
			return NewPlaceholderProvider(name, model, false, profile.MaxContext)
		}
		return NewGoogleProvider(model, apiKey, baseURL, profile.MaxTokens, profile.MaxContext)
	case "openai", "openai-compatible":
		if name == "generic" && strings.TrimSpace(baseURL) == "" {
			return NewPlaceholderProvider(name, model, false, profile.MaxContext)
		}
		if name != "generic" && apiKey == "" {
			return NewPlaceholderProvider(name, model, false, profile.MaxContext)
		}
		return NewOpenAICompatibleProvider(name, model, apiKey, baseURL, profile.MaxTokens, profile.MaxContext)
	default:
		configured := apiKey != "" || baseURL != ""
		return NewPlaceholderProvider(name, model, configured, profile.MaxContext)
	}
}

func normalizedProtocol(name, protocol string) string {
	p := strings.ToLower(strings.TrimSpace(protocol))
	if p != "" {
		return p
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "anthropic", "minimax":
		return "anthropic"
	case "openai":
		return "openai"
	case "google", "gemini":
		return "google"
	case "deepseek", "generic", "kimi", "zai", "alibaba":
		return "openai-compatible"
	default:
		return ""
	}
}

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

func (r *Router) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, string, error) {
	order := r.ResolveOrder(req.Provider)
	// When the caller asked for tool-calling, strip providers that can't
	// honour tools out of the fallback cascade. Without this filter, a
	// mid-agent-loop error on the primary silently falls through to
	// offline (Hints.SupportsTools=false), which returns a canned analyzer
	// response with zero tool_calls — the agent loop then treats that as
	// the final answer and the user sees an "offline" reply to what was a
	// live tool-using task. Skipped providers are still eligible when the
	// caller explicitly names one via req.Provider.
	if len(req.Tools) > 0 {
		order = r.filterToolCapable(order, req.Provider)
	}
	var errs []error

	for _, name := range order {
		// If the caller's context is already dead, there is no point
		// trying the next provider — each attempt would just immediately
		// return ctx.Err() and the real cancel/deadline reason would get
		// buried inside errors.Join below. Surface it directly so agent
		// loops and cancellable CLI commands return the exact sentinel
		// (context.Canceled / context.DeadlineExceeded) the caller
		// expects.
		if cerr := ctx.Err(); cerr != nil {
			if len(errs) == 0 {
				return nil, "", cerr
			}
			return nil, "", errors.Join(append(errs, cerr)...)
		}
		p, ok := r.Get(name)
		if !ok {
			errs = append(errs, fmt.Errorf("%w: %s", ErrProviderNotFound, name))
			continue
		}
		resp, usedModel, err := r.completeWithProviderRetry(ctx, p, req)
		if err == nil {
			return resp, usedModel, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
	}

	return nil, "", errors.Join(errs...)
}

// completeWithProviderRetry tries a provider's model chain (primary + FallbackModels).
// On ErrContextOverflow it first retries the SAME model after compacting messages,
// then falls through to the next model in the chain. Returns (resp, providerName, error).
func (r *Router) completeWithProviderRetry(ctx context.Context, p Provider, req CompletionRequest) (*CompletionResponse, string, error) {
	models := p.Models()
	if len(models) == 0 {
		resp, err := r.completeWithThrottleRetry(ctx, p, req)
		return resp, p.Name(), err
	}
	var errs []error
	for _, model := range models {
		reqWithModel := req
		reqWithModel.Model = model
		resp, err := r.completeWithThrottleRetry(ctx, p, reqWithModel)
		if err == nil {
			return resp, p.Name(), nil
		}
		if isContextOverflow(err) {
			// Compact and retry the SAME model before giving up on it.
			compacted, trimmed := compactMessagesForRetry(reqWithModel.Messages)
			if trimmed > 0 {
				retryReq := reqWithModel
				retryReq.Messages = compacted
				resp, err2 := r.completeWithThrottleRetry(ctx, p, retryReq)
				if err2 == nil {
					return resp, p.Name(), nil
				}
				errs = append(errs, fmt.Errorf("%s (model %s, context overflow, compacted %d msgs, retry failed): %w", p.Name(), model, trimmed, err2))
			} else {
				errs = append(errs, fmt.Errorf("%s (model %s): %w", p.Name(), model, err))
			}
			continue // try next model in chain
		}
		// Non-overflow error: give up on this provider immediately.
		errs = append(errs, fmt.Errorf("%s (model %s): %w", p.Name(), model, err))
		break
	}
	return nil, p.Name(), errors.Join(errs...)
}

// completeWithThrottleRetry is the same-provider wrapper that honors
// ThrottledError. On 429/503 we wait (Retry-After if the provider
// supplied one, otherwise exponential backoff from 1s) and retry up to
// maxThrottleRetries times before surfacing the error to the fallback
// cascade. Every other error path is a straight passthrough so existing
// caller behaviour is unchanged. Respects ctx.Done() during every
// wait — an agent-loop cancel aborts retries immediately.
func (r *Router) completeWithThrottleRetry(ctx context.Context, p Provider, req CompletionRequest) (*CompletionResponse, error) {
	var lastErr error
	for attempt := 0; attempt <= maxThrottleRetries; attempt++ {
		resp, err := p.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		if !errors.Is(err, ErrProviderThrottled) {
			return nil, err
		}
		lastErr = err
		if attempt == maxThrottleRetries {
			break
		}
		wait := throttleWait(err, attempt)
		r.emitThrottleNotice(ThrottleNotice{
			Provider: p.Name(),
			Attempt:  attempt + 1,
			Wait:     wait,
			Stream:   false,
			Err:      err,
		})
		if wait <= 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ctx.Err()
}

// throttleWait prefers the upstream Retry-After hint when present,
// falling back to exponential backoff for providers that didn't surface
// one. Attempts are 0-indexed so the first backoff is 1s.
func throttleWait(err error, attempt int) time.Duration {
	var te *ThrottledError
	if errors.As(err, &te) && te.RetryAfter > 0 {
		return te.RetryAfter
	}
	return backoffForAttempt(attempt)
}

const maxThrottleRetries = 3

// streamWithThrottleRetry mirrors completeWithThrottleRetry for the
// streaming path. Note that providers MAY return the throttle error
// synchronously from Stream() before the channel opens — that's the
// path we retry here. Errors that arrive as StreamEvent values over an
// already-open channel are delivered to the caller unchanged; retrying
// mid-stream is unsafe because partial output has already been seen.
func (r *Router) streamWithThrottleRetry(ctx context.Context, p Provider, req CompletionRequest) (<-chan StreamEvent, error) {
	var lastErr error
	for attempt := 0; attempt <= maxThrottleRetries; attempt++ {
		stream, err := p.Stream(ctx, req)
		if err == nil {
			return stream, nil
		}
		if !errors.Is(err, ErrProviderThrottled) {
			return nil, err
		}
		lastErr = err
		if attempt == maxThrottleRetries {
			break
		}
		wait := throttleWait(err, attempt)
		r.emitThrottleNotice(ThrottleNotice{
			Provider: p.Name(),
			Attempt:  attempt + 1,
			Wait:     wait,
			Stream:   true,
			Err:      err,
		})
		if wait <= 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ctx.Err()
}

// CompleteRaced issues req against every candidate concurrently and returns
// the first successful response. The losing calls are cancelled as soon as a
// winner is declared. If every candidate errors, the joined error is returned.
//
// When candidates is empty, the router derives them from ResolveOrder but
// strips "offline" — racing the offline stub is pointless because it always
// answers instantly with a canned analyzer response. If stripping leaves no
// candidates (e.g. only offline is configured), offline is kept so the call
// still returns something.
//
// Use this when latency or reliability matters more than cost — e.g. you have
// a fast-but-flaky primary and a slow-but-reliable fallback and want the best
// of both. It is NOT a drop-in replacement for Complete: it pays N× the token
// bill per call.
func (r *Router) CompleteRaced(ctx context.Context, req CompletionRequest, candidates []string) (*CompletionResponse, string, error) {
	targets := r.resolveRaceTargets(req.Provider, candidates)
	if len(targets) == 0 {
		return nil, "", fmt.Errorf("%w: no candidates for race", ErrProviderNotFound)
	}
	if len(targets) == 1 {
		resp, err := targets[0].p.Complete(ctx, req)
		if err != nil {
			return nil, targets[0].p.Name(), err
		}
		return resp, targets[0].p.Name(), nil
	}

	type result struct {
		resp *CompletionResponse
		name string
		err  error
	}
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := make(chan result, len(targets))
	for _, t := range targets {
		go func(t raceTarget) {
			resp, err := t.p.Complete(raceCtx, req)
			out <- result{resp: resp, name: t.p.Name(), err: err}
		}(t)
	}
	var errs []error
	for range targets {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case r := <-out:
			if r.err == nil {
				return r.resp, r.name, nil
			}
			errs = append(errs, fmt.Errorf("%s: %w", r.name, r.err))
		}
	}
	return nil, "", errors.Join(errs...)
}

type raceTarget struct {
	name string
	p    Provider
}

// resolveRaceTargets normalises/dedupes the candidate list and resolves each
// name to a concrete provider. Unknown names are silently dropped — the
// alternative (hard error) would make the race abort on a single typo even
// when viable providers are still in the list.
func (r *Router) resolveRaceTargets(requested string, candidates []string) []raceTarget {
	if len(candidates) == 0 {
		order := r.ResolveOrder(requested)
		for _, name := range order {
			if name != "offline" {
				candidates = append(candidates, name)
			}
		}
		if len(candidates) == 0 {
			candidates = order
		}
	}
	seen := map[string]struct{}{}
	var targets []raceTarget
	for _, c := range candidates {
		n := normalizeProviderName(c)
		if n == "" {
			continue
		}
		if _, ok := seen[n]; ok {
			continue
		}
		seen[n] = struct{}{}
		p, ok := r.Get(n)
		if !ok {
			continue
		}
		targets = append(targets, raceTarget{name: n, p: p})
	}
	return targets
}

// filterToolCapable returns the subset of `order` whose providers report
// SupportsTools=true, with one exception: if the caller explicitly named
// `requested`, that provider survives even when it lacks tool support. That
// way `--provider offline` still works for users who actively opt in; only
// the silent-fallback path is closed off.
func (r *Router) filterToolCapable(order []string, requested string) []string {
	req := normalizeProviderName(requested)
	out := make([]string, 0, len(order))
	for _, name := range order {
		if name == req {
			out = append(out, name)
			continue
		}
		p, ok := r.Get(name)
		if !ok {
			// Keep unknown names so the caller still gets the existing
			// ErrProviderNotFound message instead of an empty cascade.
			out = append(out, name)
			continue
		}
		if p.Hints().SupportsTools {
			out = append(out, name)
		}
	}
	return out
}

func (r *Router) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, string, error) {
	order := r.ResolveOrder(req.Provider)
	if len(req.Tools) > 0 {
		order = r.filterToolCapable(order, req.Provider)
	}
	var errs []error

	for _, name := range order {
		p, ok := r.Get(name)
		if !ok {
			errs = append(errs, fmt.Errorf("%w: %s", ErrProviderNotFound, name))
			continue
		}
		stream, usedModel, err := r.streamWithProviderRetry(ctx, p, req)
		if err == nil {
			return stream, usedModel, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
	}

	return nil, "", errors.Join(errs...)
}

// streamWithProviderRetry tries a provider's model chain on context overflow.
// On ErrContextOverflow it first retries the SAME model after compacting messages,
// then falls through to the next model. Returns (stream, providerName, error).
func (r *Router) streamWithProviderRetry(ctx context.Context, p Provider, req CompletionRequest) (<-chan StreamEvent, string, error) {
	models := p.Models()
	if len(models) == 0 {
		stream, err := r.streamWithThrottleRetry(ctx, p, req)
		return stream, p.Name(), err
	}
	var errs []error
	for _, model := range models {
		reqWithModel := req
		reqWithModel.Model = model
		stream, err := r.streamWithThrottleRetry(ctx, p, reqWithModel)
		if err == nil {
			return stream, p.Name(), nil
		}
		if isContextOverflow(err) {
			compacted, trimmed := compactMessagesForRetry(reqWithModel.Messages)
			if trimmed > 0 {
				retryReq := reqWithModel
				retryReq.Messages = compacted
				stream, err2 := r.streamWithThrottleRetry(ctx, p, retryReq)
				if err2 == nil {
					return stream, p.Name(), nil
				}
				errs = append(errs, fmt.Errorf("%s (model %s, context overflow, compacted %d msgs, retry failed): %w", p.Name(), model, trimmed, err2))
			} else {
				errs = append(errs, fmt.Errorf("%s (model %s): %w", p.Name(), model, err))
			}
			continue
		}
		errs = append(errs, fmt.Errorf("%s (model %s): %w", p.Name(), model, err))
		break
	}
	return nil, p.Name(), errors.Join(errs...)
}

// isContextOverflow matches either the explicit ErrContextOverflow sentinel or
// the well-known upstream phrasing used by Anthropic and OpenAI. New upstreams
// can just wrap ErrContextOverflow — the string-match branch is a best-effort
// catch for providers that haven't been taught to.
func isContextOverflow(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrContextOverflow) {
		return true
	}
	msg := strings.ToLower(err.Error())
	phrases := []string{
		"context_length_exceeded",
		"maximum context length",
		"prompt is too long",
		"context length",
		"too many tokens",
		"context window",
		"input is too long",
		"request too large",
	}
	for _, p := range phrases {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// compactMessagesForRetry drops the oldest non-tail messages and preserves:
//   - the final user turn (required for every provider),
//   - any trailing assistant/tool-result chain that follows that user turn,
//   - a synthetic [context compacted] note so the model sees *why* older
//     turns are missing instead of treating them as never having happened.
//
// Returns the compacted slice and the count of messages that were actually
// dropped. When trimming would leave fewer than 2 messages, returns the
// original slice and 0 — giving up is better than shipping a stub.
func compactMessagesForRetry(msgs []Message) ([]Message, int) {
	if len(msgs) <= 2 {
		return msgs, 0
	}
	// Find the last user index — that's the start of the tail we must keep.
	lastUser := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if string(msgs[i].Role) == "user" {
			lastUser = i
			break
		}
	}
	if lastUser <= 0 {
		return msgs, 0
	}
	// If only one message would be dropped, don't bother — the retry is
	// unlikely to fit otherwise.
	if lastUser < 2 {
		return msgs, 0
	}
	tail := msgs[lastUser:]
	notice := Message{
		Role:    "user",
		Content: "[prior conversation compacted to fit context window; " + fmt.Sprintf("%d", lastUser) + " older messages omitted]",
	}
	compacted := make([]Message, 0, len(tail)+1)
	compacted = append(compacted, notice)
	compacted = append(compacted, tail...)
	return compacted, lastUser
}
