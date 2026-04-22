package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/config"
	"github.com/dontfuckmycode/dfmc/internal/engine"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func newCLITestEngine(t *testing.T) *engine.Engine {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	cfg := config.DefaultConfig()
	eng, err := engine.New(cfg)
	if err != nil {
		t.Fatalf("engine.New: %v", err)
	}
	if err := eng.Init(context.Background()); err != nil {
		t.Fatalf("eng.Init: %v", err)
	}
	t.Cleanup(func() { _ = eng.Shutdown() })
	return eng
}

func TestRunConversationLifecycleAndBranch(t *testing.T) {
	eng := newCLITestEngine(t)

	if code := runConversation(context.Background(), eng, []string{"new"}, true); code != 0 {
		t.Fatalf("conversation new exit=%d", code)
	}
	active := eng.ConversationActive()
	if active == nil {
		t.Fatal("expected active conversation")
	}

	eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleUser,
		Content:   "q1",
		Timestamp: time.Now(),
	})
	eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleAssistant,
		Content:   "a1",
		TokenCnt:  42,
		Timestamp: time.Now(),
	})

	if code := runConversation(context.Background(), eng, []string{"save"}, true); code != 0 {
		t.Fatalf("conversation save exit=%d", code)
	}
	if code := runConversation(context.Background(), eng, []string{"list"}, true); code != 0 {
		t.Fatalf("conversation list exit=%d", code)
	}
	if code := runConversation(context.Background(), eng, []string{"active"}, true); code != 0 {
		t.Fatalf("conversation active exit=%d", code)
	}
	if code := runConversation(context.Background(), eng, []string{"undo"}, true); code != 0 {
		t.Fatalf("conversation undo exit=%d", code)
	}
	if got := len(eng.ConversationActive().Messages()); got != 0 {
		t.Fatalf("expected empty messages after undo, got %d", got)
	}
	eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleUser,
		Content:   "main-q",
		Timestamp: time.Now(),
	})
	eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleAssistant,
		Content:   "main-a",
		TokenCnt:  12,
		Timestamp: time.Now(),
	})

	convID := active.ID
	if code := runConversation(context.Background(), eng, []string{"new"}, true); code != 0 {
		t.Fatalf("conversation new(2) exit=%d", code)
	}
	if code := runConversation(context.Background(), eng, []string{"load", convID}, true); code != 0 {
		t.Fatalf("conversation load exit=%d", code)
	}

	if code := runConversation(context.Background(), eng, []string{"branch", "create", "alt"}, true); code != 0 {
		t.Fatalf("conversation branch create exit=%d", code)
	}
	if code := runConversation(context.Background(), eng, []string{"branch", "switch", "alt"}, true); code != 0 {
		t.Fatalf("conversation branch switch exit=%d", code)
	}
	eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:      types.RoleUser,
		Content:   "alt-q",
		Timestamp: time.Now(),
	})
	if code := runConversation(context.Background(), eng, []string{"branch", "compare", "main", "alt"}, true); code != 0 {
		t.Fatalf("conversation branch compare exit=%d", code)
	}
}

func TestRunChatSlashProviderModelAndBranch(t *testing.T) {
	eng := newCLITestEngine(t)
	_ = eng.ConversationStart()

	exit, handled := runChatSlash(context.Background(), eng, "/provider openai")
	if exit || !handled {
		t.Fatalf("expected handled provider command, exit=%v handled=%v", exit, handled)
	}
	if st := eng.Status(); st.Provider != "openai" {
		t.Fatalf("expected provider openai, got %s", st.Provider)
	}

	exit, handled = runChatSlash(context.Background(), eng, "/model gpt-5.4")
	if exit || !handled {
		t.Fatalf("expected handled model command, exit=%v handled=%v", exit, handled)
	}
	if st := eng.Status(); st.Model != "gpt-5.4" {
		t.Fatalf("expected model gpt-5.4, got %s", st.Model)
	}

	exit, handled = runChatSlash(context.Background(), eng, "/branch exp")
	if exit || !handled {
		t.Fatalf("expected handled branch command, exit=%v handled=%v", exit, handled)
	}
	active := eng.ConversationActive()
	if active == nil || active.Branch != "exp" {
		t.Fatalf("expected active branch exp, got %+v", active)
	}
}

