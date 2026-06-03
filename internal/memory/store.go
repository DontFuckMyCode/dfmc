package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

const (
	bucketEpisodic   = "memory_episodic"
	bucketSemantic   = "memory_semantic"
	bucketWorking    = "memory_working"
	bucketWorkingKey = "working" // single key holding JSON WorkingMemory
)

type WorkingMemory struct {
	RecentFiles   []string `json:"recent_files"`
	RecentSymbols []string `json:"recent_symbols"`
	LastQuestion  string   `json:"last_question,omitempty"`
	LastAnswer    string   `json:"last_answer,omitempty"`
}

type Store struct {
	mu        sync.RWMutex
	persistMu sync.Mutex // serializes Persist disk I/O; prevents lost-write races between concurrent Persist calls
	storage   *storage.Store
	working   WorkingMemory
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
	if s.storage == nil || s.storage.DB() == nil {
		return nil
	}
	data, err := s.storage.BucketGet(bucketWorking, bucketWorkingKey)
	if err != nil {
		return err
	}
	if data == nil {
		return nil // no data yet; working memory stays at zero value
	}
	var wm WorkingMemory
	if err := json.Unmarshal(data, &wm); err != nil {
		return nil // corrupt data; keep zero value, Persist will overwrite
	}
	s.mu.Lock()
	s.working = wm
	s.mu.Unlock()
	return nil
}

func (s *Store) Persist() error {
	if s.storage == nil || s.storage.DB() == nil {
		return nil
	}
	// persistMu serializes the entire snapshot + marshal + SQLite write
	// sequence so concurrent calls cannot silently lose each other's updates.
	s.persistMu.Lock()
	defer s.persistMu.Unlock()

	s.mu.Lock()
	wm := WorkingMemory{
		RecentFiles:   append([]string(nil), s.working.RecentFiles...),
		RecentSymbols: append([]string(nil), s.working.RecentSymbols...),
		LastQuestion:  s.working.LastQuestion,
		LastAnswer:    s.working.LastAnswer,
	}
	s.mu.Unlock()
	data, err := json.Marshal(wm)
	if err != nil {
		return err
	}
	return s.storage.BucketPut(bucketWorking, bucketWorkingKey, data)
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
		// Microsecond-resolution timestamps collide when two goroutines
		// (e.g. parallel sub-agents writing memory) call Add within the
		// same microsecond — SQLite INSERT would conflict on primary key.
		// The 6-byte random suffix mirrors taskstore.NewTaskID
		// and reduces the collision space to ~2^-48.
		var rnd [6]byte
		_, _ = rand.Read(rnd[:])
		entry.ID = fmt.Sprintf("mem_%s_%s",
			time.Now().Format("20060102_150405.000000"),
			hex.EncodeToString(rnd[:]))
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
	return s.storage.BucketPut(bucket, entry.ID, data)
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
	err := s.storage.BucketForEach(bucket, func(_, v []byte) error {
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
	// Clamp non-positive limits the way List does. Without this, a negative
	// limit (reachable straight from `dfmc memory search --limit -1`, a plain
	// int flag) panicked at `make([]MemoryEntry, 0, limit)` with "makeslice:
	// cap out of range" — and limit*5 below would wrap on overflow too.
	if limit <= 0 {
		limit = 100
	}
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

// Delete removes a single entry by ID. Walks both episodic and semantic
// buckets so callers don't have to know which tier the entry lives in
// (the TUI surface — Phase H item 1 — already shows merged tiers).
// Returns nil when the ID isn't found so callers can treat "already
// gone" as success; pair with a List/Search beforehand if presence
// matters.
func (s *Store) Delete(id string) error {
	if s.storage == nil || s.storage.DB() == nil {
		return fmt.Errorf("memory storage is not available")
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("memory entry id is required")
	}
	for _, bucket := range []string{bucketEpisodic, bucketSemantic} {
		data, err := s.storage.BucketGet(bucket, id)
		if err != nil {
			return err
		}
		if data != nil {
			return s.storage.BucketDelete(bucket, id)
		}
	}
	return nil
}

// Update mutates the human-editable fields of an existing entry: Key,
// Value, Category. Tier and Project are immutable through this path —
// promote moves between tiers, and Project is the SQLite-level scope so
// changing it would orphan the row from List filters. Returns an error
// when the ID isn't found.
func (s *Store) Update(id string, key, value, category string) error {
	if s.storage == nil || s.storage.DB() == nil {
		return fmt.Errorf("memory storage is not available")
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("memory entry id is required")
	}
	for _, bucket := range []string{bucketEpisodic, bucketSemantic} {
		data, err := s.storage.BucketGet(bucket, id)
		if err != nil {
			return err
		}
		if data == nil {
			continue
		}
		var entry types.MemoryEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return fmt.Errorf("decode entry %q: %w", id, err)
		}
		entry.Key = key
		entry.Value = value
		entry.Category = category
		entry.UpdatedAt = time.Now()
		out, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		return s.storage.BucketPut(bucket, id, out)
	}
	return fmt.Errorf("memory entry %q not found", id)
}

// Promote moves an entry from the episodic bucket into the semantic
// bucket — the canonical "this turned out to be a long-lived fact"
// graduation path. No-op when the entry is already semantic. Returns an
// error when the ID isn't found in either bucket.
func (s *Store) Promote(id string) error {
	if s.storage == nil || s.storage.DB() == nil {
		return fmt.Errorf("memory storage is not available")
	}
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("memory entry id is required")
	}
	// Already semantic? Treat as a no-op so the TUI can call promote
	// without first checking the current tier.
	data, err := s.storage.BucketGet(bucketSemantic, id)
	if err != nil {
		return err
	}
	if data != nil {
		return nil
	}

	data, err = s.storage.BucketGet(bucketEpisodic, id)
	if err != nil {
		return err
	}
	if data == nil {
		return fmt.Errorf("memory entry %q not found in episodic tier", id)
	}
	var entry types.MemoryEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return fmt.Errorf("decode entry %q: %w", id, err)
	}
	entry.Tier = types.MemorySemantic
	entry.UpdatedAt = time.Now()
	out, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if err := s.storage.BucketPut(bucketSemantic, id, out); err != nil {
		return err
	}
	return s.storage.BucketDelete(bucketEpisodic, id)
}

