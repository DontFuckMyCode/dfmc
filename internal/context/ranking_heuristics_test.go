package context

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/codemap"
)

// TestIsLikelyEntryPoint covers the entry-point detection logic.
func TestIsLikelyEntryPoint(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Standard entry points
		{name: "main_lowercase", input: "main", expected: true},
		{name: "main_uppercase", input: "MAIN", expected: true},
		{name: "main_mixed", input: "Main", expected: true},
		{name: "init_lowercase", input: "init", expected: true},
		{name: "init_uppercase", input: "INIT", expected: true},
		{name: "test_lowercase", input: "test", expected: true},

		// Test file conventions
		{name: "test_prefix", input: "test_helpers", expected: true},
		{name: "test_suffix", input: "helpers_test", expected: true},
		{name: "test_file", input: "example_test", expected: true},

		// Non-entry points
		{name: "regular_func", input: "foo", expected: false},
		{name: "camel_case", input: "myFunction", expected: false},
		{name: "pascal_case", input: "MyFunction", expected: false},
		{name: "snake_case", input: "my_function", expected: false},
		{name: "under_score", input: "_private", expected: false},
		{name: "single_letter", input: "a", expected: false},
		{name: "empty_string", input: "", expected: false},

		// Edge cases with test-like substrings
		{name: "testing_not_test", input: "testing", expected: false},
		{name: "testify_not_test", input: " testify ", expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isLikelyEntryPoint(tt.input)
			if got != tt.expected {
				t.Errorf("isLikelyEntryPoint(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

// TestRefactorBoost_NilGraph covers the nil-check guard in refactorBoost.
func TestRefactorBoost_NilGraph(t *testing.T) {
	scores := map[string]float64{}
	sources := map[string]string{}
	refactorBoost(nil, scores, sources)
	if len(scores) != 0 {
		t.Errorf("nil graph should produce no scores, got %v", scores)
	}
}

// TestRefactorBoost_OrphanFunction verifies orphan detection boosts a file's score.
func TestRefactorBoost_OrphanFunction(t *testing.T) {
	g := codemap.NewGraph()
	g.AddNodesWithEdges(
		[]codemap.Node{
			{ID: "file:pkg/a.go", Name: "a.go", Kind: "file", Path: "pkg/a.go"},
			{ID: "fn:unused", Name: "unused", Kind: "function", Path: "pkg/a.go"},
		},
		[]codemap.Edge{},
	)
	scores := map[string]float64{}
	sources := map[string]string{}
	refactorBoost(g, scores, sources)
	if scores["pkg/a.go"] != 3.0 {
		t.Errorf("orphan file expected score 3.0, got %f", scores["pkg/a.go"])
	}
	if sources["pkg/a.go"] != "orphan_candidate" {
		t.Errorf("orphan source: got %q", sources["pkg/a.go"])
	}
}

// TestRefactorBoost_SkipsEntryPoints verifies main/init are not flagged as orphans.
func TestRefactorBoost_SkipsEntryPoints(t *testing.T) {
	g := codemap.NewGraph()
	g.AddNodesWithEdges(
		[]codemap.Node{
			{ID: "file:pkg/main.go", Name: "main.go", Kind: "file", Path: "pkg/main.go"},
			{ID: "fn:main", Name: "main", Kind: "function", Path: "pkg/main.go"},
		},
		[]codemap.Edge{},
	)
	scores := map[string]float64{}
	sources := map[string]string{}
	refactorBoost(g, scores, sources)
	if scores["pkg/main.go"] != 0 {
		t.Errorf("entry point should have score 0, got %f", scores["pkg/main.go"])
	}
}

// TestRefactorBoost_SkipsTestFiles verifies test files are not flagged as orphans.
func TestRefactorBoost_SkipsTestFiles(t *testing.T) {
	g := codemap.NewGraph()
	g.AddNodesWithEdges(
		[]codemap.Node{
			{ID: "file:pkg/a_test.go", Name: "a_test.go", Kind: "file", Path: "pkg/a_test.go"},
			{ID: "fn:helper", Name: "helper", Kind: "function", Path: "pkg/a_test.go"},
		},
		[]codemap.Edge{},
	)
	scores := map[string]float64{}
	sources := map[string]string{}
	refactorBoost(g, scores, sources)
	if scores["pkg/a_test.go"] != 0 {
		t.Errorf("test file orphan should have score 0, got %f", scores["pkg/a_test.go"])
	}
}
