package context

import "time"

// ContextChunkRef is a reference to a chunk in the context, without the full
// content. Used for caching and for attaching retrieval outcomes to tasks.
type ContextChunkRef struct {
	Path      string  `json:"path"`
	Language  string  `json:"language"`
	LineStart int     `json:"line_start"`
	LineEnd   int     `json:"line_end"`
	Score     float64 `json:"score"`
	Source    string  `json:"source"` // "query-match"|"symbol-match"|"graph-neighborhood"|"hotspot"|"marker"
}

// ContextSnapshot captures the retrieval outcome for a single query. Attached to
// a supervisor task so resume/replay can reuse the same chunks instead of
// re-running retrieval from scratch.
type ContextSnapshot struct {
	Query       string            `json:"query"`
	Chunks      []ContextChunkRef `json:"chunks"`
	BudgetUsed  int               `json:"budget_used"`
	Task        string            `json:"task"`       // "security"|"debug"|"review"|"refactor"|etc.
	Confidence  float64           `json:"confidence"` // avg chunk score, 0-1
	RetrievedAt time.Time         `json:"retrieved_at"`
}

// ShouldReuse reports whether this snapshot is fresh enough (within maxAge)
// and confident enough (>= minConf) to be reused instead of re-running retrieval.
func (s *ContextSnapshot) ShouldReuse(maxAge time.Duration, minConf float64) bool {
	if s == nil {
		return false
	}
	if s.RetrievedAt.IsZero() || time.Since(s.RetrievedAt) > maxAge {
		return false
	}
	return s.Confidence >= minConf
}

// MergeWith merges child (receiver) with parent, returning a new snapshot.
// Child chunks override parent chunks for the same file path.
// BudgetUsed is summed; Query and Task are taken from child.
func (s *ContextSnapshot) MergeWith(parent *ContextSnapshot) *ContextSnapshot {
	if s == nil {
		return parent
	}
	if parent == nil {
		return s
	}
	// Build override map from parent chunks keyed by Path
	override := make(map[string]ContextChunkRef)
	for _, c := range parent.Chunks {
		override[c.Path] = c
	}
	// Child chunks take precedence
	for _, c := range s.Chunks {
		override[c.Path] = c
	}
	// Merge Chunks preserving child priority order
	seen := make(map[string]bool)
	var merged []ContextChunkRef
	for _, c := range s.Chunks {
		if !seen[c.Path] {
			merged = append(merged, c)
			seen[c.Path] = true
		}
	}
	for _, c := range parent.Chunks {
		if !seen[c.Path] {
			merged = append(merged, c)
			seen[c.Path] = true
		}
	}
	// Confidence is weighted average of snapshot confidences, weighted by chunk count.
	conf := (s.Confidence*float64(len(s.Chunks)) + parent.Confidence*float64(len(parent.Chunks))) /
		float64(len(s.Chunks)+len(parent.Chunks))
	return &ContextSnapshot{
		Query:      s.Query,
		Chunks:     merged,
		BudgetUsed: s.BudgetUsed + parent.BudgetUsed,
		Task:       s.Task,
		Confidence: conf,
		RetrievedAt: s.RetrievedAt,
	}
}

// chunkMap returns a map from Path to Chunk for the receiver.
func (s *ContextSnapshot) chunkMap() map[string]ContextChunkRef {
	m := make(map[string]ContextChunkRef)
	for _, c := range s.Chunks {
		m[c.Path] = c
	}
	return m
}
