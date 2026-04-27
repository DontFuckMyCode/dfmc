// Pin tests for conversation persistence (List on disk, BranchList,
// BranchCreate/Switch edge cases, ID collision recovery, Search miss).
// manager_test.go covers Save/Load/Search/UndoLast/BranchCompare at
// the happy-path level; this file covers the corner cases that bit
// us in the past (ID collision when two Start() calls land in the
// same millisecond, BranchSwitch to a non-existent branch, List with
// stale .jsonl files, etc.).

package conversation

import (
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/storage"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func openConvStore(t *testing.T) *storage.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// BranchList must always include "main" once a conversation has
// started, and must surface every branch added since. The TUI
// branch picker reads this directly.
func TestBranchList_IncludesMainAndCustom(t *testing.T) {
	mgr := New(openConvStore(t))
	mgr.Start("offline", "offline-v1")

	if err := mgr.BranchCreate("alt"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := mgr.BranchCreate("experiment"); err != nil {
		t.Fatalf("create: %v", err)
	}

	branches := mgr.BranchList()
	got := strings.Join(sortStrs(branches), ",")
	if got != "alt,experiment,main" {
		t.Fatalf("BranchList=%q want main+alt+experiment", got)
	}
}

// BranchList on a manager with no active conversation must not panic
// and must return nil (or empty). The web /branches endpoint hits
// this on an idle engine.
func TestBranchList_NoActiveReturnsEmpty(t *testing.T) {
	mgr := New(openConvStore(t))
	if got := mgr.BranchList(); len(got) != 0 {
		t.Fatalf("BranchList with no active should be empty; got %v", got)
	}
}

// BranchSwitch to a missing branch must error rather than silently
// rebooting on main — that was the old behavior and it cost a user
// their working notes.
func TestBranchSwitch_MissingBranchErrors(t *testing.T) {
	mgr := New(openConvStore(t))
	mgr.Start("offline", "offline-v1")
	err := mgr.BranchSwitch("doesnt_exist")
	if err == nil {
		t.Fatalf("switching to missing branch should error")
	}
	if !strings.Contains(err.Error(), "doesnt_exist") &&
		!strings.Contains(err.Error(), "branch") {
		t.Fatalf("error should mention branch name or 'branch'; got %q", err.Error())
	}
}

// BranchCreate must reject a duplicate name — silently overwriting
// "main" would erase the conversation.
func TestBranchCreate_DuplicateRejected(t *testing.T) {
	mgr := New(openConvStore(t))
	mgr.Start("offline", "offline-v1")
	if err := mgr.BranchCreate("alt"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	err := mgr.BranchCreate("alt")
	if err == nil {
		t.Fatalf("duplicate create should error")
	}
}

// newConversationID must produce different IDs even when called
// twice in the same millisecond. The fall-through uses Nanosecond()
// to disambiguate; pin the contract so a future cleanup doesn't
// re-introduce the collision.
func TestNewConversationID_DisambiguatesOnCollision(t *testing.T) {
	now := time.Now()
	base := "conv_" + now.Format("20060102_150405.000")
	active := &Conversation{ID: base}
	got := newConversationID(active, "")
	if got == base {
		t.Fatalf("collision case should not return same ID; got %q", got)
	}
	if !strings.HasPrefix(got, base+"_") {
		t.Fatalf("collision ID should extend the base; got %q want prefix %q_", got, base)
	}
}

// newConversationID with a tag must include it.
func TestNewConversationID_WithTag(t *testing.T) {
	got := newConversationID(nil, "handoff")
	if !strings.Contains(got, "_handoff") {
		t.Fatalf("tag not embedded in ID: %q", got)
	}
}

// List() must round-trip saved conversations. After SaveActive +
// fresh Load via List, the message count and ID must match. This
// catches the JSONL writer/reader pair drifting (one of the top-3
// risk areas in the codebase per the analysis).
func TestList_RoundTripsSavedConversation(t *testing.T) {
	st := openConvStore(t)
	mgr := New(st)
	mgr.Start("offline", "offline-v1")
	mgr.AddMessage("offline", "offline-v1", types.Message{
		Role: types.RoleUser, Content: "ping", Timestamp: time.Now(),
	})
	mgr.AddMessage("offline", "offline-v1", types.Message{
		Role: types.RoleAssistant, Content: "pong", Timestamp: time.Now(),
	})
	if err := mgr.SaveActive(); err != nil {
		t.Fatalf("save: %v", err)
	}

	summaries, err := mgr.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0].MessageN != 2 {
		t.Fatalf("expected 2 messages, got %d", summaries[0].MessageN)
	}
}

// List() on a fresh store (no conversations on disk yet) must return
// (nil, nil) — not an error. Web /conversations endpoint relies on
// this to render an empty list on first run.
func TestList_EmptyStoreReturnsNil(t *testing.T) {
	mgr := New(openConvStore(t))
	got, err := mgr.List()
	if err != nil {
		t.Fatalf("empty store should not error; got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty store should return empty list; got %v", got)
	}
}

// Search with a query that matches nothing must return empty (not
// fall through to List). Pin so a future "smart fallback" doesn't
// accidentally surface unrelated conversations.
func TestSearch_NoMatchReturnsEmpty(t *testing.T) {
	st := openConvStore(t)
	mgr := New(st)
	mgr.Start("offline", "offline-v1")
	mgr.AddMessage("offline", "offline-v1", types.Message{
		Role: types.RoleUser, Content: "auth flow question", Timestamp: time.Now(),
	})
	if err := mgr.SaveActive(); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := mgr.Search("payment-processing-no-match-keyword", 10)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("search with no-match keyword should be empty; got %d", len(got))
	}
}

// Messages() on a nil receiver returns nil instead of panicking.
// engine.handleAgentLoop checks for empty conversations via
// active.Messages() before any nil-guard.
func TestConversation_MessagesNilReceiver(t *testing.T) {
	var c *Conversation
	if got := c.Messages(); got != nil {
		t.Fatalf("nil receiver should return nil; got %v", got)
	}
}

// AddMessage must auto-Start when called against a nil active. Many
// CLI flows do `mgr.AddMessage(...)` without an explicit Start —
// pin the implicit-start contract so a future strictness change
// breaks loudly here.
func TestAddMessage_AutoStartsActive(t *testing.T) {
	mgr := New(openConvStore(t))
	if mgr.Active() != nil {
		t.Fatalf("expected no active conversation pre-AddMessage")
	}
	mgr.AddMessage("offline", "offline-v1", types.Message{
		Role: types.RoleUser, Content: "hi", Timestamp: time.Now(),
	})
	if mgr.Active() == nil {
		t.Fatalf("AddMessage should auto-Start an active conversation")
	}
	if got := len(mgr.Active().Messages()); got != 1 {
		t.Fatalf("auto-started conversation should hold the message; got %d", got)
	}
}

func sortStrs(in []string) []string {
	out := append([]string(nil), in...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// TestSaveActive_DoesNotBlockConcurrentAddMessage pins H4: SaveActive
// must release the manager mutex before doing the disk write so a slow
// fsync doesn't pile up every concurrent writer behind it. We hammer
// SaveActive in one goroutine while another goroutine appends messages,
// and assert the appender's progress is steady — not stalled by the I/O.
//
// The previous implementation held m.mu (RLock) across SaveConversationLog,
// which on Windows + indexer noise could stall writers for >100ms per save.
// This test would have failed there because the appender's per-iteration
// observed progress would have correlated with save completions instead of
// running freely.
func TestSaveActive_DoesNotBlockConcurrentAddMessage(t *testing.T) {
	mgr := New(openConvStore(t))
	mgr.Start("offline", "offline-v1")
	// Seed a non-empty branch so SaveActive actually writes.
	mgr.AddMessage("offline", "offline-v1", types.Message{
		Role: types.RoleUser, Content: "seed", Timestamp: time.Now(),
	})

	const totalAppends = 200
	var (
		wg         sync.WaitGroup
		appended   atomic.Int64
		stopSaving atomic.Bool
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stopSaving.Load() {
			_ = mgr.SaveActive()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < totalAppends; i++ {
			mgr.AddMessage("offline", "offline-v1", types.Message{
				Role: types.RoleUser, Content: "msg", Timestamp: time.Now(),
			})
			appended.Add(1)
		}
		stopSaving.Store(true)
	}()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("save-while-appending stalled: appended=%d/%d (deadlock or severe lock contention)", appended.Load(), totalAppends)
	}

	if got := appended.Load(); got != totalAppends {
		t.Fatalf("appender did not finish under concurrent SaveActive; got=%d want=%d", got, totalAppends)
	}
}
