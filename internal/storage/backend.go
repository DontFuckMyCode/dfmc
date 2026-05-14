package storage

// backend.go — StorageBackend interface for abstracting the underlying
// key-value store. This allows swapping bbolt for PostgreSQL, SQLite,
// or any other backend without changing the Store logic.
//
// Current usage patterns:
//   - bbolt buckets: drive runs, memory (episodic/semantic/working), codemap, AST cache
//   - File system: conversation JSONL/JSON logs (managed by Store, not buckets)
//   - Backup: bbolt WriteTo snapshots
//
// The interface covers the bbolt operations used throughout the codebase.
// File-based operations (conversation logs, artifacts) are handled at the
// Store level and don't need backend abstraction.

import (
	"context"
	"errors"
)

// Common errors returned by backends.
var (
	ErrKeyNotFound = errors.New("key not found")
	ErrTxNil       = errors.New("transaction is nil")
)

// Tx represents a read-only or read-write transaction.
// Implementations may be nested (nested bbolt transactions) or flat.
type Tx interface {
	// Get retrieves the value for key in the bucket.
	// Returns ErrKeyNotFound if the key does not exist.
	Get(bucket, key []byte) ([]byte, error)

	// Put stores the value for key in the bucket.
	// Creates the bucket if it does not exist.
	Put(bucket, key, value []byte) error

	// Delete removes the key from the bucket.
	Delete(bucket, key []byte) error

	// ForEach iterates over all key-value pairs in a bucket.
	// The fn callback receives (key, value) and should return nil to continue
	// or an error to abort iteration.
	ForEach(bucket []byte, fn func(k, v []byte) error) error

	// BucketNames returns all existing bucket names.
	// Some backends may return an empty list if bucket enumeration is not supported.
	BucketNames() ([]string, error)
}

// ReadOnlyTx is a transaction that only allows reads.
// Returned by Backend.View().
type ReadOnlyTx interface {
	Tx
	// No Put or Delete — this is enforced by the interface.
}

// ReadWriteTx is a transaction that allows both reads and writes.
// Returned by Backend.Update().
type ReadWriteTx interface {
	Tx
	// Put and Delete are allowed.
}

// Backend is the abstraction layer for the underlying key-value store.
// The default implementation uses bbolt; alternatives (Postgres, SQLite, etc.)
// must implement this interface.
type Backend interface {
	// View executes fn in a read-only transaction.
	// The transaction is automatically rolled back if fn returns an error.
	View(ctx context.Context, fn func(tx ReadOnlyTx) error) error

	// Update executes fn in a read-write transaction.
	// The transaction is automatically rolled back if fn returns an error.
	Update(ctx context.Context, fn func(tx ReadWriteTx) error) error

	// Ping checks if the backend is reachable.
	Ping(ctx context.Context) error

	// Close shuts down the backend and releases any resources.
	Close() error
}

// BackendOption configures a Backend at creation time.
type BackendOption func(b *backendConfig)

type backendConfig struct {
	path string
}

// WithPath sets the data directory path for the backend.
// Used by file-based backends (bbolt, SQLite) to locate the database.
func WithPath(path string) BackendOption {
	return func(b *backendConfig) {
		b.path = path
	}
}

// BackendType identifies the type of storage backend.
type BackendType string

const (
	BackendBbolt    BackendType = "bbolt"    // go.etcd.io/bbolt (default)
	BackendPostgres BackendType = "postgres" // PostgreSQL
	BackendSQLite   BackendType = "sqlite"   // SQLite
	// Add new backends here: BackendMemcached, BackendBadger, etc.
)

// NewBackend creates a backend of the given type with optional configuration.
// Returns the backend and its type identifier.
func NewBackend(backendType BackendType, opts ...BackendOption) (Backend, BackendType, error) {
	cfg := &backendConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	switch backendType {
	case BackendBbolt, "":
		return newBboltBackend(cfg.path), BackendBbolt, nil
	// case BackendSQLite:
	// 	return newSQLiteBackend(cfg.path), BackendSQLite, nil
	// case BackendPostgres:
	// 	return newPostgresBackend(cfg.path), BackendPostgres, nil
	default:
		return nil, backendType, errors.New("storage.NewBackend: unknown backend type: " + string(backendType))
	}
}
