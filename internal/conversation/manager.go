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
	return cloneMessages(c.Branches[c.Branch])
}

type Summary struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	MessageN  int       `json:"message_count"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
}

type BranchComparison struct {
	BranchA       string `json:"branch_a"`
	BranchB       string `json:"branch_b"`
	MessagesA     int    `json:"messages_a"`
	MessagesB     int    `json:"messages_b"`
	SharedPrefixN int    `json:"shared_prefix_count"`
	OnlyA         int    `json:"only_a"`
	OnlyB         int    `json:"only_b"`
}

type Manager struct {
	mu      sync.RWMutex
	saveMu  sync.Mutex // serializes saves so snapshots are never stale
	store   *storage.Store
	active  *Conversation
	baseDir string
}

type persistedConversation struct {
	ID        string                     `json:"id"`
	Provider  string                     `json:"provider"`
	Model     string                     `json:"model"`
	StartedAt time.Time                  `json:"started_at"`
	Branch    string                     `json:"branch"`
	Branches  map[string][]types.Message `json:"branches"`
	Metadata  map[string]string          `json:"metadata,omitempty"`
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

// newConversationID returns a fresh conversation ID that is guaranteed not
// to collide with the currently-active conversation (if any). The base is a
// human-friendly millisecond timestamp; on collision we fall back to the
// full nanosecond clock so rapid-fire Start() calls — e.g. an auto-handoff
// firing in the same millisecond the previous session was created — still
// produce unique IDs.
func newConversationID(active *Conversation, tag string) string {
	now := time.Now()
	base := "conv_" + now.Format("20060102_150405.000")
	if tag != "" {
		base += "_" + tag
	}
	if active == nil || active.ID != base {
		return base
	}
	return base + "_" + fmt.Sprintf("%09d", now.Nanosecond()%1_000_000_000)
}

func (m *Manager) Start(provider, model string) *Conversation {
	m.mu.Lock()
	defer m.mu.Unlock()

	id := newConversationID(m.active, "")
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
	id := newConversationID(m.active, "")
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
	copyMsgs := cloneMessages(current)
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

func (m *Manager) BranchCompare(branchA, branchB string) (BranchComparison, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return BranchComparison{}, fmt.Errorf("no active conversation")
	}
	a := strings.TrimSpace(branchA)
	b := strings.TrimSpace(branchB)
	if a == "" || b == "" {
		return BranchComparison{}, fmt.Errorf("both branch names are required")
	}
	msgsA, ok := m.active.Branches[a]
	if !ok {
		return BranchComparison{}, fmt.Errorf("branch not found: %s", a)
	}
	msgsB, ok := m.active.Branches[b]
	if !ok {
		return BranchComparison{}, fmt.Errorf("branch not found: %s", b)
	}
	shared := sharedPrefixLen(msgsA, msgsB)
	return BranchComparison{
		BranchA:       a,
		BranchB:       b,
		MessagesA:     len(msgsA),
		MessagesB:     len(msgsB),
		SharedPrefixN: shared,
		OnlyA:         max(0, len(msgsA)-shared),
		OnlyB:         max(0, len(msgsB)-shared),
	}, nil
}

func (m *Manager) UndoLast() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return 0, fmt.Errorf("no active conversation")
	}
	msgs := m.active.Branches[m.active.Branch]
	if len(msgs) == 0 {
		return 0, nil
	}

	removed := 1
	trimTo := len(msgs) - 1
	if len(msgs) >= 2 {
		last := msgs[len(msgs)-1]
		prev := msgs[len(msgs)-2]
		if prev.Role == types.RoleUser && last.Role == types.RoleAssistant {
			removed = 2
			trimTo = len(msgs) - 2
		}
	}
	if trimTo < 0 {
		trimTo = 0
	}
	next := cloneMessages(msgs[:trimTo])
	m.active.Branches[m.active.Branch] = next
	return removed, nil
}

func (m *Manager) SaveActive() error {
	// Snapshot id + a defensive copy of the message slice under the lock,
	// then release before doing the disk write. Holding m.mu across
	// SaveConversationLog (atomic-rename + fsync, often tens to hundreds
	// of ms) would block every concurrent AddMessage / Branch* / UndoLast
	// call for the entire write — and is a classic footgun for re-entrant
	// deadlocks if the store ever calls back through anything that takes
	// m.mu (event hooks, error reporters, etc.). The snapshot copy is
	// cheap relative to the fsync cost.
	// saveMu serializes concurrent SaveActive calls so the snapshot taken
	// by one is not invalidated by another goroutine's AddMessage between
	// RUnlock and the disk write. The original comment about not holding
	// m.mu across I/O still applies â we keep the RLock short.
	m.saveMu.Lock()
	defer m.saveMu.Unlock()

	m.mu.RLock()
	if m.active == nil || m.store == nil {
		m.mu.RUnlock()
		return nil
	}
	snapshot := cloneConversation(m.active)
	store := m.store
	m.mu.RUnlock()

	state := persistedConversation{
		ID:        snapshot.ID,
		Provider:  snapshot.Provider,
		Model:     snapshot.Model,
		StartedAt: snapshot.StartedAt,
		Branch:    snapshot.Branch,
		Branches:  snapshot.Branches,
		Metadata:  snapshot.Metadata,
	}
	if err := store.SaveConversationState(snapshot.ID, state); err != nil {
		return err
	}
	return store.SaveConversationLog(snapshot.ID, snapshot.Branches[snapshot.Branch])
}

func (m *Manager) Load(id string) (*Conversation, error) {
	c, err := m.loadFromStore(id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.active = c
	m.mu.Unlock()
	return cloneConversation(c), nil
}

// LoadReadOnly returns a conversation without setting it as active.
// Use this for previews / inspection surfaces (e.g. the TUI Conversations
// tab highlighting an entry) where mutating the active conversation would
// silently switch the user's chat history out from under them.
func (m *Manager) LoadReadOnly(id string) (*Conversation, error) {
	c, err := m.loadFromStore(id)
	if err != nil {
		return nil, err
	}
	return cloneConversation(c), nil
}

// loadFromStore is the shared scaffolding behind Load and LoadReadOnly.
// Disk I/O happens outside m.mu (the store handles its own concurrency)
// so a long history scan can't block readers.
func (m *Manager) loadFromStore(id string) (*Conversation, error) {
	m.mu.RLock()
	store := m.store
	m.mu.RUnlock()
	if store == nil {
		return nil, fmt.Errorf("store not available")
	}
	var state persistedConversation
	if err := store.LoadConversationState(id, &state); err == nil {
		return normalizeConversation(&Conversation{
			ID:        state.ID,
			Provider:  state.Provider,
			Model:     state.Model,
			StartedAt: state.StartedAt,
			Branch:    state.Branch,
			Branches:  state.Branches,
			Metadata:  state.Metadata,
		}), nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	msgs, err := store.LoadConversationLog(id)
	if err != nil {
		return nil, err
	}
	startedAt := legacyConversationStartedAt(store, id)
	return normalizeConversation(&Conversation{
		ID:        id,
		Provider:  "unknown",
		Model:     "unknown",
		StartedAt: startedAt,
		Branch:    "main",
		Branches: map[string][]types.Message{
			"main": msgs,
		},
		Metadata: map[string]string{},
	}), nil
}

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

func cloneConversation(in *Conversation) *Conversation {
	if in == nil {
		return nil
	}
	out := *in
	out.Metadata = cloneMap(in.Metadata)
	out.Branches = map[string][]types.Message{}
	for k, v := range in.Branches {
		out.Branches[k] = cloneMessages(v)
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

func cloneMessages(in []types.Message) []types.Message {
	if len(in) == 0 {
		return nil
	}
	out := make([]types.Message, len(in))
	for i, msg := range in {
		out[i] = cloneMessage(msg)
	}
	return out
}

func cloneMessage(in types.Message) types.Message {
	out := in
	out.Metadata = cloneMap(in.Metadata)
	if len(in.ToolCalls) > 0 {
		out.ToolCalls = make([]types.ToolCallRecord, len(in.ToolCalls))
		for i, call := range in.ToolCalls {
			out.ToolCalls[i] = call
			out.ToolCalls[i].Params = cloneAnyMap(call.Params)
			out.ToolCalls[i].Metadata = cloneMap(call.Metadata)
		}
	}
	if len(in.Results) > 0 {
		out.Results = make([]types.ToolResultRecord, len(in.Results))
		for i, result := range in.Results {
			out.Results[i] = result
			out.Results[i].Metadata = cloneMap(result.Metadata)
		}
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func sharedPrefixLen(a, b []types.Message) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i].Role != b[i].Role || a[i].Content != b[i].Content {
			return i
		}
	}
	return n
}
