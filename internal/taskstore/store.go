package taskstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

const taskBucket = "tasks"

// ErrTaskVersionConflict is returned by UpdateTaskCAS when the stored
// task's Version no longer matches the caller-supplied expected
// version. The caller is expected to LoadTask afresh and re-apply the
// update against the new state.
var ErrTaskVersionConflict = errors.New("taskstore: task version conflict")

// ErrTaskNotFound is returned by UpdateTask / UpdateTaskCAS when the
// id has no stored task. HTTP/MCP layers detect this with errors.Is
// to convert to a 404 / "not found" structured response without
// resorting to string matching on the error message.
var ErrTaskNotFound = errors.New("taskstore: task not found")

type Store struct {
	db *sql.DB
	mu sync.Mutex // serializes concurrent UpdateTask calls on the same task ID
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) SaveTask(t *supervisor.Task) error {
	if t == nil {
		return fmt.Errorf("taskstore.SaveTask: task is nil")
	}
	if t.ID == "" {
		return fmt.Errorf("taskstore.SaveTask: task.ID is empty")
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	sqlStmt := fmt.Sprintf(`
		INSERT INTO "%s" (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, taskBucket)
	_, err = s.db.Exec(sqlStmt, t.ID, data)
	return err
}

func (s *Store) LoadTask(id string) (*supervisor.Task, error) {
	if id == "" {
		return nil, fmt.Errorf("taskstore.LoadTask: id is empty")
	}
	var data []byte
	sqlStmt := fmt.Sprintf(`SELECT value FROM "%s" WHERE key = ?`, taskBucket)
	err := s.db.QueryRow(sqlStmt, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, nil
	}
	var t supervisor.Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("unmarshal task %q: %w", id, err)
	}
	return &t, nil
}

// UpdateTask reads, mutates, and writes a task atomically inside a single
// SQLite transaction. The whole-store mutex still serializes
// UpdateTask calls against each other (so concurrent readers see one
// definite intermediate state, not torn writes), and the surrounding
// txn closes the load-modify-save window where a concurrent SaveTask
// or DeleteTask could otherwise lose-update between the read and write.
//
// On a successful mutation Version is incremented automatically before
// the write. Callers that need to detect concurrent writers across
// LoadTask → user-decision → UpdateTask windows should use
// UpdateTaskCAS instead, which refuses the write when the stored
// Version no longer matches the value the caller observed.
func (s *Store) UpdateTask(id string, fn func(*supervisor.Task) error) error {
	return s.updateTaskInternal(id, -1, fn)
}

// UpdateTaskCAS is the optimistic-concurrency-controlled variant of
// UpdateTask. expectedVersion is the Version the caller observed in a
// prior LoadTask. If the current stored Version differs (because
// another writer mutated the row in between), the call returns
// ErrTaskVersionConflict and no write happens. The caller is expected
// to LoadTask afresh and re-apply the mutation against the new state.
//
// Pass expectedVersion = 0 to mean "task should be at its initial
// version (zero) — fail if anyone has bumped it". Pass any other
// observed value to require an exact match.
func (s *Store) UpdateTaskCAS(id string, expectedVersion int, fn func(*supervisor.Task) error) error {
	if expectedVersion < 0 {
		return fmt.Errorf("taskstore.UpdateTaskCAS: expectedVersion must be >= 0")
	}
	return s.updateTaskInternal(id, expectedVersion, fn)
}

// updateTaskInternal is the shared implementation. expectedVersion < 0
// disables the CAS check (UpdateTask path). Bumps Version on success.
func (s *Store) updateTaskInternal(id string, expectedVersion int, fn func(*supervisor.Task) error) error {
	if id == "" {
		return fmt.Errorf("taskstore.UpdateTask: id is empty")
	}
	if fn == nil {
		return fmt.Errorf("taskstore.UpdateTask: fn is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	sqlStmt := fmt.Sprintf(`SELECT value FROM "%s" WHERE key = ?`, taskBucket)
	var raw []byte
	if err := tx.QueryRow(sqlStmt, id).Scan(&raw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
		}
		return err
	}
	if raw == nil {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, id)
	}
	var t supervisor.Task
	if err := json.Unmarshal(raw, &t); err != nil {
		return fmt.Errorf("unmarshal task %q: %w", id, err)
	}
	if expectedVersion >= 0 && t.Version != expectedVersion {
		return fmt.Errorf("%w: expected v%d, got v%d for task %s", ErrTaskVersionConflict, expectedVersion, t.Version, id)
	}
	if err := fn(&t); err != nil {
		return err
	}
	if t.ID == "" {
		return fmt.Errorf("taskstore.UpdateTask: fn cleared task.ID")
	}
	t.Version++ // bump on every successful mutation
	data, err := json.MarshalIndent(&t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}
	upsert := fmt.Sprintf(`
		INSERT INTO "%s" (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, taskBucket)
	if _, err := tx.Exec(upsert, t.ID, data); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) DeleteTask(id string) error {
	if id == "" {
		return nil
	}
	sqlStmt := fmt.Sprintf(`DELETE FROM "%s" WHERE key = ?`, taskBucket)
	_, err := s.db.Exec(sqlStmt, id)
	return err
}

type ListOptions struct {
	ParentID string
	RunID    string
	State    string
	Label    string
	Limit    int
	Offset   int
}

func (s *Store) ListTasks(opts ListOptions) ([]*supervisor.Task, error) {
	var tasks []*supervisor.Task
	sqlStmt := fmt.Sprintf(`SELECT value FROM "%s"`, taskBucket)
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
		var t supervisor.Task
		if err := json.Unmarshal(data, &t); err != nil {
			// Skip corrupt rows so the listing surface still works for
			// every healthy entry, but log so the operator can spot a
			// bad bucket instead of silently losing tasks from the UI.
			log.Printf("taskstore: skipping corrupt task: %v", err)
			continue
		}
		if opts.ParentID != "" && t.ParentID != opts.ParentID {
			continue
		}
		if opts.RunID != "" && t.RunID != opts.RunID {
			continue
		}
		if opts.State != "" && string(t.State) != opts.State {
			continue
		}
		if opts.Label != "" {
			found := false
			for _, l := range t.Labels {
				if l == opts.Label {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		tasks = append(tasks, &t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].StartedAt.After(tasks[j].StartedAt)
	})
	if opts.Offset > 0 {
		// Past-the-end offset must yield an empty page, not the full list.
		// The old `&& opts.Offset < len(tasks)` guard turned an out-of-range
		// offset into a no-op, so e.g. offset=60 on a 50-task store returned
		// all 50 — wrong page contents leaking across pagination.
		if opts.Offset >= len(tasks) {
			tasks = nil
		} else {
			tasks = tasks[opts.Offset:]
		}
	}
	if opts.Limit > 0 && opts.Limit < len(tasks) {
		tasks = tasks[:opts.Limit]
	}
	return tasks, nil
}

func (s *Store) ListChildren(parentID string) ([]*supervisor.Task, error) {
	return s.ListTasks(ListOptions{ParentID: parentID})
}
