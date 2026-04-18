package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanContentFindings(t *testing.T) {
	src := []byte(`
package main
const apiKey = "sk-abcdefghijklmnopqrstuvwxyz1234567890"
func run(input string) {
  q := "SELECT * FROM users WHERE id=" + input
  eval(input)
}
`)
	s := New()
	secrets, vulns := s.ScanContent("sample.go", src)
	if len(secrets) == 0 {
		t.Fatal("expected secret findings")
	}
	if len(vulns) == 0 {
		t.Fatal("expected vulnerability findings")
	}
}

func TestScanContent_GenericAPIKeyLowEntropySuppressed(t *testing.T) {
	src := []byte(`package main
const apiKey = "aaaaaaaaaaaaaaaaaaaaaa"
`)
	s := New()
	secrets, _ := s.ScanContent("sample.go", src)
	if len(secrets) != 0 {
		t.Fatalf("expected low-entropy generic api key example to be suppressed, got %+v", secrets)
	}
}

func TestScanPaths_SkipsTestdataFixtures(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "testdata", "fixture.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`package main
const apiKey = "sk-abcdefghijklmnopqrstuvwxyz1234567890"
`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	s := New()
	report, err := s.ScanPaths([]string{path})
	if err != nil {
		t.Fatalf("ScanPaths: %v", err)
	}
	if report.FilesScanned != 0 {
		t.Fatalf("expected testdata fixture to be skipped, got FilesScanned=%d", report.FilesScanned)
	}
	if len(report.Secrets) != 0 || len(report.Vulnerabilities) != 0 {
		t.Fatalf("expected skipped fixture to produce no findings, got %+v", report)
	}
}
