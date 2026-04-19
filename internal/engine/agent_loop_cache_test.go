package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/provider"
)

func TestCacheableToolCallKey_OnlyReadClassWhitelisted(t *testing.T) {
	cases := []struct {
		name    string
		call    provider.ToolCall
		wantKey bool
	}{
		{
			name: "read_file with args is cacheable",
			call: provider.ToolCall{Name: "tool_call", Input: map[string]any{
				"name": "read_file",
				"args": map[string]any{"path": "foo.go", "line_start": 1, "line_end": 50},
			}},
			wantKey: true,
		},
		{
			name: "list_dir is cacheable",
			call: provider.ToolCall{Name: "tool_call", Input: map[string]any{
				"name": "list_dir",
				"args": map[string]any{"path": "internal/"},
			}},
			wantKey: true,
		},
		{
			name: "write_file MUST NOT be cached (side effect)",
			call: provider.ToolCall{Name: "tool_call", Input: map[string]any{
				"name": "write_file",
				"args": map[string]any{"path": "foo.go", "content": "x"},
			}},
			wantKey: false,
		},
		{
			name: "edit_file MUST NOT be cached",
			call: provider.ToolCall{Name: "tool_call", Input: map[string]any{
				"name": "edit_file",
			}},
			wantKey: false,
		},
		{
			name: "run_command MUST NOT be cached (output may change)",
			call: provider.ToolCall{Name: "tool_call", Input: map[string]any{
				"name": "run_command",
			}},
			wantKey: false,
		},
		{
			name: "non-meta-tool name skipped",
			call: provider.ToolCall{Name: "read_file", Input: map[string]any{
				"path": "foo.go",
			}},
			wantKey: false,
		},
		{
			name: "missing inner name → skipped",
			call: provider.ToolCall{Name: "tool_call", Input: map[string]any{
				"args": map[string]any{"path": "foo.go"},
			}},
			wantKey: false,
		},
	}
	for _, c := range cases {
		_, ok := cacheableToolCallKey(c.call)
		if ok != c.wantKey {
			t.Errorf("%s: cacheable=%v, want %v", c.name, ok, c.wantKey)
		}
	}
}

func TestCacheableToolCallKey_StableUnderArgReordering(t *testing.T) {
	// Two calls with the same args in different map iteration order
	// must collapse to the same cache key — Go's map order is
	// nondeterministic so without canonicalization this cache would
	// silently miss on every other revisit.
	a := provider.ToolCall{Name: "tool_call", Input: map[string]any{
		"name": "read_file",
		"args": map[string]any{"path": "foo.go", "line_start": 1, "line_end": 50},
	}}
	b := provider.ToolCall{Name: "tool_call", Input: map[string]any{
		"name": "read_file",
		"args": map[string]any{"line_end": 50, "path": "foo.go", "line_start": 1},
	}}
	keyA, _ := cacheableToolCallKey(a)
	keyB, _ := cacheableToolCallKey(b)
	if keyA == "" || keyB == "" {
		t.Fatalf("both calls should be cacheable: keyA=%q keyB=%q", keyA, keyB)
	}
	if keyA != keyB {
		t.Fatalf("equivalent calls should hash to the same key:\n  a=%q\n  b=%q", keyA, keyB)
	}
}

func TestCacheableToolCallKey_DifferentRangesDifferentKeys(t *testing.T) {
	a := provider.ToolCall{Name: "tool_call", Input: map[string]any{
		"name": "read_file",
		"args": map[string]any{"path": "foo.go", "line_start": 1, "line_end": 50},
	}}
	b := provider.ToolCall{Name: "tool_call", Input: map[string]any{
		"name": "read_file",
		"args": map[string]any{"path": "foo.go", "line_start": 51, "line_end": 100},
	}}
	keyA, _ := cacheableToolCallKey(a)
	keyB, _ := cacheableToolCallKey(b)
	if keyA == keyB {
		t.Fatalf("different ranges must hash differently: both=%q", keyA)
	}
}

