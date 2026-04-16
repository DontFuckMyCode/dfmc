package context

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

// ChunkSourceQueryMatch means the file ranked in on keyword/substring hits
// against its path or contents. Baseline ranking — available even when the
// codemap is empty or symbol-aware retrieval is disabled.
const ChunkSourceQueryMatch = "query-match"

// ChunkSourceSymbolMatch means an identifier in the query resolved to a
// symbol node in the codemap and we pulled the file that defines it.
// Higher-confidence than query-match because the resolution is semantic,
// not substring.
const ChunkSourceSymbolMatch = "symbol-match"

// ChunkSourceGraphNeighborhood means the file wasn't directly named or
// matched, but shares imports (or is imported by) a seed file that was.
// Surfaced so callers/collaborators come along for the ride without the
// user listing them.
const ChunkSourceGraphNeighborhood = "graph-neighborhood"

// ChunkSourceHotspot means the file has high graph centrality (many in/out
// edges) — a weak "this is probably relevant to anything" signal. Lowest
// confidence, used as tie-breaker.
const ChunkSourceHotspot = "hotspot"

// ChunkSourceMarker means the user explicitly asked for the file via a
// [[file:...]] or [[symbol:...]] marker in the query. Never pruned.
const ChunkSourceMarker = "marker"

// chunkSourceRank returns a precedence weight for a ChunkSource* value so
// the scorer can pick the strongest label when the same file attracts
// multiple signals. Higher = better. Unknown values rank zero so they
// never displace a known-good source.
func chunkSourceRank(src string) int {
	switch src {
	case ChunkSourceMarker:
		return 5
	case ChunkSourceSymbolMatch:
		return 4
	case ChunkSourceGraphNeighborhood:
		return 3
	case ChunkSourceQueryMatch:
		return 2
	case ChunkSourceHotspot:
		return 1
	default:
		return 0
	}
}