func (s *Store) Clear(tier types.MemoryTier) error {
	if s.storage == nil || s.storage.DB() == nil {
		return nil
	}
	bucket := bucketForTier(tier)
	return s.storage.BucketClear(bucket)
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

// RequestLLMUpdate calls the LLM with a reflection prompt and adds any
// suggested memory entries returned by the model. It is best-effort:
// errors are logged but not propagated, and an empty/nil response from
// the LLM produces zero added entries. The confidence value from the
// response is used directly; entries with confidence below threshold are
// discarded.
func (s *Store) RequestLLMUpdate(ctx context.Context, question, answer string, llmUpdater LLMUpdater, threshold float64) error {
	prompt := "You just answered this question in a coding session:\n\nQ: " + question + "\n\nA: " + answer + "\n\nShould any of this be remembered for future sessions? " +
		"Respond ONLY with a JSON array of memory entries to add to persistent memory, or [] if nothing is worth remembering. " +
		"Each entry must have: key (short question/topic phrase), value (concise answer or finding), category (one word: pattern|fact|todo|decision|context), confidence (0.0-1.0). " +
		`Example: [{"key":"postgres jsonb performance","value":"Use GIN indexes for jsonb contains queries","category":"fact","confidence":0.85}]`

	resp, err := llmUpdater.Call(ctx, "", "", prompt)
	if err != nil {
		// best-effort: log and continue
		return nil
	}
	if resp == "" {
		return nil
	}

	resp = strings.TrimSpace(resp)
	if resp == "" || resp == "[]" {
		return nil
	}

	// Strip markdown code fences if present
	resp = strings.TrimPrefix(resp, "```json")
	resp = strings.TrimPrefix(resp, "```")
	resp = strings.TrimSuffix(resp, "```")
	resp = strings.TrimSpace(resp)

	var entries []struct {
		Key        string  `json:"key"`
		Value      string  `json:"value"`
		Category   string  `json:"category"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(resp), &entries); err != nil {
		return nil // best-effort
	}

	for _, e := range entries {
		if e.Confidence < threshold {
			continue
		}
		entry := types.MemoryEntry{
			Tier:       types.MemoryEpisodic,
			Category:   e.Category,
			Key:        e.Key,
			Value:      e.Value,
			Confidence: e.Confidence,
		}
		_ = s.Add(entry) // best-effort; errors are logged inside Add
	}
	return nil
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
