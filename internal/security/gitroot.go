// Git-root sanitization shared between the TUI and web/server git-diff
// helpers. Both previously called `exec.Command("git", "-C", root, ...)`
// with an unvalidated `root`. Go's exec.Command doesn't invoke a shell,
// so classic CWE-78 command injection isn't possible — arguments go
// straight to the execve() syscall as a literal argv array. But a root
// path starting with `-` can still get parsed by git as an option flag
// (e.g. `--upload-pack=...`), which is argument injection and worth
// defending against regardless of the scanner flagging it.
//
// This helper normalises + rejects anything obviously wrong. Callers
// should additionally pass the sanitised root via `cmd.Dir` rather
// than `-C <root>`, so the path is never handed to git's CLI parser.

package security

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrInvalidGitRoot is returned when SanitizeGitRoot can't produce a
// safe path to use as a git working directory. Wraps via errors.Is so
// callers can surface a clean message without leaking detail.
var ErrInvalidGitRoot = errors.New("invalid git root")

// SanitizeGitRoot returns a canonicalised absolute directory suitable
// for use as `exec.Cmd.Dir` when running git. Rules:
//   - Empty input falls back to the current working directory.
//   - The path gets Clean()'d and made Absolute.
//   - The base name must NOT start with '-' (otherwise git or any tool
//     might interpret the path as a flag if it ever leaks into argv).
//   - The path must exist AND be a directory.
//
// The returned string is always an absolute, cleaned path on success.
// On failure the error wraps ErrInvalidGitRoot.
func SanitizeGitRoot(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("%w: cannot resolve cwd: %v", ErrInvalidGitRoot, err)
		}
		s = cwd
	}

	abs, err := filepath.Abs(s)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidGitRoot, err)
	}
	abs = filepath.Clean(abs)

	// Defensive: no component of the path may begin with '-'. In
	// practice this only matters for the basename since we pass via
	// cmd.Dir, but a user-supplied root like "-fakeflag" would still
	// be surprising if we ever changed the call site.
	for _, part := range strings.Split(abs, string(os.PathSeparator)) {
		if strings.HasPrefix(part, "-") {
			return "", fmt.Errorf("%w: path component starts with '-': %q", ErrInvalidGitRoot, part)
		}
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%w: stat failed: %v", ErrInvalidGitRoot, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: not a directory: %s", ErrInvalidGitRoot, abs)
	}
	return abs, nil
}
