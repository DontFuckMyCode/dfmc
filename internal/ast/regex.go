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

	// Ruby patterns. Note that `def self.foo` (class method) and
	// `def foo` (instance method) both match -- capture group covers
	// both shapes by anchoring on the trailing identifier.
	reRubyClass  = regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)\b`)
	reRubyModule = regexp.MustCompile(`^\s*module\s+([A-Za-z_]\w*)\b`)
	reRubyDef    = regexp.MustCompile(`^\s*def\s+(?:self\.)?([A-Za-z_][\w?!]*)`)

	// Java patterns. Modifier lists vary widely (public / private /
	// protected / abstract / static / final / synchronized / native /
	// default / strictfp), so the modifier list is matched as zero
	// or more of those keywords separated by whitespace. The method
	// regex deliberately rejects lines that look like declarations
	// without a body (`abstract void foo();`) by NOT requiring a `{`
	// -- callers care about names, not bodies.
	reJavaClass     = regexp.MustCompile(`^\s*(?:public|private|protected|abstract|final|static)?(?:\s+(?:public|private|protected|abstract|final|static))*\s*class\s+([A-Za-z_]\w*)\b`)
	reJavaInterface = regexp.MustCompile(`^\s*(?:public|private|protected|abstract)?(?:\s+(?:public|private|protected|abstract))*\s*interface\s+([A-Za-z_]\w*)\b`)
	reJavaEnum      = regexp.MustCompile(`^\s*(?:public|private|protected)?(?:\s+(?:public|private|protected))*\s*enum\s+([A-Za-z_]\w*)\b`)
	// Java method: a return type (one identifier, possibly with
	// generics `<...>` and `[]` array brackets) then a method name
	// followed by `(`. Constructors are caught too because the
	// "return type" pattern matches the class name standing alone.
	// We anchor on `(` so plain field declarations don't match.
	reJavaMethod = regexp.MustCompile(`^\s*(?:public|private|protected|abstract|static|final|synchronized|native|default|strictfp)(?:\s+(?:public|private|protected|abstract|static|final|synchronized|native|default|strictfp))*\s+(?:<[^>]+>\s+)?[A-Za-z_][\w<>\[\],\s]*\s+([A-Za-z_]\w*)\s*\(`)

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

	// Ruby require shapes. `require_relative "./foo"` and
	// `require "foo"` both end up as the unquoted dependency name in
	// the import list. The captured group strips quotes.
	reRubyRequire = regexp.MustCompile(`^\s*require(?:_relative)?\s+['"]([^'"]+)['"]`)

	// Java package + import statements. Imports may be of the
	// `import some.pkg.*;` form or single-class `import some.pkg.Foo;`;
	// the captured group is everything between `import ` and `;`.
	reJavaImport  = regexp.MustCompile(`^\s*import\s+(?:static\s+)?([A-Za-z0-9_.*]+)\s*;`)
	reJavaPackage = regexp.MustCompile(`^\s*package\s+([A-Za-z0-9_.]+)\s*;`)
)
