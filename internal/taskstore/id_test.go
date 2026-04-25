package taskstore

import (
	"strings"
	"testing"
)

func TestNewTaskID(t *testing.T) {
	id := NewTaskID()
	if id == "" {
		t.Fatal("NewTaskID() returned empty string")
	}
	if !strings.HasPrefix(id, "tsk-") {
		t.Errorf("NewTaskID() = %q, want prefix 'tsk-'", id)
	}
}

func TestNewTaskID_Unique(t *testing.T) {
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := NewTaskID()
		if ids[id] {
			t.Fatalf("duplicate ID generated: %s", id)
		}
		ids[id] = true
	}
}
