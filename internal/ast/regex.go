package ast

import (
	"regexp"
)

// Pre-compiled regex patterns for symbol extraction — hoisted from function
// scope to package level so they are compiled exactly once, not on every call.

// JavaScript / TypeScript patterns
var (
	reJSFunc       = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?function\s+([A-Za-z_]\w*)\s*\(`)
	reJSClass      = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_]\w*)\b`)
	reJSInterface  = regexp.MustCompile(`^\s*(?:export\s+)?interface\s+([A-Za-z_]\w*)\b`)
	reJSType       = regexp.MustCompile(`^\s*(?:export\s+)?type\s+([A-Za-z_]\w*)\b`)
	reJSEnum       = regexp.MustCompile(`^\s*(?:export\s+)?const\s+enum\s+([A-Za-z_]\w*)\b|^\s*(?:export\s+)?enum\s+([A-Za-z_]\w*)\b`)
	reJSConstArrow = regexp.MustCompile(`^\s*(?:export\s+)?const\s+([A-Za-z_]\w*)\s*=\s*(?:async\s*)?(?:\([^)]*\)|[A-Za-z_]\w*)\s*=>`)

	// Python patterns
	rePyAsyncFunc = regexp.MustCompile(`^\s*async\s+def\s+([A-Za-z_]\w*)\s*\(`)
	rePyFunc      = regexp.MustCompile(`^\s*def\s+([A-Za-z_]\w*)\s*\(`)
	rePyClass     = regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)\s*[:(]`)

	// Rust patterns
	reRustFunc   = regexp.MustCompile(`^\s*(?:pub\s+)?fn\s+([A-Za-z_]\w*)\s*\(`)
	reRustStruct = regexp.MustCompile(`^\s*(?:pub\s+)?struct\s+([A-Za-z_]\w*)\b`)
	reRustEnum   = regexp.MustCompile(`^\s*(?:pub\s+)?enum\s+([A-Za-z_]\w*)\b`)
	reRustTrait  = regexp.MustCompile(`^\s*(?:pub\s+)?trait\s+([A-Za-z_]\w*)\b`)

	// Import regexes for the extractImports fallback path (JS/TS, Python,
	// Rust). Hoisted alongside the symbol regexes above; pre-fix these
	// were rebuilt on every extractImports call, multiplying the
	// regex-compile cost on !cgo builds and on tree-sitter parse-failure
	// fallbacks.
	reJSImport   = regexp.MustCompile(`^\s*import\s+.*from\s+['"]([^'"]+)['"]`)
	reJSRequire  = regexp.MustCompile(`require\(['"]([^'"]+)['"]\)`)
	rePyImport   = regexp.MustCompile(`^\s*import\s+([A-Za-z0-9_\.]+)`)
	rePyFrom     = regexp.MustCompile(`^\s*from\s+([A-Za-z0-9_\.]+)\s+import`)
	reRustUseDep = regexp.MustCompile(`^\s*use\s+([A-Za-z0-9_:]+)`)
)
