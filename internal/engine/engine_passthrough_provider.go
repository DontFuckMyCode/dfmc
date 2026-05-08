package engine

// engine_passthrough_provider.go — provider/model selection,
// fallback chain, pipeline activation, and the read-side helpers that
// resolve the effective provider+model+profile for Status reporting.
//
// Setters take e.mu.Lock(); read helpers take RLock or rely on the
// caller already holding the lock (see providerProfileStatusLocked,
// which is invoked from Status under RLock).

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// ProviderProbeResult is the outcome of a single TestProviderConnection
// call: whether the round trip succeeded, how long it took, the
// provider+model that handled it, and a one-line error message when it
// failed. Used by the TUI Providers panel (Phase I item 1) to render a
// per-row status chip after the user presses `T` to probe.
type ProviderProbeResult struct {
	Provider   string
	Model      string
	OK         bool
	DurationMs int
	Err        string
	At         time.Time
}

// TestProviderConnection issues a tiny no-op completion against the
// named provider with a hard timeout. Confirms the provider's API key,
// network path, and model name are all wired before the user commits a
// real prompt to it. Pure read against the provider — no engine state
// is mutated, no event bus is fired.
//
// `name` is matched case-insensitively (Router.Get handles
// normalisation). `timeout` is enforced via context.WithTimeout; pass 0
// to fall back to a sensible 8s default.
func (e *Engine) TestProviderConnection(name string, timeout time.Duration) ProviderProbeResult {
	now := time.Now()
	res := ProviderProbeResult{Provider: strings.TrimSpace(name), At: now}
	if e == nil || e.Providers == nil {
		res.Err = "engine not initialised"
		return res
	}
	p, ok := e.Providers.Get(name)
	if !ok {
		res.Err = "provider not registered"
		return res
	}
	res.Model = p.Model()
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	start := time.Now()
	_, err := p.Complete(ctx, provider.CompletionRequest{
		Provider: name,
		Model:    res.Model,
		System:   "ping",
		Messages: []provider.Message{{Role: types.RoleUser, Content: "ping"}},
	})
	res.DurationMs = int(time.Since(start).Milliseconds())
	if err != nil {
		res.Err = err.Error()
		return res
	}
	res.OK = true
	return res
}

func (e *Engine) SetProviderModel(provider, model string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.providerOverride = provider
	e.modelOverride = model
}

func (e *Engine) SetPrimaryProvider(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Config != nil {
		e.Config.Providers.Primary = name
	}
	if e.Providers != nil {
		e.Providers.SetPrimary(name)
	}
}

func (e *Engine) SetFallbackProviders(names []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Config != nil {
		e.Config.Providers.Fallback = append([]string(nil), names...)
	}
	if e.Providers != nil {
		e.Providers.SetFallback(names)
	}
}

func (e *Engine) FallbackProviders() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.Providers != nil {
		return e.Providers.Fallback()
	}
	if e.Config != nil {
		return append([]string(nil), e.Config.Providers.Fallback...)
	}
	return nil
}

// ActivatePipeline sets the engine's provider routing to follow the named
// pipeline. Step 1 becomes primary+model override; remaining steps become
// the fallback chain. Each step's model is written into the provider profile
// so the router's per-provider model retry honours the pipeline's intent.
func (e *Engine) ActivatePipeline(name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Config == nil || e.Config.Pipelines == nil {
		return fmt.Errorf("no pipelines configured")
	}
	pipe, ok := e.Config.Pipelines[name]
	if !ok {
		return fmt.Errorf("pipeline %q not found", name)
	}
	if len(pipe.Steps) == 0 {
		return fmt.Errorf("pipeline %q has no steps", name)
	}

	// Step 1: primary + model override
	first := pipe.Steps[0]
	e.providerOverride = first.Provider
	e.modelOverride = first.Model
	if e.Config != nil {
		e.Config.Providers.Primary = first.Provider
		if e.Config.Providers.Profiles == nil {
			e.Config.Providers.Profiles = map[string]config.ModelConfig{}
		}
		prof := e.Config.Providers.Profiles[first.Provider]
		prof.Model = first.Model
		e.Config.Providers.Profiles[first.Provider] = prof
	}
	if e.Providers != nil {
		e.Providers.SetPrimary(first.Provider)
	}

	// Steps 2+: fallback chain
	fallbackProviders := make([]string, 0, len(pipe.Steps)-1)
	for i := 1; i < len(pipe.Steps); i++ {
		step := pipe.Steps[i]
		fallbackProviders = append(fallbackProviders, step.Provider)
		if e.Config != nil {
			prof := e.Config.Providers.Profiles[step.Provider]
			prof.Model = step.Model
			e.Config.Providers.Profiles[step.Provider] = prof
		}
	}
	if e.Config != nil {
		e.Config.Providers.Fallback = fallbackProviders
	}
	if e.Providers != nil {
		e.Providers.SetFallback(fallbackProviders)
	}
	return nil
}

