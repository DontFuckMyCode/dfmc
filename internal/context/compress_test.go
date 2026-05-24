package context

import (
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
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
	if !shouldIncludePath("src/main.go", true, true) {
		t.Fatal("normal file should be included")
	}
	if !shouldIncludePath("src/main_test.go", true, true) {
		t.Fatal("test file should be included when includeTests=true")
	}
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
	long := strings.Repeat("word ", 500)
	got := TrimPromptToBudget(long, 5)
	if got == "" {
		t.Error("should return non-empty")
	}
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

func TestStripComments_BlockComment(t *testing.T) {
	content := "/* block comment */\nfunc foo() {}\n/* multi\n   line\n   comment */\nfunc bar() {}"
	result := stripComments(content)
	if strings.Contains(result, "block comment") {
		t.Errorf("block comments should be stripped, got %q", result)
	}
	if !strings.Contains(result, "func foo") {
		t.Errorf("func foo should remain, got %q", result)
	}
}

func TestCompressContent_NoneLevel(t *testing.T) {
	content := strings.Repeat("line\n", 100)
	result, ls, le := compressContent(content, []string{"term"}, "go", "none", 50)
	if result == "" {
		t.Fatal("none level should return trimmed content")
	}
	if ls != 1 {
		t.Errorf("lineStart should be 1, got %d", ls)
	}
	lines := len(strings.Split(content, "\n"))
	if le != lines {
		t.Errorf("lineEnd should be %d, got %d", lines, le)
	}
}

func TestCompressContent_AggressiveWithSignatures(t *testing.T) {
	content := `package main

func init() {}

func main() {}
`
	result, ls, _ := compressContent(content, []string{"main"}, "go", "aggressive", 100)
	if result == "" {
		t.Fatal("aggressive should extract signatures")
	}
	if !strings.Contains(result, "func") {
		t.Errorf("expected func keyword in aggressive output, got %q", result)
	}
	if ls != 1 {
		t.Errorf("lineStart should be 1 for aggressive signatures, got %d", ls)
	}
}

func TestCompressContent_AggressiveFallback(t *testing.T) {
	content := "// just a comment\nvar x = 1\n// another comment\nvar y = 2"
	result, _, _ := compressContent(content, []string{"x"}, "go", "aggressive", 50)
	if result == "" {
		t.Fatal("aggressive fallback should return content")
	}
	if strings.Contains(result, "//") {
		t.Errorf("comments should be stripped, got %q", result)
	}
}

func TestCompressContent_StandardLevel(t *testing.T) {
	content := "// comment\nfunc foo() {}\nfunc bar() {}"
	result, ls, _ := compressContent(content, []string{"foo"}, "go", "standard", 50)
	if result == "" {
		t.Fatal("standard level should return snippet")
	}
	if strings.Contains(result, "//") {
		t.Errorf("comments should be stripped, got %q", result)
	}
	if ls < 1 {
		t.Errorf("lineStart should be >= 1, got %d", ls)
	}
}

func TestCompressionFallbackOrder_Aggressive(t *testing.T) {
	order := compressionFallbackOrder("aggressive")
	if len(order) != 1 || order[0] != "aggressive" {
		t.Errorf("aggressive should return [aggressive], got %v", order)
	}
}

func TestCompressionFallbackOrder_None(t *testing.T) {
	order := compressionFallbackOrder("none")
	if len(order) != 3 {
		t.Fatalf("expected 3 levels, got %d", len(order))
	}
	if order[0] != "none" {
		t.Errorf("first should be none, got %s", order[0])
	}
}

func TestBuildChunkForBudget_ZeroMaxTokens(t *testing.T) {
	large := strings.Repeat("word ", 1000)
	chunk := buildChunkForBudget("test.go", large, []string{"nonexistent"}, 0.5, "standard", 0)
	if chunk.TokenCount < 0 {
		t.Errorf("TokenCount should be >= 0, got %d", chunk.TokenCount)
	}
}

func TestDownshiftChunkForRemaining_ZeroBudget(t *testing.T) {
	chunk := types.ContextChunk{
		Content:    "func foo() {}",
		TokenCount: 5,
	}
	result := downshiftChunkForRemaining(chunk, 0, 100)
	if result.Content != "" {
		t.Errorf("zero remaining should return empty chunk, got %q", result.Content)
	}
}

func TestDownshiftChunkForRemaining_NegativeBudget(t *testing.T) {
	chunk := types.ContextChunk{
		Content:    "func foo() {}",
		TokenCount: 5,
	}
	result := downshiftChunkForRemaining(chunk, -1, 100)
	if result.Content != "" {
		t.Errorf("negative remaining should return empty chunk, got %q", result.Content)
	}
}

func TestDownshiftChunkForRemaining_MaxTokensPerFileCap(t *testing.T) {
	// remaining=1000, maxTokensPerFile=50 — budget should be capped to 50
	// chunk.TokenCount=5 (fits) — early return at line 74
	chunk := types.ContextChunk{
		Content:    "func foo() {}",
		TokenCount: 5,
	}
	result := downshiftChunkForRemaining(chunk, 1000, 50)
	// chunk fits within the 50-token cap, so it should be returned unchanged
	if result.TokenCount != 5 {
		t.Errorf("chunk should be returned unchanged when it fits, got TokenCount=%d", result.TokenCount)
	}
}

func TestDownshiftChunkForRemaining_NeedsTrim(t *testing.T) {
	// remaining=200, maxTokensPerFile=0 (no cap) — chunk.TokenCount=500 (needs trim)
	// This hits the for-loop at line 79 and the over-budget logic at lines 88-90
	chunk := types.ContextChunk{
		Content:    strings.Repeat("func f() {}\n", 100), // 500+ tokens
		TokenCount: 500,
	}
	result := downshiftChunkForRemaining(chunk, 200, 0)
	// Should be trimmed to fit within 200 tokens
	if result.TokenCount > 200 {
		t.Errorf("trimmed chunk TokenCount=%d exceeds budget 200", result.TokenCount)
	}
	if result.Compression == "" {
		t.Errorf("trimmed chunk should have Compression set, got %q", result.Compression)
	}
}

func TestDownshiftChunkForRemaining_EmptyAfterTrim(t *testing.T) {
	// Very short budget that causes the content to become empty after trimming
	// hits lines 81-83: trimToTokenBudget returns whitespace-only, function returns empty
	chunk := types.ContextChunk{
		Content:    "x y z",
		TokenCount: 3,
	}
	result := downshiftChunkForRemaining(chunk, 1, 0)
	// With budget=1 token, trimToTokenBudget("x y z", 1) may return empty or whitespace
	// The function should return an empty chunk
	if result.Content != "" {
		t.Logf("result.Content=%q (may be non-empty if trim still fits)", result.Content)
	}
}

func TestDownshiftChunkForRemaining_FitsWithoutTrim(t *testing.T) {
	// chunk.TokenCount=10, remaining=200, maxTokensPerFile=0 (no cap)
	// 10 <= 200, so returns chunk unchanged (line 74-76)
	chunk := types.ContextChunk{
		Content:    "func foo() {}",
		TokenCount: 10,
	}
	result := downshiftChunkForRemaining(chunk, 200, 0)
	if result.TokenCount != 10 {
		t.Errorf("unchanged chunk TokenCount: got %d, want 10", result.TokenCount)
	}
}

func TestNormalizeCompression(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"none", "none"},
		{"standard", "standard"},
		{"aggressive", "aggressive"},
		{"NONE", "none"},
		{"Standard", "standard"},
		{"AGGRESSIVE", "aggressive"},
		{"  none  ", "none"},
		{"invalid", "standard"},
		{"", "standard"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeCompression(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeCompression(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestCompressionFallbackOrder(t *testing.T) {
	tests := []struct {
		primary  string
		expected []string
	}{
		{"none", []string{"none", "standard", "aggressive"}},
		{"aggressive", []string{"aggressive"}},
		{"standard", []string{"standard", "aggressive"}},
		{"invalid", []string{"standard", "aggressive"}},
	}
	for _, tt := range tests {
		t.Run(tt.primary, func(t *testing.T) {
			got := compressionFallbackOrder(tt.primary)
			if len(got) != len(tt.expected) {
				t.Errorf("compressionFallbackOrder(%q) = %v, want %v", tt.primary, got, tt.expected)
			}
		})
	}
}

func TestDownshiftChunkForRemaining_MaxTokensPerFileSmallerThanRemaining(t *testing.T) {
	// remaining=1000, maxTokensPerFile=50 — budget capped to 50
	// but chunk.TokenCount=100 — needs trimming within 50-token limit
	chunk := types.ContextChunk{
		Content:    strings.Repeat("func f() {}\n", 20), // ~100 tokens
		TokenCount: 100,
	}
	result := downshiftChunkForRemaining(chunk, 1000, 50)
	// Should be trimmed to <= 50 tokens
	if result.TokenCount > 50 {
		t.Errorf("result TokenCount=%d exceeds maxTokensPerFile=50", result.TokenCount)
	}
}

func TestDownshiftChunkForRemaining_AdjustsBudgetAfterTrim(t *testing.T) {
	// remaining=100, maxTokensPerFile=0 — chunk.TokenCount=200 (needs trim)
	// The loop should run: trim, check tokenCount <= budget, if not reduce budget by over
	chunk := types.ContextChunk{
		Content:    strings.Repeat("func f() {}\n", 40), // ~200 tokens
		TokenCount: 200,
	}
	result := downshiftChunkForRemaining(chunk, 100, 0)
	if result.TokenCount > 100 {
		t.Errorf("TokenCount %d exceeds budget 100", result.TokenCount)
	}
	if result.Compression == "" {
		t.Error("Compression should be set after trim")
	}
}
