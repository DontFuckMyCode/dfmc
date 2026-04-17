package storage

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.etcd.io/bbolt"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestOpenAndConversationRoundtrip(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	msgs := []types.Message{
		{
			Role:      types.RoleUser,
			Content:   "hello",
			Timestamp: time.Now(),
		},
		{
			Role:      types.RoleAssistant,
			Content:   "world",
			Timestamp: time.Now().Add(1 * time.Second),
		},
	}

	if err := store.SaveConversationLog("conv_test", msgs); err != nil {
		t.Fatalf("save log: %v", err)
	}

	got, err := store.LoadConversationLog("conv_test")
	if err != nil {
		t.Fatalf("load log: %v", err)
	}

	if len(got) != len(msgs) {
		t.Fatalf("expected %d messages, got %d", len(msgs), len(got))
	}
	if got[0].Content != "hello" || got[1].Content != "world" {
		t.Fatalf("unexpected content: %#v", got)
	}
}

func TestOpenErrorWrapsStoreLocked(t *testing.T) {
	err := &OpenError{
		Path:  "C:/tmp/dfmc.db",
		Cause: errors.Join(ErrStoreLocked, bbolt.ErrTimeout),
	}

	if !errors.Is(err, ErrStoreLocked) {
		t.Fatal("expected store lock sentinel to be preserved")
	}
	if !errors.Is(err, bbolt.ErrTimeout) {
		t.Fatal("expected timeout cause to be preserved")
	}
	if got := err.Error(); got == "" || !containsAll(got, []string{"locked", "DFMC/TUI", "C:/tmp/dfmc.db"}) {
		t.Fatalf("unexpected open error message: %q", got)
	}
}

func containsAll(haystack string, needles []string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}

// A single message can carry large tool output — a multi-megabyte
// grep result, a pasted patch, a serialized AST dump. bufio.Scanner's
// default line limit (64 KiB) used to trip here with "token too
// long" and fail the whole load. Pin the new 8 MiB buffer by
// round-tripping a ~1 MiB message — well past the default, well under
// the new ceiling.
func TestConversationLog_RoundtripsLargeMessage(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// ~1 MiB of a repeating pattern — larger than 64 KiB but cheap to
	// construct and diff if the test ever fails.
	big := strings.Repeat("abcdefghij ", 100_000) // ~1.1 MiB
	msgs := []types.Message{
		{Role: types.RoleUser, Content: "hi", Timestamp: time.Now()},
		{Role: types.RoleAssistant, Content: big, Timestamp: time.Now()},
	}
	if err := store.SaveConversationLog("conv_big", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.LoadConversationLog("conv_big")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 || got[1].Content != big {
		t.Fatalf("roundtrip lost the large message (len got=%d want=%d)", len(got[1].Content), len(big))
	}
}

// Temp-and-rename atomicity: confirm SaveConversationLog leaves no
// .dfmc-tmp-* files behind after a successful save. A failed rename
// would leave debris, so this indirectly pins the cleanup path.
func TestConversationLog_SaveLeavesNoTempDebris(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	msgs := []types.Message{{Role: types.RoleUser, Content: "x", Timestamp: time.Now()}}
	if err := store.SaveConversationLog("conv_debris", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}
	convDir := filepath.Join(dir, "data", "artifacts", "conversations")
	entries, err := os.ReadDir(convDir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "dfmc-tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}
