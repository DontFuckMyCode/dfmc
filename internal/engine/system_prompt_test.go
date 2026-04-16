package engine

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/ast"
	"github.com/dontfuckmycode/dfmc/internal/codemap"
	"github.com/dontfuckmycode/dfmc/internal/config"
	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
	"github.com/dontfuckmycode/dfmc/internal/promptlib"
	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	cfg := config.DefaultConfig()
	mgr := ctxmgr.New(codemap.New(ast.New()))
	return &Engine{
		Config:      cfg,
		Context:     mgr,
		ProjectRoot: t.TempDir(),
		Tools:       tools.New(*cfg),
	}
}

// TestBuildSystemPrompt_ReturnsCacheableBlocks exercises the engine-side
// helper that composes the system prompt bundle. The default template carries
// the cache-break sentinel, so the helper should emit structured SystemBlocks
// with a stable+cacheable prefix and a dynamic tail.
func TestBuildSystemPrompt_ReturnsCacheableBlocks(t *testing.T) {
	eng := newTestEngine(t)

	text, blocks := eng.buildSystemPrompt("how do I ship this?", nil)
	if strings.TrimSpace(text) == "" {
		t.Fatalf("expected non-empty system prompt text")
	}
	if len(blocks) == 0 {
		t.Fatalf("expected SystemBlocks when template has cache-break marker")
	}
	if strings.Contains(text, promptlib.CacheBreakMarker) {
		t.Fatalf("rendered text must not contain the raw marker: %q", text)
	}

	sawStable, sawDynamic := false, false
	for _, b := range blocks {
		if b.Cacheable {
			sawStable = true
			if strings.Contains(b.Text, "how do I ship this?") {
				t.Fatalf("cacheable block must not carry per-request user query: %q", b.Text)
			}
		} else {
			sawDynamic = true
			if !strings.Contains(b.Text, "how do I ship this?") {
				t.Fatalf("dynamic block should carry user query, got %q", b.Text)
			}
		}
	}
	if !sawStable {
		t.Fatalf("expected at least one cacheable block, got blocks=%+v", blocks)
	}
	if !sawDynamic {
		t.Fatalf("expected at least one dynamic block, got blocks=%+v", blocks)
	}
}

// TestBuildSystemPrompt_NilContextReturnsEmpty documents the guard that keeps
// the helper safe when the engine is wired without a context manager (e.g.
// degraded startup path).
func TestBuildSystemPrompt_NilContextReturnsEmpty(t *testing.T) {
	eng := &Engine{}
	text, blocks := eng.buildSystemPrompt("anything", nil)
	if text != "" || blocks != nil {
		t.Fatalf("expected empty return when Context is nil, got text=%q blocks=%+v", text, blocks)
	}
}

// TestBundleToSystemBlocks_NoCacheMeansNilBlocks verifies the fast-path: if
// the bundle reports no cacheable sections, the paired blocks slice is nil so
// providers that don't support caching stay on the flat-string path.
func TestBundleToSystemBlocks_NoCacheMeansNilBlocks(t *testing.T) {
	bundle := &promptlib.PromptBundle{Sections: []promptlib.PromptSection{
		{Label: "dynamic", Text: "only dynamic content", Cacheable: false},
	}}
	text, blocks := bundleToSystemBlocks(bundle)
	if text != "only dynamic content" {
		t.Fatalf("text mismatch: %q", text)
	}
	if blocks != nil {
		t.Fatalf("expected nil blocks when bundle has no cacheable section, got %+v", blocks)
	}
}

// TestBuildNativeToolSystemPromptBundle_BridgeCached verifies the native tool
// system prompt folds the meta-tool instructions into the cacheable prefix so
// the bridge (~40 tokens) rides along with Anthropic prompt caching.
func TestBuildNativeToolSystemPromptBundle_BridgeCached(t *testing.T) {
	eng := newTestEngine(t)

	text, blocks := eng.buildNativeToolSystemPromptBundle("analyze", nil)
	if !strings.Contains(text, "[DFMC native tool surface]") {
		t.Fatalf("flat text should include native tool bridge: %q", text)
	}
	if len(blocks) == 0 {
		t.Fatalf("expected blocks when template carries marker")
	}
	bridgeInStable := false
	for _, b := range blocks {
		if b.Cacheable && strings.Contains(b.Text, "[DFMC native tool surface]") {
			bridgeInStable = true
			break
		}
	}
	if !bridgeInStable {
		t.Fatalf("native tool bridge should live inside the cacheable prefix; blocks=%+v", blocks)
	}
	sawDynamic := false
	for _, b := range blocks {
		if !b.Cacheable && strings.Contains(b.Text, "analyze") {
			sawDynamic = true
			break
		}
	}
	if !sawDynamic {
		t.Fatalf("expected user query to land in dynamic block; blocks=%+v", blocks)
	}
}

// sanity check that provider.SystemBlock carries the exact shape the anthropic
// payload helper expects.
func TestSystemBlockShape(t *testing.T) {
	b := provider.SystemBlock{Label: "x", Text: "y", Cacheable: true}
	if b.Label != "x" || b.Text != "y" || !b.Cacheable {
		t.Fatalf("SystemBlock field round-trip failed: %+v", b)
	}
}
