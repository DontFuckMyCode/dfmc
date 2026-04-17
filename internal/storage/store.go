package storage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

var defaultBuckets = []string{
	"conversations",
	"memory_episodic",
	"memory_semantic",
	"codemap_cache",
	"ast_cache",
	"config",
	"plugins",
}

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
		return ""
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
		if errors.Is(err, bbolt.ErrTimeout) {
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

	return &Store{
		db:          db,
		dataDir:     dataDir,
		artifactDir: artifactDir,
	}, nil
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

func (s *Store) SaveConversationLog(convID string, messages []types.Message) error {
	if convID == "" {
		return fmt.Errorf("conversation id is required")
	}

	dir := filepath.Join(s.artifactDir, "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create conversation dir: %w", err)
	}

	path := filepath.Join(dir, convID+".jsonl")

	// Encode in-memory first, then atomically rename into place. The
	// previous os.Create approach truncated the existing file up-front
	// — a crash or signal mid-write would leave the user's conversation
	// history truncated (or zero-length, if nothing had been flushed).
	// Buffering + temp-then-rename guarantees the on-disk file is
	// either the old full log OR the new full log, never a torn
	// in-between state.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, msg := range messages {
		if err := enc.Encode(msg); err != nil {
			return fmt.Errorf("encode message: %w", err)
		}
	}
	tmp, err := os.CreateTemp(dir, "."+convID+".jsonl.dfmc-tmp-*")
	if err != nil {
		return fmt.Errorf("create temp for conversation: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp conversation: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync temp conversation: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp conversation: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp conversation: %w", err)
	}
	return nil
}

func (s *Store) LoadConversationLog(convID string) ([]types.Message, error) {
	if convID == "" {
		return nil, fmt.Errorf("conversation id is required")
	}

	path := filepath.Join(s.artifactDir, "conversations", convID+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var messages []types.Message
	sc := bufio.NewScanner(f)
	// bufio.Scanner's default line limit is 64 KiB (MaxScanTokenSize).
	// A single tool-output message — a long `run_command` stdout, a
	// pasted patch, a big code block — easily exceeds that and used to
	// fail the whole load with "token too long". 8 MiB covers
	// essentially any realistic message while still capping the
	// per-line memory grab so a corrupted file can't pull unbounded
	// RAM.
	const maxLineBytes = 8 * 1024 * 1024
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg types.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, fmt.Errorf("decode message: %w", err)
		}
		messages = append(messages, msg)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan file: %w", err)
	}

	return messages, nil
}
