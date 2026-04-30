package taskstore

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"go.etcd.io/bbolt"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

const taskBucket = "tasks"

type Store struct {
	db *bbolt.DB
	mu sync.Mutex // serializes concurrent UpdateTask calls on the same task ID
}

func NewStore(db *bbolt.DB) *Store {
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
	return s.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(taskBucket))
		if err != nil {
			return err
		}
		return b.Put([]byte(t.ID), data)
	})
}

func (s *Store) LoadTask(id string) (*supervisor.Task, error) {
	if id == "" {
		return nil, fmt.Errorf("taskstore.LoadTask: id is empty")
	}
	var data []byte
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(taskBucket))
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
	var t supervisor.Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("unmarshal task %q: %w", id, err)
	}
	return &t, nil
}

// UpdateTask reads, mutates, and writes a task atomically inside a single
// bbolt.Update transaction. The whole-store mutex still serializes
// UpdateTask calls against each other (so concurrent readers see one
// definite intermediate state, not torn writes), and the surrounding
// txn closes the load-modify-save window where a concurrent SaveTask
// or DeleteTask could otherwise lose-update between the read and write.
func (s *Store) UpdateTask(id string, fn func(*supervisor.Task) error) error {
	if id == "" {
		return fmt.Errorf("taskstore.UpdateTask: id is empty")
	}
	if fn == nil {
		return fmt.Errorf("taskstore.UpdateTask: fn is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bbolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(taskBucket))
		if err != nil {
			return err
		}
		raw := b.Get([]byte(id))
		if raw == nil {
			return fmt.Errorf("task not found: %s", id)
		}
		var t supervisor.Task
		if err := json.Unmarshal(raw, &t); err != nil {
			return fmt.Errorf("unmarshal task %q: %w", id, err)
		}
		if err := fn(&t); err != nil {
			return err
		}
		if t.ID == "" {
			return fmt.Errorf("taskstore.UpdateTask: fn cleared task.ID")
		}
		data, err := json.MarshalIndent(&t, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal task: %w", err)
		}
		return b.Put([]byte(t.ID), data)
	})
}

func (s *Store) DeleteTask(id string) error {
	if id == "" {
		return nil
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(taskBucket))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(id))
	})
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
	err := s.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(taskBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, v []byte) error {
			var t supervisor.Task
			if err := json.Unmarshal(v, &t); err != nil {
				return nil
			}
			if opts.ParentID != "" && t.ParentID != opts.ParentID {
				return nil
			}
			if opts.RunID != "" && t.RunID != opts.RunID {
				return nil
			}
			if opts.State != "" && string(t.State) != opts.State {
				return nil
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
					return nil
				}
			}
			tasks = append(tasks, &t)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].StartedAt.After(tasks[j].StartedAt)
	})
	if opts.Offset > 0 && opts.Offset < len(tasks) {
		tasks = tasks[opts.Offset:]
	}
	if opts.Limit > 0 && opts.Limit < len(tasks) {
		tasks = tasks[:opts.Limit]
	}
	return tasks, nil
}

func (s *Store) ListChildren(parentID string) ([]*supervisor.Task, error) {
	return s.ListTasks(ListOptions{ParentID: parentID})
}
