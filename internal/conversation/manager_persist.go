package conversation

// manager_persist.go — disk persistence and recall for the conversation
// manager. SaveActive / SaveActiveAsync round-trip the active branch
// through the storage.Store; Load / LoadReadOnly pull historical
// conversations back from disk. List / Search + the read-side helpers
// (normalizeConversation, totalMessageCount, legacyConversationStartedAt)
// live in manager_query.go. The split keeps the core in-memory mutation
// paths in manager.go from being drowned out by the I/O scaffolding here.
//
// Concurrency contract: m.mu is read-locked only long enough to snapshot
// the immutable refs (store, active id) — disk I/O happens with no
// engine lock held so a long history scan can never block writers.
// saveMu serializes blocking + async saves so the snapshot taken by
// one is not invalidated mid-fsync by another goroutine's AddMessage.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

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
	// m.mu across I/O still applies — we keep the RLock short.
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

// SaveActiveAsync persists the active conversation without blocking the
// caller. Failures are logged but never propagated — this is best-effort
// durability for crash-before-shutdown scenarios. Uses saveMu to serialize
// with the blocking SaveActive call so the two never race. saveWg lets
// Close drain pending writes before the underlying bbolt store is shut
// down — without it, a goroutine scheduled microseconds before Shutdown
// could try to write to a closed handle.
func (m *Manager) SaveActiveAsync() {
	m.saveWg.Add(1)
	go func() {
		defer m.saveWg.Done()
		m.saveMu.Lock()
		defer m.saveMu.Unlock()

		m.mu.RLock()
		if m.active == nil || m.store == nil {
			m.mu.RUnlock()
			return
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
			m.reportError("state", err)
			return
		}
		if err := store.SaveConversationLog(snapshot.ID, snapshot.Branches[snapshot.Branch]); err != nil {
			m.reportError("log", err)
		}
	}()
}

// SetErrorReporter wires an optional callback that fires when an
// async save step fails. The engine passes a reporter that publishes
// a conversation:save:error event on the bus; tests pass nil to
// keep the fallback log path. Safe to call after Manager has been
// in use — single store under m.mu.
func (m *Manager) SetErrorReporter(r ErrorReporter) {
	m.mu.Lock()
	m.reporter = r
	m.mu.Unlock()
}

func (m *Manager) reportError(stage string, err error) {
	if err == nil {
		return
	}
	m.mu.RLock()
	r := m.reporter
	m.mu.RUnlock()
	if r != nil {
		r(stage, err)
		return
	}
	log.Printf("conversation: SaveActiveAsync %s: %v", stage, err)
}

// Close drains any in-flight SaveActiveAsync goroutines so callers can
// shut the underlying bbolt store down without races. Call this before
// closing the store; otherwise an async save scheduled microseconds
// earlier may run on a closed handle and silently lose the turn.
func (m *Manager) Close() {
	m.saveWg.Wait()
}

func (m *Manager) Load(id string) (*Conversation, error) {
	c, err := m.loadFromStore(id)
	if err != nil {
		return nil, err
	}
	// Hold the write lock for the entire swap so concurrent Active()
	// readers see an consistent pointer, not a partial write.
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

// isJSONError reports whether err indicates a JSON file that is absent,
// empty, or structurally malformed — in all these cases the .jsonl
// fallback is worth attempting.
func isJSONError(err error) bool {
	if err == nil {
		return false
	}
	// os.IsNotExist — .json absent (redundant with caller check, but defensive)
	if os.IsNotExist(err) {
		return true
	}
	// json.Unmarshal wraps its errors; check the chain.
	// *json.SyntaxError — truncated/garbage JSON (e.g. crash mid-write)
	// *json.UnmarshalTypeError — wrong types (partial schema match)
	// io.EOF — empty file
	var synErr *json.SyntaxError
	var typErr *json.UnmarshalTypeError
	if errors.As(err, &synErr) || errors.As(err, &typErr) || errors.Is(err, io.EOF) {
		return true
	}
	return false
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
	} else if os.IsNotExist(err) {
		// .json does not exist — fall through to .jsonl fallback
	} else if isJSONError(err) {
		// .json is corrupted (truncated/malformed) — fall through to .jsonl fallback
	} else {
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

