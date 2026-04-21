package tools

import (
	"context"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/config"
)

func TestASTBackedToolConstructorsInitializeEngine(t *testing.T) {
	if tool := NewASTQueryTool(); tool.engine == nil {
		t.Fatal("NewASTQueryTool must initialize its AST engine eagerly")
	}
	if tool := NewFindSymbolTool(); tool.engine == nil {
		t.Fatal("NewFindSymbolTool must initialize its AST engine eagerly")
	}
	if tool := NewCodemapTool(); tool.engine == nil {
		t.Fatal("NewCodemapTool must initialize its AST engine eagerly")
	}
}

type closingStubTool struct{ closed bool }

func (t *closingStubTool) Name() string        { return "closing_stub" }
func (t *closingStubTool) Description() string { return "stub" }
func (t *closingStubTool) Execute(_ context.Context, _ Request) (Result, error) {
	return Result{}, nil
}
func (t *closingStubTool) Close() error {
	t.closed = true
	return nil
}

func TestToolsEngineCloseInvokesRegisteredClosers(t *testing.T) {
	eng := New(*config.DefaultConfig())
	stub := &closingStubTool{}
	eng.Register(stub)

	if err := eng.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !stub.closed {
		t.Fatal("tools engine close must invoke registered tool closers")
	}
}
