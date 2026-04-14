package conversation

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type Conversation struct {
	ID        string                     `json:"id"`
	Provider  string                     `json:"provider"`
	Model     string                     `json:"model"`
	StartedAt time.Time                  `json:"started_at"`
	Branch    string                     `json:"branch"`
	Branches  map[string][]types.Message `json:"branches"`
	Metadata  map[string]string          `json:"metadata,omitempty"`
}

func (c *Conversation) Messages() []types.Message {
	if c == nil {
		return nil
	}
	msgs := c.Branches[c.Branch]
	out := make([]types.Message, len(msgs))
	copy(out, msgs)
	return out
}

type Summary struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	MessageN  int       `json:"message_count"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
}

type Manager struct {
	mu      sync.RWMutex
	store   *storage.Store
	active  *Conversation
	baseDir string
}

func New(store *storage.Store) *Manager {
	baseDir := ""
	if store != nil {
		baseDir = filepath.Join(store.ArtifactsDir(), "conversations")
	}
	return &Manager{
		store:   store,
		baseDir: baseDir,
	}
}

func (m *Manager) Start(provider, model string) *Conversation {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := "conv_" + time.Now().Format("20060102_150405.000")
	c := &Conversation{
		ID:        id,
		Provider:  provider,
		Model:     model,
		StartedAt: time.Now(),
		Branch:    "main",
		Branches: map[string][]types.Message{
			"main": {},
		},
		Metadata: map[string]string{},
	}
	m.active = c
	return cloneConversation(c)
}

func (m *Manager) Active() *Conversation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneConversation(m.active)
}

func (m *Manager) ensureActiveLocked(provider, model string) {
	if m.active != nil {
		return
	}
	id := "conv_" + time.Now().Format("20060102_150405.000")
	m.active = &Conversation{
		ID:        id,
		Provider:  provider,
		Model:     model,
		StartedAt: time.Now(),
		Branch:    "main",
		Branches: map[string][]types.Message{
			"main": {},
		},
		Metadata: map[string]string{},
	}
}

func (m *Manager) AddMessage(provider, model string, msg types.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureActiveLocked(provider, model)
	msgs := m.active.Branches[m.active.Branch]
	msgs = append(msgs, msg)
	m.active.Branches[m.active.Branch] = msgs
}

func (m *Manager) BranchCreate(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return fmt.Errorf("no active conversation")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("branch name is required")
	}
	if _, ok := m.active.Branches[name]; ok {
		return fmt.Errorf("branch already exists: %s", name)
	}
	current := m.active.Branches[m.active.Branch]
	copyMsgs := make([]types.Message, len(current))
	copy(copyMsgs, current)
	m.active.Branches[name] = copyMsgs
	return nil
}

func (m *Manager) BranchSwitch(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return fmt.Errorf("no active conversation")
	}
	if _, ok := m.active.Branches[name]; !ok {
		return fmt.Errorf("branch not found: %s", name)
	}
	m.active.Branch = name
	return nil
}

func (m *Manager) BranchList() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return nil
	}
	out := make([]string, 0, len(m.active.Branches))
	for name := range m.active.Branches {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) SaveActive() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil || m.store == nil {
		return nil
	}
	msgs := m.active.Branches[m.active.Branch]
	if len(msgs) == 0 {
		return nil
	}
	return m.store.SaveConversationLog(m.active.ID, msgs)
}

func (m *Manager) Load(id string) (*Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.store == nil {
		return nil, fmt.Errorf("store not available")
	}
	msgs, err := m.store.LoadConversationLog(id)
	if err != nil {
		return nil, err
	}
	c := &Conversation{
		ID:        id,
		Provider:  "unknown",
		Model:     "unknown",
		StartedAt: time.Now(),
		Branch:    "main",
		Branches: map[string][]types.Message{
			"main": msgs,
		},
		Metadata: map[string]string{},
	}
	m.active = c
	return cloneConversation(c), nil
}

func (m *Manager) List() ([]Summary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if strings.TrimSpace(m.baseDir) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(m.baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Summary, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		msgs, err := m.store.LoadConversationLog(id)
		if err != nil {
			continue
		}
		if len(msgs) == 0 {
			continue
		}
		mod := time.Time{}
		if info, e2 := e.Info(); e2 == nil {
			mod = info.ModTime()
		}
		out = append(out, Summary{
			ID:        id,
			StartedAt: mod,
			MessageN:  len(msgs),
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
		msgs, err := m.store.LoadConversationLog(item.ID)
		if err != nil {
			continue
		}
		found := false
		for _, msg := range msgs {
			if strings.Contains(strings.ToLower(msg.Content), query) {
				found = true
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

func cloneConversation(in *Conversation) *Conversation {
	if in == nil {
		return nil
	}
	out := *in
	out.Metadata = cloneMap(in.Metadata)
	out.Branches = map[string][]types.Message{}
	for k, v := range in.Branches {
		cp := make([]types.Message, len(v))
		copy(cp, v)
		out.Branches[k] = cp
	}
	return &out
}

func cloneMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
