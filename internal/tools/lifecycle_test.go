package tools

import (
	"errors"
	"testing"
)

// NOTE: Full Engine.Close() testing requires complex setup with Engine struct.
// These tests cover the exported symbols and simple helpers.

func TestErrEngineClosed(t *testing.T) {
	if ErrEngineClosed.Error() != "tools engine is closed" {
		t.Error("unexpected error message")
	}
}

func TestErrEngineClosed_Is(t *testing.T) {
	if !errors.Is(ErrEngineClosed, ErrEngineClosed) {
		t.Error("sentinel should be Is itself")
	}
}

func TestToolCloserInterface(t *testing.T) {
	// Verify toolCloser interface is satisfied by types that implement Close()
	var _ toolCloser = (*mockToolWithClose)(nil)
}

type mockToolWithClose struct {
	closeErr error
}

func (m *mockToolWithClose) Close() error { return m.closeErr }

func TestLockPath_empty(t *testing.T) {
	// LockPath with empty string returns a nop function
	e := &Engine{}
	release := e.LockPath("")
	if release == nil {
		t.Fatal("LockPath(\"\") should return non-nil release func")
	}
	// Calling it twice should not panic
	release()
	release()
}