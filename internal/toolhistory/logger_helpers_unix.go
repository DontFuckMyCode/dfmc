//go:build !windows

package toolhistory

import "os"

// syncDirPlatform fsyncs the parent directory so the rename in
// writeFileAtomic is durable. POSIX semantics: opening a directory
// read-only and calling Sync() on the descriptor commits the rename
// to disk. Required for crash-safety on ext4/xfs/btrfs/etc.
func syncDirPlatform(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = f.Sync()
	_ = f.Close()
	return err
}
