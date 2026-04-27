package context

import (
	"testing"
	"time"
)

func TestContextSnapshot_ShouldReuse(t *testing.T) {
	now := time.Now()
	old := now.Add(-10 * time.Minute)

	tests := []struct {
		name           string
		snapshot       *ContextSnapshot
		maxAge         time.Duration
		minConf        float64
		want           bool
	}{
		{
			name:     "nil snapshot",
			snapshot: nil,
			maxAge:   5 * time.Minute,
			minConf:  0.7,
			want:     false,
		},
		{
			name: "fresh high confidence",
			snapshot: &ContextSnapshot{
				RetrievedAt: now,
				Confidence:  0.8,
			},
			maxAge:  5 * time.Minute,
			minConf: 0.7,
			want:    true,
		},
		{
			name: "fresh low confidence",
			snapshot: &ContextSnapshot{
				RetrievedAt: now,
				Confidence:  0.5,
			},
			maxAge:  5 * time.Minute,
			minConf: 0.7,
			want:    false,
		},
		{
			name: "stale high confidence",
			snapshot: &ContextSnapshot{
				RetrievedAt: old,
				Confidence:  0.9,
			},
			maxAge:  5 * time.Minute,
			minConf: 0.7,
			want:    false,
		},
		{
			name: "stale low confidence",
			snapshot: &ContextSnapshot{
				RetrievedAt: old,
				Confidence:  0.3,
			},
			maxAge:  5 * time.Minute,
			minConf: 0.7,
			want:    false,
		},
		{
			name: "boundary age exactly at limit",
			snapshot: &ContextSnapshot{
				RetrievedAt: now.Add(-5 * time.Minute),
				Confidence:  0.8,
			},
			maxAge:  5 * time.Minute,
			minConf: 0.7,
			want:    true, // exactly at limit is not > limit
		},
		{
			name: "just over age limit",
			snapshot: &ContextSnapshot{
				RetrievedAt: now.Add(-5*time.Minute - 1*time.Second),
				Confidence:  0.9,
			},
			maxAge:  5 * time.Minute,
			minConf: 0.7,
			want:    false,
		},
		{
			name: "exactly at confidence threshold",
			snapshot: &ContextSnapshot{
				RetrievedAt: now,
				Confidence:  0.7,
			},
			maxAge:  5 * time.Minute,
			minConf: 0.7,
			want:    true, // >= minConf
		},
		{
			name: "just below confidence threshold",
			snapshot: &ContextSnapshot{
				RetrievedAt: now,
				Confidence:  0.69,
			},
			maxAge:  5 * time.Minute,
			minConf: 0.7,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.snapshot.ShouldReuse(tt.maxAge, tt.minConf)
			if got != tt.want {
				t.Errorf("ShouldReuse(%v, %v) = %v; want %v", tt.maxAge, tt.minConf, got, tt.want)
			}
		})
	}
}

