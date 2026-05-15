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
	"strings"
	"sync"
	"time"
)

// LearnedPattern represents a pattern learned from tool interactions.
type LearnedPattern struct {
	ID          string `json:"id"`
	Date        string `json:"date"`         // YYYY-MM-DD
	Pattern     string `json:"pattern"`      // Kısa kalıp açıklaması
	Situation   string `json:"situation"`    // Hangi durumda öğrenildi
	OldApproach string `json:"old_approach"` // Önceki yaklaşım
	NewApproach string `json:"new_approach"` // Daha iyi yaklaşım
	Application string `json:"application"`  // Nasıl uygulanır
	Success     bool   `json:"success"`      // Uygulama başarılı mı
	LastUsed    string `json:"last_used"`    // Son kullanım tarihi
	UseCount    int    `json:"use_count"`    // Kaç kez kullanıldı
}

// LearnedPatternStore manages learned patterns persistence.
type LearnedPatternStore struct {
	dir      string
	mu       sync.RWMutex
	patterns map[string]*LearnedPattern // key = pattern ID
	dirty    bool
	loadErr  error
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
}

func (s *LearnedPatternStore) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Create(s.path())
	if err != nil {
		return err
	}
	defer f.Close()
	for _, p := range s.patterns {
		if b, err := json.Marshal(p); err == nil {
			f.Write(b)
			f.Write([]byte("\n"))
		}
	}
	s.dirty = false
	return nil
}

// Add adds a new learned pattern.
func (s *LearnedPatternStore) Add(pattern, situation, oldApproach, newApproach, application string) *LearnedPattern {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	id := now.Format("20060102-150405")
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
	go s.save()
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
	// Sort by date descending (bubble sort for simplicity)
	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].Date > result[i].Date {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

// GetRecent returns patterns from the last n days.
func (s *LearnedPatternStore) GetRecent(days int) []*LearnedPattern {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")
	var result []*LearnedPattern
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
	defer s.mu.Unlock()
	if p, ok := s.patterns[id]; ok {
		p.UseCount++
		p.LastUsed = time.Now().UTC().Format(time.RFC3339)
		s.dirty = true
		go s.save()
	}
}

// ExportForContext returns patterns formatted for context injection.
func (s *LearnedPatternStore) ExportForContext() string {
	patterns := s.GetRecent(30) // Son 30 günden kalıplar
	if len(patterns) == 0 {
		return ""
	}
	var lines []string
	lines = append(lines, "## Öğrenilen Kalıplar (Son 30 gün)")
	lines = append(lines, "")
	for _, p := range patterns {
		lines = append(lines, "- **"+p.Pattern+"** ("+p.Date+"): "+p.Application)
	}
	lines = append(lines, "")
	lines = append(lines, "<!-- self-learn:patterns end -->")
	result := "<!-- self-learn:patterns begin -->\n"
	for _, l := range lines {
		result += l + "\n"
	}
	return result
}

// Close flushes any pending writes.
func (s *LearnedPatternStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty {
		return s.save()
	}
	return nil
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
	defer s.mu.Unlock()
	for id, p := range other.patterns {
		if _, exists := s.patterns[id]; !exists {
			cp := *p // copy to avoid sharing the underlying pointer
			s.patterns[id] = &cp
			s.dirty = true
		}
	}
	if s.dirty {
		go s.save()
	}
}