func (e *Engine) PipelineNames() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.Config == nil || e.Config.Pipelines == nil {
		return nil
	}
	names := make([]string, 0, len(e.Config.Pipelines))
	for n := range e.Config.Pipelines {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (e *Engine) Pipeline(name string) (config.PipelineConfig, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.Config == nil || e.Config.Pipelines == nil {
		return config.PipelineConfig{}, false
	}
	p, ok := e.Config.Pipelines[name]
	return p, ok
}

func (e *Engine) provider() string {
	if e.providerOverride != "" {
		return e.providerOverride
	}
	if e.Config == nil {
		return ""
	}
	return e.Config.Providers.Primary
}

func (e *Engine) model() string {
	if e.modelOverride != "" {
		return e.modelOverride
	}
	if e.Config == nil {
		return ""
	}
	profile, ok := e.Config.Providers.Profiles[e.provider()]
	if !ok {
		return ""
	}
	return profile.Model
}

// providerProfileStatusLocked is the read-side projection used by
// Status(). The caller MUST already hold e.mu (RLock or Lock); the
// function does not lock itself.
func (e *Engine) providerProfileStatusLocked() ProviderProfileStatus {
	status := ProviderProfileStatus{
		Name: strings.TrimSpace(e.provider()),
	}
	if e.Config == nil {
		status.Model = strings.TrimSpace(e.model())
		return status
	}
	if status.Name == "" {
		status.Name = strings.TrimSpace(e.Config.Providers.Primary)
	}
	if profile, ok := e.Config.Providers.Profiles[status.Name]; ok {
		status.Model = strings.TrimSpace(profile.Model)
		status.Protocol = strings.TrimSpace(profile.Protocol)
		status.BaseURL = strings.TrimSpace(profile.BaseURL)
		status.MaxTokens = profile.MaxTokens
		status.MaxContext = profile.MaxContext
		status.CostPer1kTokens = profile.CostPer1kTokens
		status.Configured = providerProfileConfigured(status.Name, profile)
		status.Advisories = config.ProviderProfileAdvisories(status.Name, profile)
	}
	if status.Model == "" {
		status.Model = strings.TrimSpace(e.model())
	}
	if override := strings.TrimSpace(e.modelOverride); override != "" {
		status.Model = override
	}
	return status
}

func modelsDevCacheStatus() ModelsDevCacheStatus {
	path := config.ModelsDevCachePath()
	status := ModelsDevCacheStatus{
		Path: strings.TrimSpace(path),
	}
	if status.Path == "" {
		return status
	}
	info, err := os.Stat(status.Path)
	if err != nil {
		return status
	}
	status.Exists = true
	status.UpdatedAt = info.ModTime()
	status.SizeBytes = info.Size()
	return status
}

func providerProfileConfigured(name string, profile config.ModelConfig) bool {
	apiKey := strings.TrimSpace(profile.APIKey)
	baseURL := strings.TrimSpace(profile.BaseURL)

	switch strings.ToLower(strings.TrimSpace(name)) {
	case "generic":
		return baseURL != ""
	default:
		return apiKey != "" || baseURL != ""
	}
}
