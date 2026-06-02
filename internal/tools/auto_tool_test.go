package tools

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newAutoTestReq(params map[string]any) Request { return Request{Params: params} }

// TestAutoTest_GenerateProducesValidImports is the regression guard for the
// import-generation bug: handleGenerate used to copy the source file's
// imports and run %q over the already-quoted ast literal, emitting invalid
// `import "\"fmt\""` lines while never importing the testing package the
// stub actually uses. The generated file must now parse cleanly and import
// exactly "testing".
func TestAutoTest_GenerateProducesValidImports(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "sample.go")
	const source = `package sample

import (
	"fmt"
	"strings"
)

func Greet(name string) string { return fmt.Sprintf("hi %s", strings.TrimSpace(name)) }
`
	if err := os.WriteFile(src, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	tool := NewAutoTestTool()
	res, err := tool.Execute(context.Background(), newAutoTestReq(map[string]any{"mode": "generate", "target": src}))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	gen, _ := res.Data["generated"].(string)
	if gen == "" {
		t.Fatal("empty generated output")
	}

	// 1. The output must be syntactically valid Go.
	fset := token.NewFileSet()
	parsed, perr := parser.ParseFile(fset, "sample_test.go", gen, parser.AllErrors)
	if perr != nil {
		t.Fatalf("generated code does not parse: %v\n---\n%s", perr, gen)
	}

	// 2. Exactly one import, and it is the testing package.
	if len(parsed.Imports) != 1 {
		t.Fatalf("expected exactly 1 import (testing), got %d: %s", len(parsed.Imports), gen)
	}
	if got := parsed.Imports[0].Path.Value; got != `"testing"` {
		t.Fatalf("import path = %s, want \"testing\"", got)
	}

	// 3. No double-quoted artifact, and the source's own imports must NOT
	//    leak in (they'd be unused -> compile error).
	if strings.Contains(gen, `\"`) {
		t.Errorf("generated code contains a double-quoted import artifact:\n%s", gen)
	}
	if strings.Contains(gen, `"fmt"`) || strings.Contains(gen, `"strings"`) {
		t.Errorf("source imports leaked into the white-box stub:\n%s", gen)
	}
	// 4. Same-package white-box stub + the expected test function.
	if !strings.Contains(gen, "package sample\n") {
		t.Errorf("expected same-package stub, got:\n%s", gen)
	}
	if !strings.Contains(gen, "func TestGreet(t *testing.T)") {
		t.Errorf("expected TestGreet stub, got:\n%s", gen)
	}
}

// TestAutoTest_GenerateNoParamsFunc covers the no-parameter branch (a bare
// call comment instead of a table) and confirms it still parses.
func TestAutoTest_GenerateNoParamsFunc(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "noparams.go")
	if err := os.WriteFile(src, []byte("package np\n\nfunc DoThing() {}\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tool := NewAutoTestTool()
	res, err := tool.Execute(context.Background(), newAutoTestReq(map[string]any{"mode": "generate", "target": src}))
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	gen, _ := res.Data["generated"].(string)
	fset := token.NewFileSet()
	if _, perr := parser.ParseFile(fset, "np_test.go", gen, parser.AllErrors); perr != nil {
		t.Fatalf("generated no-params code does not parse: %v\n%s", perr, gen)
	}
	if !strings.Contains(gen, "func TestDoThing(t *testing.T)") {
		t.Errorf("missing TestDoThing stub:\n%s", gen)
	}
}

func TestAutoTest_GenerateMissingTarget(t *testing.T) {
	tool := NewAutoTestTool()
	_, err := tool.Execute(context.Background(), newAutoTestReq(map[string]any{"mode": "generate"}))
	if err == nil {
		t.Fatal("expected error when target is missing")
	}
	if !strings.Contains(err.Error(), "target") {
		t.Errorf("error should mention the missing target param: %v", err)
	}
}

func TestAutoTest_GenerateUnreadableTarget(t *testing.T) {
	tool := NewAutoTestTool()
	_, err := tool.Execute(context.Background(), newAutoTestReq(map[string]any{
		"mode":   "generate",
		"target": filepath.Join(t.TempDir(), "does-not-exist.go"),
	}))
	if err == nil {
		t.Fatal("expected error for a non-existent target file")
	}
}

func TestAutoTest_GenerateNoTestableFuncs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "empty.go")
	if err := os.WriteFile(src, []byte("package empty\n\nvar X = 1\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tool := NewAutoTestTool()
	_, err := tool.Execute(context.Background(), newAutoTestReq(map[string]any{"mode": "generate", "target": src}))
	if err == nil {
		t.Fatal("expected error when the file has no functions")
	}
	if !strings.Contains(err.Error(), "no testable functions") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestAutoTest_RunIsHonestPlaceholder pins that mode=run does NOT pretend to
// run tests — it returns an error pointing the model at run_command, rather
// than a fake "all green".
func TestAutoTest_RunMode(t *testing.T) {
	tool := NewAutoTestTool()
	_, err := tool.Execute(context.Background(), newAutoTestReq(map[string]any{"mode": "run", "target": "./pkg"}))
	if err == nil {
		t.Fatal("run mode should return a placeholder error, not success")
	}
	if !strings.Contains(err.Error(), "run_command") || !strings.Contains(err.Error(), "go test ./pkg") {
		t.Errorf("run error should redirect to run_command with the target: %v", err)
	}
}

func TestAutoTest_FindWithoutCodemap(t *testing.T) {
	tool := NewAutoTestTool() // no codemap set
	_, err := tool.Execute(context.Background(), newAutoTestReq(map[string]any{"mode": "find"}))
	if err == nil {
		t.Fatal("find mode without a codemap should error")
	}
	if !strings.Contains(err.Error(), "codemap") {
		t.Errorf("error should mention the missing codemap: %v", err)
	}
}

func TestAutoTest_InvalidMode(t *testing.T) {
	tool := NewAutoTestTool()
	_, err := tool.Execute(context.Background(), newAutoTestReq(map[string]any{"mode": "frobnicate"}))
	if err == nil {
		t.Fatal("invalid mode should error")
	}
	if !strings.Contains(err.Error(), "not valid") {
		t.Errorf("error should explain valid modes: %v", err)
	}
}
