package conversation

// manager.go — core conversation types and the Manager mutator surface
// (Start, Active, AddMessage, RemoveMessagesByID, ID assignment).
//
// Branch lifecycle and undo live in manager_branches.go. Disk
// persistence + history recall (Save/Load/List/Search) live in
// manager_persist.go. Defensive-copy helpers live in manager_clone.go.
//
// Concurrency: m.mu guards every read or write of m.active and m.reporter.
// saveMu serializes blocking and async saves so a snapshot taken by one
// is never invalidated mid-fsync by an AddMessage on the other side.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// AssignMessageID returns a short opaque ID like "u-3f29a1" for users
// or "a-7b40c2" for assistants. Generated from 4 random bytes (8 hex
// chars truncated to 6) — collision-safe within any realistic
// conversation length (~16M IDs per role before birthday risk). The
// role-prefix is purely for the LLM's readability when it needs to
// name a cleanup target ("drop a-7b40c2 — superseded by a-9c12d3").
//
// Exported so the engine layer can assign IDs to outbound messages it
// constructs directly (StreamAsk, agent_loop_events) without going
// through AddMessage, keeping the contract that EVERY persisted
// message has an ID.
func AssignMessageID(role types.MessageRole) string {
	prefix := "x"
	switch role {
	case types.RoleUser:
		prefix = "u"
	case types.RoleAssistant:
		prefix = "a"
	case types.RoleSystem:
		prefix = "s"
	case types.RoleTool:
		prefix = "t"
	}
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Fallback to PID + bottom 32 bits of UnixNano + random suffix.
		// rand.Read essentially never fails so this path is almost never
		// hit, but it could theoretically happen inside a sandbox with
		// blocked /dev/urandom. Including PID disambiguates processes on
		// the same machine, and the random suffix prevents collisions
		// when multiple conversations start in the same PID within the
		// same 4.3-second nanotime window.
		return fmt.Sprintf("%s-%x-%x", prefix, os.Getpid(), time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buf[:])[:6]
}

// Conversation represents a single AI conversation session with branch support.
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

// ErrorReporter is the optional callback the Manager calls when an
// async save fails. The engine wires this to publish a
// conversation:save:error event on its bus so the TUI / web can
// surface "your last turn didn't persist" instead of letting the
// failure vanish into log.Printf. Nil reporter falls back to log.
type ErrorReporter func(stage string, err error)

type Manager struct {
	mu       sync.RWMutex
	saveMu   sync.Mutex     // serializes saves so snapshots are never stale
	saveWg   sync.WaitGroup // tracks in-flight SaveActiveAsync goroutines so Close drains before SQLite is shut down
	store    *storage.Store
	active   *Conversation
	baseDir  string
	reporter ErrorReporter
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

var conversationIDNow = time.Now

// newConversationID returns a fresh conversation ID that is guaranteed not
// to collide with the currently-active conversation (if any). The base is a
// human-friendly millisecond timestamp; on collision we fall back to the
// full nanosecond clock so rapid-fire Start() calls — e.g. an auto-handoff
// firing in the same millisecond the previous session was created — still
// produce unique IDs.
func newConversationID(active *Conversation, tag string) string {
	now := conversationIDNow()
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
	if strings.TrimSpace(msg.ID) == "" {
		msg.ID = AssignMessageID(msg.Role)
	}
	msgs := m.active.Branches[m.active.Branch]
	msgs = append(msgs, msg)
	m.active.Branches[m.active.Branch] = msgs
}

// RemoveMessagesByID drops every message in the active branch whose
// ID matches one in the given set. Returns the number of messages
// removed. Used when the LLM emits a [cleanup: id1, id2] hint and the
// engine wants to honour it. Safe to call with unknown/empty IDs —
// no-ops silently.
func (m *Manager) RemoveMessagesByID(ids []string) int {
	if len(ids) == 0 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		return 0
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			want[id] = struct{}{}
		}
	}
	if len(want) == 0 {
		return 0
	}
	src := m.active.Branches[m.active.Branch]
	if len(src) == 0 {
		return 0
	}
	out := make([]types.Message, 0, len(src))
	dropped := 0
	for _, msg := range src {
		if _, drop := want[strings.TrimSpace(msg.ID)]; drop && msg.ID != "" {
			dropped++
			continue
		}
		out = append(out, msg)
	}
	if dropped == 0 {
		return 0
	}
	m.active.Branches[m.active.Branch] = out
	return dropped
}
