// Workspace patch/diff handlers for the web API. Extracted from server.go
// to keep the construction/wiring lean. Diff/patch/apply endpoints share the
// same unified-diff parsing and `git` shell helpers, so they cluster here.

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/security"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func (s *Server) handleWorkspaceDiff(w http.ResponseWriter, _ *http.Request) {
	root := strings.TrimSpace(s.engine.Status().ProjectRoot)
	if root == "" {
		root = "."
	}
	diff, err := gitWorkingDiffWeb(root, 200_000)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	changed, err := gitChangedFilesWeb(root, 24)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"diff":          diff,
		"clean":         strings.TrimSpace(diff) == "",
		"changed_files": changed,
	})
}

func (s *Server) handleWorkspacePatch(w http.ResponseWriter, _ *http.Request) {
	patch := latestAssistantUnifiedDiffWeb(s.engine.ConversationActive())
	writeJSON(w, http.StatusOK, map[string]any{
		"patch":     patch,
		"available": strings.TrimSpace(patch) != "",
	})
}

func (s *Server) handleWorkspaceApply(w http.ResponseWriter, r *http.Request) {
	req := WorkspaceApplyRequest{}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
	}
	patch := strings.TrimSpace(req.Patch)
	if patch == "" && strings.EqualFold(strings.TrimSpace(req.Source), "latest") {
		patch = latestAssistantUnifiedDiffWeb(s.engine.ConversationActive())
	}
	if patch == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "patch is required or source=latest must resolve to an assistant diff"})
		return
	}
	root := strings.TrimSpace(s.engine.Status().ProjectRoot)
	if root == "" {
		root = "."
	}
	if err := applyUnifiedDiffWeb(root, patch, req.CheckOnly); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if req.CheckOnly {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":     "ok",
			"check_only": true,
			"valid":      true,
		})
		return
	}
	changed, err := gitChangedFilesWeb(root, 24)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":        "ok",
		"check_only":    false,
		"changed_files": changed,
	})
}

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
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		root = "."
	}
	patch = strings.ReplaceAll(patch, "\r\n", "\n")
	if patch != "" && !strings.HasSuffix(patch, "\n") {
		patch += "\n"
	}
	args := []string{"-C", root, "apply", "--whitespace=nowarn", "--recount"}
	if checkOnly {
		args = append(args, "--check")
	}
	cmd := exec.Command("git", args...)
	cmd.Stdin = strings.NewReader(patch)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func gitChangedFilesWeb(projectRoot string, limit int) ([]string, error) {
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		root = "."
	}
	cmd := exec.Command("git", "-C", root, "status", "--short", "--")
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
