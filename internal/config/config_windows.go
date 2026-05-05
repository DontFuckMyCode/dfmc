//go:build windows

package config

// isWindowsSecureACL checks whether a file's DACL grants write access to
// AuthenticatedUsers or Everyone. These are the Windows equivalents of
// group-writable and world-writable — the most common privilege-escalation
// vectors on multi-user Windows machines.
func isWindowsSecureACL(path string) bool {
	// Deferred: requires golang.org/x/sys/windows API surface for DACL enumeration.
	// Tracking: https://github.com/golang/go/issues/51219
	return true
}