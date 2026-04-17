// Pin tests for AST language detection (extension map + shebang
// fallback) and content hashing. The CGO tree-sitter / regex backend
// switching is exercised by engine_test.go's parse round-trip; this
// file targets the lower-level helpers that drive the cache key and
// the language router.

package ast

import (
	"context"
	"strings"
	"testing"
)

// detectLanguage must:
//  1. recognise extensions (.go → "go", .py → "python", etc.)
//  2. recognise filename-only matches (Dockerfile → "dockerfile")
//  3. fall back to the shebang for python/node/bash scripts that
//     lack a known extension
//  4. return "" for unknown — the engine uses that to surface a
//     "language not supported" error rather than guessing
func TestDetectLanguage_ExtensionMap(t *testing.T) {
	e := New()
	cases := []struct {
		path string
		want string
	}{
		{"main.go", "go"},
		{"index.ts", "typescript"},
		{"app.tsx", "tsx"},
		{"script.js", "javascript"},
		{"module.mjs", "javascript"},
		{"common.cjs", "javascript"},
		{"app.py", "python"},
		{"lib.rs", "rust"},
		{"Main.java", "java"},
		{"Program.cs", "csharp"},
		{"index.php", "php"},
		{"script.sh", "bash"},
		{"playbook.yaml", "yaml"},
		{"Dockerfile", "dockerfile"},
		{"Containerfile", "dockerfile"},
	}
	for _, tc := range cases {
		got := e.detectLanguage(tc.path, nil)
		if got != tc.want {
			t.Errorf("detectLanguage(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestDetectLanguage_ShebangFallback(t *testing.T) {
	e := New()
	cases := []struct {
		name    string
		path    string
		content string
		want    string
	}{
		{"python shebang", "noext_script", "#!/usr/bin/env python\nprint('hi')\n", "python"},
		{"python3 shebang", "tool", "#!/usr/bin/python3\n", "python"},
		{"node shebang", "cli", "#!/usr/bin/env node\nconsole.log()\n", "javascript"},
		{"bash shebang", "deploy", "#!/usr/bin/env bash\nls\n", "bash"},
		{"sh shebang (matched via /sh suffix)", "boot", "#!/bin/sh\nls\n", "bash"},
	}
	for _, tc := range cases {
		got := e.detectLanguage(tc.path, []byte(tc.content))
		if got != tc.want {
			t.Errorf("%s: detectLanguage(%q, shebang) = %q, want %q", tc.name, tc.path, got, tc.want)
		}
	}
}

// Shebang detection must not fire on files that happen to start with
// '#' for reasons other than a hashbang (e.g. markdown headers).
func TestDetectLanguage_NotMisledByHash(t *testing.T) {
	e := New()
	got := e.detectLanguage("README", []byte("# Title\n\nbody\n"))
	if got != "" {
		t.Fatalf("markdown header should not match shebang heuristic; got %q", got)
	}
}

// Empty / very-short content must not panic in the shebang branch
// (which indexes content[0] and content[1]).
func TestDetectLanguage_EmptyContentSafe(t *testing.T) {
	e := New()
	if got := e.detectLanguage("nope", nil); got != "" {
		t.Fatalf("empty content + unknown ext should return ''; got %q", got)
	}
	if got := e.detectLanguage("nope", []byte{'#'}); got != "" {
		t.Fatalf("single-byte content should return ''; got %q", got)
	}
	if got := e.detectLanguage("nope", []byte("#!")); got != "" {
		t.Fatalf("bare shebang prefix without language token should return ''; got %q", got)
	}
}

// hashContent must be deterministic and stable across calls. The
// parse cache uses this as part of the cache key — a non-deterministic
// hash would make the cache useless and balloon parse cost.
func TestHashContent_Deterministic(t *testing.T) {
	body := []byte("package main\n\nfunc main() {}\n")
	h1 := hashContent(body)
	h2 := hashContent(body)
	if h1 != h2 {
		t.Fatalf("hashContent should be deterministic; got %d vs %d", h1, h2)
	}
	if h1 == 0 {
		t.Fatalf("hashContent of non-empty input should not be zero")
	}
}

// hashContent must distinguish slightly different content (cache
// invalidation precision). FNV-64a collisions on small inputs are
// astronomically rare; this just pins that we're hashing, not
// returning a constant.
func TestHashContent_DiscriminatesContent(t *testing.T) {
	a := hashContent([]byte("package main\n"))
	b := hashContent([]byte("package main\n\nfunc x() {}\n"))
	if a == b {
		t.Fatalf("different content produced identical hash; cache invalidation broken")
	}
}

// hashContent on empty/nil must not panic and must return a stable
// zero-input value. parseContent treats this as "valid empty file"
// not "unhashed".
func TestHashContent_Empty(t *testing.T) {
	hNil := hashContent(nil)
	hEmpty := hashContent([]byte{})
	if hNil != hEmpty {
		t.Fatalf("nil and empty []byte should hash identically; got %d vs %d", hNil, hEmpty)
	}
	// FNV-64a's offset basis is 14695981039346656037; nil/empty input
	// should hash to that constant.
	const fnv64aOffset uint64 = 14695981039346656037
	if hNil != fnv64aOffset {
		t.Fatalf("empty input should yield FNV-64a offset basis %d; got %d", fnv64aOffset, hNil)
	}
}

// Round-trip: ParseContent on the same input twice must hit the
// cache the second time. The parse-metrics tracker is the source of
// truth for cache hit/miss; pin that the second parse increments
// the hit counter.
func TestParseContent_CacheHitOnSecondCall(t *testing.T) {
	e := New()
	body := []byte("package main\n\nfunc Hello() string { return \"hi\" }\n")
	ctx := context.Background()

	r1, err := e.ParseContent(ctx, "hello.go", body)
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}
	if len(r1.Symbols) == 0 {
		t.Fatalf("expected at least one symbol from Hello func; got 0")
	}

	r2, err := e.ParseContent(ctx, "hello.go", body)
	if err != nil {
		t.Fatalf("second parse: %v", err)
	}
	// Cached result must carry the same hash (deterministic).
	if r1.Hash != r2.Hash {
		t.Fatalf("cached parse should match hash; got %d vs %d", r1.Hash, r2.Hash)
	}

	// The metrics snapshot should report at least 1 hit by now.
	m := e.metrics.snapshot()
	if m.CacheHits == 0 {
		t.Fatalf("expected cache hit recorded after re-parse; got %+v", m)
	}
}

// Unsupported language path must surface a useful error and increment
// the unsupported counter. Pin the error shape so tooling that greps
// for "unsupported" keeps working.
func TestParseContent_UnsupportedLanguage(t *testing.T) {
	e := New()
	_, err := e.ParseContent(context.Background(), "weird.unknownext", []byte("data"))
	if err == nil {
		t.Fatalf("expected error for unsupported language")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unsupported") {
		t.Fatalf("error should mention 'unsupported'; got %q", err.Error())
	}
}

// Cancelled context must propagate before we touch the file. The
// agent loop relies on this to abort mid-pass without dragging the
// last in-flight parse to completion.
func TestParseContent_RespectsCancelledContext(t *testing.T) {
	e := New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err := e.ParseContent(ctx, "main.go", []byte("package main\n"))
	if err == nil {
		t.Fatalf("expected context.Canceled error")
	}
	if !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "context") {
		t.Fatalf("error should mention canceled/context; got %q", err.Error())
	}
}
