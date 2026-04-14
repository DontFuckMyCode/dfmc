package engine

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/provider"
)

func TestContextBuildOptions_AutoCapsTotalBudget(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxFiles = 20
	cfg.Context.MaxTokensPerFile = 2000
	cfg.Context.MaxTokensTotal = 0

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	eng := &Engine{Config: cfg, Providers: router}
	opts := eng.contextBuildOptions("explain auth middleware and rate limiting behavior")

	if opts.MaxTokensTotal <= 0 {
		t.Fatalf("expected positive budget, got %d", opts.MaxTokensTotal)
	}
	if opts.MaxTokensTotal > defaultContextTotalCapTokens {
		t.Fatalf("expected auto cap <= %d, got %d", defaultContextTotalCapTokens, opts.MaxTokensTotal)
	}
	if opts.MaxTokensPerFile > opts.MaxTokensTotal {
		t.Fatalf("per-file budget cannot exceed total budget: %d > %d", opts.MaxTokensPerFile, opts.MaxTokensTotal)
	}
}

func TestContextBuildOptions_RespectsExplicitTotalBudget(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxFiles = 10
	cfg.Context.MaxTokensPerFile = 2000
	cfg.Context.MaxTokensTotal = 6000

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	eng := &Engine{Config: cfg, Providers: router}
	opts := eng.contextBuildOptions("refactor auth module")

	if opts.MaxTokensTotal != 6000 {
		t.Fatalf("expected total budget 6000, got %d", opts.MaxTokensTotal)
	}
	if opts.MaxTokensPerFile != 600 {
		t.Fatalf("expected per-file budget to be adjusted to 600, got %d", opts.MaxTokensPerFile)
	}
}

func TestContextBuildOptions_ProviderLimitWins(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxFiles = 20
	cfg.Context.MaxTokensPerFile = 2000
	cfg.Context.MaxTokensTotal = 20000

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	eng := &Engine{
		Config:           cfg,
		Providers:        router,
		providerOverride: "offline",
	}
	opts := eng.contextBuildOptions(strings.Repeat("token ", 1000))

	if opts.MaxTokensTotal >= 20000 {
		t.Fatalf("expected provider cap to reduce total budget, got %d", opts.MaxTokensTotal)
	}
	if opts.MaxTokensTotal > 6500 {
		t.Fatalf("expected offline provider limit to cap budget strongly, got %d", opts.MaxTokensTotal)
	}
	if opts.MaxTokensPerFile > opts.MaxTokensTotal {
		t.Fatalf("per-file budget cannot exceed total budget: %d > %d", opts.MaxTokensPerFile, opts.MaxTokensTotal)
	}
}
