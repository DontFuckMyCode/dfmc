// Workspace-wide symbol index for call resolution
// (dfmc_report_ast.md §R3, phase 2).
//
// Phase 1 (calls.go) gave us per-file Call entries -- "this line
// calls something named `os.path.join`". Phase 2 takes a corpus
// of parsed files, builds a name → definition index, and turns
// the per-file calls into edges that point at the file:line where
// the callee is actually defined.
//
// Scope of this phase is deliberately pragmatic, not exhaustive:
//
//   * Resolution looks at the LAST segment of a dotted callee
//     (`os.path.join` -> `join`). Full receiver-type inference
//     (`User.Save` vs `Server.Save`) is left for a future phase
//     that would need actual type information.
//
//   * Same-file matches win over workspace-wide matches when a
//     symbol of the same name exists in both. This is the most
//     conservative tiebreaker -- a method named `Run` in the
//     caller file is almost always the intended target over the
//     same-name symbol in a far-away module.
//
//   * Ambiguous lookups (multiple workspace matches with no
//     same-file winner) return nil rather than guessing. An
//     unresolved edge is recoverable; a wrong edge poisons
//     downstream analysis.
//
//   * Only "callable" symbol kinds (Function, Class, Interface,
//     Method, Type, Enum) become Definitions. Variables and
//     constants produce too much noise without helping call
//     resolution.

package ast

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// Definition is one place where a callable symbol is defined.
// Multiple Definitions can share the same Name when the same
// identifier is declared in different files (e.g. a method `Run`
// on two unrelated types). Resolution tiebreakers in SymbolIndex
// pick the most likely target.
type Definition struct {
	Name     string           `json:"name"`
	File     string           `json:"file"`
	Line     int              `json:"line"`
	Kind     types.SymbolKind `json:"kind"`
	Language string           `json:"language,omitempty"`
}

// SymbolIndex is a workspace-wide map from bare symbol name to
// every declaration site that name appears at. Used to answer
// "what does this call point to?" queries by ResolveCall.
//
// Nil-safe: every method returns the zero value (nil / empty
// slice) on a nil receiver, so callers can treat "no index" and
// "index says unknown" identically.
type SymbolIndex struct {
	byName map[string][]Definition
}

// BuildSymbolIndex indexes every callable symbol in a corpus of
// parsed files. Order of `files` matches insertion order in the
// resulting per-name slices, so callers that want deterministic
// tiebreakers should pass files in a stable order (lexicographic
// by path is the convention).
func BuildSymbolIndex(files []*ParseResult) *SymbolIndex {
	idx := &SymbolIndex{byName: map[string][]Definition{}}
	for _, f := range files {
		if f == nil {
			continue
		}
		for _, sym := range f.Symbols {
			if !isCallableSymbolKind(sym.Kind) {
				continue
			}
			idx.byName[sym.Name] = append(idx.byName[sym.Name], Definition{
				Name:     sym.Name,
				File:     f.Path,
				Line:     sym.Line,
				Kind:     sym.Kind,
				Language: f.Language,
			})
		}
	}
	return idx
}

// Lookup returns every Definition for the given bare name (no
// dot resolution -- callers that want dotted-name lookup should
// use Resolve). The returned slice is a fresh copy so callers
// can mutate it without affecting the index.
func (idx *SymbolIndex) Lookup(name string) []Definition {
	if idx == nil || idx.byName == nil {
		return nil
	}
	defs := idx.byName[name]
	if len(defs) == 0 {
		return nil
	}
	out := make([]Definition, len(defs))
	copy(out, defs)
	return out
}

// Resolve attempts to resolve a call's Callee to a single
// Definition. Dotted callees use the LAST segment as the lookup
// key (`os.path.join` -> `join`), since the leading segments
// describe the module path which we may not have indexed.
//
// Tiebreakers:
//
//  1. A definition in callerFile wins over any other match. This
//     captures the common case where the caller calls its own
//     helper.
//
//  2. A single workspace-wide match resolves cleanly.
//
//  3. Multiple matches with no same-file winner: return nil.
//     Ambiguity is reported as "unresolved" rather than guessed.
//
// Returns nil for an empty / unknown name, or when ambiguity
// cannot be resolved without additional type information.
func (idx *SymbolIndex) Resolve(callerFile, callee string) *Definition {
	if idx == nil || idx.byName == nil {
		return nil
	}
	bare := lastDotSegment(callee)
	if bare == "" {
		return nil
	}
	defs := idx.byName[bare]
	if len(defs) == 0 {
		return nil
	}
	// 1. Same-file preference.
	for i := range defs {
		if defs[i].File == callerFile {
			d := defs[i]
			return &d
		}
	}
	// 2. Single workspace-wide match.
	if len(defs) == 1 {
		d := defs[0]
		return &d
	}
	// 3. Ambiguous -- decline to guess.
	return nil
}

// Size reports the number of distinct bare names in the index.
// Useful for diagnostics / status surfaces; not load-bearing for
// resolution itself.
func (idx *SymbolIndex) Size() int {
	if idx == nil || idx.byName == nil {
		return 0
	}
	return len(idx.byName)
}

// ResolvedEdge pairs a Call with the resolved (or unresolved)
// Target. Used by callers that want the entire call graph for a
// file at once.
type ResolvedEdge struct {
	Call   Call        `json:"call"`
	Target *Definition `json:"target,omitempty"`
}

// ResolveCalls applies Resolve to every Call in `calls`, treating
// `callerFile` as the caller's source path for tiebreaker purposes.
// Unresolved calls appear in the result with Target=nil so callers
// can see the full edge list including misses.
func (idx *SymbolIndex) ResolveCalls(callerFile string, calls []Call) []ResolvedEdge {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ResolvedEdge, 0, len(calls))
	for _, c := range calls {
		out = append(out, ResolvedEdge{
			Call:   c,
			Target: idx.Resolve(callerFile, c.Callee),
		})
	}
	return out
}

// isCallableSymbolKind reports whether a symbol kind is plausibly
// the target of a call. Functions, methods, and class-like kinds
// (constructors) qualify; variables and constants don't.
func isCallableSymbolKind(k types.SymbolKind) bool {
	switch k {
	case types.SymbolFunction,
		types.SymbolClass,
		types.SymbolInterface,
		types.SymbolType,
		types.SymbolEnum:
		return true
	}
	return false
}

// lastDotSegment returns the trailing dot-separated piece of a
// possibly-qualified identifier. Used by Resolve to pull the bare
// name out of dotted callees.
func lastDotSegment(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[i+1:]
	}
	return s
}
