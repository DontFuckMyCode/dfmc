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
