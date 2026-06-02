package bridge

import (
	"testing"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/drive"
	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

func TestNormalizeWorkerClass(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"planner", string(supervisor.WorkerPlanner)},
		{"researcher", string(supervisor.WorkerResearcher)},
		{"reviewer", string(supervisor.WorkerReviewer)},
		{"tester", string(supervisor.WorkerTester)},
		{"security", string(supervisor.WorkerSecurity)},
		{"synthesizer", string(supervisor.WorkerSynthesizer)},
		{"verifier", string(supervisor.WorkerVerifier)},
		{"coder", string(supervisor.WorkerCoder)},
		// Case / whitespace tolerance (the switch lowercases+trims).
		{"  REVIEWER  ", string(supervisor.WorkerReviewer)},
		{"Planner", string(supervisor.WorkerPlanner)},
		// Unknown / empty fall back to the safe coder default.
		{"", string(supervisor.WorkerCoder)},
		{"wizard", string(supervisor.WorkerCoder)},
	}
	for _, c := range cases {
		if got := normalizeWorkerClass(c.in); got != c.want {
			t.Errorf("normalizeWorkerClass(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNormalizeVerification(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"none", string(supervisor.VerifyNone)},
		{"light", string(supervisor.VerifyLight)},
		{"deep", string(supervisor.VerifyDeep)},
		{"  DEEP ", string(supervisor.VerifyDeep)},
		// Unknown / empty fall back to required (the safe, stricter default).
		{"", string(supervisor.VerifyRequired)},
		{"required", string(supervisor.VerifyRequired)},
		{"bogus", string(supervisor.VerifyRequired)},
	}
	for _, c := range cases {
		if got := normalizeVerification(c.in); got != c.want {
			t.Errorf("normalizeVerification(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestTaskFromDriveTodo verifies every field is carried across, the
// worker-class / verification strings are normalized, and the slice
// fields are independent copies (mutating the source todo afterwards must
// not bleed into the produced task).
func TestTaskFromDriveTodo(t *testing.T) {
	start := time.Now().Add(-time.Hour)
	end := time.Now()
	todo := drive.Todo{
		ID:            "t1",
		ParentID:      "p0",
		Origin:        "planner",
		Title:         "do the thing",
		Detail:        "in detail",
		Status:        drive.TodoStatus("running"),
		DependsOn:     []string{"a", "b"},
		FileScope:     []string{"x.go"},
		ReadOnly:      true,
		ProviderTag:   "code",
		WorkerClass:   "REVIEWER", // normalized -> reviewer
		Skills:        []string{"go"},
		AllowedTools:  []string{"read_file"},
		Labels:        []string{"urgent"},
		Verification:  "bogus", // normalized -> required
		Confidence:    0.75,
		Brief:         "a brief",
		Error:         "boom",
		BlockedReason: drive.BlockReason("deadlock"),
		Attempts:      3,
		StartedAt:     start,
		EndedAt:       end,
	}

	task := TaskFromDriveTodo(todo)

	if task.ID != "t1" || task.ParentID != "p0" || task.Origin != "planner" {
		t.Errorf("identity fields mismatch: %+v", task)
	}
	if task.Title != "do the thing" || task.Detail != "in detail" {
		t.Errorf("title/detail mismatch: %+v", task)
	}
	if string(task.State) != "running" {
		t.Errorf("State = %q, want running", task.State)
	}
	if string(task.WorkerClass) != string(supervisor.WorkerReviewer) {
		t.Errorf("WorkerClass = %q, want reviewer (normalized)", task.WorkerClass)
	}
	if string(task.Verification) != string(supervisor.VerifyRequired) {
		t.Errorf("Verification = %q, want required (normalized fallback)", task.Verification)
	}
	if task.BlockedReason != "deadlock" {
		t.Errorf("BlockedReason = %q, want deadlock", task.BlockedReason)
	}
	if task.Summary != "a brief" {
		t.Errorf("Summary = %q, want 'a brief' (from Brief)", task.Summary)
	}
	if !task.ReadOnly || task.ProviderTag != "code" || task.Confidence != 0.75 ||
		task.Error != "boom" || task.Attempts != 3 {
		t.Errorf("scalar passthrough mismatch: %+v", task)
	}
	if !task.StartedAt.Equal(start) || !task.EndedAt.Equal(end) {
		t.Errorf("timestamp mismatch: started=%v ended=%v", task.StartedAt, task.EndedAt)
	}

	// Defensive-copy check: the produced slices must not alias the source.
	todo.DependsOn[0] = "MUTATED"
	todo.Skills[0] = "MUTATED"
	if task.DependsOn[0] != "a" {
		t.Errorf("DependsOn aliased the source slice: %v", task.DependsOn)
	}
	if task.Skills[0] != "go" {
		t.Errorf("Skills aliased the source slice: %v", task.Skills)
	}
}

func TestRunFromDrive_Nil(t *testing.T) {
	got := RunFromDrive(nil)
	if got.ID != "" || len(got.Tasks) != 0 {
		t.Fatalf("nil run should yield empty supervisor.Run, got %+v", got)
	}
}

func TestRunFromDrive_MapsTodos(t *testing.T) {
	run := &drive.Run{
		ID:     "r1",
		Task:   "ship it",
		Status: drive.RunStatus("running"),
		Reason: "because",
		Todos: []drive.Todo{
			{ID: "t1", Title: "first", WorkerClass: "tester"},
			{ID: "t2", Title: "second"}, // empty worker class -> coder
		},
	}
	out := RunFromDrive(run)
	if out.ID != "r1" || out.Task != "ship it" || out.Status != "running" || out.Reason != "because" {
		t.Errorf("run header mismatch: %+v", out)
	}
	if len(out.Tasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(out.Tasks))
	}
	if out.Tasks[0].ID != "t1" || string(out.Tasks[0].WorkerClass) != string(supervisor.WorkerTester) {
		t.Errorf("task[0] mismatch: %+v", out.Tasks[0])
	}
	if string(out.Tasks[1].WorkerClass) != string(supervisor.WorkerCoder) {
		t.Errorf("task[1] worker class = %q, want coder default", out.Tasks[1].WorkerClass)
	}
}
