package engine

import (
	"context"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
)

// fixedContextProvider is a minimal Provider stub whose only job is to
// report a configurable MaxContext so agentLimits elasticity can be
// exercised without spinning up a full router script.
type fixedContextProvider struct {
	name       string
	maxContext int
}

func (p *fixedContextProvider) Name() string                  { return p.name }
func (p *fixedContextProvider) Model() string                 { return "stub" }
func (p *fixedContextProvider) CountTokens(text string) int   { return len(text) }
func (p *fixedContextProvider) MaxContext() int               { return p.maxContext }
func (p *fixedContextProvider) Hints() provider.ProviderHints { return provider.ProviderHints{} }
func (p *fixedContextProvider) Complete(_ context.Context, _ provider.CompletionRequest) (*provider.CompletionResponse, error) {
	return &provider.CompletionResponse{}, nil
}
func (p *fixedContextProvider) Stream(_ context.Context, _ provider.CompletionRequest) (<-chan provider.StreamEvent, error) {
	return nil, nil
}

func newEngineWithWindow(t *testing.T, window int) *Engine {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Providers.Primary = "stub"
	cfg.Providers.Profiles["stub"] = config.ModelConfig{
		Model:      "stub",
		MaxTokens:  4096,
		MaxContext: window,
	}
	// Start with cfg.Agent defaults — 120k tokens, 3200 chars, 1200 data
	// chars. The elastic scale-up should beat those whenever the window
	// is big enough.
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	router.Register(&fixedContextProvider{name: "stub", maxContext: window})
	return &Engine{Config: cfg, Providers: router}
}

func TestAgentLimits_ElasticScalesTokensWithWindow(t *testing.T) {
	eng := newEngineWithWindow(t, 1_000_000)
	lim := eng.agentLimits()
	want := int(1_000_000 * elasticToolTokensRatio)
	if lim.MaxTokens != want {
		t.Fatalf("MaxTokens should scale with 1M window, want %d got %d", want, lim.MaxTokens)
	}
	if lim.MaxTokens <= defaultMaxNativeToolTokens {
		t.Fatalf("elastic MaxTokens (%d) should beat the 120k default", lim.MaxTokens)
	}
}

func TestAgentLimits_ElasticPreservesFloorForSmallWindows(t *testing.T) {
	eng := newEngineWithWindow(t, 64_000)
	lim := eng.agentLimits()
	if lim.MaxTokens != defaultMaxNativeToolTokens {
		t.Fatalf("small window should leave the default floor intact, got %d", lim.MaxTokens)
	}
	if lim.MaxResultChars != defaultMaxNativeToolResultChars {
		t.Fatalf("small window should leave result-chars floor intact, got %d", lim.MaxResultChars)
	}
}

func TestAgentLimits_UserConfigIsRespectedAsFloor(t *testing.T) {
	eng := newEngineWithWindow(t, 200_000)
	// User wants a hefty result-chars budget that beats the elastic ratio
	// (200k * 1/40 = 5000). With 10k configured, the explicit value wins.
	eng.Config.Agent.MaxToolResultChars = 10_000
	lim := eng.agentLimits()
	if lim.MaxResultChars != 10_000 {
		t.Fatalf("explicit cfg should be respected when higher than elastic, got %d", lim.MaxResultChars)
	}
}

func TestAgentLimits_ElasticScalesResultCharsWithWindow(t *testing.T) {
	eng := newEngineWithWindow(t, 1_000_000)
	lim := eng.agentLimits()
	wantChars := int(1_000_000 * elasticToolResultCharsRatio)
	if lim.MaxResultChars != wantChars {
		t.Fatalf("MaxResultChars should scale with 1M window, want %d got %d", wantChars, lim.MaxResultChars)
	}
	wantData := int(1_000_000 * elasticToolDataCharsRatio)
	if lim.MaxDataChars != wantData {
		t.Fatalf("MaxDataChars should scale with 1M window, want %d got %d", wantData, lim.MaxDataChars)
	}
}

func TestAgentLimits_ElasticRatiosCanBeConfigured(t *testing.T) {
	eng := newEngineWithWindow(t, 1_000_000)
	eng.Config.Agent.ElasticToolTokensRatio = 0.25
	eng.Config.Agent.ElasticToolResultCharsRatio = 1.0 / 80.0
	eng.Config.Agent.ElasticToolDataCharsRatio = 1.0 / 200.0

	lim := eng.agentLimits()
	if want := int(1_000_000 * 0.25); lim.MaxTokens != want {
		t.Fatalf("configured token ratio should win, want %d got %d", want, lim.MaxTokens)
	}
	if want := int(1_000_000 * (1.0 / 80.0)); lim.MaxResultChars != want {
		t.Fatalf("configured result-char ratio should win, want %d got %d", want, lim.MaxResultChars)
	}
	if want := int(1_000_000 * (1.0 / 200.0)); lim.MaxDataChars != want {
		t.Fatalf("configured data-char ratio should win, want %d got %d", want, lim.MaxDataChars)
	}
}
