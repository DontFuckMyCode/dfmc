//go:build windows

package toolhistory

import "os"

// syncDirPlatform is a no-op on Windows: NTFS commits a rename
// atomically via the journaling layer, and there is no Win32 API
// equivalent to POSIX fsync on a directory descriptor. A read-only
// handle returned by os.Open(dir) cannot have FlushFileBuffers called
// on it (Access denied), and opening the directory read-write
// requires FILE_FLAG_BACKUP_SEMANTICS plumbing the standard library
// doesn't expose. The atomicity guarantee writeFileAtomic relies on
// (rename-replace within the same volume) holds on NTFS without an
// explicit dir flush, so skipping it is correct — not a workaround.
//
// dir is accepted as a parameter to satisfy a uniform syncDir
// signature with the Unix variant; we only stat it so a missing
// directory still produces an error consistent with Unix.
func syncDirPlatform(dir string) error {
	_, err := os.Stat(dir)
	return err
}
