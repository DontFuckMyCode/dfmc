package storage

// store.go — SQLite handle, default table layout, schema migrations,
// and the Open/Close lifecycle. Backups (BackupTo, ListBackups,
// TrimBackups) live in store_backups.go. Conversation persistence
// (SaveConversationLog/State, LoadConversationLog/State,
// writeFileAtomic, syncDir, validateConvID) lives in
// store_conversation.go.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
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
	"subagent_journal",
	"drive-runs",
	"tasks",
}

const (
	// schemaVersion is the current database schema version.
	// Bump this whenever a migration is added and runMigrations is updated.
	schemaVersion = 1
	// metaBucket is the table used to store db-level metadata (e.g. schema version).
	metaBucket       = "_meta"
	schemaVersionKey = "schema_version"
)

var ErrStoreLocked = errors.New("storage database is locked")

// Options controls how the store is opened.
type Options struct {
	// ReadOnly opens the database in read-only mode when true.
	// Multiple read-only processes can access the same database concurrently.
	ReadOnly bool
}

type Store struct {
	db          *sql.DB
	dataDir     string
	artifactDir string
	readOnly    bool
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

// Open opens the SQLite database at dataDir/dfmc.db.
// It creates default tables and runs migrations when opened in write mode.
// Use OpenWithOpts for read-only access (e.g. secondary CLI/TUI instances).
func Open(dataDir string) (*Store, error) {
	return OpenWithOpts(dataDir, Options{})
}

// OpenWithOpts opens the SQLite database with custom options.
// When opts.ReadOnly is true, the database is opened with a shared lock,
// allowing multiple read-only processes to access it concurrently.
func OpenWithOpts(dataDir string, opts Options) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	artifactDir := filepath.Join(dataDir, "artifacts")
	if !opts.ReadOnly {
		if err := os.MkdirAll(artifactDir, 0o755); err != nil {
			return nil, fmt.Errorf("create artifact dir: %w", err)
		}
	}

	dbPath := filepath.Join(dataDir, "dfmc.db")
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)"
	if opts.ReadOnly {
		dsn = dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&mode=ro"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, &OpenError{Path: dbPath, Cause: err}
	}

	// Verify connection works
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, &OpenError{Path: dbPath, Cause: err}
	}

	// Skip table creation and migrations in read-only mode.
	if !opts.ReadOnly {
		if err := createTables(db); err != nil {
			_ = db.Close()
			return nil, err
		}

		if err := runMigrations(db); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("run migrations: %w", err)
		}
	}

	return &Store{
		db:          db,
		dataDir:     dataDir,
		artifactDir: artifactDir,
		readOnly:    opts.ReadOnly,
	}, nil
}

// createTables creates the kv_store table for each bucket.
// Each bucket gets its own table with key/value columns where value is a JSON blob.
func createTables(db *sql.DB) error {
	for _, bucket := range defaultBuckets {
		sql := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS "%s" (
				key TEXT PRIMARY KEY,
				value BLOB
			);
		`, bucket)
		if _, err := db.Exec(sql); err != nil {
			return fmt.Errorf("create table %s: %w", bucket, err)
		}
	}
	return nil
}

// runMigrations checks the schema version stored in the _meta table and
// runs any needed migrations to bring the database up to the current
// schemaVersion. It is called on every store Open so that upgrades are
// applied automatically. The migration functions are idempotent — safe to
// re-run if a previous run was interrupted or if the db is already at the
// target version.
func runMigrations(db *sql.DB) error {
	var currentVersion int
	row := db.QueryRow(fmt.Sprintf(`SELECT value FROM "%s" WHERE key = ?`, metaBucket), schemaVersionKey)
	var verData []byte
	if err := row.Scan(&verData); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read schema version: %w", err)
		}
		// No version stored yet — start at 0
	} else if verData != nil {
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
		// migrations go here.
		if err := writeSchemaVersion(db, 1); err != nil {
			return fmt.Errorf("v0->v1: %w", err)
		}
		currentVersion = 1
	}
	// Add: if currentVersion < 2 { migrateV2... }
	return nil
}

func writeSchemaVersion(db *sql.DB, version int) error {
	data, err := json.Marshal(version)
	if err != nil {
		return err
	}
	sql := fmt.Sprintf(`
		INSERT INTO "%s" (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, metaBucket)
	_, err = db.Exec(sql, schemaVersionKey, data)
	return err
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) DataDir() string {
	return s.dataDir
}

func (s *Store) ArtifactsDir() string {
	return s.artifactDir
}

// ReadOnly returns true if the store was opened in read-only mode.
func (s *Store) ReadOnly() bool {
	if s == nil {
		return false
	}
	return s.readOnly
}

// BucketPut stores a key-value pair in the named bucket (table).
func (s *Store) BucketPut(bucket, key string, value []byte) error {
	if s.db == nil {
		return errors.New("store is not open")
	}
	sql := fmt.Sprintf(`
		INSERT INTO "%s" (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, bucket)
	_, err := s.db.Exec(sql, key, value)
	return err
}

// BucketGet retrieves a value by key from the named bucket (table).
func (s *Store) BucketGet(bucket, key string) ([]byte, error) {
	if s.db == nil {
		return nil, errors.New("store is not open")
	}
	var value []byte
	err := s.db.QueryRow(fmt.Sprintf(`SELECT value FROM "%s" WHERE key = ?`, bucket), key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return value, err
}

// BucketDelete removes a key from the named bucket (table).
func (s *Store) BucketDelete(bucket, key string) error {
	if s.db == nil {
		return errors.New("store is not open")
	}
	_, err := s.db.Exec(fmt.Sprintf(`DELETE FROM "%s" WHERE key = ?`, bucket), key)
	return err
}

// BucketForEach iterates over all key-value pairs in the named bucket.
// If fn returns an error, iteration stops and that error is returned.
func (s *Store) BucketForEach(bucket string, fn func(key, value []byte) error) error {
	if s.db == nil {
		return errors.New("store is not open")
	}
	rows, err := s.db.Query(fmt.Sprintf(`SELECT key, value FROM "%s"`, bucket))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key, value []byte
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		if err := fn(key, value); err != nil {
			return err
		}
	}
	return rows.Err()
}

// BucketClear deletes all rows from the named bucket (table).
func (s *Store) BucketClear(bucket string) error {
	if s.db == nil {
		return errors.New("store is not open")
	}
	_, err := s.db.Exec(fmt.Sprintf(`DELETE FROM "%s"`, bucket))
	return err
}