func TestContextSnapshot_MergeWith(t *testing.T) {
	child := &ContextSnapshot{
		Query:      "child query",
		Task:       "code",
		Confidence: 0.8,
		BudgetUsed: 100,
		RetrievedAt: time.Now(),
		Chunks: []ContextChunkRef{
			{Path: "a.go", Language: "go", Score: 0.9, Source: "symbol-match"},
			{Path: "b.go", Language: "go", Score: 0.7, Source: "query-match"},
		},
	}

	parent := &ContextSnapshot{
		Query:      "parent query",
		Task:       "code",
		Confidence: 0.6,
		BudgetUsed: 80,
		RetrievedAt: time.Now().Add(-1 * time.Minute),
		Chunks: []ContextChunkRef{
			{Path: "a.go", Language: "go", Score: 0.5, Source: "hotspot"},
			{Path: "c.go", Language: "go", Score: 0.4, Source: "graph-neighborhood"},
		},
	}

	merged := child.MergeWith(parent)

	// Child chunks override parent chunks for same path
	if len(merged.Chunks) != 3 {
		t.Errorf("len(Chunks) = %d; want 3", len(merged.Chunks))
	}

	// Verify a.go has child's score (override)
	var aChunk ContextChunkRef
	for _, c := range merged.Chunks {
		if c.Path == "a.go" {
			aChunk = c
			break
		}
	}
	if aChunk.Score != 0.9 {
		t.Errorf("a.go score = %v; want 0.9 (child overrides)", aChunk.Score)
	}
	if aChunk.Source != "symbol-match" {
		t.Errorf("a.go source = %v; want symbol-match", aChunk.Source)
	}

	// Verify b.go from child is present
	foundB := false
	for _, c := range merged.Chunks {
		if c.Path == "b.go" {
			foundB = true
			break
		}
	}
	if !foundB {
		t.Error("b.go not found in merged chunks")
	}

	// Verify c.go from parent is present
	foundC := false
	for _, c := range merged.Chunks {
		if c.Path == "c.go" {
			foundC = true
			break
		}
	}
	if !foundC {
		t.Error("c.go not found in merged chunks")
	}

	// BudgetUsed should be sum
	if merged.BudgetUsed != 180 {
		t.Errorf("BudgetUsed = %d; want 180", merged.BudgetUsed)
	}

	// Child's Query and Task preserved
	if merged.Query != "child query" {
		t.Errorf("Query = %q; want child query", merged.Query)
	}
	if merged.Task != "code" {
		t.Errorf("Task = %q; want code", merged.Task)
	}
}

func TestContextSnapshot_MergeWith_NilParent(t *testing.T) {
	child := &ContextSnapshot{
		Query:      "child only",
		Confidence: 0.8,
		BudgetUsed: 50,
		RetrievedAt: time.Now(),
		Chunks: []ContextChunkRef{
			{Path: "x.go", Language: "go", Score: 0.9, Source: "symbol-match"},
		},
	}
	merged := child.MergeWith(nil)
	if merged != child {
		t.Error("MergeWith(nil) should return self")
	}
}

func TestContextSnapshot_MergeWith_NilChild(t *testing.T) {
	parent := &ContextSnapshot{
		Query:      "parent only",
		Confidence: 0.6,
		BudgetUsed: 30,
		RetrievedAt: time.Now(),
		Chunks: []ContextChunkRef{
			{Path: "y.go", Language: "go", Score: 0.5, Source: "query-match"},
		},
	}
	merged := (*ContextSnapshot)(nil).MergeWith(parent)
	if merged != parent {
		t.Error("MergeWith with nil child should return parent")
	}
}

func TestContextSnapshot_MergeWith_BothNil(t *testing.T) {
	merged := (*ContextSnapshot)(nil).MergeWith(nil)
	if merged != nil {
		t.Error("MergeWith(nil, nil) should return nil")
	}
}

func TestContextSnapshot_MergeWith_WeightedConfidence(t *testing.T) {
	child := &ContextSnapshot{
		Query:      "c",
		Confidence: 0.8,
		BudgetUsed: 100,
		RetrievedAt: time.Now(),
		Chunks: []ContextChunkRef{
			{Path: "a.go", Language: "go", Score: 0.9, Source: "symbol-match"},
			{Path: "b.go", Language: "go", Score: 0.7, Source: "query-match"},
		},
	}
	parent := &ContextSnapshot{
		Query:      "p",
		Confidence: 0.4,
		BudgetUsed: 50,
		RetrievedAt: time.Now(),
		Chunks: []ContextChunkRef{
			{Path: "c.go", Language: "go", Score: 0.3, Source: "hotspot"},
		},
	}
	// child has 2 chunks at 0.8, parent has 1 chunk at 0.4
	// expected avg = (0.8*2 + 0.4*1) / 3 = 2.0/3 ≈ 0.667
	merged := child.MergeWith(parent)
	if merged.Confidence < 0.666 || merged.Confidence > 0.668 {
		t.Errorf("Confidence = %v; want ~0.667", merged.Confidence)
	}
}
