package promptlib

import (
	"strings"
	"testing"
)

func TestSpliceAppendBeforeCacheBreak_InsertsOverlaysInStableRegion(t *testing.T) {
	base := "stable prefix\n\n" + CacheBreakMarker + "\n\nUser request: {{q}}"
	overlays := []string{"Profile directive: be compact."}

	got := spliceAppendBeforeCacheBreak(base, overlays)

	stable, dynamic, found := strings.Cut(got, CacheBreakMarker)
	if !found {
		t.Fatalf("splice output lost the cache-break marker:\n%s", got)
	}
	if !strings.Contains(stable, "Profile directive") {
		t.Fatalf("overlay should land before the marker, got stable=%q", stable)
	}
	if strings.Contains(dynamic, "Profile directive") {
		t.Fatalf("overlay must not leak into the dynamic tail, got dynamic=%q", dynamic)
	}
	if !strings.Contains(dynamic, "User request:") {
		t.Fatalf("dynamic tail should keep the per-request section, got dynamic=%q", dynamic)
	}
}

func TestSpliceAppendBeforeCacheBreak_NoMarkerFallsBackToAppend(t *testing.T) {
	// Templates without a cache-break marker keep the historical layout:
	// overlays are appended, splitRenderedBundle will treat the whole
	// output as a single dynamic section.
	base := "stable prefix with no marker"
	overlays := []string{"Profile overlay"}

	got := spliceAppendBeforeCacheBreak(base, overlays)

	if strings.Contains(got, CacheBreakMarker) {
		t.Fatalf("fallback must not invent a marker, got:\n%s", got)
	}
	if !strings.HasPrefix(got, "stable prefix with no marker") {
		t.Fatalf("base content should come first in fallback, got:\n%s", got)
	}
	if !strings.Contains(got, "Profile overlay") {
		t.Fatalf("overlay must still be present in fallback, got:\n%s", got)
	}
}

func TestSpliceAppendBeforeCacheBreak_EmptyOverlaysReturnsBaseUnchanged(t *testing.T) {
	base := "untouched base\n\n" + CacheBreakMarker + "\n\ndynamic"
	if got := spliceAppendBeforeCacheBreak(base, nil); got != base {
		t.Fatalf("nil overlays should return base verbatim, got:\n%s", got)
	}
	if got := spliceAppendBeforeCacheBreak(base, []string{}); got != base {
		t.Fatalf("empty overlays should return base verbatim, got:\n%s", got)
	}
}

// RenderBundle end-to-end with the shipped default templates: every
// compose=append overlay picked for a request must land in the cacheable
// prefix, not the dynamic tail. This guards against a future refactor
// that accidentally restores the old "overlays after the marker" layout.
func TestRenderBundle_ShippedOverlaysJoinCacheablePrefix(t *testing.T) {
	lib := New() // loads embedded defaults

	bundle := lib.RenderBundle(RenderRequest{
		Type:    "system",
		Task:    "general",
		Profile: "compact",
		Vars:    map[string]string{"user_query": "help me"},
	})
	if !bundle.HasCacheable() {
		t.Fatalf("expected cacheable section with default templates, got %+v", bundle)
	}
	stable := bundle.CacheableText()
	// The "Compact profile directives:" overlay is compose=append
	// and has no per-request vars, so it MUST end up in the cacheable
	// prefix once spliced before the cache-break marker.
	if !strings.Contains(stable, "Compact profile directives") {
		t.Fatalf("compact overlay should be in cacheable prefix, got:\n%s", stable)
	}
	dyn := bundle.DynamicText()
	if strings.Contains(dyn, "Compact profile directives") {
		t.Fatalf("compact overlay must not leak into dynamic tail, got:\n%s", dyn)
	}
	// Sanity: the user query still belongs in the dynamic section.
	if !strings.Contains(dyn, "help me") {
		t.Fatalf("user query should be in dynamic tail, got:\n%s", dyn)
	}
}
