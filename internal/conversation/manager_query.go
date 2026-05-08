package conversation

// manager_query.go — read-side disk crawl helpers split out of
// manager_persist.go so the save/load surface there stays focused on
// the active conversation. List walks the artifacts dir, summarises
// each persisted conversation, and returns them newest-first; Search
// filters that list by case-insensitive substring across every branch.
//
// Both run with no engine lock held while the disk I/O happens — m.mu
// is read-locked only long enough to snapshot the baseDir/store
// references, mirroring the pattern in manager_persist.go.
//
// normalizeConversation + totalMessageCount + legacyConversationStartedAt
// live here because they're pure read-side helpers used by loadFromStore
// and List/Search; keeping them adjacent to their callers means a
// reader doesn't have to jump between files to follow the recall path.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (m *Manager) List() ([]Summary, error) {
	// Snapshot the immutable refs we need (baseDir, store), release the
	// lock, then do the directory scan + per-file loads. The previous
	// version held m.mu for the whole crawl which scaled with conversation
	// count and starved every concurrent reader.
	m.mu.RLock()
	baseDir := m.baseDir
	m.mu.RUnlock()

	if strings.TrimSpace(baseDir) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Summary, 0, len(entries))
	seenIDs := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		id := ""
		switch {
		case strings.HasSuffix(name, ".json"):
			id = strings.TrimSuffix(name, ".json")
		case strings.HasSuffix(name, ".jsonl"):
			id = strings.TrimSuffix(name, ".jsonl")
		default:
			continue
		}
		if id == "" {
			continue
		}
		if _, seen := seenIDs[id]; seen {
			continue
		}
		seenIDs[id] = struct{}{}
		conv, err := m.loadFromStore(id)
		if err != nil {
			continue
		}
		msgCount := totalMessageCount(conv)
		mod := time.Time{}
		if info, e2 := e.Info(); e2 == nil {
			mod = info.ModTime()
		}
		startedAt := conv.StartedAt
		if startedAt.IsZero() {
			startedAt = mod
		}
		out = append(out, Summary{
			ID:        id,
			StartedAt: startedAt,
			MessageN:  msgCount,
			Provider:  conv.Provider,
			Model:     conv.Model,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}

func (m *Manager) Search(query string, limit int) ([]Summary, error) {
	if limit <= 0 {
		limit = 20
	}
	query = strings.TrimSpace(strings.ToLower(query))
	if query == "" {
		return m.List()
	}
	all, err := m.List()
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, min(limit, len(all)))
	for _, item := range all {
		conv, err := m.loadFromStore(item.ID)
		if err != nil {
			continue
		}
		found := false
		for _, msgs := range conv.Branches {
			for _, msg := range msgs {
				if strings.Contains(strings.ToLower(msg.Content), query) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if found {
			out = append(out, item)
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func legacyConversationStartedAt(store *storage.Store, id string) time.Time {
	if store == nil {
		return time.Time{}
	}
	info, err := os.Stat(filepath.Join(store.ArtifactsDir(), "conversations", id+".jsonl"))
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func normalizeConversation(c *Conversation) *Conversation {
	if c == nil {
		return nil
	}
	if c.Branches == nil {
		c.Branches = map[string][]types.Message{}
	}
	if strings.TrimSpace(c.Branch) == "" {
		c.Branch = "main"
	}
	if _, ok := c.Branches[c.Branch]; !ok {
		c.Branches[c.Branch] = []types.Message{}
	}
	if c.Metadata == nil {
		c.Metadata = map[string]string{}
	}
	if c.Provider == "" {
		c.Provider = "unknown"
	}
	if c.Model == "" {
		c.Model = "unknown"
	}
	if c.StartedAt.IsZero() {
		c.StartedAt = time.Now()
	}
	return c
}

func totalMessageCount(c *Conversation) int {
	if c == nil {
		return 0
	}
	total := 0
	for _, msgs := range c.Branches {
		total += len(msgs)
	}
	return total
}
