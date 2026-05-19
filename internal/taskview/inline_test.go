package taskview

import (
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

// mockStore implements a minimal task store for testing
type mockStore struct {
	tasks []*supervisor.Task
}

func (m *mockStore) ListTasks(opts taskstore.ListOptions) ([]*supervisor.Task, error) {
	return m.tasks, nil
}

func (m *mockStore) LoadTask(id string) (*supervisor.Task, error) {
	for _, t := range m.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, nil
}

func (m *mockStore) DeleteTask(id string) error {
	return nil
}

func TestStateIcon(t *testing.T) {
	tests := []struct {
		state    supervisor.TaskState
		expected string
	}{
		{supervisor.TaskDone, "✓"},
		{supervisor.TaskRunning, "…"},
		{supervisor.TaskBlocked, "✗"},
		{supervisor.TaskSkipped, "⤳"},
		{supervisor.TaskWaiting, "⧖"},
		{supervisor.TaskExternalReview, "⚠"},
		{supervisor.TaskState("unknown"), "○"},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			result := StateIcon(tt.state)
			if result != tt.expected {
				t.Errorf("StateIcon(%q) = %q, want %q", tt.state, result, tt.expected)
			}
		})
	}
}

func TestRenderNode(t *testing.T) {
	task := &supervisor.Task{
		ID:    "test-1",
		Title: "Test Task",
		State: supervisor.TaskRunning,
	}
	result := RenderNode(task)
	if result == "" {
		t.Error("RenderNode returned empty string")
	}
	// Should contain the title and state icon
}

func TestRenderList(t *testing.T) {
	tasks := []*supervisor.Task{
		{ID: "1", Title: "Task 1", State: supervisor.TaskDone},
		{ID: "2", Title: "Task 2", State: supervisor.TaskRunning},
	}
	result := RenderList(tasks)
	if result == "" {
		t.Error("RenderList returned empty string")
	}
}

func TestRenderList_empty(t *testing.T) {
	result := RenderList([]*supervisor.Task{})
	if result != "(no tasks)" {
		t.Errorf("RenderList(empty) = %q, want %q", result, "(no tasks)")
	}
}

func TestRenderList_nil(t *testing.T) {
	result := RenderList(nil)
	if result != "(no tasks)" {
		t.Errorf("RenderList(nil) = %q, want %q", result, "(no tasks)")
	}
}

func TestFormatDetail(t *testing.T) {
	task := &supervisor.Task{
		ID:       "test-1",
		Title:    "Test Task",
		State:    supervisor.TaskDone,
		Detail:   "Some detail",
		ParentID: "parent-1",
		Labels:   []string{"label1", "label2"},
	}
	result := FormatDetail(task)
	if result == "" {
		t.Error("FormatDetail returned empty string")
	}
}

func TestFormatDetail_empty(t *testing.T) {
	task := &supervisor.Task{
		ID:    "test-1",
		Title: "Test Task",
		State: supervisor.TaskDone,
	}
	result := FormatDetail(task)
	if result == "" {
		t.Error("FormatDetail returned empty string for minimal task")
	}
}

func TestFormatDetail_withConfidence(t *testing.T) {
	task := &supervisor.Task{
		ID:         "test-1",
		Title:      "Test Task",
		State:      supervisor.TaskDone,
		Confidence: 0.85,
	}
	result := FormatDetail(task)
	if result == "" {
		t.Error("FormatDetail returned empty string")
	}
}

func TestFormatDetail_withTimes(t *testing.T) {
	now := time.Now()
	task := &supervisor.Task{
		ID:        "test-1",
		Title:     "Test Task",
		State:     supervisor.TaskDone,
		StartedAt: now,
		EndedAt:   now,
	}
	result := FormatDetail(task)
	if result == "" {
		t.Error("FormatDetail returned empty string")
	}
}

func TestFormatDetail_withDependsOn(t *testing.T) {
	task := &supervisor.Task{
		ID:        "test-1",
		Title:     "Test Task",
		State:     supervisor.TaskBlocked,
		BlockedReason: "waiting",
		DependsOn: []string{"dep-1", "dep-2"},
	}
	result := FormatDetail(task)
	if result == "" {
		t.Error("FormatDetail returned empty string")
	}
}

func TestFormatDetail_withVerification(t *testing.T) {
	task := &supervisor.Task{
		ID:          "test-1",
		Title:       "Test Task",
		State:       supervisor.TaskDone,
		Verification: "passed",
	}
	result := FormatDetail(task)
	if result == "" {
		t.Error("FormatDetail returned empty string")
	}
}

func TestFormatDetail_withWorkerClass(t *testing.T) {
	task := &supervisor.Task{
		ID:          "test-1",
		Title:       "Test Task",
		State:       supervisor.TaskRunning,
		WorkerClass: "coder",
	}
	result := FormatDetail(task)
	if result == "" {
		t.Error("FormatDetail returned empty string")
	}
}

func TestFormatDetail_withSummary(t *testing.T) {
	task := &supervisor.Task{
		ID:      "test-1",
		Title:   "Test Task",
		State:   supervisor.TaskDone,
		Summary: "What was accomplished",
	}
	result := FormatDetail(task)
	if result == "" {
		t.Error("FormatDetail returned empty string")
	}
}

func TestFormatDetail_withError(t *testing.T) {
	task := &supervisor.Task{
		ID:    "test-1",
		Title: "Test Task",
		State: supervisor.TaskBlocked,
		Error: "something went wrong",
	}
	result := FormatDetail(task)
	if result == "" {
		t.Error("FormatDetail returned empty string")
	}
}