package storage

import (
	"path/filepath"
	"testing"
	"time"

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
