package storage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
		if errors.Is(err, bbolt.ErrTimeout) {
			return nil, &OpenError{
				Path:  dbPath,
				Cause: fmt.Errorf("storage database is locked: %w", err),
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

// BackupTo creates a consistent hot backup of the bbolt database and writes
// it to dst. The destination is a valid bbolt database that can be opened
// independently with bbolt.Open. Backup is atomic via os.Rename so dst is
// either the previous backup or the new one, never partially written.
// BackupTo uses db.View so it is safe to call while the Store is open and
// accepting reads/writes — no exclusive lock is held during backup.
func (s *Store) BackupTo(dst string) error {
	if s == nil || s.db == nil {
		return errors.New("store is not open")
	}
	// M5: use CreateTemp instead of a predictable .dfmc-backup-tmp name.
	// This prevents a TOCTOU symlink attack where an attacker pre-creates
	// a symlink at the predictable path, causing BackupTo to overwrite
	// an arbitrary target file.
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".dfmc-backup-*.tmp")
	if err != nil {
		return fmt.Errorf("create backup temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := s.db.View(func(tx *bbolt.Tx) error {
		_, err := tx.WriteTo(tmp)
		return err
	}); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("backup write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close backup: %w", err)
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return fmt.Errorf("rename backup: %w", err)
	}
	return nil
}

// BackupInfo describes a single backup file on disk.
type BackupInfo struct {
	Path      string
	Size      int64
	CreatedAt time.Time
}

// ListBackups returns all `.dfmc.db` backup files in dir sorted by creation
// time (newest first). Empty or nonexistent directories return nil.
func ListBackups(dir string) ([]BackupInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backup dir: %w", err)
	}
	var out []BackupInfo
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".db" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, e.Name())
		out = append(out, BackupInfo{
			Path:      path,
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
		})
	}
	sortBackupsByTime(out)
	return out, nil
}

// TrimBackups removes all but the newest `keep` backups in dir.
// If keep < 0 it removes all backups. Returns the number of files deleted
// and any error encountered while reading the directory.
func TrimBackups(dir string, keep int) (int, error) {
	backups, err := ListBackups(dir)
	if err != nil {
		return 0, err
	}
	if len(backups) == 0 {
		return 0, nil
	}
	if keep < 0 {
		keep = 1 // S3: keep at least the newest; negative means "remove all" was a footgun
	}
	if keep >= len(backups) {
		return 0, nil
	}
	toDelete := backups[keep:]
	for _, b := range toDelete {
		if err := os.Remove(b.Path); err != nil {
			return 0, fmt.Errorf("remove backup %s: %w", b.Path, err)
		}
	}
	return len(toDelete), nil
}

func sortBackupsByTime(b []BackupInfo) {
	sort.Slice(b, func(i, j int) bool { return b[i].CreatedAt.After(b[j].CreatedAt) })
}

func (s *Store) SaveConversationLog(convID string, messages []types.Message) error {
	if err := validateConvID(convID); err != nil {
		return err
	}

	dir := filepath.Join(s.artifactDir, "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create conversation dir: %w", err)
	}

	path := conversationLogPath(dir, convID)

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
	if err := writeFileAtomic(path, buf.Bytes(), "."+convID+".jsonl.dfmc-tmp-*"); err != nil {
		return fmt.Errorf("save conversation log: %w", err)
	}
	return nil
}

func (s *Store) SaveConversationState(convID string, state any) error {
	if err := validateConvID(convID); err != nil {
		return err
	}
	dir := filepath.Join(s.artifactDir, "conversations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create conversation dir: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode conversation state: %w", err)
	}
	if err := writeFileAtomic(conversationStatePath(dir, convID), data, "."+convID+".json.dfmc-tmp-*"); err != nil {
		return fmt.Errorf("save conversation state: %w", err)
	}
	return nil
}

func (s *Store) LoadConversationState(convID string, dst any) error {
	if err := validateConvID(convID); err != nil {
		return err
	}
	if dst == nil {
		return fmt.Errorf("conversation state destination is nil")
	}
	path := conversationStatePath(filepath.Join(s.artifactDir, "conversations"), convID)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("decode conversation state: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, pattern string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	// Best-effort durability: flush the parent directory entry after the
	// rename so a sudden power loss is less likely to lose the new name.
	if err := syncDir(dir); err != nil {
		return fmt.Errorf("sync parent dir: %w", err)
	}
	return nil
}

func (s *Store) LoadConversationLog(convID string) ([]types.Message, error) {
	if err := validateConvID(convID); err != nil {
		return nil, err
	}

	path := conversationLogPath(filepath.Join(s.artifactDir, "conversations"), convID)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

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

func conversationLogPath(dir, convID string) string {
	return filepath.Join(dir, convID+".jsonl")
}

func conversationStatePath(dir, convID string) string {
	return filepath.Join(dir, convID+".json")
}

func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil {
		// Some filesystems do not support directory sync. The rename still
		// gave us atomic replacement, so do not fail persistence outright.
		return nil
	}
	return nil
}

// validateConvID rejects conversation IDs that would escape the
// artifactDir/conversations directory when concatenated into a path.
// Pre-fix the IDs were joined raw, so a `convID="../../etc/passwd"`
// would write/read outside the project's artifact tree — a path
// traversal flagged as C1 in the 2026-04-17 review. Conversation IDs
// are allowed to contain dashes, dots, alphanumerics, and underscores
// only; anything else is treated as a synthetic / hostile value.
func validateConvID(id string) error {
	if id == "" {
		return fmt.Errorf("conversation id is required")
	}
	if filepath.IsAbs(id) {
		return fmt.Errorf("invalid conversation id %q: must be a relative basename", id)
	}
	// `..` segments and any path separator make the value capable of
	// escaping the artifact dir. Both POSIX `/` and Windows `\` count.
	if strings.ContainsAny(id, "/\\") {
		return fmt.Errorf("invalid conversation id %q: must not contain path separators", id)
	}
	if id == "." || id == ".." || strings.Contains(id, "..") {
		return fmt.Errorf("invalid conversation id %q: must not contain `..`", id)
	}
	// Reject control bytes and the NUL byte — these can split paths on
	// some filesystems and they're never legitimate in a conversation
	// identifier we generate ourselves.
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid conversation id: contains control character")
		}
	}
	return nil
}
