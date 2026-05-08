package engine

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
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
	// MaxHistoryMessages should reflect the resolved trim window even when
	// the user hasn't set it — falling back to the engine default. Without
	// this field the CLI/HTTP/remote consumers would have to duplicate the
	// resolution logic to surface "stored msgs / N max" displays.
	if preview.MaxHistoryMessages <= 0 {
		t.Fatalf("expected MaxHistoryMessages > 0 (engine default), got %d", preview.MaxHistoryMessages)
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

func TestBuildContextChunks_AutoIncludeFilesOffByDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	// AutoIncludeFiles=false is the modern default: a tool-using model
	// retrieves on demand via grep_codebase / find_symbol / read_file
	// rather than receiving a pre-loaded scrap pile. A bare query must
	// therefore SKIP workspace retrieval; only explicit markers force
	// inclusion.
	if cfg.Context.AutoIncludeFiles {
		t.Fatal("default AutoIncludeFiles should be false; tool-using models retrieve on demand")
	}
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{Config: cfg, Providers: router}
	chunks := eng.buildContextChunks("explain the provider router")

	if len(chunks) != 0 {
		t.Fatalf("AutoIncludeFiles=false: bare query must yield 0 chunks, got %d", len(chunks))
	}
}

// providerWindowScale is sized for modern 200K+ defaults: most
// providers get 1.0 (no change to base config) and only the rarefied
// 1M-window class (Opus 4.7 etc.) gets a modest 1.25 stretch. The
// reserve/available-budget clamps already handle small-window
// auto-throttle, so this scaler doesn't need <1.0 tiers.
func TestProviderWindowScale_TiersByWindowSize(t *testing.T) {
	cases := []struct {
		windowTokens int
		want         float64
	}{
		{0, 1.0},          // unknown — pass-through
		{8_000, 1.0},      // tight (clamp handles fit)
		{32_000, 1.0},     // small
		{128_000, 1.0},    // mid
		{200_000, 1.0},    // Sonnet/GPT class — base default
		{511_999, 1.0},    // just under threshold
		{512_000, 1.25},   // Opus 1M class
		{1_000_000, 1.25}, // 1M
	}
	for _, tc := range cases {
		got := providerWindowScale(tc.windowTokens)
		if got != tc.want {
			t.Errorf("providerWindowScale(%d): want %.2f, got %.2f", tc.windowTokens, tc.want, got)
		}
	}
}

// Auto-computed budgets (no user-set MaxFiles/MaxTokensTotal) should
// scale UP for big-window providers but NEVER below the configured
// floor for small-window providers. Explicit user values pass
// through without scaling — verified separately by
// TestContextBudgetPreview_ReflectsEffectiveOptions.
func TestContextBuildOptions_WindowScaleRespectsUserSetValues(t *testing.T) {
	cfg := config.DefaultConfig()
	// User explicitly sets MaxFiles + MaxTokensTotal — these MUST NOT
	// be inflated by windowScale, even when the provider has a huge
	// context window. Otherwise users who tighten their config
	// silently get re-bloated.
	cfg.Context.MaxFiles = 6
	cfg.Context.MaxTokensTotal = 3000
	cfg.Context.MaxTokensPerFile = 500

	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{Config: cfg, Providers: router}
	opts := eng.contextBuildOptions("explore the architecture")

	// MaxFiles user-set — even on a 1M-window provider it should
	// stay ~6 (profile.FileScale may nudge slightly but windowScale
	// must not multiply it further).
	if opts.MaxFiles > 8 {
		t.Errorf("user-set MaxFiles=6 must not be inflated by windowScale; got %d", opts.MaxFiles)
	}
	// MaxTokensTotal user-set — same contract.
	if opts.MaxTokensTotal > 4000 {
		t.Errorf("user-set MaxTokensTotal=3000 must not be inflated by windowScale; got %d", opts.MaxTokensTotal)
	}
}

