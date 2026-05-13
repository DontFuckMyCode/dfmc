package storage

// store_backups.go — hot-backup surface for the bbolt database.
// BackupTo writes a consistent snapshot atomically via tmp+rename;
// ListBackups / TrimBackups / sortBackupsByTime help retention
// callers (CLI, scheduled jobs) curate the on-disk history. Sibling
// to the conversation persistence in store_conversation.go and the
// open/lifecycle/schema-migration core in store.go.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"go.etcd.io/bbolt"
)

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
	// M5: use os.MkdirTemp instead of os.CreateTemp to avoid class 1 WORM
	// vulnerability (predictable temp name + attacker pre-creates symlink).
	// MkdirTemp creates a directory only, then we open the file inside it.
	tmpDir, err := os.MkdirTemp(filepath.Dir(dst), ".dfmc-backup-*")
	if err != nil {
		return err
	}
	tmp := filepath.Join(tmpDir, filepath.Base(dst))
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create backup temp file: %w", err)
	}
	tmpPath := f.Name()
	defer func() { _ = os.RemoveAll(tmpDir) }() // remove dir (file is already closed here)
	if err := s.db.View(func(tx *bbolt.Tx) error {
		_, err := tx.WriteTo(f)
		return err
	}); err != nil {
		_ = f.Close()
		return fmt.Errorf("backup write: %w", err)
	}
	if err := f.Close(); err != nil {
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

// TrimBackups removes all but the newest `keep` backups in dir. Returns the
// number of files deleted and any error encountered while reading the
// directory.
//
// keep == 0 wipes every backup in dir (used by the "purge" CLI flag).
// keep < 0 was the same wipe knob in older callers but turned out to be a
// footgun — a stale uninitialised integer would silently nuke the backup
// directory. Negative values are now clamped to "keep at least the newest";
// callers that want a true wipe must pass 0 explicitly.
func TrimBackups(dir string, keep int) (int, error) {
	backups, err := ListBackups(dir)
	if err != nil {
		return 0, err
	}
	if len(backups) == 0 {
		return 0, nil
	}
	if keep < 0 {
		keep = 1
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
