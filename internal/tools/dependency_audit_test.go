package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeGoMod is a test helper that writes a go.mod file into dir.
func writeGoMod(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile go.mod: %v", err)
	}
}

func TestDependencyAuditTool_Name(t *testing.T) {
	tool := NewDependencyAuditTool()
	if got := tool.Name(); got != "dependency_audit" {
		t.Errorf("Name() = %q, want %q", got, "dependency_audit")
	}
}

func TestDependencyAuditTool_Description(t *testing.T) {
	tool := NewDependencyAuditTool()
	if got := tool.Description(); got == "" {
		t.Error("Description() returned empty string")
	}
}

func TestDependencyAuditTool_Risk(t *testing.T) {
	tool := NewDependencyAuditTool()
	if got := tool.Risk(); got != RiskRead {
		t.Errorf("Risk() = %v, want RiskRead", got)
	}
}

func TestDependencyAuditTool_Idempotent(t *testing.T) {
	tool := NewDependencyAuditTool()
	if !tool.Idempotent() {
		t.Error("Idempotent() returned false, want true")
	}
}

func TestDependencyAuditTool_SetEngine(t *testing.T) {
	tool := NewDependencyAuditTool()
	tool.SetEngine(nil)
}

func TestDependencyAuditTool_Spec(t *testing.T) {
	tool := NewDependencyAuditTool()
	spec := tool.Spec()
	if spec.Name != "dependency_audit" {
		t.Errorf("Spec().Name = %q, want %q", spec.Name, "dependency_audit")
	}
	if spec.Title == "" {
		t.Error("Spec().Title is empty")
	}
	if spec.Summary == "" {
		t.Error("Spec().Summary is empty")
	}
}

func TestDependencyAuditTool_Execute_nonexistentRoot(t *testing.T) {
	tool := NewDependencyAuditTool()
	_, err := tool.Execute(context.Background(), Request{
		ProjectRoot: "/__nonexistent_root_path_for_dep_audit_test__",
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute returned error for nonexistent root: %v", err)
	}
}

func TestDependencyAuditTool_Execute_emptyDir(t *testing.T) {
	dir := t.TempDir()
	tool := NewDependencyAuditTool()
	_, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute returned error for empty dir: %v", err)
	}
}

func TestDependencyAuditTool_Execute_cleanGoMod(t *testing.T) {
	dir := t.TempDir()
	writeGoMod(t, dir, "module example.com/mymodule\n\ngo 1.21\n")
	tool := NewDependencyAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.Output == "" {
		t.Error("Execute returned empty output for clean go.mod")
	}
}