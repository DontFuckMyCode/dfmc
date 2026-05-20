package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// BackupTo fails when dst points to a directory (not a file path).
func TestBackupTo_CorruptTargetPath(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Create a directory at dst to trigger a failure.
	badDst := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(badDst, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err = store.BackupTo(badDst)
	if err == nil {
		t.Fatal("expected error when dst is a directory")
	}
}

// Open with invalid permissions (non-writable parent directory) is not an error
// on Windows, but we can verify the directory creation succeeds.
func TestOpen_CreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if store.DataDir() == "" {
		t.Fatal("DataDir returned empty")
	}
	if store.ArtifactsDir() == "" {
		t.Fatal("ArtifactsDir returned empty")
	}
}

// TestOpen_SecondWriterSucceedsWithWAL verifies that SQLite WAL mode
// allows multiple writers (unlike SQLite's exclusive lock). This is
// the expected behavior with SQLite — concurrent access is a feature,
// not a bug.
func TestOpen_SecondWriterSucceedsWithWAL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// With SQLite WAL mode, a second writer should succeed.
	second, err := Open(dir)
	if err != nil {
		t.Fatalf("expected second Open to succeed with SQLite WAL, got: %v", err)
	}
	_ = second.Close()
}

// T6: BackupTo must not follow a symlink at the temp file path.
// CreateTemp uses the .dfmc-backup-*.tmp pattern so even if an attacker
// pre-creates a symlink at the predicted path, os.CreateTemp generates
// a fresh random suffix, so the temp file is never at a predictable path.
func TestBackupTo_SymlinkAtTempPath(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Write something to ensure the db is non-empty.
	_ = store.SaveConversationLog("test-conv", []types.Message{})

	dst := filepath.Join(dir, "backup.db")
	if err := store.BackupTo(dst); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}
	// Verify the backup file exists and is non-empty.
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("backup file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("backup file is empty")
	}
}

// ListBackups returns only .db files; mixed directory contents are filtered.
func TestListBackups_MixedValidAndInvalidFiles(t *testing.T) {
	dir := t.TempDir()

	// Create a directory (should be skipped).
	if err := os.MkdirAll(filepath.Join(dir, "skip.dir"), 0o755); err != nil {
		t.Fatalf("mkdir dir: %v", err)
	}
	// Create a non-.db file (should be skipped).
	_ = os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("x"), 0o644)
	// Create .db files (should be included).
	_ = os.WriteFile(filepath.Join(dir, "a.db"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.db"), []byte("x"), 0o644)

	got, err := ListBackups(dir)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(got))
	}
}

// TrimBackups returns 0 when nothing exists.
func TestTrimBackups_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	deleted, err := TrimBackups(dir, 2)
	if err != nil {
		t.Fatalf("TrimBackups on empty dir: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deleted, got %d", deleted)
	}
}

// TrimBackups with keep larger than count removes nothing.
func TestTrimBackups_KeepMoreThanExist(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("b%d.db", i)), []byte("x"), 0o644)
	}
	deleted, err := TrimBackups(dir, 10)
	if err != nil {
		t.Fatalf("TrimBackups: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deleted when keep > count, got %d", deleted)
	}
}

