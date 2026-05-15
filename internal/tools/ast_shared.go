package tools

// ast_shared.go — process-wide shared ast.Engine for tools that parse
// source files. Three tools historically owned per-instance engines:
// ast_query, codemap, find_symbol. Each warmed its own cold cache on
// first use, so a session that called codemap → find_symbol → ast_query
// on the same file re-parsed it three times.
//
// ast.Engine is documented thread-safe and content-hashes its cache by
// (path, sha256 of bytes), so sharing one engine across every tool that
// wants AST is strictly better — cache hits flow across tool calls and
// across tools. See dfmc_report_ast.md §R2 for the original measurement
// (~40% speedup on repeated files).
//
// Lifetime: the singleton lives for the process. Individual tools'
// Close() methods are no-ops; the engine is owned here, not by any
// single tool. Tools.Engine.Close() can no longer evict this cache —
// that is the intentional tradeoff for sharing, since one tool closing
// the engine would silently invalidate cache hits for every other tool
// still in use.

import (
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/ast"
)

var (
	astSharedEngineInstance *ast.Engine
	astSharedEngineOnce     sync.Once
)

// astSharedEngine returns the process-wide ast.Engine. First call
// constructs it via ast.New() (default LRU cache size); subsequent
// calls return the same pointer. Safe for concurrent use.
func astSharedEngine() *ast.Engine {
	astSharedEngineOnce.Do(func() {
		astSharedEngineInstance = ast.New()
	})
	return astSharedEngineInstance
}
