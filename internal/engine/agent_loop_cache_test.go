package engine

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

// toolResult is a tiny test-only constructor for tools.Result. The
// range-merge tests only care about Output and Truncated; the rest of
// the struct (Success, Data, DurationMs) doesn't influence cache logic.
func toolResult(out string, truncated bool) tools.Result {
	return tools.Result{Output: out, Truncated: truncated}
}

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
	results := eng.executeToolCallsParallel(context.Background(), []provider.ToolCall{call}, 1, "agent", cache, mu, nil)
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
	results = eng.executeToolCallsParallel(context.Background(), []provider.ToolCall{call}, 1, "agent", cache, mu, nil)
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("cached call should succeed, got %+v", results)
	}
	if got := atomic.LoadInt32(&counter); got != 1 {
		t.Fatalf("second call should HIT cache (counter unchanged): got %d, want 1", got)
	}
}

// TestInvalidateCacheForFiles_RemovesMatchingPath asserts that the
// invalidator drops entries whose canonical args reference the given
// path, and leaves unrelated entries intact.
func TestInvalidateCacheForFiles_RemovesMatchingPath(t *testing.T) {
	cache := map[string]string{
		"read_file|" + `{"path":"foo.go"}`:    "old foo content",
		"read_file|" + `{"path":"bar.go"}`:    "bar content",
		"list_dir|" + `{"path":"internal"}`:   "listing",
		"grep_codebase|" + `{"q":"foo.go"}`:   "grep result mentioning foo.go via path arg substring",
	}
	mu := &sync.Mutex{}

	invalidateCacheForFiles(cache, mu, []string{"foo.go"}, nil)

	if _, ok := cache["read_file|"+`{"path":"foo.go"}`]; ok {
		t.Error("foo.go read entry should have been invalidated")
	}
	if _, ok := cache["read_file|"+`{"path":"bar.go"}`]; !ok {
		t.Error("bar.go entry should be preserved")
	}
	if _, ok := cache["list_dir|"+`{"path":"internal"}`]; !ok {
		t.Error("unrelated list_dir entry should be preserved")
	}
}

// TestInvalidateCacheForFiles_WildcardWipes asserts that the "*"
// sentinel (used after apply_patch) wipes the entire cache.
func TestInvalidateCacheForFiles_WildcardWipes(t *testing.T) {
	cache := map[string]string{
		"read_file|" + `{"path":"a.go"}`: "a",
		"read_file|" + `{"path":"b.go"}`: "b",
	}
	mu := &sync.Mutex{}

	invalidateCacheForFiles(cache, mu, []string{"*"}, nil)

	if len(cache) != 0 {
		t.Errorf("wildcard must wipe all entries, %d remain", len(cache))
	}
}

