package tools

import (
	"testing"
)

func TestNormalizeToolParams_nil(t *testing.T) {
	result := normalizeToolParams("read_file", nil)
	if result == nil {
		t.Fatal("normalizeToolParams returned nil")
	}
	if _, ok := result["path"]; ok {
		t.Error("nil params should initialize empty map")
	}
}

func TestNormalizeToolParams_readFile(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected map[string]any
	}{
		{
			name:  "canonical params unchanged",
			input: map[string]any{"path": "/foo", "line_start": 10, "line_end": 20},
			expected: map[string]any{
				"path":       "/foo",
				"line_start": 10,
				"line_end":   20,
			},
		},
		{
			name:  "alias file promoted to path",
			input: map[string]any{"file": "/foo"},
			expected: map[string]any{
				"path":       "/foo",
				"line_start": 1,
				"line_end":   200,
			},
		},
		{
			name:  "alias filepath promoted to path",
			input: map[string]any{"filepath": "/bar"},
			expected: map[string]any{
				"path":       "/bar",
				"line_start": 1,
				"line_end":   200,
			},
		},
		{
			name:  "alias start promoted to line_start",
			input: map[string]any{"path": "/foo", "start": 5},
			expected: map[string]any{
				"path":       "/foo",
				"line_start": 5,
				"line_end":   204, // start + 199 default
			},
		},
		{
			name:  "negative line_start clamped to 1",
			input: map[string]any{"path": "/foo", "line_start": -5},
			expected: map[string]any{
				"path":       "/foo",
				"line_start": 1,
				"line_end":   200,
			},
		},
		{
			name:  "line range capped at 400 lines",
			input: map[string]any{"path": "/foo", "line_start": 1, "line_end": 1000},
			expected: map[string]any{
				"path":       "/foo",
				"line_start": 1,
				"line_end":   400,
			},
		},
		{
			name:  "end before start gets clamped to start",
			input: map[string]any{"path": "/foo", "line_start": 50, "line_end": 10},
			expected: map[string]any{
				"path":       "/foo",
				"line_start": 50,
				"line_end":   50,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeToolParams("read_file", tt.input)
			for k, v := range tt.expected {
				if got, ok := result[k]; !ok {
					t.Errorf("missing key %q", k)
				} else if got != v {
					t.Errorf("%s = %v, want %v", k, got, v)
				}
			}
		})
	}
}

func TestNormalizeToolParams_listDir(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]any
		expected map[string]any
	}{
		{
			name:     "canonical max_entries",
			input:    map[string]any{"path": "/foo", "max_entries": 100},
			expected: map[string]any{"max_entries": 100},
		},
		{
			name:     "alias limit promoted",
			input:    map[string]any{"path": "/foo", "limit": 50},
			expected: map[string]any{"max_entries": 50},
		},
		{
			name:     "zero max_entries defaults to 200",
			input:    map[string]any{"path": "/foo", "max_entries": 0},
			expected: map[string]any{"max_entries": 200},
		},
		{
			name:     "negative max_entries defaults to 200",
			input:    map[string]any{"path": "/foo", "max_entries": -1},
			expected: map[string]any{"max_entries": 200},
		},
		{
			name:     "max_entries over 500 capped",
			input:    map[string]any{"path": "/foo", "max_entries": 1000},
			expected: map[string]any{"max_entries": 500},
		},
		{
			name:     "alias recursive",
			input:    map[string]any{"path": "/foo", "recurse": true},
			expected: map[string]any{"recursive": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeToolParams("list_dir", tt.input)
			for k, v := range tt.expected {
				if got, ok := result[k]; !ok {
					t.Errorf("missing key %q", k)
				} else if got != v {
					t.Errorf("%s = %v, want %v", k, got, v)
				}
			}
		})
	}
}

func TestNormalizeToolParams_grepCodebase(t *testing.T) {
	result := normalizeToolParams("grep_codebase", map[string]any{
		"query": "foo",
		"limit": 50,
	})
	if result["pattern"] != "foo" {
		t.Errorf("pattern = %v, want foo", result["pattern"])
	}
	if result["max_results"] != 50 {
		t.Errorf("max_results = %v, want 50", result["max_results"])
	}
}

func TestNormalizeToolParams_editFile(t *testing.T) {
	result := normalizeToolParams("edit_file", map[string]any{
		"file": "/foo",
		"old":  "old text",
		"new":  "new text",
	})
	if result["path"] != "/foo" {
		t.Errorf("path = %v, want /foo", result["path"])
	}
	if result["old_string"] != "old text" {
		t.Errorf("old_string = %v, want 'old text'", result["old_string"])
	}
	if result["new_string"] != "new text" {
		t.Errorf("new_string = %v, want 'new text'", result["new_string"])
	}
}

func TestNormalizeToolParams_writeFile(t *testing.T) {
	result := normalizeToolParams("write_file", map[string]any{
		"file": "/foo",
		"text": "content here",
	})
	if result["path"] != "/foo" {
		t.Errorf("path = %v, want /foo", result["path"])
	}
	if result["content"] != "content here" {
		t.Errorf("content = %v, want 'content here'", result["content"])
	}
}

func TestNormalizeToolParams_runCommand(t *testing.T) {
	result := normalizeToolParams("run_command", map[string]any{
		"cmd":     "go",
		"argv":    []string{"build"},
		"timeout": 30000,
	})
	if result["command"] != "go" {
		t.Errorf("command = %v, want go", result["command"])
	}
	if result["args"].([]string)[0] != "build" {
		t.Error("args not promoted from argv")
	}
	if result["timeout_ms"] != 30000 {
		t.Errorf("timeout_ms = %v, want 30000", result["timeout_ms"])
	}
}

func TestNormalizeToolParams_runCommand_capped(t *testing.T) {
	result := normalizeToolParams("run_command", map[string]any{
		"cmd": "go", "timeout_ms": 999_000_000, // over 2min cap
	})
	if result["timeout_ms"] != 120_000 {
		t.Errorf("timeout_ms over cap = %v, want 120000", result["timeout_ms"])
	}
}

func TestPromoteFirstAlias_nil(t *testing.T) {
	promoteFirstAlias(nil, "canonical", "alias1")
	// Should not panic
}

func TestPromoteFirstAlias_canonicalExists(t *testing.T) {
	params := map[string]any{"canonical": "value", "alias1": "other"}
	promoteFirstAlias(params, "canonical", "alias1")
	if params["canonical"] != "value" {
		t.Error("canonical should not be overwritten")
	}
}

func TestPromoteFirstAlias_aliasPromoted(t *testing.T) {
	params := map[string]any{"alias1": "value"}
	promoteFirstAlias(params, "canonical", "alias1")
	if params["canonical"] != "value" {
		t.Error("alias1 should be promoted to canonical")
	}
	if _, ok := params["alias1"]; ok {
		t.Error("alias1 should be deleted after promotion")
	}
}

func TestPromoteFirstAlias_secondAliasPromoted(t *testing.T) {
	params := map[string]any{"alias2": "value"}
	promoteFirstAlias(params, "canonical", "alias1", "alias2")
	if params["canonical"] != "value" {
		t.Error("alias2 should be promoted to canonical")
	}
}
