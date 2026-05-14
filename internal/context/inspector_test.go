package context

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

func TestNewInspector(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "/proj/main.go", Content: "package main\nfunc main() {}", TokenCount: 10, Language: "go", LineStart: 1, LineEnd: 2},
		{Path: "/proj/utils.go", Content: "func helper() {}", TokenCount: 5, Language: "go", LineStart: 1, LineEnd: 1},
	}
	ci := NewInspector("/proj", chunks)
	if ci.projectRoot != "/proj" {
		t.Errorf("expected projectRoot /proj, got %s", ci.projectRoot)
	}
	if len(ci.chunks) != 2 {
		t.Errorf("expected 2 chunks, got %d", len(ci.chunks))
	}
	if ci.maxTokens != 16000 {
		t.Errorf("expected default maxTokens 16000, got %d", ci.maxTokens)
	}
}

func TestNewInspectorWithBudget(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "/proj/main.go", Content: "package main", TokenCount: 3, Language: "go", LineStart: 1, LineEnd: 1},
	}
	ci := NewInspectorWithBudget("/proj", chunks, 8000)
	if ci.maxTokens != 8000 {
		t.Errorf("expected maxTokens 8000, got %d", ci.maxTokens)
	}
}

func TestInspectEmptyChunks(t *testing.T) {
	result := Inspect("/proj", nil, 16000)
	if result.TotalFiles != 0 {
		t.Errorf("expected 0 files, got %d", result.TotalFiles)
	}
	if result.TotalTokens != 0 {
		t.Errorf("expected 0 tokens, got %d", result.TotalTokens)
	}
	if result.TotalLines != 0 {
		t.Errorf("expected 0 lines, got %d", result.TotalLines)
	}
	if result.Budget.Total != 16000 {
		t.Errorf("expected Budget.Total 16000, got %d", result.Budget.Total)
	}
	if result.Budget.Used != 0 {
		t.Errorf("expected Budget.Used 0, got %d", result.Budget.Used)
	}
	if result.Budget.Remaining != 16000 {
		t.Errorf("expected Budget.Remaining 16000, got %d", result.Budget.Remaining)
	}
	if result.Budget.UsedPct != 0 {
		t.Errorf("expected Budget.UsedPct 0, got %f", result.Budget.UsedPct)
	}
}

func TestInspectSingleChunk(t *testing.T) {
	chunks := []types.ContextChunk{
		{
			Path:       "/proj/cmd/main.go",
			Content:    "package main\nfunc main() {}\n",
			TokenCount: 12,
			Language:   "go",
			LineStart:  1,
			LineEnd:    2,
			Source:     "file",
			Score:      0.95,
		},
	}
	result := Inspect("/proj", chunks, 16000)

	if result.TotalFiles != 1 {
		t.Errorf("expected 1 file, got %d", result.TotalFiles)
	}
	if result.TotalTokens != 12 {
		t.Errorf("expected 12 tokens, got %d", result.TotalTokens)
	}
	if result.TotalLines != 2 {
		t.Errorf("expected 2 lines, got %d", result.TotalLines)
	}
	src, ok := result.BySource["file"]
	if !ok {
		t.Fatal("expected 'file' source in BySource")
	}
	if src.Count != 1 {
		t.Errorf("expected source count 1, got %d", src.Count)
	}
	lang, ok := result.ByLanguage["go"]
	if !ok {
		t.Fatal("expected 'go' language in ByLanguage")
	}
	if lang.Count != 1 {
		t.Errorf("expected language count 1, got %d", lang.Count)
	}
}

func TestInspectMultipleSources(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "/proj/a.go", Content: "package main", TokenCount: 5, Language: "go", LineStart: 1, LineEnd: 1, Source: "file"},
		{Path: "/proj/b.ts", Content: "const x = 1;", TokenCount: 5, Language: "typescript", LineStart: 1, LineEnd: 1, Source: "file"},
		{Path: "/proj/c.txt", Content: "some text", TokenCount: 5, Language: "unknown", LineStart: 1, LineEnd: 1, Source: "search"},
	}
	result := Inspect("/proj", chunks, 16000)

	if result.TotalFiles != 3 {
		t.Errorf("expected 3 files, got %d", result.TotalFiles)
	}
	if len(result.BySource) != 2 {
		t.Errorf("expected 2 sources, got %d", len(result.BySource))
	}
	if len(result.ByLanguage) != 3 {
		t.Errorf("expected 3 languages, got %d", len(result.ByLanguage))
	}
}

