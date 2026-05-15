// Import alias resolution (dfmc_report_ast.md §R12).
//
// The existing extractImports captures the module / package path
// strings as a flat []string. That's enough for "what does this
// file depend on?" questions but loses the LOCAL binding name --
// the identifier the surrounding code actually uses to reach the
// imported symbol. For example:
//
//	from os import path as p
//	x = p.join("/", "a")
//
// extractImports returns ["os"] -- correct as a dependency, but a
// downstream call-graph builder that sees `p.join(...)` has no way
// to learn that `p` is really `os.path`. ImportAliases keeps that
// link by recording (module, symbol, local) per import binding.
//
// First cut covers Python and JS / TS (where renaming is common)
// and Rust (where `use foo::bar as baz` is the idiom). Java doesn't
// have import aliases so it's intentionally omitted -- adding a
// no-op case would just inflate the switch. Go is handled by the
// Go-specific extractor and falls back to the empty-slice default
// since Go's `import "pkg"` form has no rename in the syntax most
// callers reach for (the optional `alias "pkg"` form is rare and
// covered by a dedicated entry below).

package ast

import (
	"regexp"
	"strings"
)

// ImportAlias is one local binding produced by an import / require /
// use statement. The fields capture:
//
//   - Module: the module path / package identifier as imported. For
//     `from os import path as p` this is "os"; for
//     `import { foo as bar } from 'pkg'` it's "pkg".
//
//   - Symbol: the imported member name, BEFORE rename. For
//     `from os import path as p` it's "path"; for plain
//     `import os` it's empty (the whole module is the binding).
//     For star imports / re-exports we use "*".
//
//   - Local: the identifier the surrounding file uses. For
//     `from os import path as p` this is "p"; for plain
//     `import os` it's "os".
//
// Callers that resolve `p.join(...)` look up `p` in Local and find
// (Module="os", Symbol="path"), then know `p.join` is actually
// `os.path.join`.
type ImportAlias struct {
	Module string `json:"module"`
	Symbol string `json:"symbol,omitempty"`
	Local  string `json:"local"`
}

// extractImportAliases returns the per-binding alias table for a
// file. The flat []string from extractImports is a strict subset
// (just the Module values, deduplicated); callers that need the
// alias mapping should use this. Returns nil for languages without
// alias-resolution support so the JSON field stays absent.
func extractImportAliases(lang string, content []byte) []ImportAlias {
	lines := strings.Split(string(content), "\n")
	switch lang {
	case "python":
		return extractPythonImportAliases(lines)
	case "javascript", "typescript", "jsx", "tsx":
		return extractJSImportAliases(lines)
	case "rust":
		return extractRustImportAliases(lines)
	case "go":
		return extractGoImportAliases(lines)
	}
	return nil
}

// --- Python -----------------------------------------------------------

// rePyImportAlias matches `import X [as Y]` and `import X.Y.Z [as W]`,
// with optional alias. Comma-separated imports (`import os, sys`)
// are handled by splitting the captured group manually -- the regex
// returns the FULL list after `import` so we can walk it.
var (
	rePyImportLine = regexp.MustCompile(`^\s*import\s+(.+?)\s*(?:#.*)?$`)
	rePyFromLine   = regexp.MustCompile(`^\s*from\s+([A-Za-z0-9_.]+)\s+import\s+(.+?)\s*(?:#.*)?$`)
)

func extractPythonImportAliases(lines []string) []ImportAlias {
	var out []ImportAlias
	for _, line := range lines {
		if m := rePyFromLine.FindStringSubmatch(line); m != nil {
			module := m[1]
			// The imports list can be `path`, `path as p`,
			// `path, sep`, `path as p, sep as s`, or
			// parenthesised across lines `( path, sep )`. We
			// don't follow continuations -- the regex only sees
			// one line -- but the common case is single-line.
			body := strings.Trim(m[2], "() ")
			for _, raw := range strings.Split(body, ",") {
				name, local := parseAsAlias(raw)
				if name == "" {
					continue
				}
				out = append(out, ImportAlias{
					Module: module,
					Symbol: name,
					Local:  local,
				})
			}
			continue
		}
		if m := rePyImportLine.FindStringSubmatch(line); m != nil {
			// `import os` / `import os, sys` / `import os as o`.
			body := strings.TrimSpace(m[1])
			for _, raw := range strings.Split(body, ",") {
				module, local := parseAsAlias(raw)
				if module == "" {
					continue
				}
				// For `import X [as Y]` the Symbol is empty --
				// the whole module is the binding. Local is the
				// alias if present, else the module name itself.
				if local == "" || local == module {
					local = lastDottedSegment(module)
				}
				out = append(out, ImportAlias{
					Module: module,
					Symbol: "",
					Local:  local,
				})
			}
		}
	}
	return out
}

