package storage

// backend_bbolt.go — bbolt implementation of the Backend interface.
// Wraps the go.etcd.io/bbolt package to satisfy the storage.Backend interface.

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
)

// bboltBackend implements Backend using bbolt.
type bboltBackend struct {
	db     *bbolt.DB
	dbPath string
	mu     sync.Mutex // serializes Update calls; View is already safe
}

// newBboltBackend creates a new bbolt backend.
// The path should be the directory where dfmc.db will be created.
func newBboltBackend(path string) *bboltBackend {
	return &bboltBackend{dbPath: path}
}

// Open initializes the bbolt database.
// Call this before using the backend.
func (b *bboltBackend) Open() error {
	if b.db != nil {
		return nil
	}

	dbFile := filepath.Join(b.dbPath, "dfmc.db")
	db, err := bbolt.Open(dbFile, 0o600, &bbolt.Options{
		Timeout:      1 * time.Second,
		FreelistType: bbolt.FreelistMapType,
	})
	if err != nil {
		if errors.Is(err, berrors.ErrTimeout) {
			return ErrBackendLocked
		}
		return err
	}
	b.db = db
	return nil
}

// View executes fn in a read-only transaction.
func (b *bboltBackend) View(ctx context.Context, fn func(tx ReadOnlyTx) error) error {
	if b.db == nil {
		return ErrBackendNotOpen
	}

	return b.db.View(func(btx *bbolt.Tx) error {
		return fn(&bboltReadOnlyTx{tx: btx})
	})
}

// Update executes fn in a read-write transaction.
func (b *bboltBackend) Update(ctx context.Context, fn func(tx ReadWriteTx) error) error {
	if b.db == nil {
		return ErrBackendNotOpen
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	return b.db.Update(func(btx *bbolt.Tx) error {
		return fn(&bboltReadWriteTx{tx: btx})
	})
}

// Ping checks if the database is reachable.
func (b *bboltBackend) Ping(ctx context.Context) error {
	if b.db == nil {
		return ErrBackendNotOpen
	}
	return b.db.View(func(*bbolt.Tx) error {
		return nil
	})
}

// Close shuts down the database.
func (b *bboltBackend) Close() error {
	if b.db == nil {
		return nil
	}
	err := b.db.Close()
	b.db = nil
	return err
}

// DB returns the underlying bbolt.DB for cases that need direct access.
// Use with caution — prefer the Backend interface methods when possible.
func (b *bboltBackend) DB() *bbolt.DB {
	return b.db
}

// EnsureBuckets creates all specified buckets if they don't exist.
// This is a convenience method for initialization.
func (b *bboltBackend) EnsureBuckets(bucketNames []string) error {
	return b.Update(context.Background(), func(tx ReadWriteTx) error {
		for _, name := range bucketNames {
			if err := tx.Put([]byte(name), nil, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// bboltReadOnlyTx wraps a bbolt read-only transaction.
type bboltReadOnlyTx struct {
	tx *bbolt.Tx
}

func (t *bboltReadOnlyTx) Get(bucket, key []byte) ([]byte, error) {
	b := t.tx.Bucket(bucket)
	if b == nil {
		return nil, ErrKeyNotFound
	}
	val := b.Get(key)
	if val == nil {
		return nil, ErrKeyNotFound
	}
	return append([]byte(nil), val...), nil
}

func (t *bboltReadOnlyTx) Put(bucket, key, value []byte) error {
	return errors.New("Put not allowed in read-only transaction")
}

func (t *bboltReadOnlyTx) Delete(bucket, key []byte) error {
	return errors.New("Delete not allowed in read-only transaction")
}

func (t *bboltReadOnlyTx) ForEach(bucket []byte, fn func(k, v []byte) error) error {
	b := t.tx.Bucket(bucket)
	if b == nil {
		return nil
	}
	return b.ForEach(func(k, v []byte) error {
		return fn(append([]byte(nil), k...), append([]byte(nil), v...))
	})
}

func (t *bboltReadOnlyTx) BucketNames() ([]string, error) {
	return bboltBucketNames(t.tx)
}

// bboltReadWriteTx wraps a bbolt read-write transaction.
type bboltReadWriteTx struct {
	tx *bbolt.Tx
}

func (t *bboltReadWriteTx) Get(bucket, key []byte) ([]byte, error) {
	b := t.tx.Bucket(bucket)
	if b == nil {
		return nil, ErrKeyNotFound
	}
	val := b.Get(key)
	if val == nil {
		return nil, ErrKeyNotFound
	}
	return append([]byte(nil), val...), nil
}

func (t *bboltReadWriteTx) Put(bucket, key, value []byte) error {
	b, err := t.tx.CreateBucketIfNotExists(bucket)
	if err != nil {
		return err
	}
	return b.Put(key, value)
}

func (t *bboltReadWriteTx) Delete(bucket, key []byte) error {
	b := t.tx.Bucket(bucket)
	if b == nil {
		return nil
	}
	return b.Delete(key)
}

func (t *bboltReadWriteTx) ForEach(bucket []byte, fn func(k, v []byte) error) error {
	b := t.tx.Bucket(bucket)
	if b == nil {
		return nil
	}
	return b.ForEach(func(k, v []byte) error {
		return fn(append([]byte(nil), k...), append([]byte(nil), v...))
	})
}

func (t *bboltReadWriteTx) BucketNames() ([]string, error) {
	return bboltBucketNames(t.tx)
}

func bboltBucketNames(tx *bbolt.Tx) ([]string, error) {
	var names []string
	err := tx.ForEach(func(name []byte, _ *bbolt.Bucket) error {
		names = append(names, string(append([]byte(nil), name...)))
		return nil
	})
	return names, err
}

// Common backend errors.
var (
	ErrBackendNotOpen = errors.New("storage backend is not open")
	ErrBackendLocked  = errors.New("storage backend is locked")
)