func TestExecuteToolCallsParallel_CacheHitSkipsExecution(t *testing.T) {
	eng := newTestEngine(t)
	var counter int32
	registerTestSleepTool(t, eng, "__test_cached_read", 0, &counter)

	// Build a meta-tool wrapper that routes through tool_call(name=...)
	// to the registered backend. Since the cache key is keyed on the
	// inner name and we only whitelist read_file/list_dir/grep_codebase,
	// we re-register the test tool under one of those names.
	registerTestSleepTool(t, eng, "read_file", 0, &counter)

	cache := map[string]string{}
	mu := &sync.Mutex{}

	call := provider.ToolCall{
		ID:   "c1",
		Name: "tool_call",
		Input: map[string]any{
			"name": "read_file",
			"args": map[string]any{"path": "note.txt"},
		},
	}

	// First run: cache miss → tool fires once, result cached.
	results := eng.executeToolCallsParallel(context.Background(), []provider.ToolCall{call}, 1, "agent", cache, mu)
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("first call should succeed, got %+v", results)
	}
	if got := atomic.LoadInt32(&counter); got != 1 {
		t.Fatalf("first call should execute the tool once, counter=%d", got)
	}
	if len(cache) != 1 {
		t.Fatalf("cache should hold one entry after miss+store, got %d", len(cache))
	}

	// Second run with the SAME canonical input: cache hit → tool NOT fired.
	results = eng.executeToolCallsParallel(context.Background(), []provider.ToolCall{call}, 1, "agent", cache, mu)
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("cached call should succeed, got %+v", results)
	}
	if got := atomic.LoadInt32(&counter); got != 1 {
		t.Fatalf("second call should HIT cache (counter unchanged): got %d, want 1", got)
	}
}

func TestExecuteToolCallsParallel_NilCacheStillExecutes(t *testing.T) {
	eng := newTestEngine(t)
	var counter int32
	registerTestSleepTool(t, eng, "__test_no_cache", 0, &counter)

	call := provider.ToolCall{
		ID:    "c1",
		Name:  "__test_no_cache",
		Input: map[string]any{"x": 1},
	}
	// Nil cache: execution proceeds normally (no panic, counter bumps).
	results := eng.executeToolCallsParallel(context.Background(), []provider.ToolCall{call}, 1, "agent", nil, nil)
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if got := atomic.LoadInt32(&counter); got != 1 {
		t.Fatalf("nil cache must still run the tool, got counter=%d", got)
	}
}

func TestResumeMaxMultiplier_DefaultAndOverride(t *testing.T) {
	// Engine with no Config falls back to the default multiplier.
	var nilEng *Engine
	if got := nilEng.resumeMaxMultiplier(); got != defaultResumeMaxMultiplier {
		t.Fatalf("nil engine should return default %d, got %d", defaultResumeMaxMultiplier, got)
	}
	eng := newTestEngine(t)
	if got := eng.resumeMaxMultiplier(); got != defaultResumeMaxMultiplier {
		t.Fatalf("zero config should return default %d, got %d", defaultResumeMaxMultiplier, got)
	}
	eng.Config.Agent.ResumeMaxMultiplier = 25
	if got := eng.resumeMaxMultiplier(); got != 25 {
		t.Fatalf("explicit 25 should pass through, got %d", got)
	}
	eng.Config.Agent.ResumeMaxMultiplier = -3
	if got := eng.resumeMaxMultiplier(); got != defaultResumeMaxMultiplier {
		t.Fatalf("negative value should fall back to default, got %d", got)
	}
}

func TestProactiveCompactRatio_FiresEarlierThanReactive(t *testing.T) {
	// Sanity: the proactive ratio must be strictly LESS than the
	// reactive AutoCompactThresholdRatio default (0.7) so the
	// step-boundary trigger actually pre-empts the budget gate.
	if proactiveCompactRatio >= 0.7 {
		t.Fatalf("proactive ratio %.2f must be < reactive default 0.7", proactiveCompactRatio)
	}
	if proactiveCompactRatio <= 0 {
		t.Fatalf("proactive ratio %.2f must be positive", proactiveCompactRatio)
	}
}