func TestSummarizeMessageUsage(t *testing.T) {
	msgs := []types.Message{
		{Role: types.RoleUser, Content: "hello world"},
		{Role: types.RoleAssistant, Content: "answer text", TokenCnt: 7},
	}
	messages, users, assistants, tokens := summarizeMessageUsage(msgs)
	if messages != 2 || users != 1 || assistants != 1 {
		t.Fatalf("unexpected counts: m=%d u=%d a=%d", messages, users, assistants)
	}
	if tokens <= 7 {
		t.Fatalf("expected fallback+explicit tokens, got %d", tokens)
	}
	if estimateConversationCostUSD("unknown", 1000) >= 0 {
		t.Fatal("expected unknown provider cost to be negative")
	}
}

func TestGitWorkingDiff(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		return cmd.Run()
	}
	if err := run("init"); err != nil {
		t.Skipf("git is unavailable: %v", err)
	}
	_ = run("config", "user.name", "dfmc-test")
	_ = run("config", "user.email", "dfmc@test.local")

	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_ = run("add", "a.txt")
	_ = run("commit", "-m", "init")
	if err := os.WriteFile(target, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("rewrite file: %v", err)
	}

	diff, err := gitWorkingDiff(root, 10_000)
	if err != nil {
		t.Fatalf("gitWorkingDiff error: %v", err)
	}
	if !strings.Contains(diff, "a.txt") {
		t.Fatalf("expected a.txt in diff, got: %s", diff)
	}
}

func TestRunChatSlashApplyFromLatestAssistantDiff(t *testing.T) {
	eng := newCLITestEngine(t)
	root := t.TempDir()
	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		return cmd.Run()
	}
	if err := run("init"); err != nil {
		t.Skipf("git is unavailable: %v", err)
	}
	_ = run("config", "user.name", "dfmc-test")
	_ = run("config", "user.email", "dfmc@test.local")

	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_ = run("add", "a.txt")
	_ = run("commit", "-m", "init")

	eng.ProjectRoot = root
	_ = eng.ConversationStart()
	eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:    types.RoleAssistant,
		Content: "```diff\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1,2 @@\n hello\n+world\n```\n",
	})

	exit, handled := runChatSlash(context.Background(), eng, "/apply")
	if exit || !handled {
		t.Fatalf("expected handled /apply command, exit=%v handled=%v", exit, handled)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !strings.Contains(string(data), "world") {
		t.Fatalf("expected patch to apply, got: %s", string(data))
	}
}

func TestRunChatSlashApplyCheckOnlyDoesNotModify(t *testing.T) {
	eng := newCLITestEngine(t)
	root := t.TempDir()
	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		return cmd.Run()
	}
	if err := run("init"); err != nil {
		t.Skipf("git is unavailable: %v", err)
	}
	_ = run("config", "user.name", "dfmc-test")
	_ = run("config", "user.email", "dfmc@test.local")

	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_ = run("add", "a.txt")
	_ = run("commit", "-m", "init")

	eng.ProjectRoot = root
	_ = eng.ConversationStart()
	eng.Conversation.AddMessage("offline", "offline-analyzer-v1", types.Message{
		Role:    types.RoleAssistant,
		Content: "```diff\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1,2 @@\n hello\n+world\n```\n",
	})

	exit, handled := runChatSlash(context.Background(), eng, "/apply --check")
	if exit || !handled {
		t.Fatalf("expected handled /apply --check command, exit=%v handled=%v", exit, handled)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if strings.Contains(string(data), "world") {
		t.Fatalf("expected check mode to avoid file changes, got: %s", string(data))
	}
}

func TestGitChangedFilesPreservesLeadingStatusColumn(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) error {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		return cmd.Run()
	}
	if err := run("init"); err != nil {
		t.Skipf("git is unavailable: %v", err)
	}
	_ = run("config", "user.name", "dfmc-test")
	_ = run("config", "user.email", "dfmc@test.local")

	target := filepath.Join(root, "a.txt")
	if err := os.WriteFile(target, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_ = run("add", "a.txt")
	_ = run("commit", "-m", "init")
	if err := os.WriteFile(target, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("rewrite file: %v", err)
	}

	files, err := gitChangedFiles(root, 10)
	if err != nil {
		t.Fatalf("gitChangedFiles error: %v", err)
	}
	if len(files) != 1 || files[0] != "a.txt" {
		t.Fatalf("expected [a.txt], got %#v", files)
	}
}