func TestInspectRelativePath(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "/proj/src/main.go", Content: "package main", TokenCount: 5, Language: "go", LineStart: 1, LineEnd: 1, Source: "file", Score: 1},
	}
	result := Inspect("/proj", chunks, 16000)
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result.Files))
	}
	if result.Files[0].RelPath != "src/main.go" {
		t.Errorf("expected RelPath 'src/main.go', got %q", result.Files[0].RelPath)
	}
}

func TestInspectRelPathWithLeadingSlash(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "/proj/src/main.go", Content: "package main", TokenCount: 5, Language: "go", LineStart: 1, LineEnd: 1, Source: "file", Score: 1},
	}
	result := Inspect("/proj/", chunks, 16000)
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result.Files))
	}
	if result.Files[0].RelPath != "src/main.go" {
		t.Errorf("expected RelPath 'src/main.go', got %q", result.Files[0].RelPath)
	}
}

func TestInspectLongFirstLine(t *testing.T) {
	longLine := strings.Repeat("x", 70)
	chunks := []types.ContextChunk{
		{Path: "/proj/main.go", Content: longLine + "\nmore", TokenCount: 10, Language: "go", LineStart: 1, LineEnd: 2, Source: "file", Score: 1},
	}
	result := Inspect("/proj", chunks, 16000)
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result.Files))
	}
	if len(result.Files[0].FirstLine) != 63 { // 60 + "..."
		t.Errorf("expected FirstLine len 63, got %d (%q)", len(result.Files[0].FirstLine), result.Files[0].FirstLine)
	}
}

func TestInspectEmptyContent(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "/proj/main.go", Content: "", TokenCount: 0, Language: "go", LineStart: 1, LineEnd: 1, Source: "file", Score: 1},
	}
	result := Inspect("/proj", chunks, 16000)
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result.Files))
	}
	if result.Files[0].FirstLine != "" {
		t.Errorf("expected empty FirstLine, got %q", result.Files[0].FirstLine)
	}
}

func TestInspectBudgetStatus(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "/proj/a.go", Content: "package main", TokenCount: 100, Language: "go", LineStart: 1, LineEnd: 1, Source: "file"},
	}
	result := Inspect("/proj", chunks, 200)

	if result.Budget.Total != 200 {
		t.Errorf("expected Budget.Total 200, got %d", result.Budget.Total)
	}
	if result.Budget.Used != 100 {
		t.Errorf("expected Budget.Used 100, got %d", result.Budget.Used)
	}
	if result.Budget.Remaining != 100 {
		t.Errorf("expected Budget.Remaining 100, got %d", result.Budget.Remaining)
	}
	if result.Budget.UsedPct != 50.0 {
		t.Errorf("expected Budget.UsedPct 50.0, got %f", result.Budget.UsedPct)
	}
}

func TestInspectOverBudget(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "/proj/a.go", Content: "package main", TokenCount: 150, Language: "go", LineStart: 1, LineEnd: 1, Source: "file"},
	}
	result := Inspect("/proj", chunks, 100)

	if result.Budget.Remaining >= 0 {
		t.Error("expected Budget.Remaining < 0 when used > total")
	}
}

func TestInspectAvgPerFile(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "/proj/a.go", Content: "package main", TokenCount: 20, Language: "go", LineStart: 1, LineEnd: 1, Source: "file"},
		{Path: "/proj/b.go", Content: "func b() {}", TokenCount: 20, Language: "go", LineStart: 1, LineEnd: 1, Source: "file"},
	}
	result := Inspect("/proj", chunks, 16000)

	if result.Budget.AvgPerFile != 20.0 {
		t.Errorf("expected AvgPerFile 20.0, got %f", result.Budget.AvgPerFile)
	}
}

func TestInspectionResultJSON(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "/proj/main.go", Content: "package main", TokenCount: 10, Language: "go", LineStart: 1, LineEnd: 1, Source: "file"},
	}
	result := Inspect("/proj", chunks, 16000)

	json := result.JSON()
	if json == "" {
		t.Error("expected non-empty JSON output")
	}
	if result.RawJSON() != json {
		t.Error("RawJSON should equal JSON")
	}
}

