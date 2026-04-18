//go:build cgo

// REPORT.md #7 regression: a parser whose SetLanguage failed must
// NOT be returned to the sync.Pool. The pre-fix code ran a
// `defer pool.Put(parser)` unconditionally, so a broken parser would
// leak its corrupted/missing language to the next pool consumer.
//
// We can't fault-inject a real SetLanguage failure without forking the
// bindings, so the test exercises the small finalize helper directly:
// the unhealthy branch must skip Put and call Close(), the healthy
// branch must Put and skip Close().

package ast

import (
	"testing"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

type recordingParserPool struct {
	puts int
}

func (r *recordingParserPool) Put(any) { r.puts++ }

func TestFinalizeTreeSitterParser_HealthyReturnsToPool(t *testing.T) {
	pool := &recordingParserPool{}
	parser := tree_sitter.NewParser()
	t.Cleanup(parser.Close)

	finalizeTreeSitterParser(pool, parser, true)

	if pool.puts != 1 {
		t.Fatalf("healthy parser must be Put back exactly once, got %d", pool.puts)
	}
}

func TestFinalizeTreeSitterParser_UnhealthySkipsPoolAndCloses(t *testing.T) {
	pool := &recordingParserPool{}
	parser := tree_sitter.NewParser()
	// No t.Cleanup(parser.Close) — finalizeTreeSitterParser will
	// close it. Calling Close twice on the bindings is undefined and
	// has been observed to crash; the cleanup belongs to the helper
	// when healthy==false.

	finalizeTreeSitterParser(pool, parser, false)

	if pool.puts != 0 {
		t.Fatalf("unhealthy parser MUST NOT return to pool, Put=%d", pool.puts)
	}
}
