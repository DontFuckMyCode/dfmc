package toolhistory

import (
	"os"
	"path/filepath"
	"testing"
)

// mockEventBus implements the eventBus interface for testing.
type mockEventBus struct{}

func (m *mockEventBus) SubscribeFunc(eventType string, fn func(any)) func() {
	// no-op for dir creation test
	return func() {}
}

func TestExtractFiles(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   []string
	}{
		{
			name:   "single path",
			params: map[string]any{"path": "foo.go"},
			want:   []string{"foo.go"},
		},
		{
			name:   "paths array",
			params: map[string]any{"paths": []any{"foo.go", "bar.go"}},
			want:   []string{"foo.go", "bar.go"},
		},
		{
			name:   "mixed",
			params: map[string]any{"path": "foo.go", "dir": "/src"},
			want:   []string{"foo.go", "/src"},
		},
		{
			name:   "ignore empty",
			params: map[string]any{"path": ""},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFiles(tt.params)
			if len(got) != len(tt.want) {
				t.Errorf("extractFiles() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Error("truncate: short string should be unchanged")
	}
	if truncate("hello world this is long", 10) != "hello wo..." {
		t.Errorf("truncate long: got %q", truncate("hello world this is long", 10))
	}
}

func TestInitNil(t *testing.T) {
	// Nil EventBus should not panic.
	logger, err := Init(nil, "")
	if logger != nil || err != nil {
		t.Errorf("Init(nil, _) = %v, %v; want nil, nil", logger, err)
	}
}

func TestInitCreatesDir(t *testing.T) {
	tmp := t.TempDir()
	eb := &mockEventBus{}

	logger, err := Init(eb, filepath.Join(tmp, "artifacts"))
	if err != nil {
		t.Fatalf("Init() err = %v", err)
	}
	if logger == nil {
		t.Fatal("logger should not be nil")
	}

	dir := filepath.Join(tmp, "artifacts", "toolcalls")
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("artifacts dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected dir, got %v", info.Mode())
	}

	logger.Close()
}

func TestStrVal(t *testing.T) {
	m := map[string]any{"foo": "bar", "num": float64(42)}
	if strVal(m, "foo") != "bar" {
		t.Error("strVal: expected bar")
	}
	if strVal(m, "missing") != "" {
		t.Error("strVal: missing key should return empty")
	}
}

func TestIntVal(t *testing.T) {
	m := map[string]any{"a": float64(42), "b": 100}
	if intVal(m, "a") != 42 {
		t.Errorf("intVal float64: got %d", intVal(m, "a"))
	}
	if intVal(m, "b") != 100 {
		t.Errorf("intVal int: got %d", intVal(m, "b"))
	}
}

func TestBoolVal(t *testing.T) {
	m := map[string]any{"t": true, "f": false, "s": "string"}
	if !boolVal(m, "t") {
		t.Error("boolVal true")
	}
	if boolVal(m, "f") {
		t.Error("boolVal false")
	}
	if boolVal(m, "s") {
		t.Error("boolVal string should be false")
	}
}