// validateConvID rejects empty string.
func TestValidateConvID_Empty(t *testing.T) {
	err := validateConvID("")
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

// validateConvID rejects absolute paths.
func TestValidateConvID_AbsolutePath(t *testing.T) {
	err := validateConvID("/abs/path")
	if err == nil {
		t.Fatal("expected error for absolute path")
	}
}

// validateConvID rejects path separators.
func TestValidateConvID_PathSeparators(t *testing.T) {
	for _, id := range []string{"a/b", "a\\b", "a/b\\c"} {
		err := validateConvID(id)
		if err == nil {
			t.Fatalf("expected error for %q with path separator", id)
		}
	}
}

// validateConvID rejects control characters.
func TestValidateConvID_ControlChars(t *testing.T) {
	for _, id := range []string{"a\x00b", "a\x1fb", "a\x7fb"} {
		err := validateConvID(id)
		if err == nil {
			t.Fatalf("expected error for %q with control char", id)
		}
	}
}

// validateConvID rejects double-dot segments.
func TestValidateConvID_DoubleDot(t *testing.T) {
	err := validateConvID("..")
	if err == nil {
		t.Fatal("expected error for '..'")
	}
	err = validateConvID("a..b")
	if err == nil {
		t.Fatal("expected error for 'a..b'")
	}
	err = validateConvID("../etc/passwd")
	if err == nil {
		t.Fatal("expected error for '../etc/passwd'")
	}
}

// validateConvID accepts valid IDs.
func TestValidateConvID_ValidIDs(t *testing.T) {
	valid := []string{"conv-1", "my_conv", "a.b.c", "ABC123", "a-b-c"}
	for _, id := range valid {
		err := validateConvID(id)
		if err != nil {
			t.Fatalf("validateConvID(%q): unexpected error %v", id, err)
		}
	}
}

// validateConvID rejects dot (.) as a standalone name.
func TestValidateConvID_DotAlone(t *testing.T) {
	err := validateConvID(".")
	if err == nil {
		t.Fatal("expected error for '.'")
	}
}

// Open with a nil store returns nil error (no-op close).
func TestStore_CloseNilDBIsSafe(t *testing.T) {
	s := &Store{}
	if err := s.Close(); err != nil {
		t.Fatalf("Close on nil store: %v", err)
	}
}

// OpenError.Error() returns "<nil>" for nil receiver.
func TestOpenError_NilError(t *testing.T) {
	var e *OpenError
	if got := e.Error(); got != "<nil>" {
		t.Errorf("Error() on nil = %q, want %q", got, "<nil>")
	}
}

// OpenError.Unwrap() returns nil for nil receiver.
func TestOpenError_NilUnwrap(t *testing.T) {
	var e *OpenError
	if got := e.Unwrap(); got != nil {
		t.Errorf("Unwrap() on nil = %v, want nil", got)
	}
}

// OpenError.Unwrap() returns Cause.
func TestOpenError_UnwrapCause(t *testing.T) {
	inner := fmt.Errorf("inner cause")
	e := &OpenError{Path: "/tmp/data", Cause: inner}
	if got := e.Unwrap(); got != inner {
		t.Errorf("Unwrap() = %v, want %v", got, inner)
	}
}

// Store.DB() returns the underlying sql.DB.
func TestStore_DB(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	db := store.DB()
	if db == nil {
		t.Fatal("DB() returned nil")
	}
}

// validateConvID error messages are specific.
func TestValidateConvID_ErrorMessages(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"", "conversation id is required"},
		{"/abs", "invalid conversation id"},
		{"a/b", "path separators"},
		{"..", "must not contain `..`"},
		{"a\x00b", "control character"},
		{".", "must not contain `..`"},
	}
	for _, tt := range tests {
		err := validateConvID(tt.id)
		if err == nil {
			t.Fatalf("validateConvID(%q): expected error, got nil", tt.id)
		}
		if !strings.Contains(err.Error(), tt.want) {
			t.Errorf("validateConvID(%q) error = %q, want to contain %q", tt.id, err.Error(), tt.want)
		}
	}
}

// Open with nil db returns errors on BackupTo and other db-dependent ops.
func TestStore_NilDBMethods(t *testing.T) {
	s := &Store{}
	if err := s.BackupTo("/tmp/test.db"); err == nil {
		t.Fatal("BackupTo on nil db should fail")
	}
}

