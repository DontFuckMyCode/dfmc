package engine

import (
	"testing"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
)

// The default system prompt carries <<DFMC_CACHE_BREAK>>, so an engine
// with a wired Context manager should report a non-zero cacheable
// share — this is the observable signal that prompt caching is active.
func TestPromptRecommendation_SurfacesCacheSplit(t *testing.T) {
	eng := newTestEngine(t)

	info := eng.PromptRecommendationWithRuntime("how do I ship this?", ctxmgr.PromptRuntime{})

	if info.CacheableTokens <= 0 {
		t.Fatalf("expected non-zero cacheable tokens (default template has cache-break marker), got %d", info.CacheableTokens)
	}
	if info.DynamicTokens <= 0 {
		t.Fatalf("expected non-zero dynamic tokens (user query + injected sections), got %d", info.DynamicTokens)
	}
	if info.CacheablePercent <= 0 || info.CacheablePercent > 100 {
		t.Fatalf("cacheable_percent should be in (0,100], got %d", info.CacheablePercent)
	}
	// Sanity: with DFMC's long base prompt and a tiny question, the
	// stable share should dominate. If this flips under 50% something
	// has regressed in the template layout.
	if info.CacheablePercent < 50 {
		t.Fatalf("default template should be majority cacheable, got %d%%", info.CacheablePercent)
	}
}

// An engine without a Context manager wired up must return zeros for
// the cache fields rather than panic — status endpoints may hit this
// path during degraded startup when the context manager is unavailable.
func TestPromptRecommendation_NoContextManagerReturnsZeros(t *testing.T) {
	eng := newTestEngine(t)
	eng.Context = nil // simulate degraded startup

	info := eng.PromptRecommendationWithRuntime("anything", ctxmgr.PromptRuntime{})
	if info.CacheableTokens != 0 || info.DynamicTokens != 0 {
		t.Fatalf("degraded engine should report zero cache tokens, got cacheable=%d dynamic=%d",
			info.CacheableTokens, info.DynamicTokens)
	}
	if info.CacheablePercent != 0 {
		t.Fatalf("degraded engine cacheable_percent should be 0, got %d", info.CacheablePercent)
	}
}