func TestInspectionResultFileDetail(t *testing.T) {
	chunks := []types.ContextChunk{
		{
			Path:        "/proj/cmd/main.go",
			Content:     "package main\n",
			TokenCount:  15,
			Language:    "go",
			LineStart:   3,
			LineEnd:     4,
			Source:      "file",
			Score:       0.87,
			Compression: "0.3",
		},
	}
	result := Inspect("/proj", chunks, 16000)

	f := result.Files[0]
	if f.Path != "/proj/cmd/main.go" {
		t.Errorf("expected Path /proj/cmd/main.go, got %s", f.Path)
	}
	if f.Language != "go" {
		t.Errorf("expected Language go, got %s", f.Language)
	}
	if f.Lines != "3-4" {
		t.Errorf("expected Lines '3-4', got %s", f.Lines)
	}
	if f.Tokens != 15 {
		t.Errorf("expected Tokens 15, got %d", f.Tokens)
	}
	if f.Source != "file" {
		t.Errorf("expected Source file, got %s", f.Source)
	}
	if f.Score != 0.87 {
		t.Errorf("expected Score 0.87, got %f", f.Score)
	}
	if f.Compression != "0.3" {
		t.Errorf("expected Compression 0.3, got %s", f.Compression)
	}
}

// ─── Text() edge cases ─────────────────────────────────────────────────────────

func TestInspectionResultTextNoSources(t *testing.T) {
	r := InspectionResult{
		TotalFiles: 1, TotalTokens: 10, TotalLines: 1,
		BySource:   map[string]SourceStats{},
		ByLanguage: map[string]LanguageStats{"go": {Count: 1, Tokens: 10}},
		Files:      []FileDetail{{Path: "/proj/main.go", RelPath: "main.go", Language: "go", Lines: "1-1", Tokens: 10, Source: "file"}},
		Budget:     BudgetStatus{Total: 16000, Used: 10, Remaining: 15990, UsedPct: 0.0625, AvgPerFile: 10},
	}
	text := r.Text()
	if strings.Contains(text, "Sources:") {
		t.Error("expected no Sources section when BySource is empty")
	}
}

func TestInspectionResultTextNoLanguages(t *testing.T) {
	r := InspectionResult{
		TotalFiles: 1, TotalTokens: 10, TotalLines: 1,
		BySource:   map[string]SourceStats{"file": {Count: 1, Tokens: 10, Lines: 1}},
		ByLanguage: map[string]LanguageStats{},
		Files:      []FileDetail{{Path: "/proj/main.go", RelPath: "main.go", Language: "go", Lines: "1-1", Tokens: 10, Source: "file"}},
		Budget:     BudgetStatus{Total: 16000, Used: 10, Remaining: 15990, UsedPct: 0.0625, AvgPerFile: 10},
	}
	text := r.Text()
	if strings.Contains(text, "Languages:") {
		t.Error("expected no Languages section when ByLanguage is empty")
	}
}

func TestInspectionResultTextFilesTruncation(t *testing.T) {
	files := make([]FileDetail, 20)
	for i := range files {
		files[i] = FileDetail{Path: "/proj/f.go", RelPath: "f.go", Language: "go", Lines: "1-1", Tokens: 1, Source: "file", Score: float64(20 - i)}
	}
	r := InspectionResult{
		TotalFiles: 20, TotalTokens: 20, TotalLines: 20,
		BySource:   map[string]SourceStats{"file": {Count: 20, Tokens: 20, Lines: 20}},
		ByLanguage: map[string]LanguageStats{"go": {Count: 20, Tokens: 20}},
		Files:      files,
		Budget:     BudgetStatus{Total: 100, Used: 20, Remaining: 80, UsedPct: 20, AvgPerFile: 1},
	}
	text := r.Text()
	if !strings.Contains(text, "more file(s)") {
		t.Error("expected truncation text for >15 files")
	}
}

func TestInspectionResultTextFirstLineNoNewline(t *testing.T) {
	chunks := []types.ContextChunk{
		{Path: "/proj/a.go", Content: "x", TokenCount: 1, Language: "go", LineStart: 1, LineEnd: 1, Source: "file", Score: 1},
	}
	result := Inspect("/proj", chunks, 16000)
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result.Files))
	}
	if result.Files[0].FirstLine != "x" {
		t.Errorf("expected FirstLine 'x', got %q", result.Files[0].FirstLine)
	}
}

func TestInspectionResultJSONNoPanic(t *testing.T) {
	r := InspectionResult{
		TotalFiles: 0, TotalTokens: 0, TotalLines: 0,
		BySource:   map[string]SourceStats{},
		ByLanguage: map[string]LanguageStats{},
		Files:      []FileDetail{},
		Budget:     BudgetStatus{Total: 0, Used: 0, Remaining: 0, UsedPct: 0, AvgPerFile: 0},
	}
	_, err := json.Marshal(r)
	if err != nil {
		t.Errorf("JSON marshal should not fail: %v", err)
	}
}
