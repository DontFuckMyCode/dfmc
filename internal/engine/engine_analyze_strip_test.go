package engine

import (
	"strings"
	"testing"
)

// --- stripCFamilyComments -----------------------------------------

func TestStripCFamilyComments_basic(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		comment string // substring that should NOT appear in output
	}{
		{
			name:    "line comment removed",
			input:   "func foo() { // this is a comment\n  x = 1\n}",
			comment: "this is a comment",
		},
		{
			name:    "block comment removed",
			input:   "func foo() {/*comment*/ x = 1}",
			comment: "comment",
		},
		{
			name:    "strings preserved",
			input:   `x := "hello // not a comment"`,
			comment: "", // no comment present
		},
		{
			name:    "raw string preserved",
			input:   "x := `hello // not a comment`",
			comment: "", // no comment present
		},
		{
			name:    "no changes",
			input:   "func foo() { x = 1 }",
			comment: "",
		},
		{
			name:    "empty",
			input:   "",
			comment: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCFamilyComments(tt.input)
			if tt.comment != "" && strings.Contains(got, tt.comment) {
				t.Errorf("stripCFamilyComments(): comment %q still present in:\n%s", tt.comment, got)
			}
		})
	}
}

// --- stripPythonComments ------------------------------------------

func TestStripPythonComments_basic(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		comment string
	}{
		{
			name:    "hash comment removed",
			input:   "x = 1  # this is a comment",
			comment: "this is a comment",
		},
		{
			name:    "triple quote docstring",
			input:   `"""docstring"""`,
			comment: "docstring",
		},
		{
			name:    "single line strings preserved",
			input:   `x = "hello # not a comment"`,
			comment: "", // string preserved, hash is inside string
		},
		{
			name:    "no comments",
			input:   "x = 1",
			comment: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripPythonComments(tt.input)
			if tt.comment != "" && strings.Contains(got, tt.comment) {
				t.Errorf("stripPythonComments(): comment %q still present in:\n%s", tt.comment, got)
			}
		})
	}
}

// --- stripCFamily -------------------------------------------------

func TestStripCFamily_basic(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		removed string // substring that should NOT appear (removed)
		kept    string // substring that should still appear (preserved)
	}{
		{
			name:    "line comment removed",
			input:   "func foo() { // comment\n  x = 1\n}",
			removed: "comment",
			kept:    "func foo()",
		},
		{
			name:    "strings removed",
			input:   `x := "hello world"`,
			removed: "hello world",
			kept:    "x :=",
		},
		{
			name:    "raw strings removed",
			input:   "`hello world`",
			removed: "hello world",
			kept:    "",
		},
		{
			name:    "no changes",
			input:   "func foo() { x = 1 }",
			removed: "",
			kept:    "func foo() { x = 1 }",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCFamily(tt.input)
			if tt.removed != "" && strings.Contains(got, tt.removed) {
				t.Errorf("stripCFamily(): expected %q to be removed, got:\n%s", tt.removed, got)
			}
			if tt.kept != "" && !strings.Contains(got, tt.kept) {
				t.Errorf("stripCFamily(): expected %q to be preserved, got:\n%s", tt.kept, got)
			}
		})
	}
}

// --- stripPython --------------------------------------------------

func TestStripPython_basic(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		removed string
		kept    string
	}{
		{
			name:    "hash comment removed",
			input:   "x = 1  # comment",
			removed: "comment",
			kept:    "x = 1",
		},
		{
			name:    "single line strings removed",
			input:   `x = "hello"`,
			removed: "hello",
			kept:    "x =",
		},
		{
			name:    "no changes",
			input:   "x = 1",
			removed: "",
			kept:    "x = 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripPython(tt.input)
			if tt.removed != "" && strings.Contains(got, tt.removed) {
				t.Errorf("stripPython(): expected %q to be removed, got:\n%s", tt.removed, got)
			}
			if tt.kept != "" && !strings.Contains(got, tt.kept) {
				t.Errorf("stripPython(): expected %q to be preserved, got:\n%s", tt.kept, got)
			}
		})
	}
}

// --- stripStringsAndComments -------------------------------------

func TestStripStringsAndComments(t *testing.T) {
	tests := []struct {
		name    string
		ext     string
		input   string
		removed string
		kept    string
	}{
		{
			name:    "go file strips comments and strings",
			ext:     ".go",
			input:   "// comment\nfunc foo() { x := \"hello\" }",
			removed: "hello",
			kept:    "func foo()",
		},
		{
			name:    "ts file same as c-family",
			ext:     ".ts",
			input:   "// comment\nconst x = \"hello\";",
			removed: "hello",
			kept:    "const x",
		},
		{
			name:    "py file strips comments",
			ext:     ".py",
			input:   "# comment\nx = \"hello\"",
			removed: "comment",
			kept:    "x =",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripStringsAndComments(tt.input, tt.ext)
			if tt.removed != "" && strings.Contains(got, tt.removed) {
				t.Errorf("stripStringsAndComments(): expected %q to be removed, got:\n%s", tt.removed, got)
			}
			if tt.kept != "" && !strings.Contains(got, tt.kept) {
				t.Errorf("stripStringsAndComments(): expected %q to be preserved, got:\n%s", tt.kept, got)
			}
		})
	}
}

// --- stripCommentsOnly -------------------------------------------

func TestStripCommentsOnly(t *testing.T) {
	// Test inline comments are stripped, code is preserved
	tests := []struct {
		name    string
		ext     string
		input   string
		removed string
		kept    string
	}{
		{
			name:    "go inline comment stripped",
			ext:     ".go",
			input:   "x = 1 // inline comment",
			removed: "inline comment",
			kept:    "x = 1",
		},
		{
			name:    "py hash comment stripped",
			ext:     ".py",
			input:   "x = 1  # end comment",
			removed: "end comment",
			kept:    "x = 1",
		},
		{
			name:    "unknown extension preserves all",
			ext:     ".xyz",
			input:   "// comment",
			removed: "",
			kept:    "// comment",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCommentsOnly(tt.input, tt.ext)
			if tt.removed != "" && strings.Contains(got, tt.removed) {
				t.Errorf("stripCommentsOnly(): expected %q to be removed, got:\n%s", tt.removed, got)
			}
			if tt.kept != "" && !strings.Contains(got, tt.kept) {
				t.Errorf("stripCommentsOnly(): expected %q to be preserved, got:\n%s", tt.kept, got)
			}
		})
	}
}
