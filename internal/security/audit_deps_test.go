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
	// Version must carry the operator AND the actual version, not just the
	// bare operator (regression guard: it used to record "==" / ">=").
	byPkg := map[string]DependencyFinding{}
	for _, f := range findings {
		byPkg[f.Pkg] = f
	}
	wantVersion := map[string]string{
		"requests": "==2.31.0",
		"numpy":    ">=1.24.0",
		"flask":    "~=3.0.0",
	}
	for pkg, want := range wantVersion {
		f, ok := byPkg[pkg]
		if !ok {
			t.Errorf("missing finding for %q", pkg)
			continue
		}
		if f.Version != want {
			t.Errorf("%s Version = %q, want %q", pkg, f.Version, want)
		}
		if f.Kind != "versioned" {
			t.Errorf("%s Kind = %q, want versioned", pkg, f.Kind)
		}
	}
	if git, ok := byPkg["git+https"]; !ok || git.Kind != "git" {
		t.Errorf("git dep should have kind=git, got %+v", git)
	} else if git.Version != "abc1234" {
		t.Errorf("git dep Version = %q, want the 7-char short hash abc1234", git.Version)
	}
}

// TestScanDependencyFiles_Subdir covers scanSubdir: lock files in an
// immediate subdirectory of the root are scanned, while node_modules is
// skipped.
func TestScanDependencyFiles_Subdir(t *testing.T) {
	sc := New()
	root := t.TempDir()

	// A go.sum with a pseudo-version under a subdir -> one "unknown" finding.
	sub := filepath.Join(root, "service")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	goSum := "example.com/x v0.0.0-20240101000000-abcdef123456 h1:abc=\n"
	if err := os.WriteFile(filepath.Join(sub, "go.sum"), []byte(goSum), 0o644); err != nil {
		t.Fatal(err)
	}

	// A lock file under node_modules must be ignored.
	nm := filepath.Join(root, "node_modules", "pkg")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "go.sum"),
		[]byte("evil.com/y v0.0.0-20240101000000-deadbeef0000 h1:y=\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	findings, err := sc.ScanDependencyFiles(root)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var sawSub, sawNodeModules bool
	for _, f := range findings {
		if f.Pkg == "example.com/x" {
			sawSub = true
		}
		if f.Pkg == "evil.com/y" {
			sawNodeModules = true
		}
	}
	if !sawSub {
		t.Errorf("expected the subdir go.sum pseudo-version to be found; got %+v", findings)
	}
	if sawNodeModules {
		t.Errorf("node_modules lock files must be skipped, but a finding leaked: %+v", findings)
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
