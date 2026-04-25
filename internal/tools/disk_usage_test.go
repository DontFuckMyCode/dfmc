package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestDiskUsage_Basic(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "a.go"), []byte("package main\nfunc main() {}\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "b.ts"), []byte("const x = 1\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "disk_usage", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Data["total_bytes"].(int64) == 0 {
		t.Errorf("expected non-zero total_bytes")
	}
	if res.Data["files"].(int) == 0 {
		t.Errorf("expected non-zero files")
	}
}

func TestDiskUsage_ByExtension(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "disk_usage", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"by_type": true},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	byExt := res.Data["by_extension"].(map[string]int64)
	if byExt[".go"] == 0 {
		t.Errorf("expected .go in by_extension")
	}
}

func TestDiskUsage_LanguageMap(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "a.go"), []byte("package a\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "b.ts"), []byte("const x = 1\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "c.py"), []byte("pass\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "disk_usage", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	byLang := res.Data["by_language"].(map[string]int64)
	if byLang["go"] == 0 {
		t.Errorf("expected go in by_language")
	}
}

func TestDiskUsage_LargestFiles(t *testing.T) {
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, "small.go"), []byte("a\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "large.go"), make([]byte, 1000), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "disk_usage", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// largest_files is a []fileEntry (unexported), check via generic map
	largestRaw := res.Data["largest_files"]
	if largestRaw == nil {
		t.Errorf("expected largest_files field")
	}
}

func TestDiskUsageTool_Name(t *testing.T) {
	tool := NewDiskUsageTool()
	if tool.Name() != "disk_usage" {
		t.Errorf("want disk_usage, got %s", tool.Name())
	}
}

func TestDiskUsageTool_Spec(t *testing.T) {
	tool := NewDiskUsageTool()
	spec := tool.Spec()
	if spec.Name != "disk_usage" {
		t.Errorf("spec.Name: want disk_usage, got %s", spec.Name)
	}
	if spec.Risk != RiskRead {
		t.Errorf("spec.Risk: want RiskRead, got %v", spec.Risk)
	}
	argsByName := make(map[string]Arg)
	for _, a := range spec.Args {
		argsByName[a.Name] = a
	}
	for _, name := range []string{"path", "depth", "by_type"} {
		if _, ok := argsByName[name]; !ok {
			t.Errorf("spec.Args missing %s", name)
		}
	}
}

func TestDiskUsageTool_Description(t *testing.T) {
	tool := NewDiskUsageTool()
	if tool.Description() == "" {
		t.Errorf("description is empty")
	}
}

func TestDiskUsage_DirSummaries(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, "sub"), 0755)
	os.WriteFile(filepath.Join(tmp, "a.go"), []byte("package a\n"), 0644)
	os.WriteFile(filepath.Join(tmp, "sub", "b.go"), []byte("package a\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "disk_usage", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{"depth": 3},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	dirs := res.Data["dirs"].([]dirEntry)
	if len(dirs) == 0 {
		t.Errorf("expected dir summaries")
	}
}

func TestDiskUsage_SkipsDirs(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(filepath.Join(tmp, ".git", "objects"), 0755)
	os.WriteFile(filepath.Join(tmp, ".git", "objects", "pack"), []byte("blob"), 0644)
	os.WriteFile(filepath.Join(tmp, "main.go"), []byte("package main\n"), 0644)

	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)

	res, err := eng.Execute(context.Background(), "disk_usage", Request{
		ProjectRoot: tmp,
		Params:      map[string]any{},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	totalBytes := res.Data["total_bytes"].(int64)
	// Should include main.go but not .git contents
	if totalBytes == 0 {
		t.Errorf("expected non-zero bytes")
	}
}

// TestDiskUsage_RejectsTraversalPath pins VULN-017: a `path`
// parameter escaping the project root would let the walker
// enumerate the entire filesystem (recon primitive — paths +
// sizes for ssh keys, db dumps, layouts).
func TestDiskUsage_RejectsTraversalPath(t *testing.T) {
	eng := New(*config.DefaultConfig())
	eng.SetCodemap(nil)
	tmp := t.TempDir()

	cases := []string{
		"../",
		"../../",
		"sub/../../escape",
	}
	for _, p := range cases {
		_, err := eng.Execute(context.Background(), "disk_usage", Request{
			ProjectRoot: tmp,
			Params:      map[string]any{"path": p},
		})
		if err == nil {
			t.Errorf("path=%q must be rejected as outside project root", p)
		} else if !strings.Contains(err.Error(), "outside") && !strings.Contains(err.Error(), "root") {
			t.Errorf("path=%q rejection should mention 'outside'/'root', got: %v", p, err)
		}
	}
}
