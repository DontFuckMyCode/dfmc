package tools

import (
	"testing"
)

func TestASTQueryTool_NameAndDescription(t *testing.T) {
	tool := NewASTQueryTool()
	if tool.Name() != "ast_query" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "ast_query")
	}
	if tool.Description() == "" {
		t.Fatal("Description() returned empty string")
	}
}

func TestASTQueryTool_CloseNilEngine(t *testing.T) {
	var tool *ASTQueryTool
	if err := tool.Close(); err != nil {
		t.Fatalf("Close() on nil tool returned error: %v", err)
	}
}

func TestASTQueryTool_CloseWithEngine(t *testing.T) {
	tool := NewASTQueryTool()
	if err := tool.Close(); err != nil {
		t.Fatalf("Close() returned error: %v", err)
	}
}

func TestASTQueryTool_getEngine(t *testing.T) {
	tool := NewASTQueryTool()
	engine := tool.getEngine()
	if engine == nil {
		t.Fatal("getEngine() returned nil")
	}
}
