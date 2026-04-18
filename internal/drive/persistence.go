// Drive run persistence on bbolt.
//
// Each Run is a JSON blob stored under bucket "drive-runs", keyed by
// the run ID. The store is best-effort durable: every status change
// (planning -> running -> done, plus per-TODO transitions) writes the
// whole Run back so a crash mid-loop loses at most the in-flight
// transition. JSON over a binary schema is deliberate — drive runs are
// inspected by hand often enough that human-readable storage pays off,
// and the volume is tiny (one record per `dfmc drive` invocation).
//
// The bucket lives in the same bbolt file as memory and conversations
// (Engine.Storage), so a single file lock covers everything. That
// matches the "only one dfmc process per project" rule already enforced
// in cmd/dfmc/main.go via ErrStoreLocked.

package drive

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"go.etcd.io/bbolt"
)

const driveBucket = "drive-runs"

// Store is the bbolt-backed persistence layer for drive runs. Take a
// *bbolt.DB from engine.Storage.DB() and pass it here; the bucket is
// created lazily on the first Save.
type Store struct {
	db *bbolt.DB
}

// NewStore wraps a bbolt handle. Returns an error only if db is nil
// — bucket creation is deferred to the first write so an empty store
// is valid (List on an empty store returns nil, nil).
func NewStore(db *bbolt.DB) (*Store, error) {
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
	return s.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(driveBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(run.ID), data)
	})
}

// Load fetches a run by ID. Returns (nil, nil) on miss so callers can
// distinguish "not found" from a real error. The returned pointer is
// owned by the caller — modifications do not write back automatically.
func (s *Store) Load(id string) (*Run, error) {
	var data []byte
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(driveBucket))
		if b == nil {
			return nil
		}
		v := b.Get([]byte(id))
		if v != nil {
			data = make([]byte, len(v))
			copy(data, v)
		}
		return nil
	})
	if err != nil {
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
// scan the whole bucket — drive runs accumulate slowly in practice.
func (s *Store) List() ([]*Run, error) {
	var runs []*Run
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(driveBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, v []byte) error {
			var run Run
			if err := json.Unmarshal(v, &run); err != nil {
				return nil // skip corrupted entries instead of failing the whole list
			}
			runs = append(runs, &run)
			return nil
		})
	})
	if err != nil {
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
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(driveBucket))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(id))
	})
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
