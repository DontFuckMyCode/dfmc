package cli

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

func TestParseAutoApproveFlag_Empty(t *testing.T) {
	if got := parseAutoApproveFlag(""); got != nil {
		t.Errorf("empty input: got %v want nil", got)
	}
}

func TestParseAutoApproveFlag_Whitespace(t *testing.T) {
	if got := parseAutoApproveFlag("   "); got != nil {
		t.Errorf("whitespace: got %v want nil", got)
	}
}

func TestParseAutoApproveFlag_Single(t *testing.T) {
	got := parseAutoApproveFlag("read_file")
	if len(got) != 1 || got[0] != "read_file" {
		t.Errorf("single: got %v", got)
	}
}

func TestParseAutoApproveFlag_Multiple(t *testing.T) {
	got := parseAutoApproveFlag("read_file,write_file,edit_file")
	if len(got) != 3 {
		t.Errorf("got %v want 3 items", got)
	}
}

func TestParseAutoApproveFlag_WithSpaces(t *testing.T) {
	got := parseAutoApproveFlag("read_file , write_file , edit_file")
	if len(got) != 3 || got[0] != "read_file" || got[1] != "write_file" || got[2] != "edit_file" {
		t.Errorf("got %v", got)
	}
}

func TestParseAutoApproveFlag_EmptyEntriesFiltered(t *testing.T) {
	got := parseAutoApproveFlag("read_file,,write_file")
	if len(got) != 2 {
		t.Errorf("got %v want 2 (empty entries filtered)", got)
	}
}

func TestParseAutoApproveFlag_AllEmpty(t *testing.T) {
	got := parseAutoApproveFlag(",,,")
	if got != nil {
		t.Errorf("got %v want nil", got)
	}
}

func TestMultiString_String(t *testing.T) {
	var m multiString
	m = []string{"a", "b", "c"}
	if got := m.String(); got != "a,b,c" {
		t.Errorf("String(): got %q want 'a,b,c'", got)
	}
}

func TestMultiString_String_Empty(t *testing.T) {
	var m multiString
	if got := m.String(); got != "" {
		t.Errorf("empty String(): got %q want ''", got)
	}
}

func TestMultiString_Set(t *testing.T) {
	var m multiString
	if err := m.Set("first"); err != nil {
		t.Errorf("Set failed: %v", err)
	}
	if err := m.Set("second"); err != nil {
		t.Errorf("Set failed: %v", err)
	}
	if len(m) != 2 || m[0] != "first" || m[1] != "second" {
		t.Errorf("m = %v want [first, second]", m)
	}
}

func TestParseRouteFlags_Empty(t *testing.T) {
	got, err := parseRouteFlags(nil)
	if err != nil || got != nil {
		t.Errorf("nil input: got %v err %v", got, err)
	}
}

func TestParseRouteFlags_EmptySlice(t *testing.T) {
	got, err := parseRouteFlags([]string{})
	if err != nil || got != nil {
		t.Errorf("empty slice: got %v err %v", got, err)
	}
}

func TestParseRouteFlags_Single(t *testing.T) {
	got, err := parseRouteFlags([]string{"plan=opus"})
	if err != nil || got["plan"] != "opus" {
		t.Errorf("got %v err %v", got, err)
	}
}

func TestParseRouteFlags_Multiple(t *testing.T) {
	got, err := parseRouteFlags([]string{"plan=opus", "code=sonnet", "test=haiku"})
	if err != nil || len(got) != 3 {
		t.Errorf("got %v err %v", got, err)
	}
}

func TestParseRouteFlags_TrimsSpaces(t *testing.T) {
	got, err := parseRouteFlags([]string{" plan = opus ", " code = sonnet "})
	if err != nil || got["plan"] != "opus" || got["code"] != "sonnet" {
		t.Errorf("got %v err %v", got, err)
	}
}

func TestParseRouteFlags_RejectsNoEquals(t *testing.T) {
	_, err := parseRouteFlags([]string{"planopus"})
	if err == nil {
		t.Error("expected error for missing =")
	}
}

func TestParseRouteFlags_RejectsEmptyTag(t *testing.T) {
	_, err := parseRouteFlags([]string{"=opus"})
	if err == nil {
		t.Error("expected error for empty tag")
	}
}

func TestParseRouteFlags_RejectsEmptyProfile(t *testing.T) {
	_, err := parseRouteFlags([]string{"plan="})
	if err == nil {
		t.Error("expected error for empty profile")
	}
}

func TestParseRouteFlags_RejectsTrailingEquals(t *testing.T) {
	_, err := parseRouteFlags([]string{"plan="})
	if err == nil {
		t.Error("expected error for trailing =")
	}
}

func TestTodoMarker(t *testing.T) {
	cases := []struct {
		status  drive.TodoStatus
		want    string
	}{
		{drive.TodoDone, "[x]"},
		{drive.TodoBlocked, "[!]"},
		{drive.TodoSkipped, "[-]"},
		{drive.TodoRunning, "[*]"},
		{drive.TodoPending, "[ ]"},
		{"unknown", "[ ]"},
	}
	for _, c := range cases {
		got := todoMarker(c.status)
		if got != c.want {
			t.Errorf("todoMarker(%q) = %q want %q", c.status, got, c.want)
		}
	}
}

func TestFormatLaneCaps_Empty(t *testing.T) {
	if got := formatLaneCaps(nil, nil); got != "" {
		t.Errorf("nil inputs: got %q want empty", got)
	}
	if got := formatLaneCaps([]string{}, map[string]int{}); got != "" {
		t.Errorf("empty inputs: got %q want empty", got)
	}
}