// --- JavaScript / TypeScript ------------------------------------------

var (
	// `import X from 'pkg'` (default), `import * as ns from 'pkg'`,
	// `import { a, b as c } from 'pkg'`, and combinations like
	// `import X, { a, b } from 'pkg'`. The body capture covers
	// everything between `import` and `from`; we parse it manually.
	reJSImportLine = regexp.MustCompile(`^\s*import\s+(.+?)\s+from\s+['"]([^'"]+)['"]`)
	// `const X = require('pkg')` and `const { a, b } = require('pkg')`.
	reJSRequireLine = regexp.MustCompile(`^\s*(?:const|let|var)\s+(.+?)\s*=\s*require\(\s*['"]([^'"]+)['"]\s*\)`)
)

func extractJSImportAliases(lines []string) []ImportAlias {
	var out []ImportAlias
	for _, line := range lines {
		if m := reJSImportLine.FindStringSubmatch(line); m != nil {
			out = append(out, parseJSImportBody(m[1], m[2])...)
			continue
		}
		if m := reJSRequireLine.FindStringSubmatch(line); m != nil {
			out = append(out, parseJSRequireBody(m[1], m[2])...)
		}
	}
	return out
}

// parseJSImportBody handles the LHS of an `import ... from "pkg"`.
// Recognised shapes (the union of common ESM forms):
//
//	X                         -> default import: {pkg, "default", X}
//	* as ns                   -> namespace import: {pkg, "*", ns}
//	{ a, b as c }             -> named imports: {pkg, "a", "a"}, {pkg, "b", "c"}
//	X, { a }                  -> default + named: both entries
//	X, * as ns                -> default + namespace: both entries
func parseJSImportBody(body, module string) []ImportAlias {
	body = strings.TrimSpace(body)
	var out []ImportAlias
	// Split into pieces around the brace group so default/namespace
	// imports next to named imports are handled.
	for _, segment := range splitImportSegments(body) {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		switch {
		case strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}"):
			inner := strings.TrimSpace(segment[1 : len(segment)-1])
			for _, raw := range strings.Split(inner, ",") {
				name, local := parseAsAlias(raw)
				if name == "" {
					continue
				}
				out = append(out, ImportAlias{
					Module: module,
					Symbol: name,
					Local:  local,
				})
			}
		case strings.HasPrefix(segment, "*"):
			// `* as ns`.
			local := strings.TrimSpace(strings.TrimPrefix(segment, "*"))
			local = strings.TrimSpace(strings.TrimPrefix(local, "as"))
			if local == "" {
				continue
			}
			out = append(out, ImportAlias{
				Module: module,
				Symbol: "*",
				Local:  local,
			})
		default:
			// Plain identifier == default import.
			out = append(out, ImportAlias{
				Module: module,
				Symbol: "default",
				Local:  segment,
			})
		}
	}
	return out
}

// splitImportSegments splits an import-body at the top-level commas
// but treats `{ ... }` as a single segment. Otherwise
// `X, { a, b }` would split into "X", "{ a", "b }".
func splitImportSegments(body string) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, body[start:i])
				start = i + 1
			}
		}
	}
	if start < len(body) {
		out = append(out, body[start:])
	}
	return out
}

// parseJSRequireBody handles the LHS of `const X = require("pkg")`
// or `const { a, b: c } = require("pkg")`.
func parseJSRequireBody(body, module string) []ImportAlias {
	body = strings.TrimSpace(body)
	if strings.HasPrefix(body, "{") && strings.HasSuffix(body, "}") {
		inner := strings.TrimSpace(body[1 : len(body)-1])
		var out []ImportAlias
		for _, raw := range strings.Split(inner, ",") {
			// Destructure rename uses `:` not `as` in JS.
			raw = strings.TrimSpace(raw)
			name, local := parseDestructureRename(raw)
			if name == "" {
				continue
			}
			out = append(out, ImportAlias{
				Module: module,
				Symbol: name,
				Local:  local,
			})
		}
		return out
	}
	// `const X = require("pkg")` -- whole-module binding.
	if body == "" {
		return nil
	}
	return []ImportAlias{{
		Module: module,
		Symbol: "",
		Local:  body,
	}}
}

