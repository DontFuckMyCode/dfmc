// Drive run persistence on SQLite.
//
// Each Run is a JSON blob stored under table "drive-runs", keyed by
// the run ID. The store is best-effort durable: every status change
// (planning -> running -> done, plus per-TODO transitions) writes the
// whole Run back so a crash mid-loop loses at most the in-flight
// transition. JSON over a binary schema is deliberate — drive runs are
// inspected by hand often enough that human-readable storage pays off,
// and the volume is tiny (one record per `dfmc drive` invocation).
//
// The table lives in the same SQLite file as memory and conversations
// (Engine.Storage), so a single database covers everything. With WAL
// mode, multiple readers can coexist with one writer.

package drive

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const driveBucket = "drive-runs"

// RunStore is the interface for drive run persistence. The concrete
// *Store implements it; alternate implementations (in-memory for tests,
// remote storage for distributed setups) can satisfy this interface
// without touching the SQLite-based Store.
type RunStore interface {
	Save(run *Run) error
	Load(id string) (*Run, error)
	List() ([]*Run, error)
	Delete(id string) error
}

// Store is the SQLite-backed implementation of RunStore. Take a
// *sql.DB from engine.Storage.DB() and pass it here; the table is
// created lazily on the first Save so an empty store is valid
// (List on an empty store returns nil, nil).
type Store struct {
	db *sql.DB
}

var _ RunStore = (*Store)(nil) // compile-time check

// NewStore wraps a SQLite handle. Returns an error only if db is nil
// — table creation is deferred to the first write so an empty store
// is valid (List on an empty store returns nil, nil).
func NewStore(db *sql.DB) (RunStore, error) {
	if db == nil {
		return nil, fmt.Errorf("drive.NewStore: db is nil")
	}
	return &Store{db: db}, nil
}

// Save writes (or overwrites) the run record. Called on every state
// transition so resume works correctly even after a crash. Per-call
// JSON marshaling is fine — drive runs hold dozens of TODOs, not
// thousands, so the blob stays under a few KB.
func (s *Store) Save(run *Run) error {
	if run == nil {
		return fmt.Errorf("drive.Store.Save: run is nil")
	}
	if run.ID == "" {
		return fmt.Errorf("drive.Store.Save: run.ID is empty")
	}
	data, err := json.MarshalIndent(run, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run: %w", err)
	}
	sqlStmt := fmt.Sprintf(`
		INSERT INTO "%s" (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, driveBucket)
	_, err = s.db.Exec(sqlStmt, run.ID, data)
	return err
}

// Load fetches a run by ID. Returns (nil, nil) on miss so callers can
// distinguish "not found" from a real error. The returned pointer is
// owned by the caller — modifications do not write back automatically.
func (s *Store) Load(id string) (*Run, error) {
	var data []byte
	sqlStmt := fmt.Sprintf(`SELECT value FROM "%s" WHERE key = ?`, driveBucket)
	err := s.db.QueryRow(sqlStmt, id).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, fmt.Errorf("unmarshal run %q: %w", id, err)
	}
	return &run, nil
}

// List returns all runs ordered newest-first by CreatedAt. Used by the
// CLI's `dfmc drive list` and the TUI history view. Cheap enough to
// scan the whole table — drive runs accumulate slowly in practice.
func (s *Store) List() ([]*Run, error) {
	var runs []*Run
	sqlStmt := fmt.Sprintf(`SELECT value FROM "%s"`, driveBucket)
	rows, err := s.db.Query(sqlStmt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var run Run
		if err := json.Unmarshal(data, &run); err != nil {
			continue // skip corrupted entries instead of failing the whole list
		}
		runs = append(runs, &run)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	return runs, nil
}

// Delete removes a run from the store. Used for `dfmc drive delete <id>`
// and for cleanup when a run is older than the configured retention.
// Returns nil for non-existent IDs (idempotent).
func (s *Store) Delete(id string) error {
	sqlStmt := fmt.Sprintf(`DELETE FROM "%s" WHERE key = ?`, driveBucket)
	_, err := s.db.Exec(sqlStmt, id)
	return err
}

// newRunID produces a short timestamp-prefixed random ID. The format
// is `drv-<unix-seconds-hex>-<6-byte-random-hex>` so IDs sort roughly
// by creation time when listed alphabetically and stay short enough
// to type from memory (`dfmc drive --resume drv-1234abcd-...`).
func newRunID() string {
	var rnd [6]byte
	_, _ = rand.Read(rnd[:])
	return fmt.Sprintf("drv-%x-%s", time.Now().Unix(), hex.EncodeToString(rnd[:]))
}