func TestFormatLaneCaps_Single(t *testing.T) {
	got := formatLaneCaps([]string{"code"}, map[string]int{"code": 3})
	if got != "code=3" {
		t.Errorf("single lane: got %q", got)
	}
}

func TestFormatLaneCaps_Multiple(t *testing.T) {
	got := formatLaneCaps([]string{"plan", "code", "test"}, map[string]int{"plan": 2, "code": 3, "test": 1})
	if got != "plan=2, code=3, test=1" {
		t.Errorf("multiple lanes: got %q", got)
	}
}

func TestFormatLaneCaps_SkipsUnknownLanes(t *testing.T) {
	got := formatLaneCaps([]string{"code", "unknown"}, map[string]int{"code": 2})
	if got != "code=2" {
		t.Errorf("skip unknown: got %q", got)
	}
}

func TestFormatLaneCaps_IncludesZeroCaps(t *testing.T) {
	got := formatLaneCaps([]string{"code", "plan"}, map[string]int{"code": 2, "plan": 0})
	// cap=0 is still in the map so it IS included (no > 0 filter in impl)
	if got != "code=2, plan=0" {
		t.Errorf("include zero cap: got %q", got)
	}
}

func TestFormatLaneCaps_TrimsWhitespace(t *testing.T) {
	got := formatLaneCaps([]string{" code ", " plan "}, map[string]int{"code": 1, "plan": 2})
	if got != "code=1, plan=2" {
		t.Errorf("trim whitespace: got %q", got)
	}
}

func TestRenderDriveEventLine(t *testing.T) {
	r, w, _ := os.Pipe()
	// Test each event type branches without panic
	cases := []struct {
		typ     string
		payload map[string]any
	}{
		{drive.EventRunStart, map[string]any{"run_id": "drv-test-123", "task": "my task"}},
		{drive.EventPlanDone, map[string]any{"run_id": "drv-test-123", "todo_count": 5}},
		{drive.EventPlanAugment, map[string]any{"run_id": "drv-test-123", "added": 2}},
		{drive.EventTodoStart, map[string]any{"run_id": "drv-test-123", "todo_id": "T1", "title": "do it", "attempt": 1}},
		{drive.EventTodoDone, map[string]any{"run_id": "drv-test-123", "todo_id": "T1", "duration_ms": 100, "tool_calls": 5}},
		{drive.EventTodoBlocked, map[string]any{"run_id": "drv-test-123", "todo_id": "T1", "error": "oops", "attempts": 3}},
		{drive.EventTodoSkipped, map[string]any{"run_id": "drv-test-123", "todo_id": "T1", "reason": "not needed"}},
		{drive.EventTodoRetry, map[string]any{"run_id": "drv-test-123", "todo_id": "T1", "attempt": 2}},
		{drive.EventRunDone, map[string]any{"run_id": "drv-test-123", "status": "done", "done": 5, "blocked": 0, "skipped": 1, "duration_ms": 1234}},
		{drive.EventRunStopped, map[string]any{"run_id": "drv-test-123", "status": "stopped", "done": 3, "blocked": 0, "skipped": 2, "duration_ms": 999}},
		{drive.EventRunFailed, map[string]any{"run_id": "drv-test-123", "status": "failed", "reason": "fatal error", "done": 2, "blocked": 1, "skipped": 3, "duration_ms": 500}},
		{"drive:completely:unknown", map[string]any{"run_id": "drv-test-123"}},
	}
	for _, c := range cases {
		renderDriveEventLine(w, c.typ, c.payload)
	}
	w.Close()
	out, _ := io.ReadAll(r)
	r.Close()
	if len(out) == 0 {
		t.Errorf("event %q produced no output", cases[0].typ)
	}
}

func TestPrintDriveHelp(t *testing.T) {
	// Capture stdout via redirect
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	printDriveHelp()
	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)
	if len(out) == 0 {
		t.Error("printDriveHelp produced no output")
	}
	if !bytes.Contains(out, []byte("dfmc drive")) {
		t.Error("expected 'dfmc drive' in help output")
	}
}

func TestRenderDriveSummary(t *testing.T) {
	run := &drive.Run{
		ID:     "drv-summ-test",
		Task:   "test task summary",
		Status: drive.RunDone,
		Todos: []drive.Todo{
			{ID: "T1", Title: "first todo", Status: drive.TodoDone, Brief: "completed"},
			{ID: "T2", Title: "second todo", Status: drive.TodoBlocked, Error: "Permission denied"},
			{ID: "T3", Title: "skipped todo", Status: drive.TodoSkipped},
		},
	}
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	renderDriveSummary(w, run)
	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)
	if len(out) == 0 {
		t.Fatal("renderDriveSummary produced no output")
	}
	if !bytes.Contains(out, []byte("drv-summ-test")) {
		t.Error("expected run ID in output")
	}
	if !bytes.Contains(out, []byte("test task summary")) {
		t.Error("expected task in output")
	}
	if !bytes.Contains(out, []byte("[x]")) {
		t.Error("expected done marker in output")
	}
}

func TestRenderDriveSummary_Empty(t *testing.T) {
	run := &drive.Run{
		ID:     "drv-empty",
		Task:   "empty run",
		Status: drive.RunStopped,
		Todos:  []drive.Todo{},
	}
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	renderDriveSummary(w, run)
	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)
	if len(out) == 0 {
		t.Fatal("renderDriveSummary produced no output")
	}
}