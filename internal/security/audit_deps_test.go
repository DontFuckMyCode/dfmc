package security

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestScanGoSum(t *testing.T) {
	sc := New()
	f, err := os.CreateTemp(t.TempDir(), "go.sum")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// Normal module version
	f.WriteString("github.com/foo/bar v1.2.3 h1:abc123\n")
	// Pseudo-version (should show as "unknown")
	f.WriteString("github.com/baz/qux v0.0.0-20240101-abc1234 h1:def456\n")
	f.Close()

	findings, err := sc.scanGoSum(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 pseudo-version finding, got %d", len(findings))
	}
	if findings[0].Kind != "unknown" {
		t.Errorf("expected kind=unknown, got %q", findings[0].Kind)
	}
	if findings[0].Pkg != "github.com/baz/qux" {
		t.Errorf("expected github.com/baz/qux, got %q", findings[0].Pkg)
	}
}

func TestScanNPMLock(t *testing.T) {
	sc := New()
	tmp := t.TempDir()
	lock := filepath.Join(tmp, "package-lock.json")
	f, err := os.Create(lock)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{
  "dependencies": {
    "lodash": { "version": "4.17.21", "resolved": "https://registry.npmjs.org/lodash/-/lodash-4.17.21.tgz" },
    "debug": { "version": "4.3.4", "dev": true }
  }
}`)
	f.Close()

	findings, err := sc.scanNPMLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
	// check dev dep flag
	for _, f := range findings {
		if f.Pkg == "debug" && !f.DevOnly {
			t.Error("debug should be marked dev_only")
		}
	}
}

func TestScanRequirementsTxt(t *testing.T) {
	sc := New()
	tmp := t.TempDir()
	req := filepath.Join(tmp, "requirements.txt")
	f, err := os.Create(req)
	if err != nil {
		t.Fatal(err)
	}
	// Use fmt to get actual newlines.
	fmt.Fprintf(f, "requests==2.31.0\nnumpy>=1.24.0\n-e git+https://github.com/user/repo.git@abc1234567890#egg=mylib\n")
	fmt.Fprintf(f, "# this is a comment\n")
	fmt.Fprintf(f, "flask~=3.0.0\n")
	f.Close()

	findings, err := sc.scanRequirementsTxt(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 4 {
		t.Fatalf("expected 4 findings, got %d: %+v", len(findings), findings)
	}
	// Check git finding
	for _, f := range findings {
		if f.Pkg == "git+https" && f.Kind != "git" {
			t.Errorf("git dep should have kind=git, got %q", f.Kind)
		}
	}
}

func TestScanCargoLock(t *testing.T) {
	sc := New()
	tmp := t.TempDir()
	lock := filepath.Join(tmp, "Cargo.lock")
	f, err := os.Create(lock)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`[[package]]
name = "serde"
version = "1.0.0"

[[package]]
name = "debug"
version = "0.8.0"
`)
	f.Close()

	findings, err := sc.scanCargoLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(findings))
	}
}

func TestScanDependencyFilesRoot(t *testing.T) {
	sc := New()
	tmp := t.TempDir()
	// Write a go.sum
	f, _ := os.Create(filepath.Join(tmp, "go.sum"))
	f.WriteString("github.com/foo/bar v1.0.0 h1:abc\n")
	f.Close()
	// Write a package-lock.json
	f2, _ := os.Create(filepath.Join(tmp, "package-lock.json"))
	f2.WriteString(`{"dependencies": {"lodash": {"version": "4.17.21"}}}`)
	f2.Close()

	findings, err := sc.ScanDependencyFiles(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) == 0 {
		t.Error("expected findings from root-level lock files")
	}
}