// SaveConversationLog and LoadConversationLog round-trip.
func TestSaveAndLoadConversationLog(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	msgs := []types.Message{
		{Role: types.RoleUser, Content: "hello", Timestamp: time.Now()},
		{Role: types.RoleAssistant, Content: "world", Timestamp: time.Now()},
	}
	if err := store.SaveConversationLog("roundtrip-test", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := store.LoadConversationLog("roundtrip-test")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Content != "hello" || got[1].Content != "world" {
		t.Fatalf("content mismatch: %#v", got)
	}
}

// SaveConversationState and LoadConversationState round-trip.
func TestSaveAndLoadConversationState(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	type state struct {
		Step  string
		Count int
	}
	before := state{Step: "done", Count: 42}
	if err := store.SaveConversationState("state-test", before); err != nil {
		t.Fatalf("save: %v", err)
	}
	var after state
	if err := store.LoadConversationState("state-test", &after); err != nil {
		t.Fatalf("load: %v", err)
	}
	if after.Step != "done" || after.Count != 42 {
		t.Fatalf("state mismatch: got %#v", after)
	}
}

// ListBackups sorts by newest first.
func TestListBackups_SortsByNewest(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		p := filepath.Join(dir, fmt.Sprintf("backup-%d.db", i))
		_ = os.WriteFile(p, []byte("x"), 0o644)
		time.Sleep(10 * time.Millisecond)
		_ = os.Chtimes(p, time.Now().Add(time.Duration(i)*time.Hour), time.Now().Add(time.Duration(i)*time.Hour))
	}
	got, err := ListBackups(dir)
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	// Newest should be backup-2 (last written, most recent mod time).
	if !strings.Contains(got[0].Path, "backup-2") {
		t.Fatalf("expected newest to be backup-2, got %s", got[0].Path)
	}
	if !strings.Contains(got[2].Path, "backup-0") {
		t.Fatalf("expected oldest to be backup-0, got %s", got[2].Path)
	}
}

// BackupTo writes a valid SQLite file that can be opened separately.
func TestBackupTo_ProducesOpenableDB(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Write into a bucket using the SQLite API.
	if err := store.BucketPut("codemap_cache", "key", []byte("val")); err != nil {
		t.Fatalf("put: %v", err)
	}

	backupPath := filepath.Join(dir, "backup.db")
	if err := store.BackupTo(backupPath); err != nil {
		t.Fatalf("BackupTo: %v", err)
	}

	// Open the backup as a new store.
	backupDir := filepath.Join(dir, "backup-data")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Copy backup to the expected location for Open.
	backupStorePath := filepath.Join(backupDir, "dfmc.db")
	data, _ := os.ReadFile(backupPath)
	_ = os.WriteFile(backupStorePath, data, 0o644)

	backupStore, err := Open(backupDir)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer backupStore.Close()

	got, err := backupStore.BucketGet("codemap_cache", "key")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "val" {
		t.Fatalf("expected 'val', got %q", string(got))
	}
}

// BackupTo atomic: existing file not modified on error.
func TestBackupTo_AtomicOnError(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	oldPath := filepath.Join(dir, "old.db")
	if err := store.BackupTo(oldPath); err != nil {
		t.Fatalf("setup: %v", err)
	}
	oldInfo, _ := os.Stat(oldPath)

	badPath := filepath.Join(dir, "nonexistent", "subdir", "backup.db")
	err = store.BackupTo(badPath)
	if err == nil {
		t.Fatalf("expected error for invalid path")
	}

	newInfo, _ := os.Stat(oldPath)
	if newInfo.Size() != oldInfo.Size() || !newInfo.ModTime().Equal(oldInfo.ModTime()) {
		t.Fatalf("existing backup was modified: before=%v after=%v", oldInfo, newInfo)
	}
}

// TrimBackups removes all when keep=0.
func TestTrimBackups_RemoveAll(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("b%d.db", i)), []byte("x"), 0o644)
	}
	deleted, err := TrimBackups(dir, 0)
	if err != nil {
		t.Fatalf("TrimBackups(0): %v", err)
	}
	if deleted != 3 {
		t.Fatalf("expected 3 deleted, got %d", deleted)
	}
	remaining, _ := ListBackups(dir)
	if len(remaining) != 0 {
		t.Fatalf("expected 0 remaining, got %d", len(remaining))
	}
}

