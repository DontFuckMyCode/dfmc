package context

import (
	"testing"
)

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