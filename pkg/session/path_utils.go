package session

import (
	"os"
	"path/filepath"
)

// UserHomeDir mirrors the logic from internal/config/config.go:149.
// Returns ~/.dfmc (the dfmc config directory).
func UserHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".dfmc"
	}
	return filepath.Join(home, ".dfmc")
}

// GetLogPath returns the path to the session log file for a given project
// and session ID. Path format: <userHome>/userhome/<project>/logs/<session>.jsonl
func GetLogPath(project, sessionID string) string {
	base := filepath.Join(UserHomeDir(), "userhome", project, "logs")
	return filepath.Join(base, sessionID+".jsonl")
}

// GetLogDir returns the logs directory for a project.
// Path format: <userHome>/userhome/<project>/logs
func GetLogDir(project string) string {
	return filepath.Join(UserHomeDir(), "userhome", project, "logs")
}
