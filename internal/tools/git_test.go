package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTempRepo creates a one-commit git repo under t.TempDir() and returns
// its absolute path. Skips the test if git is unavailable.
func initTempRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--quiet", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "DFMC Test")
	run("config", "commit.gpgsign", "false")

	seed := filepath.Join(dir, "seed.txt")
	if err := os.WriteFile(seed, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	run("add", "seed.txt")
	run("commit", "--quiet", "-m", "initial")
	return dir
}

func TestGitStatus_Clean(t *testing.T) {
	dir := initTempRepo(t)
	res, err := NewGitStatusTool().Execute(context.Background(), Request{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("git_status: %v", err)
	}
	if res.Data["clean"] != true {
		t.Fatalf("expected clean=true on a fresh commit, got %+v", res.Data)
	}
	if br, _ := res.Data["branch"].(string); !strings.Contains(br, "main") {
		t.Fatalf("branch should include 'main', got %q", br)
	}
}

func TestGitStatus_DetectsModifiedAndUntracked(t *testing.T) {
	dir := initTempRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("hello2\n"), 0o644); err != nil {
		t.Fatalf("modify seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("fresh\n"), 0o644); err != nil {
		t.Fatalf("add untracked: %v", err)
	}
	res, err := NewGitStatusTool().Execute(context.Background(), Request{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("git_status: %v", err)
	}
	mod, _ := res.Data["modified"].([]string)
	untr, _ := res.Data["untracked"].([]string)
	if len(mod) == 0 || mod[0] != "seed.txt" {
		t.Fatalf("expected seed.txt in modified, got %+v", mod)
	}
	if len(untr) == 0 || untr[0] != "new.txt" {
		t.Fatalf("expected new.txt in untracked, got %+v", untr)
	}
}

func TestGitDiff_EmptyOnCleanRepo(t *testing.T) {
	dir := initTempRepo(t)
	res, err := NewGitDiffTool().Execute(context.Background(), Request{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("git_diff: %v", err)
	}
	if res.Data["empty"] != true {
		t.Fatalf("expected empty=true on clean repo, got %+v", res.Data)
	}
}

func TestGitBranch_CurrentAndList(t *testing.T) {
	dir := initTempRepo(t)
	res, err := NewGitBranchTool().Execute(context.Background(), Request{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("git_branch: %v", err)
	}
	cur, _ := res.Data["current"].(string)
	if cur != "main" {
		t.Fatalf("expected current=main, got %q", cur)
	}
	local, _ := res.Data["local"].([]string)
	if len(local) != 1 || local[0] != "main" {
		t.Fatalf("expected local=[main], got %+v", local)
	}
}

func TestGitLog_ReturnsInitialCommit(t *testing.T) {
	dir := initTempRepo(t)
	res, err := NewGitLogTool().Execute(context.Background(), Request{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("git_log: %v", err)
	}
	commits, _ := res.Data["commits"].([]map[string]string)
	if len(commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(commits))
	}
	if commits[0]["subject"] != "initial" {
		t.Fatalf("expected subject=initial, got %q", commits[0]["subject"])
	}
	if commits[0]["hash"] == "" {
		t.Fatalf("expected hash, got empty")
	}
}

func TestGitBlame_AttributesInitialCommit(t *testing.T) {
	dir := initTempRepo(t)
	res, err := NewGitBlameTool().Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"path": "seed.txt"},
	})
	if err != nil {
		t.Fatalf("git_blame: %v", err)
	}
	lines, _ := res.Data["lines"].([]map[string]any)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line for seed.txt, got %d (raw=%q)", len(lines), res.Output)
	}
	got := lines[0]
	if got["content"] != "hello" {
		t.Fatalf("expected content 'hello', got %v", got["content"])
	}
	if author, _ := got["author"].(string); author != "DFMC Test" {
		t.Fatalf("expected author 'DFMC Test', got %q", author)
	}
	if hash, _ := got["hash"].(string); len(hash) < 7 {
		t.Fatalf("expected non-empty hash, got %q", hash)
	}
	if got["line"] != 1 {
		t.Fatalf("expected line=1, got %v", got["line"])
	}
}

func TestGitBlame_RespectsLineRange(t *testing.T) {
	dir := initTempRepo(t)
	// Append more lines so a range request is meaningful.
	multiline := "alpha\nbeta\ngamma\ndelta\n"
	if err := os.WriteFile(filepath.Join(dir, "multi.txt"), []byte(multiline), 0o644); err != nil {
		t.Fatalf("write multi: %v", err)
	}
	gitRun := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	gitRun("add", "multi.txt")
	gitRun("commit", "--quiet", "-m", "add multi")

	res, err := NewGitBlameTool().Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params: map[string]any{
			"path":       "multi.txt",
			"line_start": 2,
			"line_end":   3,
		},
	})
	if err != nil {
		t.Fatalf("git_blame: %v", err)
	}
	lines, _ := res.Data["lines"].([]map[string]any)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines for range [2,3], got %d", len(lines))
	}
	if lines[0]["content"] != "beta" || lines[1]["content"] != "gamma" {
		t.Fatalf("range slice wrong, got %q / %q", lines[0]["content"], lines[1]["content"])
	}
}