// TestExtractModifiedPath_RecognisesWriteTools pins the per-tool
// extraction logic the batch invalidator relies on.
func TestExtractModifiedPath_RecognisesWriteTools(t *testing.T) {
	cases := []struct {
		name string
		call provider.ToolCall
		want string
	}{
		{
			name: "edit_file via meta",
			call: provider.ToolCall{Name: "tool_call", Input: map[string]any{
				"name": "edit_file",
				"args": map[string]any{"path": "internal/x.go"},
			}},
			want: "internal/x.go",
		},
		{
			name: "write_file via meta",
			call: provider.ToolCall{Name: "tool_call", Input: map[string]any{
				"name": "write_file",
				"args": map[string]any{"path": "new.go"},
			}},
			want: "new.go",
		},
		{
			name: "apply_patch wildcard",
			call: provider.ToolCall{Name: "tool_call", Input: map[string]any{
				"name": "apply_patch",
				"args": map[string]any{"patch": "..."},
			}},
			want: "*",
		},
		{
			name: "read_file is not a write",
			call: provider.ToolCall{Name: "tool_call", Input: map[string]any{
				"name": "read_file",
				"args": map[string]any{"path": "x.go"},
			}},
			want: "",
		},
		{
			name: "non-meta call",
			call: provider.ToolCall{Name: "edit_file", Input: map[string]any{"path": "x"}},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractModifiedPath(tc.call); got != tc.want {
				t.Errorf("extractModifiedPath = %q, want %q", got, tc.want)
			}
		})
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
	results := eng.executeToolCallsParallel(context.Background(), []provider.ToolCall{call}, 1, "agent", nil, nil, nil)
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

// TestExtractReadRangeRequest_AcceptsCanonicalShape covers the happy
// path the range index relies on: a tool_call envelope wrapping
// read_file with normalized line bounds.
func TestExtractReadRangeRequest_AcceptsCanonicalShape(t *testing.T) {
	call := provider.ToolCall{Name: "tool_call", Input: map[string]any{
		"name": "read_file",
		"args": map[string]any{"path": "foo.go", "line_start": 1, "line_end": 50},
	}}
	path, start, end, ok := extractReadRangeRequest(call)
	if !ok {
		t.Fatal("expected ok=true on canonical read_file tool_call")
	}
	if path != "foo.go" || start != 1 || end != 50 {
		t.Fatalf("got path=%q start=%d end=%d", path, start, end)
	}
}

func TestExtractReadRangeRequest_RejectsNonReadAndMissingFields(t *testing.T) {
	cases := []struct {
		name string
		call provider.ToolCall
	}{
		{
			name: "non-read tool",
			call: provider.ToolCall{Name: "tool_call", Input: map[string]any{
				"name": "list_dir",
				"args": map[string]any{"path": "foo", "line_start": 1, "line_end": 10},
			}},
		},
		{
			name: "missing path",
			call: provider.ToolCall{Name: "tool_call", Input: map[string]any{
				"name": "read_file",
				"args": map[string]any{"line_start": 1, "line_end": 10},
			}},
		},
		{
			name: "inverted range",
			call: provider.ToolCall{Name: "tool_call", Input: map[string]any{
				"name": "read_file",
				"args": map[string]any{"path": "foo.go", "line_start": 10, "line_end": 5},
			}},
		},
		{
			name: "non-meta call",
			call: provider.ToolCall{Name: "read_file", Input: map[string]any{
				"path": "foo.go", "line_start": 1, "line_end": 10,
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, ok := extractReadRangeRequest(tc.call); ok {
				t.Errorf("expected ok=false for %s", tc.name)
			}
		})
	}
}

// TestSliceReadEntry_HitsAndMisses asserts the slicing math: a request
// fully inside the recorded entry must return exactly the requested
// lines; anything outside or partially overlapping must refuse rather
// than ship wrong content.
func TestSliceReadEntry_HitsAndMisses(t *testing.T) {
	entry := readRangeEntry{
		start:   1,
		end:     5,
		content: "line1\nline2\nline3\nline4\nline5",
	}
	cases := []struct {
		name      string
		reqStart  int
		reqEnd    int
		wantOK    bool
		wantLines []string
	}{
		{"exact match", 1, 5, true, []string{"line1", "line2", "line3", "line4", "line5"}},
		{"middle slice", 2, 4, true, []string{"line2", "line3", "line4"}},
		{"single line", 3, 3, true, []string{"line3"}},
		{"start before entry", 0, 3, false, nil},
		{"end past entry", 3, 7, false, nil},
		{"both outside", 6, 9, false, nil},
		{"inverted range", 4, 2, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := sliceReadEntry(entry, tc.reqStart, tc.reqEnd)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if got != strings.Join(tc.wantLines, "\n") {
				t.Errorf("got %q, want %q", got, strings.Join(tc.wantLines, "\n"))
			}
		})
	}
}

// TestLookupToolCache_RangeMergeHit asserts that a sub-range request
// is served by slicing a previously-cached wider read, without a fresh
// dispatch. This is the whole point of the range index — re-reading
// "lines 5-15" after a cached "lines 1-50" should NOT round-trip the
// tool again.
func TestLookupToolCache_RangeMergeHit(t *testing.T) {
	cache := map[string]string{}
	mu := &sync.Mutex{}
	rangeIndex := map[string][]readRangeEntry{}

	// Seed with a wide read covering lines 1-5.
	wideCall := provider.ToolCall{Name: "tool_call", Input: map[string]any{
		"name": "read_file",
		"args": map[string]any{"path": "foo.go", "line_start": 1, "line_end": 5},
	}}
	wideContent := "alpha\nbeta\ngamma\ndelta\nepsilon"
	storeToolCache(wideCall, toolResult(wideContent, false), cache, mu, rangeIndex)

	// Sub-range request for lines 2-4 should hit by slicing.
	subCall := provider.ToolCall{Name: "tool_call", Input: map[string]any{
		"name": "read_file",
		"args": map[string]any{"path": "foo.go", "line_start": 2, "line_end": 4},
	}}
	got, ok := lookupToolCache(subCall, cache, mu, rangeIndex)
	if !ok {
		t.Fatal("sub-range request should hit the range index")
	}
	if got.Output != "beta\ngamma\ndelta" {
		t.Fatalf("expected sliced content, got %q", got.Output)
	}
}

// TestLookupToolCache_RangeMergeMiss covers the negative path: a
// request that isn't fully covered by any cached entry must miss so
// the loop dispatches a fresh tool call.
func TestLookupToolCache_RangeMergeMiss(t *testing.T) {
	cache := map[string]string{}
	mu := &sync.Mutex{}
	rangeIndex := map[string][]readRangeEntry{}

	wideCall := provider.ToolCall{Name: "tool_call", Input: map[string]any{
		"name": "read_file",
		"args": map[string]any{"path": "foo.go", "line_start": 1, "line_end": 5},
	}}
	storeToolCache(wideCall, toolResult("a\nb\nc\nd\ne", false), cache, mu, rangeIndex)

	// Asking for lines 10-20 — outside the cached window. No exact key
	// match either, so this is a clean miss.
	farCall := provider.ToolCall{Name: "tool_call", Input: map[string]any{
		"name": "read_file",
		"args": map[string]any{"path": "foo.go", "line_start": 10, "line_end": 20},
	}}
	if _, ok := lookupToolCache(farCall, cache, mu, rangeIndex); ok {
		t.Fatal("out-of-window request must miss the range index")
	}
}

// TestStoreToolCache_TruncatedSkipsRangeIndex pins the contract that
// truncated reads are exact-key cached only — the range index would
// ship wrong line counts if it sliced past a truncation marker.
func TestStoreToolCache_TruncatedSkipsRangeIndex(t *testing.T) {
	cache := map[string]string{}
	mu := &sync.Mutex{}
	rangeIndex := map[string][]readRangeEntry{}

	call := provider.ToolCall{Name: "tool_call", Input: map[string]any{
		"name": "read_file",
		"args": map[string]any{"path": "foo.go", "line_start": 1, "line_end": 200},
	}}
	storeToolCache(call, toolResult("line1\nline2\n... [truncated]", true), cache, mu, rangeIndex)

	if len(rangeIndex) != 0 {
		t.Fatalf("truncated read must NOT populate range index, got %d buckets", len(rangeIndex))
	}
	// Exact-key cache should still hold the entry.
	if len(cache) != 1 {
		t.Fatalf("exact-key cache should hold one entry, got %d", len(cache))
	}
}

// TestInvalidateCacheForFiles_ClearsRangeIndex asserts that a write
// to a path drops every range entry under that path, so a follow-up
// read after the edit doesn't serve stale content.
func TestInvalidateCacheForFiles_ClearsRangeIndex(t *testing.T) {
	cache := map[string]string{}
	mu := &sync.Mutex{}
	rangeIndex := map[string][]readRangeEntry{
		"foo.go": {{start: 1, end: 50, content: "old contents"}},
		"bar.go": {{start: 1, end: 30, content: "unrelated"}},
	}

	invalidateCacheForFiles(cache, mu, []string{"foo.go"}, rangeIndex)

	if _, ok := rangeIndex["foo.go"]; ok {
		t.Error("foo.go range bucket should have been dropped")
	}
	if _, ok := rangeIndex["bar.go"]; !ok {
		t.Error("bar.go range bucket should be preserved")
	}
}

// TestInvalidateCacheForFiles_WildcardClearsRangeIndex asserts the "*"
// sentinel wipes the entire range index alongside the exact-key cache.
func TestInvalidateCacheForFiles_WildcardClearsRangeIndex(t *testing.T) {
	cache := map[string]string{}
	mu := &sync.Mutex{}
	rangeIndex := map[string][]readRangeEntry{
		"a.go": {{start: 1, end: 10, content: "a"}},
		"b.go": {{start: 1, end: 10, content: "b"}},
	}

	invalidateCacheForFiles(cache, mu, []string{"*"}, rangeIndex)

	if len(rangeIndex) != 0 {
		t.Fatalf("wildcard wipe must drop all range buckets, %d remain", len(rangeIndex))
	}
}

// TestStoreToolCache_RangeIndexBucketCap asserts the per-path bucket
// is bounded by maxRangeEntriesPerPath. Without the cap, a long loop
// reading many overlapping windows of one file would grow the slice
// unboundedly. FIFO eviction means the newest entry stays — empirically
// it has the best hit rate against the next request.
func TestStoreToolCache_RangeIndexBucketCap(t *testing.T) {
	cache := map[string]string{}
	mu := &sync.Mutex{}
	rangeIndex := map[string][]readRangeEntry{}

	// Push (cap+5) distinct windows of foo.go through storeToolCache.
	overflow := 5
	for i := 0; i < maxRangeEntriesPerPath+overflow; i++ {
		start := i*10 + 1
		end := start + 9
		call := provider.ToolCall{Name: "tool_call", Input: map[string]any{
			"name": "read_file",
			"args": map[string]any{
				"path":       "foo.go",
				"line_start": start,
				"line_end":   end,
			},
		}}
		// Distinct content per range so we can identify which entries
		// survived eviction.
		content := strings.Repeat("x\n", 9) + "x"
		storeToolCache(call, toolResult(content, false), cache, mu, rangeIndex)
	}

	bucket := rangeIndex[readRangeIndexKey("foo.go")]
	if got := len(bucket); got != maxRangeEntriesPerPath {
		t.Fatalf("bucket should be capped at %d, got %d", maxRangeEntriesPerPath, got)
	}
	// FIFO: the oldest `overflow` entries should be gone — first
	// surviving entry should start at line (overflow*10 + 1).
	wantFirstStart := overflow*10 + 1
	if bucket[0].start != wantFirstStart {
		t.Errorf("expected oldest survivor to start at %d, got %d", wantFirstStart, bucket[0].start)
	}
	// And the newest entry should be the very last write.
	wantLastStart := (maxRangeEntriesPerPath+overflow-1)*10 + 1
	if bucket[len(bucket)-1].start != wantLastStart {
		t.Errorf("expected newest entry to start at %d, got %d", wantLastStart, bucket[len(bucket)-1].start)
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
