package engine

import (
	"context"
	"errors"
	"testing"
)

// TestErrEngineNilFromNilReceiver pins the contract that calling a
// gated method on a nil *Engine returns ErrEngineNil rather than
// panicking — the requireReady guard is the first line of defense
// for callers that haven't fully wired the engine yet.
func TestErrEngineNilFromNilReceiver(t *testing.T) {
	var e *Engine
	if _, err := e.Ask(context.Background(), "hello"); err == nil {
		t.Fatal("Ask on nil engine must return an error, not panic")
	} else if !errors.Is(err, ErrEngineNil) {
		t.Fatalf("nil-receiver Ask must wrap ErrEngineNil, got %v", err)
	}
	if _, err := e.CallTool(context.Background(), "read_file", map[string]any{"path": "go.mod"}); err == nil {
		t.Fatal("CallTool on nil engine must return an error, not panic")
	} else if !errors.Is(err, ErrEngineNil) {
		t.Fatalf("nil-receiver CallTool must wrap ErrEngineNil, got %v", err)
	}
}
