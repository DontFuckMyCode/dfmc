package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONSchemaShape(t *testing.T) {
	spec := ToolSpec{
		Name: "demo",
		Args: []Arg{
			{Name: "path", Type: ArgString, Required: true, Description: "file path"},
			{Name: "limit", Type: ArgInteger, Default: 10},
			{Name: "mode", Type: ArgString, Enum: []any{"a", "b"}},
			{Name: "paths", Type: ArgArray, Items: &Arg{Type: ArgString}},
		},
	}
	schema := spec.JSONSchema()
	if schema["type"] != "object" {
		t.Fatalf("schema type: want object, got %v", schema["type"])
	}
	req, _ := schema["required"].([]string)
	if len(req) != 1 || req[0] != "path" {
		t.Fatalf("required: want [path], got %v", req)
	}
	props, _ := schema["properties"].(map[string]any)
	if props["path"] == nil {
		t.Fatal("missing path property")
	}
	limit := props["limit"].(map[string]any)
	if limit["default"] != 10 {
		t.Fatalf("limit default: want 10, got %v", limit["default"])
	}
	mode := props["mode"].(map[string]any)
	enum := mode["enum"].([]any)
	if len(enum) != 2 {
		t.Fatalf("enum: want 2 values, got %v", enum)
	}
	paths := props["paths"].(map[string]any)
	items := paths["items"].(map[string]any)
	if items["type"] != "string" {
		t.Fatalf("items.type: want string, got %v", items["type"])
	}
	// Round-trip JSON to confirm schema is marshallable.
	if _, err := json.Marshal(schema); err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
}

func TestScoreMatchRanking(t *testing.T) {
	specs := []ToolSpec{
		{Name: "grep_codebase", Summary: "regex search across files", Tags: []string{"search", "grep"}},
		{Name: "read_file", Summary: "read text file", Tags: []string{"read", "filesystem"}},
		{Name: "write_file", Summary: "create or overwrite files", Tags: []string{"write", "filesystem"}},
	}

	// Name-exact should beat tag-match.
	if s := ScoreMatch(specs[0], "grep_codebase"); s < 50 {
		t.Fatalf("exact name match should score >=50, got %d", s)
	}
	// Tag match should produce a non-zero score.
	if s := ScoreMatch(specs[1], "filesystem"); s == 0 {
		t.Fatal("tag match should score > 0")
	}
	// Unrelated query should score 0.
	if s := ScoreMatch(specs[0], "xyzzynonexistent"); s != 0 {
		t.Fatalf("nonsense query should score 0, got %d", s)
	}
	// Name prefix should beat name-substring.
	prefix := ScoreMatch(ToolSpec{Name: "grep_codebase"}, "grep")
	substr := ScoreMatch(ToolSpec{Name: "codegrep_tool"}, "grep")
	if prefix <= substr {
		t.Fatalf("prefix score %d should beat substring score %d", prefix, substr)
	}
}

func TestShortHelpIncludesRisk(t *testing.T) {
	s := ToolSpec{Name: "edit_file", Summary: "surgical edit", Risk: RiskWrite}
	out := s.ShortHelp()
	if !strings.Contains(out, "edit_file") || !strings.Contains(out, "surgical edit") || !strings.Contains(out, "write") {
		t.Fatalf("ShortHelp missing fields: %q", out)
	}
}

func TestLongHelpFormat(t *testing.T) {
	s := ToolSpec{
		Name:    "run_command",
		Title:   "Run command",
		Summary: "execute shell",
		Risk:    RiskExecute,
		Args: []Arg{
			{Name: "command", Type: ArgString, Required: true, Description: "argv"},
			{Name: "timeout_ms", Type: ArgInteger},
		},
		Returns:  "stdout/stderr",
		Examples: []string{`{"command":"go test"}`},
		Tags:     []string{"shell"},
	}
	out := s.LongHelp()
	for _, want := range []string{
		"run_command",
		"(Run command)",
		"Risk: execute",
		"(required)",
		"Returns: stdout/stderr",
		"Examples:",
		"Tags: shell",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("LongHelp missing %q; got:\n%s", want, out)
		}
	}
}