func TestGitBlame_RequiresPath(t *testing.T) {
	dir := initTempRepo(t)
	_, err := NewGitBlameTool().Execute(context.Background(), Request{ProjectRoot: dir})
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("expected 'path is required' error, got %v", err)
	}
}

func TestGitBlame_RejectsPathOutsideRoot(t *testing.T) {
	dir := initTempRepo(t)
	_, err := NewGitBlameTool().Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"path": "../escape.txt"},
	})
	if err == nil {
		t.Fatalf("expected an out-of-root rejection")
	}
}

func TestGitBlame_RejectsInvertedRange(t *testing.T) {
	dir := initTempRepo(t)
	_, err := NewGitBlameTool().Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params: map[string]any{
			"path":       "seed.txt",
			"line_start": 5,
			"line_end":   2,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "line_end") {
		t.Fatalf("expected line_end>=line_start error, got %v", err)
	}
}

func TestGitCommit_RejectsWildcardPaths(t *testing.T) {
	dir := initTempRepo(t)
	_, err := NewGitCommitTool().Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params: map[string]any{
			"message": "test",
			"paths":   []string{"."},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid commit path") {
		t.Fatalf("expected wildcard path to be rejected, got %v", err)
	}
}

func TestGitCommit_RequiresExplicitPaths(t *testing.T) {
	dir := initTempRepo(t)
	_, err := NewGitCommitTool().Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"message": "test"},
	})
	if err == nil || !strings.Contains(err.Error(), "paths is required") {
		t.Fatalf("expected missing-paths error, got %v", err)
	}
}

func TestGitCommit_HappyPath(t *testing.T) {
	dir := initTempRepo(t)
	newFile := filepath.Join(dir, "feature.txt")
	if err := os.WriteFile(newFile, []byte("hi\n"), 0o644); err != nil {
		t.Fatalf("write feature: %v", err)
	}
	res, err := NewGitCommitTool().Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params: map[string]any{
			"message": "feat: add feature",
			"paths":   []string{"feature.txt"},
		},
	})
	if err != nil {
		t.Fatalf("git_commit: %v\n%s", err, res.Output)
	}
	hash, _ := res.Data["hash"].(string)
	if hash == "" {
		t.Fatalf("expected non-empty hash, got %+v", res.Data)
	}
}

func TestGitWorktree_AddListRemove(t *testing.T) {
	dir := initTempRepo(t)
	sibling := filepath.Join(dir, "wt")

	if _, err := NewGitWorktreeAddTool().Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params: map[string]any{
			"path":       "wt",
			"new_branch": "feature/x",
		},
	}); err != nil {
		t.Fatalf("worktree add: %v", err)
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("expected worktree dir to exist: %v", err)
	}

	listRes, err := NewGitWorktreeListTool().Execute(context.Background(), Request{ProjectRoot: dir})
	if err != nil {
		t.Fatalf("worktree list: %v", err)
	}
	trees, _ := listRes.Data["worktrees"].([]map[string]string)
	foundNew := false
	for _, tr := range trees {
		if strings.HasSuffix(tr["path"], "/wt") {
			foundNew = true
		}
	}
	if !foundNew {
		t.Fatalf("added worktree missing from list: %+v", trees)
	}

	if _, err := NewGitWorktreeRemoveTool().Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"path": "wt"},
	}); err != nil {
		t.Fatalf("worktree remove: %v", err)
	}
	if _, err := os.Stat(sibling); !os.IsNotExist(err) {
		t.Fatalf("expected worktree dir removed, stat=%v", err)
	}
}

func TestGitWorktreeAdd_RejectsBadBranchName(t *testing.T) {
	dir := initTempRepo(t)
	_, err := NewGitWorktreeAddTool().Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params: map[string]any{
			"path":       "wt",
			"new_branch": "bad name",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "branch name blocked") {
		t.Fatalf("expected branch name rejection, got %v", err)
	}
}

func TestTaskSplitTool_ReturnsSubtasks(t *testing.T) {
	res, err := NewTaskSplitTool().Execute(context.Background(), Request{
		Params: map[string]any{
			"task": "do 1) survey engine 2) survey router 3) document context",
		},
	})
	if err != nil {
		t.Fatalf("task_split: %v", err)
	}
	if count, _ := res.Data["count"].(int); count != 3 {
		t.Fatalf("expected count=3, got %v (%+v)", count, res.Data)
	}
	if par, _ := res.Data["parallel"].(bool); !par {
		t.Fatalf("numbered list without stage markers should be parallel")
	}
}

func TestTaskSplitTool_RejectsEmpty(t *testing.T) {
	_, err := NewTaskSplitTool().Execute(context.Background(), Request{Params: map[string]any{}})
	if err == nil {
		t.Fatal("expected task_split to fail on empty input")
	}
}

func TestRunGit_BlocksAmendAndNoVerify(t *testing.T) {
	dir := initTempRepo(t)
	if _, _, _, err := runGit(context.Background(), dir, 0, "commit", "--amend", "-m", "x"); err == nil || !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("--amend should be blocked, got %v", err)
	}
	if _, _, _, err := runGit(context.Background(), dir, 0, "commit", "--no-verify", "-m", "x"); err == nil || !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("--no-verify should be blocked, got %v", err)
	}
}
