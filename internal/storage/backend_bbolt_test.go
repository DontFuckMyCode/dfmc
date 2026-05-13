package storage

import (
	"context"
	"testing"
)

func TestBboltBackendImplementsBackend(t *testing.T) {
	// Verify bboltBackend implements Backend interface
	var backend Backend = &bboltBackend{}
	if backend == nil {
		t.Fatal("bboltBackend should implement Backend")
	}
}

func TestBboltReadOnlyTxImplementsReadOnlyTx(t *testing.T) {
	// Verify bboltReadOnlyTx implements ReadOnlyTx
	var tx ReadOnlyTx = &bboltReadOnlyTx{}
	if tx == nil {
		t.Fatal("bboltReadOnlyTx should implement ReadOnlyTx")
	}
}

func TestBboltReadWriteTxImplementsReadWriteTx(t *testing.T) {
	// Verify bboltReadWriteTx implements ReadWriteTx
	var tx ReadWriteTx = &bboltReadWriteTx{}
	if tx == nil {
		t.Fatal("bboltReadWriteTx should implement ReadWriteTx")
	}
}

func TestNewBackendReturnsBbolt(t *testing.T) {
	b, typ, err := NewBackend(BackendBbolt, WithPath(t.TempDir()))
	if err != nil {
		t.Fatalf("NewBackend failed: %v", err)
	}
	if typ != BackendBbolt {
		t.Errorf("expected BackendBbolt, got %v", typ)
	}
	if b == nil {
		t.Fatal("backend should not be nil")
	}
}

func TestBboltBackendOpenClose(t *testing.T) {
	tmpDir := t.TempDir()
	backend := newBboltBackend(tmpDir)

	// Open should succeed
	if err := backend.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	// Ping should work after Open
	if err := backend.Ping(context.Background()); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	// Close should succeed
	if err := backend.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestBboltBackendViewUpdate(t *testing.T) {
	tmpDir := t.TempDir()
	backend := newBboltBackend(tmpDir)

	if err := backend.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer backend.Close()

	// Write a value
	err := backend.Update(context.Background(), func(tx ReadWriteTx) error {
		return tx.Put([]byte("test_bucket"), []byte("key1"), []byte("value1"))
	})
	if err != nil {
		t.Fatalf("Update write failed: %v", err)
	}

	// Read the value
	var result []byte
	err = backend.View(context.Background(), func(tx ReadOnlyTx) error {
		var err error
		result, err = tx.Get([]byte("test_bucket"), []byte("key1"))
		return err
	})
	if err != nil {
		t.Fatalf("View read failed: %v", err)
	}
	if string(result) != "value1" {
		t.Errorf("expected 'value1', got '%s'", result)
	}
}

func TestBboltBackendDelete(t *testing.T) {
	tmpDir := t.TempDir()
	backend := newBboltBackend(tmpDir)

	if err := backend.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer backend.Close()

	// Write then delete
	err := backend.Update(context.Background(), func(tx ReadWriteTx) error {
		if err := tx.Put([]byte("del_bucket"), []byte("key1"), []byte("val")); err != nil {
			return err
		}
		return tx.Delete([]byte("del_bucket"), []byte("key1"))
	})
	if err != nil {
		t.Fatalf("Update delete failed: %v", err)
	}

	// Verify deleted
	err = backend.View(context.Background(), func(tx ReadOnlyTx) error {
		_, err := tx.Get([]byte("del_bucket"), []byte("key1"))
		if err == nil {
			t.Error("expected key to be deleted")
		}
		return nil
	})
}

func TestBboltBackendForEach(t *testing.T) {
	tmpDir := t.TempDir()
	backend := newBboltBackend(tmpDir)

	if err := backend.Open(); err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer backend.Close()

	// Write multiple values
	backend.Update(context.Background(), func(tx ReadWriteTx) error {
		for i := 0; i < 5; i++ {
			key := []byte{byte('a' + i)}
			if err := tx.Put([]byte("foreach_bucket"), key, []byte{byte(i)}); err != nil {
				return err
			}
		}
		return nil
	})

	// Count via ForEach
	count := 0
	backend.View(context.Background(), func(tx ReadOnlyTx) error {
		return tx.ForEach([]byte("foreach_bucket"), func(k, v []byte) error {
			count++
			return nil
		})
	})

	if count != 5 {
		t.Errorf("expected 5 items, got %d", count)
	}
}