func TestShouldBuildWorkspaceContext_AutoIncludeFilesOffByDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	eng := &Engine{Config: cfg}

	if cfg.Context.AutoIncludeFiles {
		t.Fatal("default config should have AutoIncludeFiles=false; tool-using models retrieve on demand")
	}
	// Bare query — workspace retrieval MUST be skipped. The model uses
	// its grep/find_symbol/read_file tools to pull only what it needs.
	if eng.shouldBuildWorkspaceContext("debug provider router") {
		t.Fatal("AutoIncludeFiles=false: bare query must NOT trigger workspace retrieval")
	}
	// Explicit per-turn opt-ins still work — these are user-driven
	// signals that "for THIS turn, dump the workspace evidence in".
	if !eng.shouldBuildWorkspaceContext("debug [[file:internal/provider/router.go]]") {
		t.Fatal("[[file:...]] marker should opt in to workspace retrieval")
	}
	if !eng.shouldBuildWorkspaceContext("debug provider router [[workspace-context]]") {
		t.Fatal("[[workspace-context]] marker should opt in to workspace retrieval")
	}
	if !eng.shouldBuildWorkspaceContext("trace #ctx-files the auth flow") {
		t.Fatal("#ctx-files flag should opt in to workspace retrieval")
	}
	// User can flip the default per-config when they want classic pre-load.
	cfg.Context.AutoIncludeFiles = true
	if !eng.shouldBuildWorkspaceContext("debug provider router") {
		t.Fatal("AutoIncludeFiles=true override: bare query must trigger retrieval")
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
	if info.Role == "" {
		t.Fatal("expected non-empty role")
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

func TestPromptRecommendationWithRuntime_UsesOverrides(t *testing.T) {
	cfg := config.DefaultConfig()
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{Config: cfg, Providers: router}

	info := eng.PromptRecommendationWithRuntime("security audit auth middleware", ctxmgr.PromptRuntime{
		ToolStyle:  "function-calling",
		MaxContext: 1000,
	})
	if info.ToolStyle != "function-calling" {
		t.Fatalf("expected tool style override to apply, got %q", info.ToolStyle)
	}
	if info.MaxContext != 1000 {
		t.Fatalf("expected max context override to apply, got %d", info.MaxContext)
	}
	if info.Profile != "compact" {
		t.Fatalf("expected compact profile for tight runtime context, got %q", info.Profile)
	}
}

func TestContextBudgetPreviewWithRuntime_UsesMaxContextOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxFiles = 20
	cfg.Context.MaxTokensPerFile = 2000
	cfg.Context.MaxTokensTotal = 30000
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{Config: cfg, Providers: router}

	base := eng.ContextBudgetPreview("security audit auth middleware")
	tight := eng.ContextBudgetPreviewWithRuntime("security audit auth middleware", ctxmgr.PromptRuntime{
		MaxContext: 1000,
	})
	if tight.ProviderMaxContext != 1000 {
		t.Fatalf("expected provider max context override, got %d", tight.ProviderMaxContext)
	}
	if tight.MaxTokensTotal >= base.MaxTokensTotal {
		t.Fatalf("expected tighter runtime to reduce total budget, got tight=%d base=%d", tight.MaxTokensTotal, base.MaxTokensTotal)
	}
}

func TestContextBudgetPreviewWithRuntime_TightRuntimeScalesReserves(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxFiles = 20
	cfg.Context.MaxTokensPerFile = 2000
	cfg.Context.MaxTokensTotal = 30000
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{Config: cfg, Providers: router}

	tight := eng.ContextBudgetPreviewWithRuntime("security audit auth middleware", ctxmgr.PromptRuntime{
		MaxContext: 1000,
	})
	if tight.ProviderMaxContext != 1000 {
		t.Fatalf("expected provider max context override, got %d", tight.ProviderMaxContext)
	}
	if tight.ReserveTotalTokens > (tight.ProviderMaxContext - minContextTotalBudgetTokens) {
		t.Fatalf("expected reserve total to be bounded for tight runtime, got reserve=%d provider_max=%d", tight.ReserveTotalTokens, tight.ProviderMaxContext)
	}
	if tight.ReserveResponseTokens >= 4096 {
		t.Fatalf("expected response reserve to be scaled down for tight runtime, got %d", tight.ReserveResponseTokens)
	}
	if tight.Compression != "aggressive" {
		t.Fatalf("expected aggressive compression for tight runtime, got %q", tight.Compression)
	}
	if tight.IncludeDocs {
		t.Fatalf("expected docs to be disabled for tight runtime, got include_docs=%t", tight.IncludeDocs)
	}
}

func TestContextTuningSuggestionsWithRuntime_ReturnsActionableSuggestions(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Context.MaxFiles = 20
	cfg.Context.MaxTokensPerFile = 2000
	cfg.Context.MaxTokensTotal = 30000
	router, err := provider.NewRouter(cfg.Providers)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	eng := &Engine{Config: cfg, Providers: router}

	suggestions := eng.ContextTuningSuggestionsWithRuntime("security audit auth middleware", ctxmgr.PromptRuntime{
		MaxContext: 1000,
	})
	if len(suggestions) == 0 {
		t.Fatal("expected non-empty tuning suggestions")
	}
	hasActionable := false
	for _, s := range suggestions {
		key := strings.TrimSpace(s.Key)
		if key == "context.max_tokens_total" || key == "context.max_history_tokens" || key == "context.compression" {
			hasActionable = true
			break
		}
	}
	if !hasActionable {
		t.Fatalf("expected actionable context tuning suggestion, got %#v", suggestions)
	}
}
