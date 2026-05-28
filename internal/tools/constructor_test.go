package tools

import (
	"context"
	"errors"
	"testing"
	"time"

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
	eng := New(ToToolsConfigSubset(config.DefaultConfig()))
	stub := &closingStubTool{}
	eng.Register(stub)

	if err := eng.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !stub.closed {
		t.Fatal("tools engine close must invoke registered tool closers")
	}
}

func TestToolsEngineCloseClearsSessionStateAndRejectsExecute(t *testing.T) {
	eng := New(ToToolsConfigSubset(config.DefaultConfig()))
	eng.readMu.Lock()
	eng.readSnapshots["a.go"] = "hash"
	eng.readSnapshotLRU = []string{"a.go"}
	eng.readMu.Unlock()
	eng.failureMu.Lock()
	eng.recentFailures["tool|path=a.go"] = 2
	eng.recentFailOrder = []string{"tool|path=a.go"}
	eng.failureOrderIdx = map[string]int{"tool|path=a.go": 0}
	eng.failureMu.Unlock()

	if err := eng.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := eng.Execute(context.Background(), "read_file", Request{}); !errors.Is(err, ErrEngineClosed) {
		t.Fatalf("execute after close error = %v, want ErrEngineClosed", err)
	}

	eng.readMu.RLock()
	gotSnapshots := len(eng.readSnapshots) + len(eng.readSnapshotLRU)
	eng.readMu.RUnlock()
	if gotSnapshots != 0 {
		t.Fatalf("close should clear read snapshot state, got %d entries", gotSnapshots)
	}
	eng.failureMu.Lock()
	gotFailures := len(eng.recentFailures) + len(eng.recentFailOrder) + len(eng.failureOrderIdx)
	eng.failureMu.Unlock()
	if gotFailures != 0 {
		t.Fatalf("close should clear failure state, got %d entries", gotFailures)
	}
}

type blockingCloseTool struct {
	started chan struct{}
	release chan struct{}
}

func (t *blockingCloseTool) Name() string        { return "blocking_close" }
func (t *blockingCloseTool) Description() string { return "blocks until released" }
func (t *blockingCloseTool) Execute(_ context.Context, _ Request) (Result, error) {
	close(t.started)
	<-t.release
	return Result{}, nil
}

func TestToolsEngineCloseWaitsForInFlightExecute(t *testing.T) {
	eng := New(ToToolsConfigSubset(config.DefaultConfig()))
	tool := &blockingCloseTool{started: make(chan struct{}), release: make(chan struct{})}
	eng.Register(tool)

	doneExecute := make(chan error, 1)
	go func() {
		_, err := eng.Execute(context.Background(), tool.Name(), Request{})
		doneExecute <- err
	}()
	<-tool.started

	doneClose := make(chan error, 1)
	go func() { doneClose <- eng.Close() }()
	select {
	case err := <-doneClose:
		t.Fatalf("Close returned before in-flight Execute completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	close(tool.release)
	if err := <-doneExecute; err != nil {
		t.Fatalf("execute: %v", err)
	}
	if err := <-doneClose; err != nil {
		t.Fatalf("close: %v", err)
	}
}
