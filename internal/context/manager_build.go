package context

// manager_build.go — Build / BuildWithOptions context-retrieval pipeline.
// Sibling to manager.go (Manager type + Invalidate + system-prompt
// rendering in manager_prompt.go) and the stateless helpers listed in
// the manager.go header.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
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

	opts = m.normalizeBuildOpts(opts)
	m.applyStrategyDefaults(&opts)

	graph := m.codemap.Graph()
	scores, sources := m.scoreQueryNodes(graph, query, opts)
	m.scoreSymbolsAndGraph(graph, query, opts, scores, sources)
	m.boostHotspots(graph, opts, scores, sources)

	rankedPaths := m.rankPaths(scores)
	chunks := m.collectChunks(rankedPaths, scores, sources, opts)
	return chunks, nil
}

func (m *Manager) normalizeBuildOpts(opts BuildOptions) BuildOptions {
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
	return opts
}

func (m *Manager) applyStrategyDefaults(opts *BuildOptions) {
	switch opts.Strategy {
	case StrategySecurity:
		opts.GraphDepth = max(opts.GraphDepth, 3)
	case StrategyDebug:
		opts.GraphDepth = max(opts.GraphDepth, 2)
	case StrategyReview:
		opts.GraphDepth = min(opts.GraphDepth, 1)
	case StrategyRefactor:
		opts.GraphDepth = max(opts.GraphDepth, 2)
		// NB: refactorBoost needs the live scores/sources maps, which
		// don't exist yet here (scoreQueryNodes creates them after this
		// runs). It is applied there instead — calling it with nil maps
		// would panic AND discard the boost.
	}
}

func (m *Manager) scoreQueryNodes(graph *codemap.Graph, query string, opts BuildOptions) (map[string]float64, map[string]string) {
	scores := map[string]float64{}
	sources := map[string]string{}

	terms := tokenizeQuery(query)
	staleWindow := 2 * time.Minute
	now := time.Now()
	for path, t := range opts.ExcludeStaleFilters {
		if now.Sub(t) > staleWindow {
			delete(opts.ExcludeStaleFilters, path)
		}
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
	// Refactor strategy: layer the orphan/cycle boost onto the live
	// maps now that they exist (see applyStrategyDefaults).
	if opts.Strategy == StrategyRefactor {
		refactorBoost(graph, scores, sources)
	}
	return scores, sources
}

func (m *Manager) scoreSymbolsAndGraph(graph *codemap.Graph, query string, opts BuildOptions, scores map[string]float64, sources map[string]string) {
	if !opts.SymbolAware {
		return
	}
	idents := extractIdentifiers(query)
	seeds := resolveSymbolSeeds(graph, idents)
	for path, strength := range seeds {
		scores[path] += 4.0 + strength
		sources[path] = ChunkSourceSymbolMatch
	}
	if len(seeds) > 0 && opts.GraphDepth > 0 {
		seedList := make([]string, 0, len(seeds))
		for path := range seeds {
			seedList = append(seedList, path)
		}
		for path, hops := range expandViaGraph(graph, seedList, opts.GraphDepth) {
			scores[path] += 1.5 / float64(hops)
			sources[path] = ChunkSourceGraphNeighborhood
		}
	}
}

func (m *Manager) boostHotspots(graph *codemap.Graph, opts BuildOptions, scores map[string]float64, sources map[string]string) {
	for _, hs := range graph.HotSpots(opts.MaxFiles * 3) {
		if hs.Path == "" {
			continue
		}
		scores[hs.Path] += 1.0
		if current, ok := sources[hs.Path]; !ok || chunkSourceRank(ChunkSourceHotspot) > chunkSourceRank(current) {
			sources[hs.Path] = ChunkSourceHotspot
		}
	}
}

func (m *Manager) rankPaths(scores map[string]float64) []struct {
	Path  string
	Score float64
} {
	rankedPaths := make([]struct {
		Path  string
		Score float64
	}, 0, len(scores))
	for path, score := range scores {
		rankedPaths = append(rankedPaths, struct {
			Path  string
			Score float64
		}{Path: path, Score: score})
	}
	sort.Slice(rankedPaths, func(i, j int) bool {
		if rankedPaths[i].Score == rankedPaths[j].Score {
			return rankedPaths[i].Path < rankedPaths[j].Path
		}
		return rankedPaths[i].Score > rankedPaths[j].Score
	})
	return rankedPaths
}

func (m *Manager) collectChunks(rankedPaths []struct {
	Path  string
	Score float64
}, scores map[string]float64, sources map[string]string, opts BuildOptions) []types.ContextChunk {
	chunks := make([]types.ContextChunk, 0, opts.MaxFiles)
	remaining := opts.MaxTokensTotal
	for _, r := range rankedPaths {
		if len(chunks) >= opts.MaxFiles || remaining <= 0 {
			break
		}
		if !shouldIncludePath(r.Path, opts.IncludeTests, opts.IncludeDocs) {
			continue
		}
		if m.isStalePath(r.Path, opts) || m.isSeenPath(r.Path, opts) {
			continue
		}

		content, err := os.ReadFile(r.Path)
		if err != nil {
			continue
		}
		chunk := buildChunkForBudget(r.Path, string(content), nil, r.Score, opts.Compression, opts.MaxTokensPerFile)
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
	return chunks
}

func (m *Manager) isStalePath(path string, opts BuildOptions) bool {
	if len(opts.ExcludeStaleFilters) == 0 {
		return false
	}
	absPath, _ := filepath.Abs(path)
	_, stale := opts.ExcludeStaleFilters[absPath]
	return stale
}

func (m *Manager) isSeenPath(path string, opts BuildOptions) bool {
	if len(opts.SeenFiles) == 0 {
		return false
	}
	absPath, _ := filepath.Abs(path)
	_, seen := opts.SeenFiles[absPath]
	return seen
}