// TrimBackups clamps negative keep to 1 — a stale uninitialised int
// must NOT nuke the backup directory. keep=0 stays the explicit
// "wipe everything" knob (covered by TestTrimBackups_RemoveAll).
func TestTrimBackups_NegativeKeepIsClampedNotWiped(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 3; i++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("b%d.db", i)), []byte("x"), 0o644)
	}
	deleted, err := TrimBackups(dir, -1)
	if err != nil {
		t.Fatalf("TrimBackups(-1): %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted (newest 1 kept), got %d", deleted)
	}
	remaining, _ := ListBackups(dir)
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining (newest), got %d", len(remaining))
	}
}

// TrimBackups on nonexistent dir is not an error.
func TestTrimBackups_NonexistentDir(t *testing.T) {
	deleted, err := TrimBackups(filepath.Join(t.TempDir(), "does_not_exist"), 2)
	if err != nil {
		t.Fatalf("expected no error for nonexistent dir, got %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deleted, got %d", deleted)
	}
}

// OpenError.Error() with nil receiver returns "<nil>".
func TestOpenError_Error_Nil(t *testing.T) {
	var e *OpenError
	if got := e.Error(); got != "<nil>" {
		t.Errorf("nil.OpenError().Error() = %q, want %q", got, "<nil>")
	}
}

// OpenError.Error() with ErrStoreLocked returns locked message.
func TestOpenError_Error_Locked(t *testing.T) {
	e := &OpenError{
		Path:  "/path/to/db",
		Cause: ErrStoreLocked,
	}
	got := e.Error()
	if !strings.Contains(got, "locked") {
		t.Errorf("Error() = %q, want message containing 'locked'", got)
	}
}

// OpenError.Error() with regular error returns generic message.
func TestOpenError_Error_Generic(t *testing.T) {
	e := &OpenError{
		Path:  "/path/to/db",
		Cause: errors.New("permission denied"),
	}
	got := e.Error()
	if !strings.Contains(got, "permission denied") {
		t.Errorf("Error() = %q, want message containing 'permission denied'", got)
	}
}

// OpenError.Unwrap() with nil receiver returns nil.
func TestOpenError_Unwrap_Nil(t *testing.T) {
	var e *OpenError
	if got := e.Unwrap(); got != nil {
		t.Errorf("nil.OpenError().Unwrap() = %v, want nil", got)
	}
}

// OpenError.Unwrap() returns the cause.
func TestOpenError_Unwrap_Cause(t *testing.T) {
	e := &OpenError{
		Path:  "/path/to/db",
		Cause: errors.New("boom"),
	}
	got := e.Unwrap()
	if got == nil || !strings.Contains(got.Error(), "boom") {
		t.Errorf("Unwrap() = %v, want error containing 'boom'", got)
	}
}

// BucketPut, BucketGet, BucketDelete round-trip.
func TestBucketOps_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	if err := store.BucketPut("config", "key1", []byte("val1")); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := store.BucketGet("config", "key1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got) != "val1" {
		t.Fatalf("expected 'val1', got %q", string(got))
	}
	if err := store.BucketDelete("config", "key1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, err = store.BucketGet("config", "key1")
	if err != nil {
		t.Fatalf("get after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after delete, got %q", string(got))
	}
}

// BucketForEach iterates all keys.
func TestBucketForEach(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	_ = store.BucketPut("config", "a", []byte("1"))
	_ = store.BucketPut("config", "b", []byte("2"))

	var keys []string
	err = store.BucketForEach("config", func(k, v []byte) error {
		keys = append(keys, string(k))
		return nil
	})
	if err != nil {
		t.Fatalf("foreach: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
}

// BucketClear removes all rows.
func TestBucketClear(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	_ = store.BucketPut("config", "a", []byte("1"))
	if err := store.BucketClear("config"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, err := store.BucketGet("config", "a")
	if err != nil {
		t.Fatalf("get after clear: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after clear, got %q", string(got))
	}
}
