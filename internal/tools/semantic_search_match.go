package tools

// semantic_search_match.go — parsedQuery + parseQuery + the kind/name
// classifiers that decide whether a parsed AST symbol matches the
// caller's pattern. Sibling to semantic_search.go which keeps the
// SemanticSearchTool surface, ToolSpec, Execute pipeline, per-file
// search loop, and the language-walk helpers.

import (
	"fmt"
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// parsedQuery holds a parsed pattern query.
type parsedQuery struct {
	nodeType   string
	name       string
	typeFilter string
	context    int
}

func parseQuery(q string) parsedQuery {
	q = strings.TrimSpace(q)
	var pq parsedQuery
	pq.context = 0

	parts := strings.Split(q, ":")
	pq.nodeType = strings.TrimSpace(parts[0])
	if len(parts) > 1 {
		for i := 1; i < len(parts); i++ {
			part := strings.TrimSpace(parts[i])
			if strings.HasPrefix(part, "type=") {
				pq.typeFilter = strings.TrimPrefix(part, "type=")
			} else if strings.HasPrefix(part, "context=") {
				_, _ = fmt.Sscanf(part, "context=%d", &pq.context)
			} else if strings.HasPrefix(part, "name=") {
				pq.name = strings.TrimPrefix(part, "name=")
			} else if pq.name == "" {
				// First non-flag :part is the bare name filter
				pq.name = part
			}
		}
	}
	return pq
}

func symKindMatchesQuery(sym types.Symbol, pq parsedQuery) bool {
	nodeKind := strings.ToLower(pq.nodeType)
	switch nodeKind {
	case "functiondecl", "functioncall":
		if sym.Kind != types.SymbolFunction {
			return false
		}
		return patternNameMatches(sym.Name, pq.name)
	case "typedecl":
		if sym.Kind != types.SymbolType && sym.Kind != types.SymbolInterface && sym.Kind != types.SymbolClass {
			return false
		}
		return patternNameMatches(sym.Name, pq.name)
	case "methoddecl":
		if sym.Kind != types.SymbolFunction {
			return false
		}
		if !strings.Contains(sym.Signature, "(") {
			return false
		}
		return patternNameMatches(sym.Name, pq.name)
	case "ifstmt", "returnstmt", "assignstmt":
		return pq.name == "" || patternNameMatches(sym.Name, pq.name)
	case "vardecl":
		if sym.Kind != types.SymbolVariable {
			return false
		}
		return patternNameMatches(sym.Name, pq.name)
	case "constdecl":
		if sym.Kind != types.SymbolConstant {
			return false
		}
		return patternNameMatches(sym.Name, pq.name)
	default:
		return pq.name == "" || patternNameMatches(sym.Name, pq.name)
	}
}

// patternNameMatches checks if symName matches the given pattern.
// pattern may be "", a plain name, or a name with * at start/end only.
// Supports: "foo" exact, "foo*" prefix, "*foo" suffix, "*Bar*" infix, "*" any.
// * in the middle of the pattern is treated as a literal character.
func patternNameMatches(symName, pattern string) bool {
	if pattern == "" {
		return true
	}
	pattern = strings.TrimPrefix(pattern, "name=")
	// Exact match (no wildcards)
	if !strings.Contains(pattern, "*") {
		return symName == pattern
	}
	// Bare * matches everything
	if pattern == "*" {
		return true
	}
	// Infix * (e.g. "foo*bar") — * at both start AND end with non-empty sides
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") {
		infix := pattern[1 : len(pattern)-1]
		return infix == "" || strings.Contains(symName, infix)
	}
	// Trailing-only * wildcard (e.g. "foo*")
	if strings.HasSuffix(pattern, "*") && !strings.Contains(pattern[:len(pattern)-1], "*") {
		prefix := pattern[:len(pattern)-1]
		return prefix == "" || strings.HasPrefix(symName, prefix)
	}
	// Leading-only * wildcard (e.g. "*Bar")
	if strings.HasPrefix(pattern, "*") && !strings.Contains(pattern[1:], "*") {
		suffix := pattern[1:]
		return suffix == "" || strings.HasSuffix(symName, suffix)
	}
	// Multiple wildcards in inconsistent positions — literal match
	return symName == pattern
}
