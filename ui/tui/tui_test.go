package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/dontfuckmycode/dfmc/internal/conversation"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestViewIncludesWorkbenchPanels(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.width = 100

	view := m.View()
	for _, needle := range []string{"DFMC TUI", "Chat", "Status", "Files", "Patch"} {
		if !strings.Contains(view, needle) {
			t.Fatalf("expected view to contain %q, got:\n%s", needle, view)
		}
	}
}

func TestTabSwitching(t *testing.T) {
	m := NewModel(context.Background(), nil)

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after tab key, got %T", nextModel)
	}
	if next.activeTab != 1 {
		t.Fatalf("expected active tab 1 after tab, got %d", next.activeTab)
	}

	prevModel, _ := next.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
	prev, ok := prevModel.(Model)
	if !ok {
		t.Fatalf("expected Model after shift+tab, got %T", prevModel)
	}
	if prev.activeTab != 0 {
		t.Fatalf("expected active tab 0 after shift+tab, got %d", prev.activeTab)
	}
}

func TestLatestAssistantUnifiedDiff(t *testing.T) {
	conv := &conversation.Conversation{
		Branch: "main",
		Branches: map[string][]types.Message{
			"main": {
				{Role: types.RoleUser, Content: "please patch this"},
				{Role: types.RoleAssistant, Content: "```diff\n--- a/demo.txt\n+++ b/demo.txt\n@@ -1 +1 @@\n-old\n+new\n```\n"},
			},
		},
	}

	patch := latestAssistantUnifiedDiff(conv)
	if !strings.Contains(patch, "+++ b/demo.txt") {
		t.Fatalf("expected unified diff, got: %q", patch)
	}
}

func TestFilesTabNavigation(t *testing.T) {
	m := NewModel(context.Background(), nil)
	m.activeTab = 2
	m.files = []string{"a.go", "b.go", "c.go"}

	nextModel, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("expected Model after j key, got %T", nextModel)
	}
	if next.fileIndex != 1 {
		t.Fatalf("expected file index 1 after j, got %d", next.fileIndex)
	}

	prevModel, _ := next.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	prev, ok := prevModel.(Model)
	if !ok {
		t.Fatalf("expected Model after k key, got %T", prevModel)
	}
	if prev.fileIndex != 0 {
		t.Fatalf("expected file index 0 after k, got %d", prev.fileIndex)
	}
}

func TestListProjectFilesSkipsIgnoredDirs(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "cmd", "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(root, ".git", "config"), "[core]\n")
	mustWriteFile(t, filepath.Join(root, "node_modules", "lib.js"), "console.log('x')\n")

	files, err := listProjectFiles(root, 20)
	if err != nil {
		t.Fatalf("listProjectFiles: %v", err)
	}
	if len(files) != 1 || files[0] != "cmd/main.go" {
		t.Fatalf("unexpected files: %#v", files)
	}
}

func TestReadProjectFileRejectsEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWriteFile(t, filepath.Join(outside, "secret.txt"), "nope\n")

	_, _, err := readProjectFile(root, filepath.Join("..", filepath.Base(outside), "secret.txt"), 1024)
	if err == nil {
		t.Fatal("expected escape error")
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}
