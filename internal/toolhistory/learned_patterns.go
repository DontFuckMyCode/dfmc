// Package toolhistory persists learned coding patterns the assistant
// can replay across sessions. The tool-call JSONL logger that lived
// here historically (Logger + bus reflection bridge + atomic writer)
// was removed as unwired dead code; provider-level call logging
// lives in internal/providerlog.
package toolhistory

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// idSeq is a process-wide monotonic counter that disambiguates IDs
// generated within the same nanosecond bucket. Different
// LearnedPatternStore instances share it so a MergeFrom from a peer
// store whose Add happened at the same wall-clock instant can't end
// up with a colliding ID.
var idSeq atomic.Uint64

// LearnedPattern represents a pattern learned from tool interactions.
type LearnedPattern struct {
	ID          string `json:"id"`
	Date        string `json:"date"`         // YYYY-MM-DD
	Pattern     string `json:"pattern"`      // short pattern name
	Situation   string `json:"situation"`    // where it was learned
	OldApproach string `json:"old_approach"` // previous approach
	NewApproach string `json:"new_approach"` // better approach
	Application string `json:"application"`  // how to apply
	Success     bool   `json:"success"`      // applied successfully
	LastUsed    string `json:"last_used"`    // last-used timestamp (RFC3339)
	UseCount    int    `json:"use_count"`    // times applied
}

// LearnedPatternStore manages learned patterns persistence.
type LearnedPatternStore struct {
	dir      string
	mu       sync.RWMutex
	patterns map[string]*LearnedPattern // key = pattern ID
	dirty    bool
	loadErr  error
	// saves tracks in-flight fire-and-forget save() goroutines so
	// Close can drain them before returning. Without this, tests using
	// t.TempDir() race with the goroutine still writing patterns.jsonl
	// and TempDir cleanup fails with "directory not empty".
	saves sync.WaitGroup
}

// InitLearnedPatterns creates or loads the learned patterns store.
func InitLearnedPatterns(artifactsDir string) (*LearnedPatternStore, error) {
	if artifactsDir == "" {
		return nil, nil
	}
	dir := filepath.Join(artifactsDir, "learned_patterns")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	store := &LearnedPatternStore{dir: dir, patterns: make(map[string]*LearnedPattern)}
	store.load()
	return store, nil
}

func (s *LearnedPatternStore) path() string {
	return filepath.Join(s.dir, "patterns.jsonl")
}

func (s *LearnedPatternStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(s.path())
	if err != nil {
		if !os.IsNotExist(err) {
			s.loadErr = err
		}
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024) // max line: 1MB (default 64KB too small for long pattern entries)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var p LearnedPattern
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			continue
		}
		s.patterns[p.ID] = &p
	}
	if err := scanner.Err(); err != nil {
		s.loadErr = err
	}
}

// save acquires the write lock and persists; safe for callers that
// do NOT already hold s.mu.
func (s *LearnedPatternStore) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

// saveLocked assumes the caller already holds s.mu.Lock(). sync.Mutex
// is non-reentrant, so paths like Close() that need to save while
// holding the lock must call this variant — calling save() from
// inside the critical section self-deadlocks.
func (s *LearnedPatternStore) saveLocked() error {
	f, err := os.Create(s.path())
	if err != nil {
		return err
	}
	defer f.Close()
	for _, p := range s.patterns {
		b, err := json.Marshal(p)
		if err != nil {
			continue
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			return err
		}
	}
	s.dirty = false
	return nil
}

// Add adds a new learned pattern.
func (s *LearnedPatternStore) Add(pattern, situation, oldApproach, newApproach, application string) *LearnedPattern {
	s.mu.Lock()
	now := time.Now().UTC()
	// Include nanoseconds + a process-wide monotonic counter so neither
	// rapid same-second Adds in one store nor MergeFrom from a peer
	// store created in the same nanosecond bucket collide on ID.
	id := now.Format("20060102-150405") + "-" + strconv.Itoa(now.Nanosecond()) + "-" + strconv.FormatUint(idSeq.Add(1), 10)
	p := &LearnedPattern{
		ID:          id,
		Date:        now.Format("2006-01-02"),
		Pattern:     pattern,
		Situation:   situation,
		OldApproach: oldApproach,
		NewApproach: newApproach,
		Application: application,
		Success:     true,
		LastUsed:    now.Format(time.RFC3339),
		UseCount:    1,
	}
	s.patterns[id] = p
	s.dirty = true
	s.mu.Unlock()
	s.saves.Go(func() { s.save() })
	return p
}

// GetAll returns all learned patterns sorted by date descending.
func (s *LearnedPatternStore) GetAll() []*LearnedPattern {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*LearnedPattern, 0, len(s.patterns))
	for _, p := range s.patterns {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Date > result[j].Date
	})
	return result
}

// GetRecent returns patterns from the last n days.
func (s *LearnedPatternStore) GetRecent(days int) []*LearnedPattern {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	result := []*LearnedPattern{}
	for _, p := range s.patterns {
		if p.Date >= cutoff {
			result = append(result, p)
		}
	}
	return result
}

// MarkUsed increments the use count for a pattern.
func (s *LearnedPatternStore) MarkUsed(id string) {
	s.mu.Lock()
	p, ok := s.patterns[id]
	if ok {
		p.UseCount++
		p.LastUsed = time.Now().UTC().Format(time.RFC3339)
		s.dirty = true
	}
	s.mu.Unlock()
	if ok {
		s.saves.Go(func() { s.save() })
	}
}

// ExportForContext returns patterns formatted for context injection.
func (s *LearnedPatternStore) ExportForContext() string {
	patterns := s.GetRecent(30)
	if len(patterns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<!-- self-learn:patterns begin -->\n")
	b.WriteString("## Learned Patterns (last 30 days)\n\n")
	for _, p := range patterns {
		b.WriteString("- **")
		b.WriteString(p.Pattern)
		b.WriteString("** (")
		b.WriteString(p.Date)
		b.WriteString("): ")
		b.WriteString(p.Application)
		b.WriteString("\n")
	}
	b.WriteString("\n<!-- self-learn:patterns end -->\n")
	return b.String()
}

// Close drains any in-flight async saves and then flushes anything
// still marked dirty. Draining first matters for tests: t.TempDir()
// runs RemoveAll on return and races a still-writing goroutine if
// Close didn't wait. Drain happens BEFORE we take s.mu so the
// goroutines (which need s.mu themselves) can finish.
func (s *LearnedPatternStore) Close() error {
	s.saves.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	return s.saveLocked()
}

// MergeFrom merges patterns from another store into this one.
// Duplicate IDs are skipped (local patterns take precedence).
// The other store is not modified.
func (s *LearnedPatternStore) MergeFrom(other *LearnedPatternStore) {
	if other == nil {
		return
	}
	other.mu.RLock()
	defer other.mu.RUnlock()
	s.mu.Lock()
	for id, p := range other.patterns {
		if _, exists := s.patterns[id]; !exists {
			cp := *p // copy to avoid sharing the underlying pointer
			s.patterns[id] = &cp
			s.dirty = true
		}
	}
	wasDirty := s.dirty
	s.mu.Unlock()
	if wasDirty {
		s.saves.Go(func() { s.save() })
	}
}
