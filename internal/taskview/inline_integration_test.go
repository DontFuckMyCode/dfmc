package taskview

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
	"github.com/dontfuckmycode/dfmc/internal/taskstore"
)

func tempDBForTaskview(t *testing.T) *sql.DB {
	tmp := t.TempDir() + "/taskview_test.db"
	db, err := sql.Open("sqlite", tmp+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS "tasks" (key TEXT PRIMARY KEY, value BLOB)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedTask(t *testing.T, s *taskstore.Store, task *supervisor.Task) {
	t.Helper()
	if err := s.SaveTask(task); err != nil {
		t.Fatalf("SaveTask %s: %v", task.ID, err)
	}
}

func TestList_EmptyStore(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	got := List(s)
	if got != "(no tasks)" {
		t.Fatalf("List(empty) = %q, want %q", got, "(no tasks)")
	}
}

func TestList_WithTasks(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	seedTask(t, s, &supervisor.Task{ID: "t1", Title: "Alpha", State: supervisor.TaskDone})
	seedTask(t, s, &supervisor.Task{ID: "t2", Title: "Beta", State: supervisor.TaskRunning})
	got := List(s)
	if !strings.Contains(got, "Alpha") {
		t.Error("List should contain Alpha")
	}
	if !strings.Contains(got, "Beta") {
		t.Error("List should contain Beta")
	}
}

func TestRoots_FiltersToRootTasks(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	seedTask(t, s, &supervisor.Task{ID: "r1", Title: "Root A", State: supervisor.TaskPending, StartedAt: time.Now()})
	seedTask(t, s, &supervisor.Task{ID: "r2", Title: "Root B", State: supervisor.TaskPending, StartedAt: time.Now()})
	seedTask(t, s, &supervisor.Task{ID: "c1", Title: "Child of A", ParentID: "r1", State: supervisor.TaskRunning, StartedAt: time.Now()})
	got := Roots(s)
	if !strings.Contains(got, "Root A") {
		t.Error("Roots should contain Root A")
	}
	if !strings.Contains(got, "Root B") {
		t.Error("Roots should contain Root B")
	}
	if strings.Contains(got, "Child of A") {
		t.Error("Roots should NOT contain children")
	}
}

func TestRoots_Empty(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	got := Roots(s)
	if got != "(no tasks)" {
		t.Fatalf("Roots(empty) = %q, want %q", got, "(no tasks)")
	}
}

func TestTree_AllRoots(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	seedTask(t, s, &supervisor.Task{ID: "r1", Title: "Root", State: supervisor.TaskPending, StartedAt: time.Now()})
	seedTask(t, s, &supervisor.Task{ID: "c1", Title: "Child1", ParentID: "r1", State: supervisor.TaskRunning, StartedAt: time.Now()})
	seedTask(t, s, &supervisor.Task{ID: "gc1", Title: "Grandchild", ParentID: "c1", State: supervisor.TaskDone, StartedAt: time.Now()})
	got := Tree(s, "")
	if !strings.Contains(got, "Root") {
		t.Error("Tree should contain root")
	}
	if !strings.Contains(got, "Child1") {
		t.Error("Tree should contain child")
	}
	if !strings.Contains(got, "Grandchild") {
		t.Error("Tree should contain grandchild")
	}
	// Verify indentation: grandchild should have deeper indent than child
	lines := strings.Split(got, "\n")
	var childLine, gcLine string
	for _, l := range lines {
		if strings.Contains(l, "Child1") {
			childLine = l
		}
		if strings.Contains(l, "Grandchild") {
			gcLine = l
		}
	}
	if childLine == "" || gcLine == "" {
		t.Fatal("missing child or grandchild lines in tree output")
	}
	// Grandchild should have more leading whitespace than child
	childIndent := len(childLine) - len(strings.TrimLeft(childLine, " "))
	gcIndent := len(gcLine) - len(strings.TrimLeft(gcLine, " "))
	if gcIndent <= childIndent {
		t.Errorf("grandchild indent (%d) should be > child indent (%d)", gcIndent, childIndent)
	}
}

func TestTree_SpecificRoot(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	seedTask(t, s, &supervisor.Task{ID: "r1", Title: "Root A", State: supervisor.TaskPending, StartedAt: time.Now()})
	seedTask(t, s, &supervisor.Task{ID: "r2", Title: "Root B", State: supervisor.TaskPending, StartedAt: time.Now()})
	seedTask(t, s, &supervisor.Task{ID: "c1", Title: "Child A1", ParentID: "r1", State: supervisor.TaskRunning, StartedAt: time.Now()})
	got := Tree(s, "r1")
	if !strings.Contains(got, "Root A") {
		t.Error("Tree(r1) should contain Root A")
	}
	if strings.Contains(got, "Root B") {
		t.Error("Tree(r1) should NOT contain Root B")
	}
	if !strings.Contains(got, "Child A1") {
		t.Error("Tree(r1) should contain child of r1")
	}
}

func TestTree_SpecificRootNotFound(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	got := Tree(s, "nonexistent")
	if !strings.Contains(got, "task not found") {
		t.Fatalf("Tree(nonexistent) = %q, want 'task not found'", got)
	}
}

func TestTree_NoTasks(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	got := Tree(s, "")
	if got != "(no tasks)" {
		t.Fatalf("Tree(empty) = %q, want %q", got, "(no tasks)")
	}
}

func TestDetail_ExistingTask(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	now := time.Now()
	seedTask(t, s, &supervisor.Task{
		ID:          "d1",
		Title:       "Auth refactor",
		State:       supervisor.TaskRunning,
		Detail:      "Rotate refresh tokens",
		WorkerClass: supervisor.WorkerCoder,
		Labels:      []string{"auth", "security"},
		StartedAt:   now,
	})
	got := Detail(s, "d1")
	if !strings.Contains(got, "Auth refactor") {
		t.Error("Detail should contain title")
	}
	if !strings.Contains(got, "running") {
		t.Error("Detail should contain state")
	}
	if !strings.Contains(got, "Rotate refresh tokens") {
		t.Error("Detail should contain detail field")
	}
	if !strings.Contains(got, "coder") {
		t.Error("Detail should contain worker class")
	}
}

func TestDetail_NotFound(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	got := Detail(s, "ghost")
	if !strings.Contains(got, "task not found") {
		t.Fatalf("Detail(ghost) = %q, want 'task not found'", got)
	}
}

func TestClearNonDrive_EmptyStore(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	got := ClearNonDrive(s)
	if !strings.Contains(got, "already empty") {
		t.Fatalf("ClearNonDrive(empty) = %q, want 'already empty'", got)
	}
}

func TestClearNonDrive_DeletesNonDriveTasks(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	seedTask(t, s, &supervisor.Task{ID: "t1", Title: "Standalone", State: supervisor.TaskDone, StartedAt: time.Now()})
	seedTask(t, s, &supervisor.Task{ID: "t2", Title: "Another", State: supervisor.TaskPending, StartedAt: time.Now()})
	got := ClearNonDrive(s)
	if !strings.Contains(got, "Cleared 2 task") {
		t.Fatalf("ClearNonDrive = %q, want mention of 2 cleared tasks", got)
	}
	// Verify tasks are gone
	list, _ := s.ListTasks(taskstore.ListOptions{})
	if len(list) != 0 {
		t.Fatalf("store should be empty after clear, got %d tasks", len(list))
	}
}

func TestClearNonDrive_SkipsDriveOwnedTasks(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	seedTask(t, s, &supervisor.Task{ID: "t1", Title: "Standalone", State: supervisor.TaskDone, StartedAt: time.Now()})
	seedTask(t, s, &supervisor.Task{ID: "d1", Title: "Drive task", RunID: "run-abc", State: supervisor.TaskRunning, StartedAt: time.Now()})
	got := ClearNonDrive(s)
	if !strings.Contains(got, "Cleared 1 task") {
		t.Fatalf("ClearNonDrive = %q, want 1 cleared", got)
	}
	if !strings.Contains(got, "1 drive-owned") {
		t.Fatalf("ClearNonDrive = %q, want mention of 1 drive-owned kept", got)
	}
	// Drive task should remain
	list, _ := s.ListTasks(taskstore.ListOptions{})
	if len(list) != 1 || list[0].ID != "d1" {
		t.Fatalf("expected drive task to remain, got %d tasks", len(list))
	}
}

func TestClearNonDrive_AllDriveOwned(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	seedTask(t, s, &supervisor.Task{ID: "d1", Title: "Drive task 1", RunID: "run-1", State: supervisor.TaskRunning, StartedAt: time.Now()})
	seedTask(t, s, &supervisor.Task{ID: "d2", Title: "Drive task 2", RunID: "run-2", State: supervisor.TaskRunning, StartedAt: time.Now()})
	got := ClearNonDrive(s)
	if !strings.Contains(got, "Cleared 0") {
		t.Fatalf("ClearNonDrive = %q, want 0 cleared", got)
	}
	if !strings.Contains(got, "2 drive-owned") {
		t.Fatalf("ClearNonDrive = %q, want mention of 2 drive-owned kept", got)
	}
}

func TestRenderNodeDepth(t *testing.T) {
	task := &supervisor.Task{ID: "x", Title: "Nested", State: supervisor.TaskDone}
	got := renderNodeDepth(task, 2)
	if !strings.Contains(got, "✓") {
		t.Error("renderNodeDepth should contain state icon")
	}
	if !strings.Contains(got, "Nested") {
		t.Error("renderNodeDepth should contain title")
	}
	// Check that indentation is applied: depth=2 means 4 spaces prefix
	indent := len(got) - len(strings.TrimLeft(got, " "))
	if indent != 4 {
		t.Errorf("renderNodeDepth(depth=2) indent = %d spaces, want 4", indent)
	}
}

func TestAddChildren(t *testing.T) {
	parent := &supervisor.Task{ID: "p", Title: "Parent", State: supervisor.TaskPending}
	child1 := &supervisor.Task{ID: "c1", Title: "Child1", ParentID: "p", State: supervisor.TaskRunning}
	child2 := &supervisor.Task{ID: "c2", Title: "Child2", ParentID: "p", State: supervisor.TaskDone}
	grandchild := &supervisor.Task{ID: "gc1", Title: "GC1", ParentID: "c1", State: supervisor.TaskBlocked}

	all := []*supervisor.Task{parent, child1, child2, grandchild}

	var b strings.Builder
	addChildren(&b, all, "p", 1)

	got := b.String()
	if !strings.Contains(got, "Child1") {
		t.Error("addChildren should contain Child1")
	}
	if !strings.Contains(got, "Child2") {
		t.Error("addChildren should contain Child2")
	}
	if !strings.Contains(got, "GC1") {
		t.Error("addChildren should contain grandchild GC1")
	}
}

func TestTree_MultipleRootsSeparated(t *testing.T) {
	s := taskstore.NewStore(tempDBForTaskview(t))
	seedTask(t, s, &supervisor.Task{ID: "r1", Title: "Root1", State: supervisor.TaskDone, StartedAt: time.Now()})
	seedTask(t, s, &supervisor.Task{ID: "r2", Title: "Root2", State: supervisor.TaskRunning, StartedAt: time.Now()})
	seedTask(t, s, &supervisor.Task{ID: "c1", Title: "ChildOf1", ParentID: "r1", State: supervisor.TaskPending, StartedAt: time.Now()})
	got := Tree(s, "")

	// Both roots should appear
	if !strings.Contains(got, "Root1") {
		t.Error("Tree should contain Root1")
	}
	if !strings.Contains(got, "Root2") {
		t.Error("Tree should contain Root2")
	}
	// Child of r1 should appear
	if !strings.Contains(got, "ChildOf1") {
		t.Error("Tree should contain ChildOf1")
	}
	// Verify blank line separates roots
	if !strings.Contains(got, "\n\n") {
		t.Error("Tree should have blank line between roots")
	}
}

func TestFormatDetail_AllFields(t *testing.T) {
	now := time.Now()
	task := &supervisor.Task{
		ID:            "full",
		Title:         "Full Task",
		State:         supervisor.TaskBlocked,
		Detail:        "detailed info",
		ParentID:      "parent-id",
		DependsOn:     []string{"dep1", "dep2"},
		BlockedReason: "missing dependency",
		WorkerClass:   supervisor.WorkerReviewer,
		Labels:        []string{"critical", "infra"},
		Verification:  "deep",
		Confidence:    0.72,
		Summary:       "what was done",
		Error:         "boom",
		StartedAt:     now,
		EndedAt:       now,
	}
	got := FormatDetail(task)

	checks := []string{
		"Full Task",
		"blocked",
		"detailed info",
		"parent-id",
		"dep1, dep2",
		"missing dependency",
		"reviewer",
		"critical, infra",
		"deep",
		"72%",
		"what was done",
		"boom",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("FormatDetail missing %q in output:\n%s", want, got)
		}
	}
}