// identifierRE matches programming identifiers the way a programmer types
// them in prose: Go-style CamelCase, snake_case, and dotted paths like
// pkg.Func or pkg/sub.Name. Excludes pure numbers and very short tokens
// (handled post-match) so we don't fire on "id", "it", "is", etc.
var identifierRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:[./][A-Za-z_][A-Za-z0-9_]*)*`)

// identifierStopwords are common English/Turkish imperative verbs and
// filler that the identifierRE happily captures but that almost never
// resolve to meaningful code symbols. Keeping the list small so we stay
// aggressive about pulling context — false negatives here mean a relevant
// symbol goes un-resolved.
var identifierStopwords = map[string]struct{}{
	"the": {}, "and": {}, "but": {}, "for": {}, "from": {}, "with": {},
	"into": {}, "onto": {}, "that": {}, "this": {}, "these": {}, "those": {},
	"please": {}, "help": {}, "fix": {}, "add": {}, "edit": {}, "update": {},
	"remove": {}, "delete": {}, "rename": {}, "write": {}, "create": {},
	"build": {}, "run": {}, "test": {}, "lint": {}, "vet": {},
	// Turkish — DFMC has a bilingual audience per project config
	"ekle": {}, "sil": {}, "yaz": {},
	// Common generic placeholders that would otherwise resolve broadly
	"src": {}, "lib": {}, "util": {}, "utils": {}, "main": {},
}

// ExtractQueryIdentifiers is the exported entry point for extractIdentifiers,
// used by the coach/engine to gauge whether a query looked symbol-like. See
// the internal helper for matching semantics.
func ExtractQueryIdentifiers(query string) []string { return extractIdentifiers(query) }

// extractIdentifiers pulls likely symbol names out of a free-text query.
// Unlike tokenizeQuery (which lowercases everything and uses the tokens
// for substring search), this preserves the original casing so we can
// match symbol Names exactly in the codemap — "parseToken" vs "parsetoken"
// matters once we start walking the graph.
//
// Returns identifiers in order of first appearance, deduplicated.
func extractIdentifiers(query string) []string {
	if strings.TrimSpace(query) == "" {
		return nil
	}
	raw := identifierRE.FindAllString(query, -1)
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, tok := range raw {
		tok = strings.Trim(tok, "._/")
		if len(tok) < 3 {
			continue
		}
		// Drop all-lowercase single words that are in the stopword list.
		// Keep CamelCase/snake_case/dotted forms even if the lowercase
		// form would match a stopword — "Fix" the PR-label is different
		// from "fix my bug".
		if !strings.ContainsAny(tok, "._/") && strings.ToLower(tok) == tok {
			if _, stop := identifierStopwords[tok]; stop {
				continue
			}
		}
		key := tok
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, tok)
		if len(out) >= 16 {
			break
		}
	}
	return out
}

// resolveSymbolSeeds matches extracted identifiers against the codemap's
// symbol nodes and returns the set of defining files (the graph stores a
// "defines" edge from file → symbol, so the symbol's Path is the file).
//
// The map value carries the total match strength so callers can prioritize
// files where multiple query identifiers landed.
func resolveSymbolSeeds(graph *codemap.Graph, identifiers []string) map[string]float64 {
	if graph == nil || len(identifiers) == 0 {
		return nil
	}
	out := map[string]float64{}
	// Build a lowercase index once so we can match case-insensitively
	// without re-scanning the whole node set per identifier. Symbol names
	// are not guaranteed unique across files, so we keep all matches.
	lowerIdx := map[string][]codemap.Node{}
	for _, n := range graph.Nodes() {
		if n.Kind == "file" || n.Kind == "module" || n.Path == "" {
			continue
		}
		lowerIdx[strings.ToLower(n.Name)] = append(lowerIdx[strings.ToLower(n.Name)], n)
	}
	for _, ident := range identifiers {
		// Dotted forms like "pkg.Func" carry path hints — the trailing
		// segment is the symbol, the prefix a directory hint. Match on
		// the trailing segment but boost hits whose file path contains
		// the prefix.
		name := ident
		prefixHint := ""
		if idx := strings.LastIndexAny(ident, "./"); idx >= 0 {
			name = ident[idx+1:]
			prefixHint = strings.ToLower(ident[:idx])
		}
		if len(name) < 3 {
			continue
		}
		nodes := lowerIdx[strings.ToLower(name)]
		if len(nodes) == 0 {
			continue
		}
		for _, n := range nodes {
			boost := 1.0
			if prefixHint != "" && strings.Contains(strings.ToLower(n.Path), prefixHint) {
				boost += 0.75
			}
			out[filepath.ToSlash(n.Path)] += boost
		}
	}
	return out
}

// expandViaGraph walks outward from seed files through the codemap's
// "imports" edges to surface sibling files — anything that imports the
// same modules, or is imported by the seeds. Depth defaults to 2 (seed →
// module → sibling file). Larger depths blow the budget without materially
// improving relevance in practice.
//
// The returned map carries the strongest hop distance for each neighbor
// (lower is closer) so the caller can inverse-scale the score.
func expandViaGraph(graph *codemap.Graph, seedPaths []string, depth int) map[string]int {
	if graph == nil || len(seedPaths) == 0 {
		return nil
	}
	if depth <= 0 {
		depth = 2
	}
	out := map[string]int{}
	seen := map[string]struct{}{}
	for _, p := range seedPaths {
		seen[filepath.ToSlash(p)] = struct{}{}
	}

	// For each seed file, pull its imported modules (outgoing "imports"),
	// then walk the incoming edges of those modules to find other files
	// that import the same modules. Cap the fan-out so a popular module
	// (e.g. "fmt", "strings") doesn't drag the whole repo in.
	const maxSiblingsPerModule = 6
	for _, seed := range seedPaths {
		seedID := "file:" + filepath.ToSlash(seed)
		for _, module := range graph.Descendants(seedID, 1) {
			if module.Kind != "module" {
				continue
			}
			siblings := 0
			for _, sib := range graph.Ancestors(module.ID, 1) {
				if sib.Kind != "file" || sib.Path == "" {
					continue
				}
				path := filepath.ToSlash(sib.Path)
				if _, isSeed := seen[path]; isSeed {
					continue
				}
				// Keep the lowest hop count (closer wins ties).
				if cur, ok := out[path]; !ok || cur > 2 {
					out[path] = 2
				}
				siblings++
				if siblings >= maxSiblingsPerModule {
					break
				}
			}
		}
	}
	return out
}

