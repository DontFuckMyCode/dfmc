package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

type Router struct {
	mu        sync.RWMutex
	primary   string
	fallback  []string
	providers map[string]Provider
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

	switch protocol {
	case "anthropic":
		if apiKey == "" {
			return NewPlaceholderProvider(name, model, false, profile.MaxContext)
		}
		return NewAnthropicProvider(model, apiKey, baseURL, profile.MaxTokens, profile.MaxContext)
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

func (r *Router) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[normalizeProviderName(name)]
	return p, ok
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
	var errs []error

	for _, name := range order {
		p, ok := r.Get(name)
		if !ok {
			errs = append(errs, fmt.Errorf("%w: %s", ErrProviderNotFound, name))
			continue
		}
		resp, err := p.Complete(ctx, req)
		if err == nil {
			return resp, p.Name(), nil
		}
		// Context overflow: trim the oldest non-tail messages and retry the
		// SAME provider once before giving up on it. Moving to a fallback
		// provider won't help if the same huge conversation just shifts over.
		if isContextOverflow(err) {
			compacted, trimmed := compactMessagesForRetry(req.Messages)
			if trimmed > 0 {
				retryReq := req
				retryReq.Messages = compacted
				resp, err2 := p.Complete(ctx, retryReq)
				if err2 == nil {
					return resp, p.Name(), nil
				}
				errs = append(errs, fmt.Errorf("%s (context overflow, compacted %d msgs, retry failed): %w", p.Name(), trimmed, err2))
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
			continue
		}
		if errors.Is(err, ErrProviderUnavailable) {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
	}

	return nil, "", errors.Join(errs...)
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

func (r *Router) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, string, error) {
	order := r.ResolveOrder(req.Provider)
	var errs []error

	for _, name := range order {
		p, ok := r.Get(name)
		if !ok {
			errs = append(errs, fmt.Errorf("%w: %s", ErrProviderNotFound, name))
			continue
		}
		stream, err := p.Stream(ctx, req)
		if err == nil {
			return stream, p.Name(), nil
		}
		if isContextOverflow(err) {
			compacted, trimmed := compactMessagesForRetry(req.Messages)
			if trimmed > 0 {
				retryReq := req
				retryReq.Messages = compacted
				stream, err2 := p.Stream(ctx, retryReq)
				if err2 == nil {
					return stream, p.Name(), nil
				}
				errs = append(errs, fmt.Errorf("%s (context overflow, compacted %d msgs, retry failed): %w", p.Name(), trimmed, err2))
				continue
			}
			errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
			continue
		}
		if errors.Is(err, ErrProviderUnavailable) {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
	}

	return nil, "", errors.Join(errs...)
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
