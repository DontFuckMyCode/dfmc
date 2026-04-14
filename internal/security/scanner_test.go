package security

import "testing"

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
