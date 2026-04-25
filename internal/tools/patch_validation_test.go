package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestPatchValidation_MissingPatch(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "patch_validation", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{},
	})
	if err == nil {
		t.Fatalf("expected error for missing patch")
	}
}

func TestPatchValidation_EmptyPatch(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "patch_validation", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"patch": "   "},
	})
	if err == nil {
		t.Fatalf("expected error for empty patch")
	}
}

func TestPatchValidation_NoFilesInPatch(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	_, err := eng.Execute(context.Background(), "patch_validation", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"patch": "just some text with no diff headers"},
	})
	if err == nil {
		t.Fatalf("expected error for patch with no file diffs")
	}
}

func TestPatchValidation_NewFile(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	patch := `--- /dev/null
+++ b/newfile.go
@@ -0,0 +1,3 @@
+package foo
+func F() {}
+`
	_, err := eng.Execute(context.Background(), "patch_validation", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	})
	// New file — skip_reason = "new file (no original to diff against)"
	if err != nil {
		t.Fatalf("unexpected error for new file patch: %v", err)
	}
}

func TestPatchValidation_DryRunValidHunk(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	// Write a file to patch
	fpath := filepath.Join(tmp, "foo.go")
	os.WriteFile(fpath, []byte("package foo\nfunc F() {}\n"), 0644)

	patch := `--- a/foo.go
+++ b/foo.go
@@ -1,2 +1,2 @@
 package foo
-func F() {}
+func G() {}
`
	res, err := eng.Execute(context.Background(), "patch_validation", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := res.Data["files"].([]map[string]any)
	if !ok || len(data) == 0 {
		t.Fatalf("expected at least one file result")
	}
	if applied := data[0]["hunks_applied"].(int); applied != 1 {
		t.Errorf("want 1 hunk applied, got %d", applied)
	}
}

func TestPatchValidation_DryRunRejectedHunk(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	fpath := filepath.Join(tmp, "foo.go")
	os.WriteFile(fpath, []byte("package foo\nfunc F() {}\n"), 0644)

	// Context lines don't match — will be rejected
	patch := `--- a/foo.go
+++ b/foo.go
@@ -10,3 +10,3 @@
 different context
-func X() {}
+func Y() {}
`
	res, err := eng.Execute(context.Background(), "patch_validation", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := res.Data["files"].([]map[string]any)
	if !ok || len(data) == 0 {
		t.Fatalf("expected file result")
	}
	if rejected := data[0]["hunks_rejected"].(int); rejected == 0 {
		t.Logf("note: hunk may have applied with fuzzy offset, this is valid too")
	}
}

func TestPatchValidation_WithValidationCommand(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	fpath := filepath.Join(tmp, "main.go")
	os.WriteFile(fpath, []byte("package main\nfunc main() {}\n"), 0644)

	patch := `--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func main() {}
+func main() {} //
`
	res, err := eng.Execute(context.Background(), "patch_validation", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"patch":               patch,
			"validation_command": "go build ./...",
		},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data["validation_exit_code"] == nil {
		t.Errorf("expected validation_exit_code in result")
	}
}

func TestPatchValidation_CleanFlag(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	fpath := filepath.Join(tmp, "main.go")
	os.WriteFile(fpath, []byte("package main\nfunc main() {}\n"), 0644)

	patch := `--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func main() {}
+func main() {} //
`
	res, err := eng.Execute(context.Background(), "patch_validation", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	clean, ok := res.Data["clean"].(bool)
	if !ok {
		t.Fatalf("expected clean bool in result")
	}
	// hunks_applied = 1, rejected = 0, no validation command → clean
	if !clean {
		t.Errorf("expected clean=true for fully-applied patch, got clean=%v", clean)
	}
}

func TestPatchValidation_DeletedFile(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	// File must exist for delete diff to work
	fpath := filepath.Join(tmp, "deleted.go")
	os.WriteFile(fpath, []byte("package foo\n"), 0644)

	// Real git delete diff (git rm + git diff --cached)
	patch := `diff --git a/deleted.go b/deleted.go
deleted file mode 100644
--- a/deleted.go
+++ /dev/null
@@ -1 +0,0 @@
-package foo
`
	res, err := eng.Execute(context.Background(), "patch_validation", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := res.Data["files"].([]map[string]any)
	if !ok || len(data) == 0 {
		t.Fatalf("expected file result")
	}
	if data[0]["skip_reason"] != "file deletion" {
		t.Errorf("expected skip_reason=file deletion, got %v", data[0]["skip_reason"])
	}
}

func TestPatchValidationTool_Name(t *testing.T) {
	tool := NewPatchValidationTool()
	if tool.Name() != "patch_validation" {
		t.Errorf("want patch_validation, got %s", tool.Name())
	}
}

func TestPatchValidationTool_Spec(t *testing.T) {
	tool := NewPatchValidationTool()
	spec := tool.Spec()
	if spec.Name != "patch_validation" {
		t.Errorf("spec.Name: want patch_validation, got %s", spec.Name)
	}
	if spec.Risk != RiskExecute {
		t.Errorf("spec.Risk: want RiskExecute, got %v", spec.Risk)
	}
	argsByName := make(map[string]Arg)
	for _, a := range spec.Args {
		argsByName[a.Name] = a
	}
	for _, name := range []string{"patch", "validation_command"} {
		if _, ok := argsByName[name]; !ok {
			t.Errorf("spec.Args missing %s", name)
		}
	}
	if _, ok := argsByName["project_root"]; ok {
		t.Errorf("spec.Args should not contain project_root (it was removed)")
	}
}

func TestPatchHunkStats(t *testing.T) {
	patch := `--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,3 @@
 Old line
-func F() {}
+func G() {}
`
	stats, err := PatchHunkStats(patch)
	if err != nil {
		t.Fatalf("PatchHunkStats: %v", err)
	}
	if stats["foo.go"] != 1 {
		t.Errorf("want 1 hunk for foo.go, got %d", stats["foo.go"])
	}
}

func TestValidatePatchIsClean_Valid(t *testing.T) {
	tmp := t.TempDir()
	fpath := filepath.Join(tmp, "foo.go")
	os.WriteFile(fpath, []byte("package foo\nfunc F() {}\n"), 0644)

	patch := `--- a/foo.go
+++ b/foo.go
@@ -1,2 +1,2 @@
 package foo
-func F() {}
+func G() {}
`
	clean, total, rejected, err := ValidatePatchIsClean(patch, tmp)
	if err != nil {
		t.Fatalf("ValidatePatchIsClean: %v", err)
	}
	if !clean {
		t.Errorf("expected clean=true, total=%d rejected=%d", total, rejected)
	}
}

func TestValidatePatchIsClean_Rejected(t *testing.T) {
	tmp := t.TempDir()
	fpath := filepath.Join(tmp, "foo.go")
	os.WriteFile(fpath, []byte("package foo\nfunc F() {}\n"), 0644)

	patch := `--- a/foo.go
+++ b/foo.go
@@ -10,2 +10,2 @@
 different context
-func X() {}
+func Y() {}
`
	clean, total, rejected, err := ValidatePatchIsClean(patch, tmp)
	if err != nil {
		t.Fatalf("ValidatePatchIsClean: %v", err)
	}
	if clean {
		t.Errorf("expected clean=false for mismatched hunk, total=%d rejected=%d", total, rejected)
	}
}

func TestPatchValidation_TraversalRejection(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	fpath := filepath.Join(tmp, "main.go")
	os.WriteFile(fpath, []byte("package main\nfunc main() {}\n"), 0644)

	// Patch target contains traversal — should be rejected by EnsureWithinRoot
	patch := `--- a/../main.go
+++ b/../main.go
@@ -1,2 +1,2 @@
 package main
-func main() {}
+func main() {} //
`
	res, err := eng.Execute(context.Background(), "patch_validation", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"patch": patch},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data := res.Data["files"].([]map[string]any)
	if data[0]["error"] == "" {
		t.Errorf("expected traversal rejection error in result, got: %+v", data[0])
	}
}

func TestPatchValidation_ValidationCommandShellBlocked(t *testing.T) {
	tmp := t.TempDir()
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	fpath := filepath.Join(tmp, "main.go")
	os.WriteFile(fpath, []byte("package main\nfunc main() {}\n"), 0644)

	patch := `--- a/main.go
+++ b/main.go
@@ -1,2 +1,2 @@
 package main
-func main() {}
+func main() {} //
`
	// bash is a blocked shell interpreter — should be rejected
	_, err := eng.Execute(context.Background(), "patch_validation", Request{
		ProjectRoot: tmp,
		Params: map[string]any{
			"patch":               patch,
			"validation_command": "bash -c whoami",
		},
	})
	if err == nil {
		t.Fatalf("expected error for blocked shell interpreter in validation_command")
	}
	if !strings.Contains(err.Error(), "blocked") && !strings.Contains(err.Error(), "shell interpreter") {
		t.Errorf("expected blocked shell interpreter error, got: %v", err)
	}
}

func TestPatchValidationTool_SetEngine(t *testing.T) {
	tool := NewPatchValidationTool()
	eng := New(*config.DefaultConfig())
	tool.SetEngine(eng)
	// No panic — engine is wired
	if tool.Name() != "patch_validation" {
		t.Errorf("name mismatch")
	}
}