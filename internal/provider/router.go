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
