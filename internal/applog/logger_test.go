package applog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNew(t *testing.T) {
	// Test with valid directory
	tmp := t.TempDir()
	logger, err := New(Config{DataDir: tmp})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if logger == nil {
		t.Fatal("New() returned nil")
	}
	logger.Close()
}

func TestNew_emptyDir(t *testing.T) {
	// Empty DataDir should use current directory
	logger, err := New(Config{DataDir: ""})
	if err != nil {
		t.Fatalf("New() with empty DataDir error = %v", err)
	}
	if logger == nil {
		t.Fatal("New() returned nil with empty DataDir")
	}
	logger.Close()
}

func TestLogger_WithComponent(t *testing.T) {
	tmp := t.TempDir()
	logger, err := New(Config{DataDir: tmp})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer logger.Close()

	l2 := logger.WithComponent("test-component")
	if l2 == nil {
		t.Fatal("WithComponent returned nil")
	}
	if l2.component != "test-component" {
		t.Errorf("WithComponent component = %q, want %q", l2.component, "test-component")
	}
}

func TestLogger_WithOperation(t *testing.T) {
	tmp := t.TempDir()
	logger, err := New(Config{DataDir: tmp})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer logger.Close()

	l2 := logger.WithOperation("test-op")
	if l2 == nil {
		t.Fatal("WithOperation returned nil")
	}
	if l2.operation != "test-op" {
		t.Errorf("WithOperation operation = %q, want %q", l2.operation, "test-op")
	}
}

func TestLogger_WithComponent_nilReceiver(t *testing.T) {
	var logger *Logger
	result := logger.WithComponent("x")
	if result != nil {
		t.Errorf("WithComponent on nil receiver = %v, want nil", result)
	}
}

func TestLogger_WithOperation_nilReceiver(t *testing.T) {
	var logger *Logger
	result := logger.WithOperation("x")
	if result != nil {
		t.Errorf("WithOperation on nil receiver = %v, want nil", result)
	}
}

func TestLogger_Info(t *testing.T) {
	tmp := t.TempDir()
	logger, err := New(Config{DataDir: tmp})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer logger.Close()

	// Should not panic
	logger.Info("test message")

	// Verify log file was created
	files, _ := os.ReadDir(filepath.Join(tmp, "app"))
	if len(files) == 0 {
		t.Error("Info did not create log file")
	}
}

func TestLogger_Warn(t *testing.T) {
	tmp := t.TempDir()
	logger, err := New(Config{DataDir: tmp})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer logger.Close()

	logger.Warn("warning message", map[string]any{"key": "value"})
}

func TestLogger_Error(t *testing.T) {
	tmp := t.TempDir()
	logger, err := New(Config{DataDir: tmp})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer logger.Close()

	errTest := os.ErrNotExist
	logger.Error("error message", errTest)
}

func TestLogger_Close(t *testing.T) {
	tmp := t.TempDir()
	logger, err := New(Config{DataDir: tmp})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := logger.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestLogger_Info_nilWriter(t *testing.T) {
	logger := &Logger{w: nil}
	// Should not panic
	logger.Info("test")
}

func TestLogger_Info_emptyPath(t *testing.T) {
	logger := &Logger{w: &writer{path: ""}}
	// Should not panic
	logger.Info("test")
}