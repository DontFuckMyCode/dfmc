package context

import (
	"strings"
	"testing"
)

func TestExtractSignatures_ZeroMaxLines(t *testing.T) {
	content := "func foo() {}\nfunc bar() {}"
	got := extractSignatures(content, "go", 0)
	if got == "" {
		t.Fatal("extractSignatures with 0 maxLines should use default")
	}
}

func TestExtractSignatures_ExtractsFuncs(t *testing.T) {
	content := `package main

func main() {}

type Server struct {}

func (s *Server) Start() {}

func init() {}
`
	got := extractSignatures(content, "go", 100)
	if got == "" {
		t.Fatal("extractSignatures returned empty")
	}
}

func TestExtractSignatures_ExtractsImports(t *testing.T) {
	content := `package main

import "fmt"
import "os"

func main() {}
`
	got := extractSignatures(content, "go", 100)
	if got == "" {
		t.Fatal("extractSignatures returned empty")
	}
}

func TestExtractSignatures_Limit(t *testing.T) {
	content := `func a() {}
func b() {}
func c() {}
func d() {}
func e() {}
`
	got := extractSignatures(content, "go", 3)
	lines := len(splitLines(got))
	if lines > 3 {
		t.Fatalf("extractSignatures limited to 3 lines but got %d", lines)
	}
}

func TestShouldIncludePath_Empty(t *testing.T) {
	if shouldIncludePath("", true, true) {
		t.Fatal("empty path should return false")
	}
}

func TestShouldIncludePath_TestFiles(t *testing.T) {
	// Normal file should always be included
	if !shouldIncludePath("src/main.go", true, true) {
		t.Fatal("normal file should be included")
	}
	// Test file with includeTests=true should be included
	if !shouldIncludePath("src/main_test.go", true, true) {
		t.Fatal("test file should be included when includeTests=true")
	}
	// Test file with includeTests=false should be excluded
	if shouldIncludePath("src/main_test.go", false, true) {
		t.Fatal("test file should be excluded when includeTests=false")
	}
}

func TestShouldIncludePath_DocFiles(t *testing.T) {
	if !shouldIncludePath("README.md", true, true) {
		t.Fatal("md file should be included when includeDocs=true")
	}
	if shouldIncludePath("README.md", true, false) {
		t.Fatal("md file should be excluded when includeDocs=false")
	}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	out := []string{}
	for _, line := range splitString(s, "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func splitString(s, sep string) []string {
	out := []string{}
	start := 0
	for {
		idx := indexString(s, sep, start)
		if idx < 0 {
			out = append(out, s[start:])
			break
		}
		out = append(out, s[start:idx])
		start = idx + len(sep)
	}
	return out
}

func indexString(s, sep string, start int) int {
	for i := start; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}

func TestTrimPromptToBudget_Small(t *testing.T) {
	got := TrimPromptToBudget("hello world", 100)
	if got == "" {
		t.Error("small input should return unchanged")
	}
}

func TestTrimPromptToBudget_Large(t *testing.T) {
	// Build a long input that will exceed any small budget
	long := strings.Repeat("word ", 500)
	got := TrimPromptToBudget(long, 5)
	if got == "" {
		t.Error("should return non-empty")
	}
	// With a budget of 5 tokens, the result should be significantly shorter
	if len(got) >= len(long) {
		t.Errorf("expected trimmed result shorter than input: got %d vs %d", len(got), len(long))
	}
}

func TestTrimPromptToBudget_ZeroMax(t *testing.T) {
	got := TrimPromptToBudget("some text", 0)
	if got != "" {
		t.Errorf("zero max should return empty, got %q", got)
	}
}
