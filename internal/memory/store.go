package memory

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.etcd.io/bbolt"

	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

const (
	bucketEpisodic = "memory_episodic"
	bucketSemantic = "memory_semantic"
)

type WorkingMemory struct {
	RecentFiles   []string `json:"recent_files"`
	RecentSymbols []string `json:"recent_symbols"`
	LastQuestion  string   `json:"last_question,omitempty"`
	LastAnswer    string   `json:"last_answer,omitempty"`
}

type Store struct {
	mu      sync.RWMutex
	storage *storage.Store
	working WorkingMemory
}

func New(store *storage.Store) *Store {
	return &Store{
		storage: store,
		working: WorkingMemory{
			RecentFiles:   []string{},
			RecentSymbols: []string{},
		},
	}
}

func (s *Store) Load() error {
	// Working memory is in-memory only for now.
	if s.storage == nil || s.storage.DB() == nil {
		return nil
	}
	return nil
}

func (s *Store) Persist() error {
	// bbolt writes are immediate; no-op for now.
	return nil
}

func (s *Store) Working() WorkingMemory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return WorkingMemory{
		RecentFiles:   append([]string(nil), s.working.RecentFiles...),
		RecentSymbols: append([]string(nil), s.working.RecentSymbols...),
		LastQuestion:  s.working.LastQuestion,
		LastAnswer:    s.working.LastAnswer,
	}
}

func (s *Store) SetWorkingQuestionAnswer(q, a string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.working.LastQuestion = q
	s.working.LastAnswer = a
}

func (s *Store) TouchFile(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.working.RecentFiles = pushUniqFront(s.working.RecentFiles, path, 50)
}

func (s *Store) TouchSymbol(sym string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.working.RecentSymbols = pushUniqFront(s.working.RecentSymbols, sym, 100)
}

func (s *Store) Add(entry types.MemoryEntry) error {
	if s.storage == nil || s.storage.DB() == nil {
		return fmt.Errorf("memory storage is not available")
	}
	if entry.Project == "" {
		return fmt.Errorf("memory entry project is required")
	}
	if entry.ID == "" {
		entry.ID = "mem_" + time.Now().Format("20060102_150405.000000")
	}
	now := time.Now()
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = now
	}
	entry.UpdatedAt = now
	entry.LastUsedAt = now
	if entry.Metadata == nil {
		entry.Metadata = map[string]string{}
	}

	var bucket string
	switch entry.Tier {
	case types.MemorySemantic:
		bucket = bucketSemantic
	default:
		entry.Tier = types.MemoryEpisodic
		bucket = bucketEpisodic
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return s.storage.DB().Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return fmt.Errorf("bucket not found: %s", bucket)
		}
		return b.Put([]byte(entry.ID), data)
	})
}

func (s *Store) List(tier types.MemoryTier, limit int, project string) ([]types.MemoryEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	bucket := bucketForTier(tier)
	if s.storage == nil || s.storage.DB() == nil {
		return nil, nil
	}
	var out []types.MemoryEntry
	err := s.storage.DB().View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, v []byte) error {
			var e types.MemoryEntry
			if err := json.Unmarshal(v, &e); err != nil {
				return nil
			}
			if project != "" && e.Project != project {
				return nil
			}
			out = append(out, e)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) Search(query string, tier types.MemoryTier, limit int, project string) ([]types.MemoryEntry, error) {
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return s.List(tier, limit, project)
	}
	list, err := s.List(tier, limit*5, project)
	if err != nil {
		return nil, err
	}
	out := make([]types.MemoryEntry, 0, limit)
	for _, e := range list {
		corpus := strings.ToLower(e.Key + " " + e.Value + " " + e.Category)
		if strings.Contains(corpus, query) {
			out = append(out, e)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *Store) Clear(tier types.MemoryTier) error {
	if s.storage == nil || s.storage.DB() == nil {
		return nil
	}
	bucket := bucketForTier(tier)
	return s.storage.DB().Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if b == nil {
			return nil
		}
		var keys [][]byte
		err := b.ForEach(func(k, _ []byte) error {
			cp := make([]byte, len(k))
			copy(cp, k)
			keys = append(keys, cp)
			return nil
		})
		if err != nil {
			return err
		}
		for _, k := range keys {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) AddEpisodicInteraction(project, question, answer string, confidence float64) error {
	entry := types.MemoryEntry{
		Project:    project,
		Tier:       types.MemoryEpisodic,
		Category:   "interaction",
		Key:        question,
		Value:      answer,
		Confidence: confidence,
	}
	return s.Add(entry)
}

func bucketForTier(tier types.MemoryTier) string {
	switch tier {
	case types.MemorySemantic:
		return bucketSemantic
	default:
		return bucketEpisodic
	}
}

func pushUniqFront(in []string, value string, max int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return in
	}
	out := make([]string, 0, len(in)+1)
	out = append(out, value)
	for _, v := range in {
		if v == value {
			continue
		}
		out = append(out, v)
		if len(out) >= max {
			break
		}
	}
	return out
}
