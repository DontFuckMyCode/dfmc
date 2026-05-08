package context

// manager_build.go — Build / BuildWithOptions context-retrieval pipeline.
// Sibling to manager.go (Manager type + Invalidate + system-prompt
// rendering in manager_prompt.go) and the stateless helpers listed in
// the manager.go header.

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (m *Manager) Build(query string, maxFiles int) ([]types.ContextChunk, error) {
	return m.BuildWithOptions(query, BuildOptions{
		MaxFiles:         maxFiles,
		MaxTokensTotal:   maxFiles * 1200,
		MaxTokensPerFile: 1200,
		Compression:      "standard",
		IncludeTests:     true,
		IncludeDocs:      true,
		SymbolAware:      true,
		GraphDepth:       2,
	})
}

func (m *Manager) BuildWithOptions(query string, opts BuildOptions) ([]types.ContextChunk, error) {
	if m == nil || m.codemap == nil || m.codemap.Graph() == nil {
		return nil, nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 6
	}
	if opts.MaxTokensPerFile <= 0 {
		opts.MaxTokensPerFile = 1200
	}
	if opts.MaxTokensTotal <= 0 {
		opts.MaxTokensTotal = opts.MaxFiles * opts.MaxTokensPerFile
	}
	if opts.MaxTokensTotal < 128 {
		opts.MaxTokensTotal = 128
	}
	opts.Compression = normalizeCompression(opts.Compression)

	graph := m.codemap.Graph()
	scores := map[string]float64{}
	sources := map[string]string{}

	// Apply task-type differentiation to retrieval strategy.
	switch opts.Strategy {
	case StrategySecurity:
		opts.GraphDepth = max(opts.GraphDepth, 3)
	case StrategyDebug:
		opts.GraphDepth = max(opts.GraphDepth, 2)
	case StrategyReview:
		opts.GraphDepth = min(opts.GraphDepth, 1)
	case StrategyRefactor:
		opts.GraphDepth = max(opts.GraphDepth, 2)
		// Detect refactoring opportunities via codemap graph:
		// - Orphan functions/methods/types that are never called or referenced
		// - Symbols involved in import/call cycles
		// Boost their files so they surface in context for the LLM to review.
		refactorBoost(graph, scores, sources)
	}

	terms := tokenizeQuery(query)
	// Exclude recently modified files (write_file/edit_file/apply_patch).
	// Files in the stale map are skipped so the LLM always reads the fresh
	// version via read_file, not a stale context chunk. The map self-prunes
	// entries older than staleWindow to avoid unbounded growth.
	staleWindow := 2 * time.Minute
	now := time.Now()
	for path, t := range opts.ExcludeStaleFilters {
		if now.Sub(t) > staleWindow {
			delete(opts.ExcludeStaleFilters, path)
		}
	}
	// Build a set of normalized seen-file paths for O(1) deduplication check.
	seenSet := make(map[string]struct{}, len(opts.SeenFiles))
	for f := range opts.SeenFiles {
		abs, _ := filepath.Abs(f)
		seenSet[abs] = struct{}{}
	}

	upgradeSource := func(path, candidate string) {
		if path == "" || candidate == "" {
			return
		}
		current, ok := sources[path]
		if !ok || chunkSourceRank(candidate) > chunkSourceRank(current) {
			sources[path] = candidate
		}
	}

	for _, n := range graph.Nodes() {
		switch n.Kind {
		case "file":
			pathLower := strings.ToLower(n.Path)
			nameLower := strings.ToLower(n.Name)
			for _, t := range terms {
				if strings.Contains(pathLower, t) || strings.Contains(nameLower, t) {
					scores[n.Path] += 2.0
					upgradeSource(n.Path, ChunkSourceQueryMatch)
				}
			}
			if _, ok := scores[n.Path]; !ok {
				scores[n.Path] = 0.15
			}
		default:
			if n.Path == "" {
				continue
			}
			nameLower := strings.ToLower(n.Name)
			for _, t := range terms {
				if strings.Contains(nameLower, t) {
					scores[n.Path] += 3.0
					upgradeSource(n.Path, ChunkSourceQueryMatch)
				}
			}
		}
	}

	// Symbol-aware pass: resolve identifiers in the query against the
	// codemap's symbol index, boost defining files, and walk outward
	// through the import graph to surface sibling files (callers/peers
	// that share module neighborhoods).
	if opts.SymbolAware {
		idents := extractIdentifiers(query)
		seeds := resolveSymbolSeeds(graph, idents)
		// Symbol hits outrank generic query-match bonuses because the
		// resolution is semantic, not substring — we know the identifier
		// *is* a defined symbol, not just a coincidental character run.
		for path, strength := range seeds {
			scores[path] += 4.0 + strength
			upgradeSource(path, ChunkSourceSymbolMatch)
		}
		if len(seeds) > 0 && opts.GraphDepth > 0 {
			seedList := make([]string, 0, len(seeds))
			for path := range seeds {
				seedList = append(seedList, path)
			}
			for path, hops := range expandViaGraph(graph, seedList, opts.GraphDepth) {
				// Inverse-scale by hop distance so closer siblings win.
				bonus := 1.5 / float64(hops)
				scores[path] += bonus
				upgradeSource(path, ChunkSourceGraphNeighborhood)
			}
		}
	}

	for _, hs := range graph.HotSpots(opts.MaxFiles * 3) {
		if hs.Path != "" {
			scores[hs.Path] += 1.0
			upgradeSource(hs.Path, ChunkSourceHotspot)
		}
	}

	type ranked struct {
		Path  string
		Score float64
	}
	rankedPaths := make([]ranked, 0, len(scores))
	for path, score := range scores {
		rankedPaths = append(rankedPaths, ranked{Path: path, Score: score})
	}
	sort.Slice(rankedPaths, func(i, j int) bool {
		if rankedPaths[i].Score == rankedPaths[j].Score {
			return rankedPaths[i].Path < rankedPaths[j].Path
		}
		return rankedPaths[i].Score > rankedPaths[j].Score
	})

	chunks := make([]types.ContextChunk, 0, opts.MaxFiles)
	remaining := opts.MaxTokensTotal
	for _, r := range rankedPaths {
		if len(chunks) >= opts.MaxFiles || remaining <= 0 {
			break
		}
		if !shouldIncludePath(r.Path, opts.IncludeTests, opts.IncludeDocs) {
			continue
		}
		// Skip recently modified files (stale filter) — the LLM must read
		// the fresh version via read_file, not an outdated context chunk.
		if len(opts.ExcludeStaleFilters) > 0 {
			absPath, _ := filepath.Abs(r.Path)
			if _, stale := opts.ExcludeStaleFilters[absPath]; stale {
				continue
			}
		}
		// Skip files already provided via read_file this session — sending
		// the same content twice via different channels wastes tokens and
		// confuses the model about which version is authoritative.
		if len(seenSet) > 0 {
			absPath, _ := filepath.Abs(r.Path)
			if _, seen := seenSet[absPath]; seen {
				continue
			}
		}

		content, err := func() ([]byte, error) {
			f, err := os.Open(r.Path)
			if err != nil {
				return nil, err
			}
			defer func() { _ = f.Close() }()
			return io.ReadAll(f)
		}()
		if err != nil {
			continue
		}
		chunk := buildChunkForBudget(r.Path, string(content), terms, r.Score, opts.Compression, opts.MaxTokensPerFile)
		if chunk.TokenCount <= 0 || strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		if chunk.TokenCount > remaining {
			chunk = downshiftChunkForRemaining(chunk, remaining, opts.MaxTokensPerFile)
		}
		if chunk.TokenCount <= 0 || strings.TrimSpace(chunk.Content) == "" {
			continue
		}
		if src, ok := sources[r.Path]; ok {
			chunk.Source = src
		} else {
			chunk.Source = ChunkSourceQueryMatch
		}
		chunks = append(chunks, chunk)
		remaining -= chunk.TokenCount
	}

	return chunks, nil
}
