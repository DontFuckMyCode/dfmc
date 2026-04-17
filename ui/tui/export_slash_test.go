package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/engine"
)

// newExportTestModel sets up a Model on a temp project root so export
// targets a sandboxed directory that t.TempDir cleans up.
func newExportTestModel(t *testing.T, lines []chatLine) Model {
	t.Helper()
	tmp := t.TempDir()
	// A bare engine pointing at the temp dir is enough — exportTranscript
	// uses m.projectRoot() which reads eng.ProjectRoot. No engine Init
	// is needed since export doesn't touch bbolt/providers/tools.
	eng := &engine.Engine{ProjectRoot: tmp}
	m := NewModel(context.Background(), eng)
	m.transcript = lines
	return m
}

func TestSlashExport_EmptyTranscriptRefuses(t *testing.T) {
	m := newExportTestModel(t, nil)
	next, _, handled := m.executeChatCommand("/export")
	if !handled {
		t.Fatalf("/export must be handled")
	}
	nm := next.(Model)
	last := nm.transcript[len(nm.transcript)-1].Content
	if !strings.Contains(strings.ToLower(last), "nothing to export") {
		t.Fatalf("empty transcript should decline; got:\n%s", last)
	}
}

func TestSlashExport_DefaultPathWritesToProjectDotDfmcExports(t *testing.T) {
	m := newExportTestModel(t, []chatLine{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "world"},
	})
	next, _, _ := m.executeChatCommand("/export")
	nm := next.(Model)

	// Should have created .dfmc/exports/transcript-*.md
	exportsDir := filepath.Join(nm.projectRoot(), ".dfmc", "exports")
	entries, err := os.ReadDir(exportsDir)
	if err != nil {
		t.Fatalf("expected exports dir to exist: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 export file, got %d", len(entries))
	}
	file := entries[0].Name()
	if !strings.HasPrefix(file, "transcript-") || !strings.HasSuffix(file, ".md") {
		t.Fatalf("unexpected export filename: %q", file)
	}

	data, err := os.ReadFile(filepath.Join(exportsDir, file))
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "## user") || !strings.Contains(content, "## assistant") {
		t.Fatalf("export should carry role headings, got:\n%s", content)
	}
	if !strings.Contains(content, "hello") || !strings.Contains(content, "world") {
		t.Fatalf("export should carry message content, got:\n%s", content)
	}

	last := nm.transcript[len(nm.transcript)-1].Content
	if !strings.Contains(last, "exported") && !strings.Contains(last, "Exported") {
		t.Fatalf("confirmation system line should say 'exported', got:\n%s", last)
	}
}

func TestSlashExport_CustomPathRelativeToProject(t *testing.T) {
	m := newExportTestModel(t, []chatLine{
		{Role: "user", Content: "Q"},
		{Role: "assistant", Content: "A"},
	})
	next, _, _ := m.executeChatCommand("/export notes/session.md")
	nm := next.(Model)

	path := filepath.Join(nm.projectRoot(), "notes", "session.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("custom export path should exist (%s): %v", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(string(data), "DFMC transcript") {
		t.Fatalf("custom export should carry the standard header, got:\n%s", string(data))
	}
}

func TestSlashExport_SaveAlias(t *testing.T) {
	m := newExportTestModel(t, []chatLine{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hey"},
	})
	next, _, handled := m.executeChatCommand("/save")
	if !handled {
		t.Fatalf("/save alias must be handled")
	}
	nm := next.(Model)
	last := nm.transcript[len(nm.transcript)-1].Content
	if !strings.Contains(last, "exported") && !strings.Contains(last, "Exported") {
		t.Fatalf("/save alias should produce an export confirmation, got:\n%s", last)
	}
}

func TestSlashExport_IncludesProviderLineWhenKnown(t *testing.T) {
	m := newExportTestModel(t, []chatLine{
		{Role: "user", Content: "Q"},
		{Role: "assistant", Content: "A"},
	})
	m.status.Provider = "anthropic"
	m.status.Model = "claude-sonnet-4-6"
	_, _, _ = m.executeChatCommand("/export")
	exportsDir := filepath.Join(m.projectRoot(), ".dfmc", "exports")
	entries, _ := os.ReadDir(exportsDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 export file, got %d", len(entries))
	}
	data, _ := os.ReadFile(filepath.Join(exportsDir, entries[0].Name()))
	content := string(data)
	if !strings.Contains(content, "anthropic") {
		t.Fatalf("export should mention configured provider, got:\n%s", content)
	}
}
