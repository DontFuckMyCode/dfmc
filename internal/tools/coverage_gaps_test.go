package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewInterfaceDiffTool(t *testing.T) {
	tool := NewInterfaceDiffTool()
	if tool == nil {
		t.Fatal("NewInterfaceDiffTool() returned nil")
	}
}

func TestInterfaceDiffToolName(t *testing.T) {
	tool := NewInterfaceDiffTool()
	if got := tool.Name(); got != "interface_diff" {
		t.Errorf("Name() = %q, want %q", got, "interface_diff")
	}
}

func TestInterfaceDiffToolDescription(t *testing.T) {
	tool := NewInterfaceDiffTool()
	desc := tool.Description()
	if desc == "" {
		t.Error("Description() returned empty string")
	}
	if len(desc) < 50 {
		t.Errorf("Description() too short: %q", desc)
	}
}

func TestInterfaceDiffToolSetEngine(t *testing.T) {
	tool := NewInterfaceDiffTool()
	// Should not panic
	tool.SetEngine(nil)
}

func TestInterfaceDiffToolRisk(t *testing.T) {
	tool := NewInterfaceDiffTool()
	if got := tool.Risk(); got != RiskRead {
		t.Errorf("Risk() = %v, want %v", got, RiskRead)
	}
}

func TestInterfaceDiffToolCacheable(t *testing.T) {
	tool := NewInterfaceDiffTool()
	if got := tool.Cacheable(); got != false {
		t.Errorf("Cacheable() = %v, want false", got)
	}
}

func TestInterfaceDiffToolExecute_MissingPaths(t *testing.T) {
	tool := NewInterfaceDiffTool()

	_, err := tool.Execute(context.Background(), Request{
		Params: map[string]any{},
	})
	if err == nil {
		t.Error("Execute() expected error for empty base_path and head_path")
	}
}