// parseDestructureRename splits a destructure entry like `body` or
// `body: b`. Returns (sourceName, localName); local defaults to
// source when no rename is present.
func parseDestructureRename(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	if colon := strings.Index(raw, ":"); colon >= 0 {
		name := strings.TrimSpace(raw[:colon])
		local := strings.TrimSpace(raw[colon+1:])
		return name, local
	}
	return raw, raw
}

// --- Rust -------------------------------------------------------------

// `use foo::bar::baz` -> Module="foo::bar", Symbol="baz", Local="baz"
// `use foo::bar as b` -> Module="foo", Symbol="bar", Local="b"
// `use foo::{a, b as c}` is the brace-group form; we handle the
// common single-symbol cases first and skip braces for now.
var reRustUseLine = regexp.MustCompile(`^\s*(?:pub\s+)?use\s+([A-Za-z0-9_:]+)(?:\s+as\s+([A-Za-z_]\w*))?\s*;`)

func extractRustImportAliases(lines []string) []ImportAlias {
	var out []ImportAlias
	for _, line := range lines {
		m := reRustUseLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		full := m[1]
		alias := m[2]
		var module, symbol string
		if idx := strings.LastIndex(full, "::"); idx >= 0 {
			module = full[:idx]
			symbol = full[idx+2:]
		} else {
			module = ""
			symbol = full
		}
		local := alias
		if local == "" {
			local = symbol
		}
		out = append(out, ImportAlias{
			Module: module,
			Symbol: symbol,
			Local:  local,
		})
	}
	return out
}

// --- Go ---------------------------------------------------------------

// `import alias "pkg"` -- the rare-but-real alias form. Plain
// `import "pkg"` produces a local binding equal to the package's
// last path segment, which we can't always determine without a
// build; we record what the line tells us and let downstream
// callers infer.
var reGoImportAlias = regexp.MustCompile(`^\s*(?:_|\.|[A-Za-z_]\w*)?\s*"([^"]+)"`)

func extractGoImportAliases(lines []string) []ImportAlias {
	var out []ImportAlias
	inBlock := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "import (") {
			inBlock = true
			continue
		}
		if inBlock && trimmed == ")" {
			inBlock = false
			continue
		}
		var body string
		switch {
		case inBlock:
			body = trimmed
		case strings.HasPrefix(trimmed, "import "):
			body = strings.TrimSpace(strings.TrimPrefix(trimmed, "import"))
		default:
			continue
		}
		if body == "" {
			continue
		}
		// Parse "alias" and "path" from the body. The body is
		// either `"pkg/path"` or `alias "pkg/path"` or `. "pkg"`
		// or `_ "pkg"`.
		alias := ""
		// Find the opening quote.
		quote := strings.Index(body, `"`)
		if quote < 0 {
			continue
		}
		if quote > 0 {
			alias = strings.TrimSpace(body[:quote])
		}
		// Trailing path.
		closeQ := strings.Index(body[quote+1:], `"`)
		if closeQ < 0 {
			continue
		}
		path := body[quote+1 : quote+1+closeQ]
		module := path
		local := alias
		if local == "" {
			// Inferred local: last segment of the import path.
			local = lastSlashSegment(path)
		} else if local == "_" || local == "." {
			// Blank / dot import: keep the marker verbatim so callers
			// can distinguish from a normal binding.
		}
		out = append(out, ImportAlias{
			Module: module,
			Symbol: "",
			Local:  local,
		})
	}
	return out
}

// --- shared helpers ---------------------------------------------------

// parseAsAlias splits a fragment like `os` / `os as o` / `path as p`
// into the source name and the local-binding name. When no alias
// is present, local defaults to the source name.
func parseAsAlias(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ""
	}
	parts := strings.Fields(raw)
	if len(parts) >= 3 && parts[1] == "as" {
		return parts[0], parts[2]
	}
	return parts[0], parts[0]
}

// lastDottedSegment returns the trailing segment of a dotted module
// path, used as the implicit local binding for plain `import os.path`
// (where `os` is the local binding) vs `import os.path as p`.
func lastDottedSegment(s string) string {
	if idx := strings.Index(s, "."); idx >= 0 {
		return s[:idx]
	}
	return s
}

// lastSlashSegment returns the trailing segment of a slash-separated
// Go-style import path. `internal/foo/bar` -> `bar`. Used to infer a
// Go import's local binding when no explicit alias is given.
func lastSlashSegment(s string) string {
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		return s[idx+1:]
	}
	return s
}
