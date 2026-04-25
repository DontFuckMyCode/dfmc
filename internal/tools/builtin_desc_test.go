package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileTool_Description(t *testing.T) {
	tool := NewWriteFileTool()
	if tool.Description() == "" {
		t.Error("WriteFileTool.Description() is empty")
	}
}

func TestReadFileTool_Description(t *testing.T) {
	tool := NewReadFileTool()
	if tool.Description() == "" {
		t.Error("ReadFileTool.Description() is empty")
	}
}

func TestEditFileTool_Description(t *testing.T) {
	tool := NewEditFileTool()
	if tool.Description() == "" {
		t.Error("EditFileTool.Description() is empty")
	}
}

func TestGrepCodebaseTool_Description(t *testing.T) {
	tool := NewGrepCodebaseTool()
	if tool.Description() == "" {
		t.Error("GrepCodebaseTool.Description() is empty")
	}
}

func TestListDirTool_Description(t *testing.T) {
	tool := NewListDirTool()
	if tool.Description() == "" {
		t.Error("ListDirTool.Description() is empty")
	}
}

func TestApplyPatchTool_Description(t *testing.T) {
	tool := NewApplyPatchTool()
	if tool.Description() == "" {
		t.Error("ApplyPatchTool.Description() is empty")
	}
}

func TestCodemapTool_Description(t *testing.T) {
	tool := NewCodemapTool()
	if tool.Description() == "" {
		t.Error("CodemapTool.Description() is empty")
	}
}

func TestDelegateTaskTool_Description(t *testing.T) {
	tool := NewDelegateTaskTool()
	if tool.Description() == "" {
		t.Error("DelegateTaskTool.Description() is empty")
	}
}

func TestDependencyGraphTool_Description(t *testing.T) {
	tool := NewDependencyGraphTool()
	if tool.Description() == "" {
		t.Error("DependencyGraphTool.Description() is empty")
	}
}

func TestFindSymbolTool_Description(t *testing.T) {
	tool := NewFindSymbolTool()
	if tool.Description() == "" {
		t.Error("FindSymbolTool.Description() is empty")
	}
}

func TestGlobTool_Description(t *testing.T) {
	tool := NewGlobTool()
	if tool.Description() == "" {
		t.Error("GlobTool.Description() is empty")
	}
}

func TestFormatGrepBlock(t *testing.T) {
	lines := []string{"line0", "line1_match", "line2", "line3"}
	// Match at idx=1, no context
	got := formatGrepBlock("f.go", lines, 1, 0, 0)
	if got != "f.go:2:line1_match" {
		t.Errorf("no context: got %q", got)
	}

	// Match at idx=1, before=1, after=1
	got = formatGrepBlock("f.go", lines, 1, 1, 1)
	if got == "" {
		t.Error("formatGrepBlock returned empty")
	}

	// Match at idx=0, before=1 (should not go negative)
	got = formatGrepBlock("f.go", lines, 0, 1, 0)
	if got == "" {
		t.Error("formatGrepBlock returned empty for idx=0")
	}

	// Match at last line, after=1 (should not exceed len)
	got = formatGrepBlock("f.go", lines, 3, 0, 1)
	if got == "" {
		t.Error("formatGrepBlock returned empty for last line")
	}
}

func TestGitignoreMatcher_MatchDir(t *testing.T) {
	// Test nil matcher returns false
	var m *gitignoreMatcher
	if m.matchDir("foo/bar") != false {
		t.Error("nil matcher should return false")
	}

	// Test with actual matcher
	tmp := t.TempDir()
	gi := filepath.Join(tmp, ".gitignore")
	if err := os.WriteFile(gi, []byte("node_modules\n*.log\nbuild/\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	matcher := loadGitignore(tmp)
	if matcher == nil {
		t.Fatal("loadGitignore returned nil")
	}

	if !matcher.matchDir("node_modules") {
		t.Error("should match node_modules")
	}
	if matcher.matchDir("foo") {
		t.Error("foo should NOT match")
	}
	if !matcher.matchDir("error.log") {
		t.Error("*.log should match error.log")
	}
	if !matcher.matchDir("build") {
		t.Error("build/ should match build")
	}
	if matcher.matchDir("src") {
		t.Error("src should NOT match")
	}
}
