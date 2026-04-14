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

func TestContextBudgetPreview_ReflectsEffectiveOptions(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxFiles = 9
	cfg.Context.MaxTokensTotal = 9000
	cfg.Context.MaxTokensPerFile = 1500
	cfg.Context.MaxHistoryTokens = 700

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{Config: cfg, Providers: router}
	preview := eng.ContextBudgetPreview("check auth flow")

	if preview.Task != "general" {
		t.Fatalf("expected task general, got %s", preview.Task)
	}
	if preview.MaxFiles != 9 {
		t.Fatalf("expected max files 9, got %d", preview.MaxFiles)
	}
	if preview.MaxTokensTotal != 9000 {
		t.Fatalf("expected max total 9000, got %d", preview.MaxTokensTotal)
	}
	if preview.MaxTokensPerFile <= 0 || preview.MaxTokensPerFile > 1500 {
		t.Fatalf("unexpected per-file budget: %d", preview.MaxTokensPerFile)
	}
	if preview.MaxHistoryTokens != 700 {
		t.Fatalf("expected history budget 700, got %d", preview.MaxHistoryTokens)
	}
	if preview.ExplicitFileMentions != 0 {
		t.Fatalf("expected explicit file mentions 0, got %d", preview.ExplicitFileMentions)
	}
	if preview.TaskTotalScale <= 0 || preview.TaskFileScale <= 0 || preview.TaskPerFileScale <= 0 {
		t.Fatalf("unexpected task scales: %#v", preview)
	}
	if preview.ContextAvailableTokens <= 0 {
		t.Fatalf("expected positive context available tokens, got %d", preview.ContextAvailableTokens)
	}
	if preview.ReserveTotalTokens <= 0 {
		t.Fatalf("expected positive reserve total, got %d", preview.ReserveTotalTokens)
	}
	sum := preview.ReservePromptTokens + preview.ReserveHistoryTokens + preview.ReserveResponseTokens + preview.ReserveToolTokens
	if sum != preview.ReserveTotalTokens {
		t.Fatalf("reserve mismatch: sum=%d total=%d", sum, preview.ReserveTotalTokens)
	}
}

func TestContextBuildOptions_TaskAdaptiveScaling(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxFiles = 20
	cfg.Context.MaxTokensPerFile = 1600
	cfg.Context.MaxTokensTotal = 12000

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{Config: cfg, Providers: router}

	sec := eng.contextBuildOptions("run security audit for auth and token handling")
	plan := eng.contextBuildOptions("plan next sprint roadmap for codebase cleanup")

	if sec.MaxTokensTotal <= plan.MaxTokensTotal {
		t.Fatalf("expected security budget > planning budget, got security=%d planning=%d", sec.MaxTokensTotal, plan.MaxTokensTotal)
	}
	if sec.MaxFiles <= plan.MaxFiles {
		t.Fatalf("expected security max files > planning max files, got security=%d planning=%d", sec.MaxFiles, plan.MaxFiles)
	}
}

func TestContextBuildOptions_ExplicitFileMentionsFocusScope(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxFiles = 20
	cfg.Context.MaxTokensPerFile = 1500
	cfg.Context.MaxTokensTotal = 12000

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{Config: cfg, Providers: router}

	general := eng.contextBuildOptions("debug auth flow")
	focused := eng.contextBuildOptions("debug [[file:internal/auth/service.go#L1-L80]] with [[file:internal/auth/token.go]]")
	preview := eng.ContextBudgetPreview("debug [[file:internal/auth/service.go#L1-L80]] with [[file:internal/auth/token.go]]")

	if focused.MaxFiles >= general.MaxFiles {
		t.Fatalf("expected explicit file markers to reduce max_files, got focused=%d general=%d", focused.MaxFiles, general.MaxFiles)
	}
	if preview.ExplicitFileMentions != 2 {
		t.Fatalf("expected explicit file mentions=2, got %d", preview.ExplicitFileMentions)
	}
}

func TestContextRecommendations_AlwaysReturnsGuidance(t *testing.T) {
	cfg := config.DefaultConfig()
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{Config: cfg, Providers: router}

	recs := eng.ContextRecommendations("security audit auth middleware")
	if len(recs) == 0 {
		t.Fatal("expected at least one recommendation")
	}
	for _, rec := range recs {
		if rec.Code == "" || rec.Message == "" {
			t.Fatalf("invalid recommendation: %#v", rec)
		}
	}
}

func TestPromptRecommendation_ContainsBudgetAndHints(t *testing.T) {
	cfg := config.DefaultConfig()
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{Config: cfg, Providers: router}

	info := eng.PromptRecommendation("security audit auth middleware")
	if info.Task != "security" {
		t.Fatalf("expected task=security, got %s", info.Task)
	}
	if info.Profile == "" {
		t.Fatal("expected non-empty profile")
	}
	if info.PromptBudgetTokens <= 0 {
		t.Fatalf("expected positive prompt budget, got %d", info.PromptBudgetTokens)
	}
	if info.ContextFiles <= 0 || info.ToolList <= 0 {
		t.Fatalf("unexpected render budget: %#v", info)
	}
	if len(info.Hints) == 0 {
		t.Fatal("expected at least one prompt hint")
	}
}
