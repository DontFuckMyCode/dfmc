package storage

// store.go — bbolt handle, default bucket layout, schema migrations,
// and the Open/Close lifecycle. Backups (BackupTo, ListBackups,
// TrimBackups) live in store_backups.go. Conversation persistence
// (SaveConversationLog/State, LoadConversationLog/State,
// writeFileAtomic, syncDir, validateConvID) lives in
// store_conversation.go.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"
	berrors "go.etcd.io/bbolt/errors"
)

var defaultBuckets = []string{
	"_meta",
	"conversations",
	"memory_episodic",
	"memory_semantic",
	"memory_working",
	"codemap_cache",
	"ast_cache",
	"config",
	"plugins",
}

const (
	// schemaVersion is the current database schema version.
	// Bump this whenever a migration is added and runMigrations is updated.
	schemaVersion = 1
	// metaBucket is the bucket used to store db-level metadata (e.g. schema version).
	metaBucket       = "_meta"
	schemaVersionKey = "schema_version"
)

var ErrStoreLocked = errors.New("storage database is locked")

type Store struct {
	db          *bbolt.DB
	dataDir     string
	artifactDir string
}

type OpenError struct {
	Path  string
	Cause error
}

func (e *OpenError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if errors.Is(e.Cause, ErrStoreLocked) {
		return fmt.Sprintf("%s; close other DFMC/TUI processes using %s and try again", ErrStoreLocked.Error(), e.Path)
	}
	return fmt.Sprintf("open storage %s: %v", e.Path, e.Cause)
}

func (e *OpenError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	artifactDir := filepath.Join(dataDir, "artifacts")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return nil, fmt.Errorf("create artifact dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "dfmc.db")
	db, err := bbolt.Open(dbPath, 0o600, &bbolt.Options{
		Timeout:      1 * time.Second,
		FreelistType: bbolt.FreelistMapType,
	})
	if err != nil {
		if errors.Is(err, berrors.ErrTimeout) {
			return nil, &OpenError{
				Path:  dbPath,
				Cause: fmt.Errorf("%w: %w", ErrStoreLocked, err),
			}
		}
		return nil, &OpenError{Path: dbPath, Cause: err}
	}

	err = db.Update(func(tx *bbolt.Tx) error {
		for _, bucket := range defaultBuckets {
			if _, e := tx.CreateBucketIfNotExists([]byte(bucket)); e != nil {
				return fmt.Errorf("create bucket %s: %w", bucket, e)
			}
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return &Store{
		db:          db,
		dataDir:     dataDir,
		artifactDir: artifactDir,
	}, nil
}

// runMigrations checks the schema version stored in the _meta bucket and
// runs any needed migrations to bring the database up to the current
// schemaVersion. It is called on every store Open so that upgrades are
// applied automatically. The migration functions are idempotent — safe to
// re-run if a previous run was interrupted or if the db is already at the
// target version.
func runMigrations(db *bbolt.DB) error {
	return db.Update(func(tx *bbolt.Tx) error {
		meta := tx.Bucket([]byte(metaBucket))
		if meta == nil {
			return fmt.Errorf("meta bucket not found")
		}
		verData := meta.Get([]byte(schemaVersionKey))
		var currentVersion int
		if verData != nil {
			if err := json.Unmarshal(verData, &currentVersion); err != nil {
				return fmt.Errorf("decode schema version: %w", err)
			}
		}
		if currentVersion >= schemaVersion {
			return nil // already at current version
		}
		// Migration runner: apply in order, updating the version key after
		// each successful step. If we add a migration function here, bump
		// schemaVersion to match.
		if currentVersion < 1 {
			// No-op migration for v0 -> v1 (initial version). Future
			// migrations go here, e.g.:
			//   if err := migrateV1AddWorkingMemory(tx); err != nil { return err }
			//   if err := writeVersion(meta, 1); err != nil { return err }
			if err := writeSchemaVersion(meta, 1); err != nil {
				return fmt.Errorf("v0->v1: %w", err)
			}
			currentVersion = 1
		}
		// Add: if currentVersion < 2 { migrateV2... }
		return nil
	})
}

func writeSchemaVersion(meta *bbolt.Bucket, version int) error {
	data, err := json.Marshal(version)
	if err != nil {
		return err
	}
	return meta.Put([]byte(schemaVersionKey), data)
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) DB() *bbolt.DB {
	return s.db
}

func (s *Store) DataDir() string {
	return s.dataDir
}

func (s *Store) ArtifactsDir() string {
	return s.artifactDir
}