func TestInterfaceDiffToolExecute_WithBasePath(t *testing.T) {
	// Create a temp file with a function
	tmpDir := t.TempDir()
	baseFile := filepath.Join(tmpDir, "base.go")
	content := `package foo

func Bar() {}
`
	if err := os.WriteFile(baseFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewInterfaceDiffTool()
	result, err := tool.Execute(context.Background(), Request{
		Params: map[string]any{
			"base_path": baseFile,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if result.Output == "" {
		t.Error("Execute() returned empty output")
	}
}

func TestInterfaceDiffToolExecute_WithBothPaths(t *testing.T) {
	tmpDir := t.TempDir()
	baseFile := filepath.Join(tmpDir, "base.go")
	headFile := filepath.Join(tmpDir, "head.go")

	baseContent := `package foo

func Bar() int { return 0 }
`
	headContent := `package foo

func Bar() string { return "" }
`
	if err := os.WriteFile(baseFile, []byte(baseContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(headFile, []byte(headContent), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewInterfaceDiffTool()
	result, err := tool.Execute(context.Background(), Request{
		Params: map[string]any{
			"base_path": baseFile,
			"head_path": headFile,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	var ifaceResult InterfaceDiffResult
	if err := json.Unmarshal([]byte(result.Output), &ifaceResult); err != nil {
		t.Fatalf("Failed to parse JSON output: %v", err)
	}

	if ifaceResult.Summary.TotalChanges == 0 {
		t.Error("Expected changes between base and head")
	}
}

func TestParseInterfaceItems_EmptyPath(t *testing.T) {
	items := parseInterfaceItems("")
	if items != nil {
		t.Errorf("parseInterfaceItems(%q) = %v, want nil", "", items)
	}
}

func TestParseInterfaceItems_NonExistentPath(t *testing.T) {
	items := parseInterfaceItems("/nonexistent/path/to/file.go")
	if items != nil {
		t.Errorf("parseInterfaceItems() on nonexistent path = %v, want nil", items)
	}
}

func TestParseInterfaceItems_SingleFile(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "test.go")
	content := `package foo

type MyInterface interface {
	MethodA()
	MethodB()
}

type MyStruct struct {
	Field1 int
	Field2 string
}

func ExportedFunc() {}

func privateFunc() {}
`
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	items := parseInterfaceItems(file)
	if len(items) == 0 {
		t.Error("parseInterfaceItems() returned no items")
	}

	// Check that we found the interface
	foundInterface := false
	for _, item := range items {
		if item.Kind == "interface" && item.Name == "MyInterface" {
			foundInterface = true
			if len(item.Methods) != 2 {
				t.Errorf("Expected 2 methods, got %d", len(item.Methods))
			}
		}
	}
	if !foundInterface {
		t.Error("Did not find MyInterface in parsed items")
	}
}

func TestParseInterfaceItems_Directory(t *testing.T) {
	tmpDir := t.TempDir()
	file1 := filepath.Join(tmpDir, "file1.go")
	file2 := filepath.Join(tmpDir, "file2.go")

	if err := os.WriteFile(file1, []byte("package foo\nfunc Func1() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file2, []byte("package foo\nfunc Func2() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	items := parseInterfaceItems(tmpDir)
	if len(items) < 2 {
		t.Errorf("Expected at least 2 items from directory, got %d", len(items))
	}
}

func TestParseFileInterfaces(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "test.go")
	content := `package foo

type MyInterface interface {
	DoSomething()
	DoAnother()
}

type MyStruct struct {
	Field1 int
	Field2 string
}

func MyFunc(a int, b string) (int, error) {
	return 0, nil
}

func privateMethod() {}
`
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	items := parseFileInterfaces(file)
	if len(items) == 0 {
		t.Fatal("parseFileInterfaces() returned no items")
	}

	// Verify interface
	var iface SymbolInfo
	var ifaceFound bool
	for _, item := range items {
		if item.Kind == "interface" && item.Name == "MyInterface" {
			iface = item
			ifaceFound = true
			break
		}
	}
	if !ifaceFound {
		t.Error("Interface MyInterface not found")
	}
	if len(iface.Methods) != 2 {
		t.Errorf("Expected 2 interface methods, got %d", len(iface.Methods))
	}

	// Verify function
	var fn SymbolInfo
	var fnFound bool
	for _, item := range items {
		if item.Kind == "function" && item.Name == "MyFunc" {
			fn = item
			fnFound = true
			break
		}
	}
	if !fnFound {
		t.Error("Function MyFunc not found")
	}
	if fn.Signature == "" {
		t.Error("Function MyFunc signature is empty")
	}
}

func TestParseFileInterfaces_NonExistent(t *testing.T) {
	items := parseFileInterfaces("/nonexistent/file.go")
	if items != nil {
		t.Errorf("parseFileInterfaces() on nonexistent file = %v, want nil", items)
	}
}

func TestCompareInterfaces_Added(t *testing.T) {
	base := []SymbolInfo{}
	head := []SymbolInfo{
		{Name: "NewFunc", Kind: "function", File: "test.go", Line: 1, Signature: "func NewFunc()"},
	}

	changes := compareInterfaces(base, head, "")
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != "added" {
		t.Errorf("Change kind = %q, want %q", changes[0].Kind, "added")
	}
	if changes[0].Severity != "info" {
		t.Errorf("Change severity = %q, want %q", changes[0].Severity, "info")
	}
}

func TestCompareInterfaces_Removed(t *testing.T) {
	base := []SymbolInfo{
		{Name: "OldFunc", Kind: "function", File: "test.go", Line: 1, Signature: "func OldFunc()"},
	}
	head := []SymbolInfo{}

	changes := compareInterfaces(base, head, "")
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != "removed" {
		t.Errorf("Change kind = %q, want %q", changes[0].Kind, "removed")
	}
	if changes[0].Severity != "breaking" {
		t.Errorf("Change severity = %q, want %q", changes[0].Severity, "breaking")
	}
}

func TestCompareInterfaces_Modified(t *testing.T) {
	base := []SymbolInfo{
		{Name: "Func", Kind: "function", File: "test.go", Line: 1, Signature: "func Func() int"},
	}
	head := []SymbolInfo{
		{Name: "Func", Kind: "function", File: "test.go", Line: 1, Signature: "func Func() string"},
	}

	changes := compareInterfaces(base, head, "")
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != "modified" {
		t.Errorf("Change kind = %q, want %q", changes[0].Kind, "modified")
	}
	if changes[0].Severity != "breaking" {
		t.Errorf("Change severity = %q, want %q", changes[0].Severity, "breaking")
	}
}

func TestCompareInterfaces_MethodRemovedFromInterface(t *testing.T) {
	base := []SymbolInfo{
		{Name: "Iface", Kind: "interface", File: "test.go", Line: 1, Methods: []string{"MethodA", "MethodB"}},
	}
	head := []SymbolInfo{
		{Name: "Iface", Kind: "interface", File: "test.go", Line: 1, Methods: []string{"MethodA"}},
	}

	changes := compareInterfaces(base, head, "")
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change, got %d", len(changes))
	}
	if changes[0].Kind != "modified" {
		t.Errorf("Change kind = %q, want %q", changes[0].Kind, "modified")
	}
	if changes[0].Message == "" {
		t.Error("Change message is empty")
	}
}

func TestCompareInterfaces_NoChanges(t *testing.T) {
	base := []SymbolInfo{
		{Name: "Func", Kind: "function", File: "test.go", Line: 1, Signature: "func Func()"},
	}
	head := []SymbolInfo{
		{Name: "Func", Kind: "function", File: "test.go", Line: 1, Signature: "func Func()"},
	}

	changes := compareInterfaces(base, head, "")
	if len(changes) != 0 {
		t.Errorf("Expected 0 changes, got %d", len(changes))
	}
}

func TestCompareInterfaces_TargetFilter(t *testing.T) {
	base := []SymbolInfo{
		{Name: "FuncA", Kind: "function", File: "test.go", Line: 1, Signature: "func FuncA()"},
		{Name: "FuncB", Kind: "function", File: "test.go", Line: 2, Signature: "func FuncB()"},
	}
	head := []SymbolInfo{
		{Name: "FuncA", Kind: "function", File: "test.go", Line: 1, Signature: "func FuncA() string"},
		{Name: "FuncB", Kind: "function", File: "test.go", Line: 2, Signature: "func FuncB() int"},
	}

	// Filter by name
	changes := compareInterfaces(base, head, "FuncA")
	if len(changes) != 1 {
		t.Fatalf("Expected 1 change when filtering by name, got %d", len(changes))
	}

	// Filter by kind
	changes = compareInterfaces(base, head, "function")
	if len(changes) != 2 {
		t.Fatalf("Expected 2 changes when filtering by kind, got %d", len(changes))
	}
}

func TestCompareInterfaces_EmptySlices(t *testing.T) {
	changes := compareInterfaces([]SymbolInfo{}, []SymbolInfo{}, "")
	if len(changes) != 0 {
		t.Errorf("Expected 0 changes for empty slices, got %d", len(changes))
	}
}

func TestCompareInterfaces_InterfaceMethodChanges_SameLength(t *testing.T) {
	// When interface methods have same count but different methods
	base := []SymbolInfo{
		{Name: "Iface", Kind: "interface", File: "test.go", Line: 1, Methods: []string{"MethodA"}},
	}
	head := []SymbolInfo{
		{Name: "Iface", Kind: "interface", File: "test.go", Line: 1, Methods: []string{"MethodB"}},
	}

	changes := compareInterfaces(base, head, "")
	// Both methods exist in head, so nothing is "removed" from head's perspective
	if len(changes) != 0 {
		t.Errorf("Expected 0 changes (same count, different content), got %d", len(changes))
	}
}

func TestDiffSlices(t *testing.T) {
	tests := []struct {
		name string
		a    []string
		b    []string
		want []string
	}{
		{
			name: "simple diff",
			a:    []string{"a", "b", "c"},
			b:    []string{"a", "b"},
			want: []string{"c"},
		},
		{
			name: "no diff",
			a:    []string{"a", "b"},
			b:    []string{"a", "b", "c"},
			want: nil,
		},
		{
			name: "empty a",
			a:    []string{},
			b:    []string{"a"},
			want: nil,
		},
		{
			name: "empty b",
			a:    []string{"a"},
			b:    []string{},
			want: []string{"a"},
		},
		{
			name: "both empty",
			a:    []string{},
			b:    []string{},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diffSlices(tt.a, tt.b)
			if len(got) != len(tt.want) {
				t.Errorf("diffSlices() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompareInterfaces_SortBySeverity(t *testing.T) {
	base := []SymbolInfo{
		{Name: "RemovedFunc", Kind: "function", File: "test.go", Line: 1, Signature: "func RemovedFunc()"},
	}
	head := []SymbolInfo{
		{Name: "AddedFunc", Kind: "function", File: "test.go", Line: 1, Signature: "func AddedFunc()"},
		{Name: "ModifiedFunc", Kind: "function", File: "test.go", Line: 2, Signature: "func ModifiedFunc() int"},
	}

	changes := compareInterfaces(base, head, "")
	if len(changes) != 3 {
		t.Fatalf("Expected 3 changes, got %d", len(changes))
	}

	// First change should be breaking (removed)
	if changes[0].Severity != "breaking" {
		t.Errorf("First change severity = %q, want %q", changes[0].Severity, "breaking")
	}

	// Last change should be info (added)
	if changes[len(changes)-1].Severity != "info" {
		t.Errorf("Last change severity = %q, want %q", changes[len(changes)-1].Severity, "info")
	}
}

func TestInterfaceDiffResult_JSON(t *testing.T) {
	tmpDir := t.TempDir()
	baseFile := filepath.Join(tmpDir, "base.go")
	headFile := filepath.Join(tmpDir, "head.go")

	baseContent := `package foo
func Removed() {}
`
	headContent := `package foo
func Added() {}
`
	if err := os.WriteFile(baseFile, []byte(baseContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(headFile, []byte(headContent), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewInterfaceDiffTool()
	result, err := tool.Execute(context.Background(), Request{
		Params: map[string]any{
			"base_path": baseFile,
			"head_path": headFile,
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Verify JSON is valid and contains expected fields
	var ifaceResult InterfaceDiffResult
	if err := json.Unmarshal([]byte(result.Output), &ifaceResult); err != nil {
		t.Fatalf("Output is not valid JSON: %v\nOutput: %s", err, result.Output)
	}

	if ifaceResult.Summary.TotalChanges != 2 {
		t.Errorf("Expected total_changes=2, got %d", ifaceResult.Summary.TotalChanges)
	}
	if ifaceResult.Summary.Breaking != 1 {
		t.Errorf("Expected breaking=1, got %d", ifaceResult.Summary.Breaking)
	}
	if ifaceResult.Summary.Infos != 1 {
		t.Errorf("Expected infos=1, got %d", ifaceResult.Summary.Infos)
	}
}

// ---------------------------------------------------------------------------
// AuditTool
// ---------------------------------------------------------------------------

func TestNewAuditTool(t *testing.T) {
	tool := NewAuditTool()
	if tool == nil {
		t.Fatal("NewAuditTool() returned nil")
	}
}

func TestAuditTool_Name(t *testing.T) {
	tool := NewAuditTool()
	if got := tool.Name(); got != "audit" {
		t.Errorf("Name() = %q, want %q", got, "audit")
	}
}

func TestAuditTool_Description(t *testing.T) {
	tool := NewAuditTool()
	desc := tool.Description()
	if desc == "" {
		t.Error("Description() returned empty string")
	}
	// Verify content is meaningful
	if !strings.Contains(desc, "Security") && !strings.Contains(desc, "security") {
		t.Errorf("Description() = %q, want to contain 'Security'", desc)
	}
}

func TestAuditTool_Spec(t *testing.T) {
	tool := NewAuditTool()
	spec := tool.Spec()
	if spec.Name != "audit" {
		t.Errorf("Spec().Name = %q, want %q", spec.Name, "audit")
	}
	if spec.Title == "" {
		t.Error("Spec().Title is empty")
	}
	if spec.Summary == "" {
		t.Error("Spec().Summary is empty")
	}
	if spec.Risk != RiskRead {
		t.Errorf("Spec().Risk = %v, want %v", spec.Risk, RiskRead)
	}
	if len(spec.Args) == 0 {
		t.Error("Spec().Args is empty")
	}
	// Check that expected args are present
	argNames := make(map[string]bool)
	for _, arg := range spec.Args {
		argNames[arg.Name] = true
	}
	for _, name := range []string{"path", "severity", "categories", "confidence"} {
		if !argNames[name] {
			t.Errorf("Spec().Args missing %q", name)
		}
	}
	// Check tags
	if len(spec.Tags) == 0 {
		t.Error("Spec().Tags is empty")
	}
	found := false
	for _, tag := range spec.Tags {
		if tag == "security" || tag == "audit" || tag == "vulnerability" {
			found = true
		}
	}
	if !found {
		t.Errorf("Spec().Tags missing security-related tags: %v", spec.Tags)
	}
}

func TestAuditTool_SetEngine(t *testing.T) {
	tool := NewAuditTool()
	// SetEngine should not panic with nil
	tool.SetEngine(nil)
}

func TestAuditSeverity_Constants(t *testing.T) {
	// Verify severity constants are distinct and non-empty
	severities := []AuditSeverity{AuditCritical, AuditHigh, AuditMedium, AuditLow, AuditInfo}
	for _, sev := range severities {
		if sev == "" {
			t.Error("AuditSeverity constant is empty string")
		}
	}
	// Verify they are all different
	seen := make(map[string]bool)
	for _, sev := range severities {
		s := string(sev)
		if seen[s] {
			t.Errorf("Duplicate AuditSeverity value: %s", s)
		}
		seen[s] = true
	}
	// Verify ordering expectations via compareAuditSeverity
	if compareAuditSeverity(AuditCritical, AuditHigh) <= 0 {
		t.Error("AuditCritical should rank higher than AuditHigh")
	}
	if compareAuditSeverity(AuditHigh, AuditMedium) <= 0 {
		t.Error("AuditHigh should rank higher than AuditMedium")
	}
	if compareAuditSeverity(AuditMedium, AuditLow) <= 0 {
		t.Error("AuditMedium should rank higher than AuditLow")
	}
	if compareAuditSeverity(AuditLow, AuditInfo) <= 0 {
		t.Error("AuditLow should rank higher than AuditInfo")
	}
	if compareAuditSeverity(AuditCritical, AuditInfo) <= 0 {
		t.Error("AuditCritical should rank higher than AuditInfo")
	}
}

func TestAuditTool_Execute_NonExistentProjectRoot(t *testing.T) {
	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: "/__nonexistent_root_path__",
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute on nonexistent ProjectRoot returned error: %v", err)
	}
	if !strings.Contains(res.Output, "No Go source files found") {
		t.Errorf("expected 'No Go source files found', got: %s", res.Output)
	}
	if res.DurationMs < 0 {
		t.Errorf("DurationMs = %d, want non-negative", res.DurationMs)
	}
}

func TestAuditTool_Execute_SkipsTestFiles(t *testing.T) {
	dir := t.TempDir()
	// Write a _test.go file with a secret — should be skipped
	path := filepath.Join(dir, "secret_test.go")
	content := `package p_test
var s = "password=supersecretvalue123"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "No Go source files found") {
		t.Errorf("_test.go files should be excluded, got: %s", res.Output)
	}
}

func TestAuditTool_Execute_SkipsVendorAndOtherDirs(t *testing.T) {
	dir := t.TempDir()
	// Create vendor dir with a "secret"
	vendorDir := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(vendorDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(vendorDir, "secret.go")
	if err := os.WriteFile(path, []byte(`package vendor
var s = "password=supersecretvalue123"
`), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if strings.Contains(res.Output, "hardcoded-secret") {
		t.Errorf("vendor directory should be skipped, got: %s", res.Output)
	}
}

func TestAuditTool_Execute_SeverityFilterInfo(t *testing.T) {
	dir := t.TempDir()
	// Write a file with a critical finding
	path := filepath.Join(dir, "secret.go")
	if err := os.WriteFile(path, []byte(`package p
var s = "password=supersecretvalue123"
`), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewAuditTool()
	// Filter to info level — should show the finding
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"severity": "info"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "hardcoded-secret") {
		t.Errorf("severity=info should show findings, got: %s", res.Output)
	}
}

func TestAuditTool_Execute_CategoryFiltering_SecretsOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.go")
	// Has hardcoded password AND sql injection pattern
	if err := os.WriteFile(path, []byte(`package p
import "fmt"
var s = "password=supersecretvalue123"
var q = fmt.Sprintf("SELECT * FROM users WHERE id=%s", id)
`), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"categories": "secrets"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	// Should find the hardcoded secret
	if !strings.Contains(res.Output, "hardcoded-secret") {
		t.Errorf("secrets category should find hardcoded-secret, got: %s", res.Output)
	}
	// Should NOT find sql-injection (different category)
	if strings.Contains(res.Output, "sql-injection") {
		t.Errorf("secrets category should not find sql-injection, got: %s", res.Output)
	}
}

func TestAuditTool_Execute_CategoryFiltering_InvalidCategory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.go")
	if err := os.WriteFile(path, []byte(`package p
func Clean() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{"categories": "nonexistent-category"},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(res.Output, "No security issues detected") {
		t.Errorf("invalid category should produce no findings, got: %s", res.Output)
	}
}

func TestAuditTool_Execute_DataField_HasFindings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.go")
	if err := os.WriteFile(path, []byte(`package p
var s = "password=supersecretvalue123"
`), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.Data == nil {
		t.Fatal("Data field should not be nil when issues found")
	}
	findings, ok := res.Data["findings"].([]AuditFinding)
	if !ok {
		t.Fatal("Data.findings should be []AuditFinding")
	}
	if len(findings) == 0 {
		t.Error("expected at least one finding")
	}
	if findings[0].File == "" {
		t.Error("finding File should not be empty")
	}
	if findings[0].Line == 0 {
		t.Error("finding Line should not be zero")
	}
	if findings[0].CWE == "" {
		t.Error("finding CWE should not be empty")
	}
	if findings[0].Message == "" {
		t.Error("finding Message should not be empty")
	}
}

func TestAuditTool_Execute_DataField_FilesCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.go")
	if err := os.WriteFile(path, []byte(`package p
func Clean() {}
`), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if res.Data == nil {
		t.Fatal("Data field should not be nil")
	}
	files, ok := res.Data["files"].(int)
	if !ok {
		t.Fatal("Data.files should be int")
	}
	if files != 1 {
		t.Errorf("Data.files = %d, want 1", files)
	}
}

func TestAuditTool_Execute_SummaryInOutput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.go")
	if err := os.WriteFile(path, []byte(`package p
var s = "password=supersecretvalue123"
`), 0644); err != nil {
		t.Fatal(err)
	}

	tool := NewAuditTool()
	res, err := tool.Execute(context.Background(), Request{
		ProjectRoot: dir,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	// Output should contain summary counts
	if !strings.Contains(res.Output, "Files scanned:") {
		t.Errorf("expected 'Files scanned:' in output, got: %s", res.Output)
	}
	if !strings.Contains(res.Output, "Issues found:") {
		t.Errorf("expected 'Issues found:' in output, got: %s", res.Output)
	}
}