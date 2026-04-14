package storage

import (
	"bufio"
	"encoding/json"
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

type Store struct {
	db          *bbolt.DB
	dataDir     string
	artifactDir string
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
		return nil, fmt.Errorf("open db: %w", err)
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
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create conversation file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, msg := range messages {
		if err := enc.Encode(msg); err != nil {
			return fmt.Errorf("encode message: %w", err)
		}
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
