// Per-turn conversation persistence regression. Without the
// SaveActive call inside recordNativeAgentInteraction, a panic /
// SIGKILL / OOM / power loss between turns drops the entire
// in-memory conversation — the JSONL never sees disk until
// engine.Shutdown(), which a crash never reaches.

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/storage"
)

func TestRecordNativeAgentInteraction_PersistsTurnImmediately(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	mgr := conversation.New(store)
	// Start a conversation so AddMessage has a target.
	mgr.Start("offline", "offline")

	eng := &Engine{
		EventBus:     NewEventBus(),
		Conversation: mgr,
	}

	// Drive one full turn through the recording path. We don't need a
	// realistic completion — just enough to exercise the AddMessage +
	// SaveActive sequence we're asserting on.
	eng.recordNativeAgentInteraction("what's up?", nativeToolCompletion{
		Provider: "offline",
		Model:    "offline",
		Answer:   "all good",
	})

	// The conversations dir must exist with exactly one .jsonl file
	// containing both the user question and the assistant answer.
	convDir := filepath.Join(dir, "data", "artifacts", "conversations")
	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Fatalf("conversations dir not created — per-turn save did not fire: %v", err)
	}
	var jsonl string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			jsonl = filepath.Join(convDir, e.Name())
			break
		}
	}
	if jsonl == "" {
		t.Fatalf("no .jsonl file written; entries: %v", entries)
	}
	body, err := os.ReadFile(jsonl)
	if err != nil {
		t.Fatalf("read conv: %v", err)
	}
	content := string(body)
	if !strings.Contains(content, "what's up?") {
		t.Fatalf("user question missing from saved JSONL:\n%s", content)
	}
	if !strings.Contains(content, "all good") {
		t.Fatalf("assistant answer missing from saved JSONL:\n%s", content)
	}
}
