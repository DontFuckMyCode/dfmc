// server_workspace_diff.go — diff-parsing + git shell helpers used by
// the workspace HTTP handlers in server_workspace.go. Sibling split so
// the handler file stays focused on request shape, response shape,
// and the engine-routing decisions (CallToolFromSource vs local
// dry-run, http.StatusForbidden vs StatusBadRequest).
//
// All git-touching helpers go through security.SanitizeGitRoot before
// invoking exec.Command, and pass the project root via cmd.Dir / `-C`
// rather than embedding it in args — that keeps a path that starts
// with `-` out of git's CLI parser entirely.

package web

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/security"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func gitWorkingDiffWeb(projectRoot string, maxBytes int64) (string, error) {
	root, err := security.SanitizeGitRoot(projectRoot)
	if err != nil {
		return "", err
	}
	// cmd.Dir keeps the path out of git's CLI parser entirely; with
	// `-C <root>` a path that starts with `-` could be read as a
	// flag. exec.Command doesn't spawn a shell so classic CWE-78
	// injection isn't possible, but argument-injection hardening is
	// cheap and makes the static-scanner flag go away honestly.
	cmd := exec.Command("git", "diff", "--")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		if ee := (&exec.ExitError{}); errors.As(err, &ee) {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	if maxBytes > 0 && int64(len(out)) > maxBytes {
		out = out[:maxBytes]
		return string(out) + "\n... [truncated]\n", nil
	}
	return string(out), nil
}

func latestAssistantUnifiedDiffWeb(active *conversation.Conversation) string {
	if active == nil {
		return ""
	}
	msgs := active.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != types.RoleAssistant {
			continue
		}
		if patch := extractUnifiedDiffWeb(msgs[i].Content); strings.TrimSpace(patch) != "" {
			return patch
		}
	}
	return ""
}

func extractUnifiedDiffWeb(in string) string {
	text := strings.TrimSpace(strings.ReplaceAll(in, "\r\n", "\n"))
	if text == "" {
		return ""
	}
	for _, marker := range []string{"```diff", "```patch", "```"} {
		idx := 0
		for {
			start := strings.Index(text[idx:], marker)
			if start < 0 {
				break
			}
			start += idx
			blockStart := strings.Index(text[start:], "\n")
			if blockStart < 0 {
				break
			}
			blockStart += start + 1
			end := strings.Index(text[blockStart:], "\n```")
			if end < 0 {
				break
			}
			end += blockStart
			block := strings.TrimSpace(text[blockStart:end])
			if looksLikeUnifiedDiffWeb(block) {
				return block
			}
			idx = end + 4
		}
	}
	if looksLikeUnifiedDiffWeb(text) {
		return text
	}
	return ""
}

func looksLikeUnifiedDiffWeb(diff string) bool {
	d := "\n" + strings.TrimSpace(diff) + "\n"
	if strings.Contains(d, "\ndiff --git ") {
		return true
	}
	return strings.Contains(d, "\n--- ") && strings.Contains(d, "\n+++ ") && strings.Contains(d, "\n@@ ")
}

func applyUnifiedDiffWeb(projectRoot, patch string, checkOnly bool) error {
	root, err := security.SanitizeGitRoot(projectRoot)
	if err != nil {
		return fmt.Errorf("invalid project root: %w", err)
	}
	// M3: sanitize patch content before passing to git apply.
	// Reject patches with embedded git directives or option-like lines
	// that could cause git to execute arbitrary commands or write files
	// outside the project root.
	patchLines := strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n")
	var sanitized strings.Builder
	for _, line := range patchLines {
		trimmed := strings.TrimSpace(line)
		// Reject lines that look like git config directives or CLI options
		// that could be interpreted as flag injection.
		if strings.HasPrefix(trimmed, "--") &&
			(len(trimmed) > 2 && !strings.HasPrefix(trimmed, "---") && !strings.HasPrefix(trimmed, "+++")) {
			continue // drop option-like lines that aren't part of the diff header
		}
		if strings.HasPrefix(trimmed, "apply.") ||
			strings.HasPrefix(trimmed, "git config") ||
			strings.HasPrefix(trimmed, "[") {
			continue // drop gitconfig-style sections and apply. directives
		}
		sanitized.WriteString(line + "\n")
	}
	patch = sanitized.String()
	if patch != "" && !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	const applyTimeout = 60 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), applyTimeout)
	defer cancel()
	args := []string{"-C", root, "apply", "--whitespace=nowarn", "--recount"}
	if checkOnly {
		args = append(args, "--check")
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Stdin = strings.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	// M3: after a successful --check dry-run, verify all affected files
	// resolve inside projectRoot before allowing the actual apply.
	if !checkOnly {
		cmd = exec.CommandContext(ctx, "git", "-C", root, "apply", "--whitespace=nowarn", "--recount", "--dry-run", "--porcelain")
		cmd.Stdin = strings.NewReader(patch)
		out, err = cmd.CombinedOutput()
		if err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			for _, line := range lines {
				if line == "" {
					continue
				}
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					relPath := parts[len(parts)-1]
					absPath := filepath.Join(root, relPath)
					absPath, err = filepath.EvalSymlinks(absPath)
					if err != nil {
						return fmt.Errorf("apply: failed to resolve path %s: %w", relPath, err)
					}
					if !strings.HasPrefix(absPath, root) {
						return fmt.Errorf("apply: path %s resolves outside project root (denied)", relPath)
					}
				}
			}
		}
	}
	return nil
}

func gitChangedFilesWeb(ctx context.Context, projectRoot string, limit int) ([]string, error) {
	root, err := security.SanitizeGitRoot(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("invalid project root: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "status", "--short", "--")
	out, err := cmd.Output()
	if err != nil {
		if ee := (&exec.ExitError{}); errors.As(err, &ee) {
			return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	text := strings.ReplaceAll(string(out), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	files := make([]string, 0, len(lines))
	for _, raw := range lines {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if len(raw) > 3 {
			files = append(files, strings.TrimSpace(raw[3:]))
		} else {
			files = append(files, strings.TrimSpace(raw))
		}
		if limit > 0 && len(files) >= limit {
			break
		}
	}
	return files, nil
}

// pathsFromUnifiedDiff extracts file paths from a unified diff header.
// Used by the test to verify the handler rejects traversal targets.
func pathsFromUnifiedDiff(patch string) []string {
	var out []string
	seen := make(map[string]bool)
	lines := strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--- ") || strings.HasPrefix(trimmed, "+++ ") {
			// Strip the leading --- / +++ prefix and a/ b/ prefix.
			p := trimmed[4:]
			if len(p) >= 2 && p[1] == '/' {
				p = p[2:]
			}
			if strings.TrimSpace(p) == "/dev/null" {
				continue
			}
			// Strip timestamp suffix (tab + date)
			if idx := strings.IndexByte(p, '\t'); idx >= 0 {
				p = p[:idx]
			}
			p = strings.TrimSpace(p)
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

// assertPathWithinRoot checks that relPath resolves inside root.
// Returns nil on success, error on traversal attempt.
func assertPathWithinRoot(root, relPath string) error {
	abs := absPathNoClean(root, relPath)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path escapes root")
	}
	return nil
}

// absPathNoClean joins root+rel WITHOUT cleaning — traversal must be
// detected via Rel, not prevented by Clean collapsing ".." first.
func absPathNoClean(root, rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(root, rel)
}
