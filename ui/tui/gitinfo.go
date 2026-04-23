package tui

// gitinfo.go — workspace metadata for the status line. Keep the shell-outs
// off the main goroutine; the UI updates are driven by a tea.Msg that the
// loadGitInfoCmd produces.

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// gitWorkspaceInfo is what the footer needs to paint the branch + churn
// chips. Zero values mean "not available" and the renderer falls back to a
// neutral display.
type gitWorkspaceInfo struct {
	Branch   string
	Inserted int
	Deleted  int
	Detached bool
	Dirty    bool
	Err      string
}

type gitInfoLoadedMsg struct {
	info gitWorkspaceInfo
}

// loadGitInfoCmd shells out to git in a goroutine and returns a tea.Msg that
// the main loop folds into the Model. Safe to call on non-git directories —
// the resulting info has Err populated.
func loadGitInfoCmd(root string) tea.Cmd {
	return func() tea.Msg {
		return gitInfoLoadedMsg{info: collectGitInfo(root)}
	}
}

func collectGitInfo(root string) gitWorkspaceInfo {
	info := gitWorkspaceInfo{}
	root = strings.TrimSpace(root)
	if root == "" {
		info.Err = "no project root"
		return info
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	branch, detached, berr := gitCurrentBranch(ctx, root)
	if berr != nil {
		info.Err = berr.Error()
		return info
	}
	info.Branch = branch
	info.Detached = detached

	inserted, deleted, dirty, derr := gitChurn(ctx, root)
	if derr != nil {
		// Branch succeeded but diff didn't — keep branch, flag the diff
		// error on its own but don't nuke the name.
		info.Err = derr.Error()
		return info
	}
	info.Inserted = inserted
	info.Deleted = deleted
	info.Dirty = dirty
	return info
}

func gitCurrentBranch(ctx context.Context, root string) (string, bool, error) {
	out, err := runGitCmd(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", false, err
	}
	name := strings.TrimSpace(out)
	if name == "HEAD" {
		// Detached — grab the short SHA instead.
		sha, err := runGitCmd(ctx, root, "rev-parse", "--short", "HEAD")
		if err != nil {
			return "", true, err
		}
		return strings.TrimSpace(sha), true, nil
	}
	return name, false, nil
}

// gitChurn tallies +added/-removed lines across the working tree (staged +
// unstaged) against HEAD. "Dirty" means any working-tree change exists even
// when both counters are zero — e.g. only untracked files.
func gitChurn(ctx context.Context, root string) (inserted, deleted int, dirty bool, err error) {
	diff, derr := runGitCmd(ctx, root, "diff", "--numstat", "HEAD")
	if derr != nil {
		return 0, 0, false, derr
	}
	ins, del, any := parseNumstat(diff)
	inserted = ins
	deleted = del
	dirty = any

	// Untracked files don't show up in `git diff --numstat`, so ask status
	// whether anything is pending.
	if !dirty {
		st, serr := runGitCmd(ctx, root, "status", "--porcelain")
		if serr == nil && strings.TrimSpace(st) != "" {
			dirty = true
		}
	}
	return
}

func parseNumstat(raw string) (inserted, deleted int, any bool) {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	// Repos with very long file paths (deep node_modules, localized
	// monorepos) can exceed bufio's default 64 KiB per-line limit and
	// silently truncate the numstat. 1 MiB covers any realistic path.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		any = true
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if n, err := strconv.Atoi(fields[0]); err == nil {
			inserted += n
		}
		if n, err := strconv.Atoi(fields[1]); err == nil {
			deleted += n
		}
	}
	return
}

func runGitCmd(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", &gitCmdError{args: args, msg: msg}
	}
	return stdout.String(), nil
}

type gitCmdError struct {
	args []string
	msg  string
}

func (e *gitCmdError) Error() string {
	return "git " + strings.Join(e.args, " ") + ": " + e.msg
}

// formatSessionDuration produces the compact "1h 23m" / "42m" / "15s" form
// the footer uses. Zero and negative durations render as "0s".
func formatSessionDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + "s"
	}
	if d < time.Hour {
		return strconv.Itoa(int(d.Minutes())) + "m"
	}
	hours := int(d.Hours())
	minutes := int(d.Minutes()) - hours*60
	if minutes == 0 {
		return strconv.Itoa(hours) + "h"
	}
	return strconv.Itoa(hours) + "h " + strconv.Itoa(minutes) + "m"
}
