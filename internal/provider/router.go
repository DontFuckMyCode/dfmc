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
		configured := strings.TrimSpace(profile.APIKey) != ""
		r.Register(NewPlaceholderProvider(name, profile.Model, configured))
	}

	if strings.TrimSpace(r.primary) == "" {
		r.primary = "offline"
	}
	return r, nil
}

func (r *Router) Register(p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[p.Name()] = p
}

func (r *Router) Get(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[name]
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
		name = strings.TrimSpace(name)
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
		if errors.Is(err, ErrProviderUnavailable) {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
	}

	return nil, "", errors.Join(errs...)
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
		if errors.Is(err, ErrProviderUnavailable) {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, fmt.Errorf("%s: %w", p.Name(), err))
	}

	return nil, "", errors.Join(errs...)
}
