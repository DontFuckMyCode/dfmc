package pluginexec

import (
	"context"
	"testing"
)

func TestManagerCloseNotLoaded(t *testing.T) {
	m := NewManager()
	err := m.Close(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for closing nonexistent plugin")
	}
}

func TestManagerListSorted(t *testing.T) {
	m := NewManager()
	names := m.List()
	if len(names) != 0 {
		t.Errorf("expected empty list, got %v", names)
	}
}

func TestManagerCloseAll(t *testing.T) {
	m := NewManager()
	err := m.CloseAll(context.Background())
	if err != nil {
		t.Errorf("CloseAll on empty manager: %v", err)
	}
}

func TestKindFromExt(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"x.py", "python"},
		{"x.js", "node"},
		{"x.mjs", "node"},
		{"x.cjs", "node"},
		{"x.sh", "shell"},
		{"x", "exec"},
		{"x.exe", "exec"},
	}
	for _, tt := range tests {
		got := kindFromExt(tt.path)
		if got != tt.want {
			t.Errorf("kindFromExt(%q): got %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestResolveArgvExec(t *testing.T) {
	got, err := resolveArgv("some-binary", "exec", nil)
	if err != nil {
		t.Fatalf("resolveArgv exec: %v", err)
	}
	if len(got) < 1 || got[0] != "some-binary" {
		t.Errorf("exec: got %v", got)
	}
}

func TestResolveArgvPython(t *testing.T) {
	got, err := resolveArgv("script.py", "python", []string{"--flag"})
	if err != nil {
		t.Skip("python not available: " + err.Error())
	}
	if len(got) < 2 {
		t.Errorf("python argv too short: %v", got)
	}
}