//go:build !windows

package config

func isWindowsSecureACL(path string) bool {
	return true
}
