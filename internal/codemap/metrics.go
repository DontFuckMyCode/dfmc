package codemap

import (
	"sync"
	"time"
)

const maxRecentBuildSamples = 8

type BuildSample struct {
	StartedAt      time.Time        `json:"started_at"`
	DurationMs     int64            `json:"duration_ms"`
	FilesRequested int64            `json:"files_requested"`
	FilesProcessed int64            `json:"files_processed"`
	FilesSkipped   int64            `json:"files_skipped"`
	ParseErrors    int64            `json:"parse_errors"`
	GraphNodes     int64            `json:"graph_nodes"`
	GraphEdges     int64            `json:"graph_edges"`
	NodesAdded     int64            `json:"nodes_added"`
	EdgesAdded     int64            `json:"edges_added"`
	Languages      map[string]int64 `json:"languages,omitempty"`
	Directories    map[string]int64 `json:"directories,omitempty"`
}

type BuildMetrics struct {
	Builds            int64            `json:"builds"`
	FilesRequested    int64            `json:"files_requested"`
	FilesProcessed    int64            `json:"files_processed"`
	FilesSkipped      int64            `json:"files_skipped"`
	ParseErrors       int64            `json:"parse_errors"`
	LastBatchFiles    int64            `json:"last_batch_files"`
	LastDurationMs    int64            `json:"last_duration_ms"`
	TotalDurationMs   int64            `json:"total_duration_ms"`
	AvgDurationMs     float64          `json:"avg_duration_ms"`
	LastGraphNodes    int64            `json:"last_graph_nodes"`
	LastGraphEdges    int64            `json:"last_graph_edges"`
	TotalNodesAdded   int64            `json:"total_nodes_added"`
	TotalEdgesAdded   int64            `json:"total_edges_added"`
	LastNodesAdded    int64            `json:"last_nodes_added"`
	LastEdgesAdded    int64            `json:"last_edges_added"`
	RecentBuilds      int64            `json:"recent_builds,omitempty"`
	RecentLanguages   map[string]int64 `json:"recent_languages,omitempty"`
	RecentDirectories map[string]int64 `json:"recent_directories,omitempty"`
	Recent            []BuildSample    `json:"recent,omitempty"`
}

type buildMetricsTracker struct {
	mu              sync.RWMutex
	builds          int64
	filesRequested  int64
	filesProcessed  int64
	filesSkipped    int64
	parseErrors     int64
	lastBatchFiles  int64
	lastDuration    time.Duration
	totalDuration   time.Duration
	lastGraphNodes  int64
	lastGraphEdges  int64
	totalNodesAdded int64
	totalEdgesAdded int64
	lastNodesAdded  int64
	lastEdgesAdded  int64
	recent          []BuildSample
}

func newBuildMetricsTracker() *buildMetricsTracker {
	return &buildMetricsTracker{}
}

func (m *buildMetricsTracker) recordBuild(sample BuildSample) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.builds++
	m.filesRequested += sample.FilesRequested
	m.filesProcessed += sample.FilesProcessed
	m.filesSkipped += sample.FilesSkipped
	m.parseErrors += sample.ParseErrors
	m.lastBatchFiles = sample.FilesRequested
	m.lastDuration = time.Duration(sample.DurationMs) * time.Millisecond
	m.totalDuration += m.lastDuration
	m.lastGraphNodes = sample.GraphNodes
	m.lastGraphEdges = sample.GraphEdges
	m.totalNodesAdded += sample.NodesAdded
	m.totalEdgesAdded += sample.EdgesAdded
	m.lastNodesAdded = sample.NodesAdded
	m.lastEdgesAdded = sample.EdgesAdded
	m.recent = append(m.recent, cloneBuildSample(sample))
	if len(m.recent) > maxRecentBuildSamples {
		m.recent = append([]BuildSample(nil), m.recent[len(m.recent)-maxRecentBuildSamples:]...)
	}
}

func (m *buildMetricsTracker) snapshot() BuildMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	avg := 0.0
	if m.builds > 0 {
		avg = float64(m.totalDuration.Milliseconds()) / float64(m.builds)
	}

	recentLanguages := map[string]int64{}
	recentDirectories := map[string]int64{}
	for _, sample := range m.recent {
		mergeCountMaps(recentLanguages, sample.Languages)
		mergeCountMaps(recentDirectories, sample.Directories)
	}

	return BuildMetrics{
		Builds:            m.builds,
		FilesRequested:    m.filesRequested,
		FilesProcessed:    m.filesProcessed,
		FilesSkipped:      m.filesSkipped,
		ParseErrors:       m.parseErrors,
		LastBatchFiles:    m.lastBatchFiles,
		LastDurationMs:    m.lastDuration.Milliseconds(),
		TotalDurationMs:   m.totalDuration.Milliseconds(),
		AvgDurationMs:     avg,
		LastGraphNodes:    m.lastGraphNodes,
		LastGraphEdges:    m.lastGraphEdges,
		TotalNodesAdded:   m.totalNodesAdded,
		TotalEdgesAdded:   m.totalEdgesAdded,
		LastNodesAdded:    m.lastNodesAdded,
		LastEdgesAdded:    m.lastEdgesAdded,
		RecentBuilds:      int64(len(m.recent)),
		RecentLanguages:   cloneCountMap(recentLanguages),
		RecentDirectories: cloneCountMap(recentDirectories),
		Recent:            cloneBuildSamples(m.recent),
	}
}

func cloneBuildSample(sample BuildSample) BuildSample {
	return BuildSample{
		StartedAt:      sample.StartedAt,
		DurationMs:     sample.DurationMs,
		FilesRequested: sample.FilesRequested,
		FilesProcessed: sample.FilesProcessed,
		FilesSkipped:   sample.FilesSkipped,
		ParseErrors:    sample.ParseErrors,
		GraphNodes:     sample.GraphNodes,
		GraphEdges:     sample.GraphEdges,
		NodesAdded:     sample.NodesAdded,
		EdgesAdded:     sample.EdgesAdded,
		Languages:      cloneCountMap(sample.Languages),
		Directories:    cloneCountMap(sample.Directories),
	}
}

func cloneBuildSamples(samples []BuildSample) []BuildSample {
	if len(samples) == 0 {
		return nil
	}
	out := make([]BuildSample, 0, len(samples))
	for _, sample := range samples {
		out = append(out, cloneBuildSample(sample))
	}
	return out
}

func cloneCountMap(input map[string]int64) map[string]int64 {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]int64, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func mergeCountMaps(dst map[string]int64, src map[string]int64) {
	for key, count := range src {
		if key == "" || count == 0 {
			continue
		}
		dst[key] += count
	}
}
