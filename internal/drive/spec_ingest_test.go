package drive

import (
	"testing"
)

func TestTodosFromSpec_BasicConversion(t *testing.T) {
	items := []map[string]any{
		{
			"id":             "phase-1-0",
			"title":          "Add the parser",
			"detail":         "Source: PLAN.md:5 · section: Phase 1",
			"kind":           "code",
			"worker_class":   "coder",
			"provider_tag":   "code",
			"read_only":      false,
			"status":         "pending",
			"source_section": "phase-1",
			"source_line":    5,
		},
		{
			"id":           "phase-1-1",
			"title":        "Verify migration",
			"kind":         "review",
			"worker_class": "reviewer",
			"read_only":    true,
			"status":       "pending",
		},
		// Item missing title — must be dropped.
		{
			"id":     "no-title",
			"detail": "ignored",
		},
		// Item with status=done — must keep terminal status.
		{
			"id":     "phase-1-2",
			"title":  "Already-done item",
			"status": "done",
		},
	}
	todos, dropped := TodosFromSpec(items)
	if dropped != 1 {
		t.Errorf("want 1 dropped (no title), got %d", dropped)
	}
	if len(todos) != 3 {
		t.Fatalf("want 3 todos, got %d", len(todos))
	}
	if todos[0].ID != "phase-1-0" || todos[0].Title != "Add the parser" {
		t.Errorf("first todo wrong: %+v", todos[0])
	}
	if todos[0].Origin != "spec" {
		t.Errorf("origin should be 'spec', got %q", todos[0].Origin)
	}
	if !todos[1].ReadOnly || todos[1].WorkerClass != "reviewer" {
		t.Errorf("review todo wrong: %+v", todos[1])
	}
	if todos[2].Status != TodoDone {
		t.Errorf("done item should keep status=done, got %q", todos[2].Status)
	}
}

func TestTodosFromSpec_StatusFallsBackToPending(t *testing.T) {
	items := []map[string]any{
		{"title": "no status field"},
		{"title": "weird status", "status": "lol"},
	}
	todos, dropped := TodosFromSpec(items)
	if dropped != 0 {
		t.Errorf("dropped should be 0, got %d", dropped)
	}
	for i, td := range todos {
		if td.Status != TodoPending {
			t.Errorf("todo %d should default to pending, got %q", i, td.Status)
		}
	}
}

func TestTodosFromSpec_AutoIDWhenMissing(t *testing.T) {
	items := []map[string]any{
		{"title": "first"},
		{"title": "second"},
	}
	todos, _ := TodosFromSpec(items)
	if len(todos) != 2 {
		t.Fatalf("want 2 todos, got %d", len(todos))
	}
	if todos[0].ID != "spec-0" || todos[1].ID != "spec-1" {
		t.Errorf("auto-id wrong: %q, %q", todos[0].ID, todos[1].ID)
	}
}

func TestTodosFromSpec_NilAndEmptyInput(t *testing.T) {
	if todos, dropped := TodosFromSpec(nil); len(todos) != 0 || dropped != 0 {
		t.Errorf("nil input should yield nothing, got %d todos / %d dropped", len(todos), dropped)
	}
	if todos, dropped := TodosFromSpec([]map[string]any{}); len(todos) != 0 || dropped != 0 {
		t.Errorf("empty input should yield nothing, got %d todos / %d dropped", len(todos), dropped)
	}
}

func TestAsMapHelpers_ToleratesWrongTypes(t *testing.T) {
	m := map[string]any{
		"str_as_int": 42,
		"bool_as_str": "true",
	}
	if got := asMapString(m, "str_as_int"); got != "" {
		t.Errorf("non-string value should yield empty string, got %q", got)
	}
	if got := asMapBool(m, "bool_as_str"); got {
		t.Errorf("non-bool value should yield false")
	}
	if got := asMapString(nil, "x"); got != "" {
		t.Errorf("nil map should yield empty string")
	}
	if got := asMapBool(nil, "x"); got {
		t.Errorf("nil map should yield false")
	}
}
