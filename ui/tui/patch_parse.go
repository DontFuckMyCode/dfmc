package tui

// patch_parse.go — pure helpers that parse and apply unified diffs.
//
// Lifted out of the 10K-line tui.go god file (REPORT.md C1) so the
// "what does a patch even look like" surface is in one obvious place.
// Every function here is stateless — no Model receiver, no
// dependency on TUI styling or bubbletea. The Model-bound rendering
// helpers (renderPatchView, patchTargetSummary, …) still live in
// tui.go for now and call these.
//
// Why git, not a hand-rolled apply: an actual `git apply` round-trip
// catches malformed hunks, unicode bidi tricks, and CRLF drift in a
// way nothing we'd ship in two hundred lines would. The cost is one
// process per apply, which is cheap relative to the LLM call that
// produced the patch.

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/internal/security"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func patchSectionPaths(items []patchSection) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if path := strings.TrimSpace(item.Path); path != "" {
			out = append(out, path)
		}
	}
	return out
}

func totalPatchHunks(items []patchSection) int {
	total := 0
	for _, item := range items {
		total += item.HunkCount
	}
	return total
}

func patchLineCounts(text string) (int, int) {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(text), "\r\n", "\n"), "\n")
	additions := 0
	deletions := 0
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "@@"):
			continue
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}

func extractPatchedFiles(patch string) []string {
	text := strings.ReplaceAll(strings.TrimSpace(patch), "\r\n", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, 8)
	seen := map[string]struct{}{}
	add := func(path string) {
		path = filepath.ToSlash(strings.TrimSpace(path))
		path = strings.TrimPrefix(path, "a/")
		path = strings.TrimPrefix(path, "b/")
		if path == "" || path == "/dev/null" || path == "dev/null" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				add(parts[3])
			}
		case strings.HasPrefix(line, "+++ "):
			add(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
		}
	}
	return out
}

func parseUnifiedDiffSections(patch string) []patchSection {
	text := strings.ReplaceAll(strings.TrimSpace(patch), "\r\n", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	sections := make([]patchSection, 0, 8)
	current := patchSection{}
	currentLines := make([]string, 0, 32)

	flush := func() {
		if len(currentLines) == 0 {
			return
		}
		current.Content = strings.Join(currentLines, "\n")
		current.Hunks = extractPatchHunks(current.Content)
		if len(current.Hunks) > 0 {
			current.HunkCount = len(current.Hunks)
		}
		if strings.TrimSpace(current.Path) == "" {
			paths := extractPatchedFiles(current.Content)
			if len(paths) > 0 {
				current.Path = paths[0]
			}
		}
		if strings.TrimSpace(current.Path) != "" {
			sections = append(sections, current)
		}
		current = patchSection{}
		currentLines = currentLines[:0]
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") && len(currentLines) > 0 {
			flush()
		}
		currentLines = append(currentLines, line)
		switch {
		case strings.HasPrefix(line, "diff --git "):
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				current.Path = normalizePatchPath(parts[3])
			}
		case strings.HasPrefix(line, "+++ "):
			path := normalizePatchPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
			if path != "" {
				current.Path = path
			}
		case strings.HasPrefix(line, "@@"):
			current.HunkCount++
		}
	}
	flush()
	return sections
}

func normalizePatchPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	if path == "" || path == "/dev/null" || path == "dev/null" {
		return ""
	}
	return path
}

func extractPatchHunks(diff string) []patchHunk {
	text := strings.ReplaceAll(strings.TrimSpace(diff), "\r\n", "\n")
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	prefix := make([]string, 0, 8)
	hunks := make([]patchHunk, 0, 8)
	current := patchHunk{}
	currentLines := make([]string, 0, 16)
	inHunk := false

	flush := func() {
		if !inHunk || len(currentLines) == 0 {
			return
		}
		current.Content = strings.Join(currentLines, "\n")
		hunks = append(hunks, current)
		current = patchHunk{}
		currentLines = currentLines[:0]
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			flush()
			inHunk = true
			current = patchHunk{Header: strings.TrimSpace(line)}
			currentLines = append(currentLines[:0], prefix...)
			currentLines = append(currentLines, line)
			continue
		}
		if !inHunk {
			prefix = append(prefix, line)
			continue
		}
		currentLines = append(currentLines, line)
	}
	flush()
	return hunks
}

func gitWorkingDiff(projectRoot string, maxBytes int64) (string, error) {
	root, err := security.SanitizeGitRoot(projectRoot)
	if err != nil {
		return "", err
	}
	// Use cmd.Dir instead of `-C <root>` so the path is never parsed
	// as a git CLI flag. A root that starts with `-` would otherwise
	// be interpreted as an option (e.g. `--upload-pack=...`) — real
	// exec.Command doesn't spawn a shell, but avoiding argument-
	// injection surface is still worth the single line.
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

func latestAssistantUnifiedDiff(active *conversation.Conversation) string {
	if active == nil {
		return ""
	}
	msgs := active.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != types.RoleAssistant {
			continue
		}
		if patch := extractUnifiedDiff(msgs[i].Content); strings.TrimSpace(patch) != "" {
			return patch
		}
	}
	return ""
}

func extractUnifiedDiff(in string) string {
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
			if looksLikeUnifiedDiff(block) {
				return block
			}
			idx = end + 4
		}
	}
	if looksLikeUnifiedDiff(text) {
		return text
	}
	return ""
}

func looksLikeUnifiedDiff(diff string) bool {
	d := "\n" + strings.TrimSpace(diff) + "\n"
	if strings.Contains(d, "\ndiff --git ") {
		return true
	}
	return strings.Contains(d, "\n--- ") && strings.Contains(d, "\n+++ ") && strings.Contains(d, "\n@@ ")
}

func applyUnifiedDiff(projectRoot, patch string, checkOnly bool) error {
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
