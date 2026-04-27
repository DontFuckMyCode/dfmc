package tui

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/drive"
)

func TestFormatMetricMap(t *testing.T) {
	cases := []struct {
		name   string
		input  map[string]int64
		expect string
	}{
		{"empty", map[string]int64{}, ""},
		{"single", map[string]int64{"a": 1}, "a=1"},
		{"multiple_sorted", map[string]int64{"b": 2, "a": 1}, "a=1,b=2"},
		{"negative", map[string]int64{"x": -5}, "x=-5"},
	}
	for _, c := range cases {
		got := formatMetricMap(c.input)
		if got != c.expect {
			t.Errorf("%s: formatMetricMap(%v) = %q, want %q", c.name, c.input, got, c.expect)
		}
	}
}

func TestTodoStatusIcon(t *testing.T) {
	cases := []struct {
		status drive.TodoStatus
		want   string
	}{
		{drive.TodoPending, "⏳"},
		{drive.TodoRunning, "\U0001f504"},
		{drive.TodoDone, "✅"},
		{drive.TodoBlocked, "❌"},
		{drive.TodoSkipped, "⏭"},
		{drive.TodoStatus("unknown"), "○"},
	}
	for _, c := range cases {
		got := todoStatusIcon(c.status)
		if got != c.want {
			t.Errorf("todoStatusIcon(%q) = %q, want %q", c.status, got, c.want)
		}
	}
}

func TestRenderWorkflowTreeRows_NilRun(t *testing.T) {
	m := newCoverageModel(t)
	rows := m.renderWorkflowTreeRows(nil, 80)
	if len(rows) != 1 {
		t.Errorf("nil run: got %d rows, want 1", len(rows))
	}
}

func TestRenderWorkflowTreeRows_EmptyTodos(t *testing.T) {
	m := newCoverageModel(t)
	run := &drive.Run{ID: "drv-empty", Todos: []drive.Todo{}}
	rows := m.renderWorkflowTreeRows(run, 80)
	if len(rows) != 1 {
		t.Errorf("empty todos: got %d rows, want 1", len(rows))
	}
	if rows[0] == "" {
		t.Error("empty todo message should not be empty string")
	}
}

func TestRenderWorkflowTreeRows_WithTodos(t *testing.T) {
	m := newCoverageModel(t)
	run := &drive.Run{
		ID: "drv-test",
		Todos: []drive.Todo{
			{ID: "T1", Title: "first task", Status: drive.TodoDone},
			{ID: "T2", Title: "second task", Status: drive.TodoRunning},
			{ID: "T3", Title: "third task", Status: drive.TodoBlocked, Error: "permission denied"},
		},
	}
	rows := m.renderWorkflowTreeRows(run, 80)
	if len(rows) < 3 {
		t.Errorf("got %d rows, want at least 3", len(rows))
	}
}

func TestParseGrepChatArgs(t *testing.T) {
	cases := []struct {
		args   []string
		want   string
		err    bool
	}{
		{[]string{}, "", true},
		{[]string{""}, "", true},
		{[]string{"foo"}, "foo", false},
		{[]string{"foo", "bar", "baz"}, "foo bar baz", false},
	}
	for _, c := range cases {
		got, err := parseGrepChatArgs(c.args)
		if c.err {
			if err == nil {
				t.Errorf("parseGrepChatArgs(%v): expected error, got nil", c.args)
			}
		} else {
			if err != nil {
				t.Errorf("parseGrepChatArgs(%v): unexpected error: %v", c.args, err)
				continue
			}
			if got["pattern"] != c.want {
				t.Errorf("pattern: got %v, want %q", got["pattern"], c.want)
			}
			if got["max_results"] != 80 {
				t.Errorf("max_results: got %v, want 80", got["max_results"])
			}
		}
	}
}

func TestParseRunCommandChatArgs(t *testing.T) {
	cases := []struct {
		args  []string
		want  string
		err   bool
	}{
		{[]string{}, "", true},
		{[]string{""}, "", true},
		{[]string{"ls"}, "ls", false},
		{[]string{"ls", "-la"}, "ls", false},
		{[]string{"npm", "run", "test"}, "npm", false},
	}
	for _, c := range cases {
		got, err := parseRunCommandChatArgs(c.args)
		if c.err {
			if err == nil {
				t.Errorf("expected error for %v", c.args)
			}
		} else {
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				continue
			}
			if got["command"] != c.want {
				t.Errorf("command: got %v, want %q", got["command"], c.want)
			}
		}
	}
}