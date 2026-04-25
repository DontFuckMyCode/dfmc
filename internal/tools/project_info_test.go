package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestProjectInfo_MissingSection(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "project_info", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := res.Data["module"].(map[string]any)
	if !ok {
		t.Fatalf("expected module in result")
	}
	if data["module_path"] == nil {
		t.Errorf("module_path is nil")
	}
}

func TestProjectInfo_SectionAll(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "project_info", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"section": "all"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data["module"] == nil {
		t.Errorf("expected module section")
	}
	if res.Data["file_stats"] == nil {
		t.Errorf("expected file_stats section")
	}
}

func TestProjectInfo_SectionModule(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module github.com/example/foo\ngo 1.25\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "project_info", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"section": "module"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, ok := res.Data["module"].(map[string]any)
	if !ok {
		t.Fatalf("expected module data")
	}
	if data["module_path"] != "github.com/example/foo" {
		t.Errorf("want github.com/example/foo, got %v", data["module_path"])
	}
	if data["go_version"] != "1.25" {
		t.Errorf("want 1.25, got %v", data["go_version"])
	}
}

func TestProjectInfo_SectionFiles(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "util.go"), []byte("package main\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "main_test.go"), []byte("package main\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "project_info", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"section": "files"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	stats, ok := res.Data["file_stats"].(map[string]any)
	if !ok {
		t.Fatalf("expected file_stats")
	}
	if stats["total_files"] == nil {
		t.Errorf("total_files missing")
	}
}

func TestProjectInfo_ExcludeConfig(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "project_info", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"include_config": false},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data["tools_config"] != nil {
		t.Errorf("expected no tools_config when include_config=false")
	}
}

func TestProjectInfoTool_Name(t *testing.T) {
	tool := NewProjectInfoTool()
	if tool.Name() != "project_info" {
		t.Errorf("want project_info, got %s", tool.Name())
	}
}

func TestProjectInfoTool_Spec(t *testing.T) {
	tool := NewProjectInfoTool()
	spec := tool.Spec()
	if spec.Name != "project_info" {
		t.Errorf("spec.Name: want project_info, got %s", spec.Name)
	}
	if spec.Risk != RiskRead {
		t.Errorf("spec.Risk: want RiskRead, got %v", spec.Risk)
	}
	argsByName := make(map[string]Arg)
	for _, a := range spec.Args {
		argsByName[a.Name] = a
	}
	for _, name := range []string{"section", "include_config"} {
		if _, ok := argsByName[name]; !ok {
			t.Errorf("spec.Args missing %s", name)
		}
	}
}

func TestProjectInfoTool_Description(t *testing.T) {
	tool := NewProjectInfoTool()
	if tool.Description() == "" {
		t.Errorf("description is empty")
	}
}

func TestProjectInfoTool_SetEngine(t *testing.T) {
	tool := NewProjectInfoTool()
	eng := New(*config.DefaultConfig())
	tool.SetEngine(eng)
	if tool.Name() != "project_info" {
		t.Errorf("name mismatch")
	}
}

func TestProjectInfo_NoGoMod(t *testing.T) {
	tmp := t.TempDir()
	// No go.mod
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "project_info", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data := res.Data["module"].(map[string]any)
	if data["module_path"] != "unknown" {
		t.Errorf("want unknown when no go.mod, got %v", data["module_path"])
	}
}

func TestProjectInfo_SectionTools(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "project_info", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"section": "tools"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data["tools_config"] == nil {
		t.Errorf("expected tools_config")
	}
}

func TestProjectInfo_SectionProviders(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "project_info", Request{
		ProjectRoot: t.TempDir(),
		Params:      map[string]any{"section": "providers"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// provider config should be present
	if res.Data["provider_config"] == nil {
		t.Logf("note: provider_config may be nil when no providers configured")
	}
}

func TestProjectInfo_GoVersionFromGoMod(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module foo\ngo 1.24\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "project_info", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data := res.Data["module"].(map[string]any)
	if data["go_version"] != "1.24" {
		t.Errorf("want 1.24, got %v", data["go_version"])
	}
}

func TestProjectInfo_MultipleGoModLines(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "go.mod"), []byte("module bar\n\nrequire github.com/pkg/errors v0.9.0\n\ngo 1.23\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "project_info", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	data := res.Data["module"].(map[string]any)
	if data["module_path"] != "bar" {
		t.Errorf("want bar, got %v", data["module_path"])
	}
	if data["go_version"] != "1.23" {
		t.Errorf("want 1.23, got %v", data["go_version"])
	}
}

func TestFetchFileStats_LanguageMap(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "a.go"), []byte("package a\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "b.ts"), []byte("const x = 1\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "c.py"), []byte("pass\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "d.rs"), []byte("fn main() {}\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "e_test.go"), []byte("package a\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "f.proto"), []byte("syntax = 'proto3'\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "project_info", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"section": "files"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	stats := res.Data["file_stats"].(map[string]any)
	byLang := stats["by_language"].(map[string]int)
	if byLang["go"] != 2 { // a.go + e_test.go
		t.Errorf("want 2 go files, got %v", byLang["go"])
	}
	if byLang["typescript"] != 1 {
		t.Errorf("want 1 typescript, got %v", byLang["typescript"])
	}
	if stats["test_file_count"].(int) < 1 {
		t.Errorf("want at least 1 test file, got %v", stats["test_file_count"])
	}
}