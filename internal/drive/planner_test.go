package drive

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParsePlanOutput_ValidJSON(t *testing.T) {
	raw := `{
		"todos": [
			{
				"id": "1",
				"title": "Setup project",
				"detail": "Initialize go mod and create base structure",
				"confidence": 0.9,
				"depends_on": []
			},
			{
				"id": "2",
				"title": "Add feature",
				"detail": "Implement the main feature",
				"confidence": 0.7,
				"depends_on": ["1"]
			}
		]
	}`

	todos, err := parsePlannerOutput(raw)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(todos) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(todos))
	}
	if todos[0].ID != "1" {
		t.Errorf("expected id=1, got %s", todos[0].ID)
	}
	if todos[1].DependsOn[0] != "1" {
		t.Errorf("expected depends_on=[1], got %v", todos[1].DependsOn)
	}
}

func TestParsePlanOutput_InvalidJSON(t *testing.T) {
	raw := `{invalid json}`
	_, err := parsePlannerOutput(raw)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParsePlanOutput_EmptyTodos(t *testing.T) {
	raw := `{"todos": []}`
	_, err := parsePlannerOutput(raw)
	if err == nil {
		t.Fatal("expected error for empty todos")
	}
	if !strings.Contains(err.Error(), "zero TODOs") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTodos_DuplicateID(t *testing.T) {
	todos := []Todo{
		{ID: "1", Title: "A", Detail: "x", Confidence: 0.8},
		{ID: "1", Title: "B", Detail: "y", Confidence: 0.9},
	}
	err := validateTodos(todos)
	if err == nil {
		t.Fatal("expected error for duplicate id")
	}
}

func TestValidateTodos_SelfLoop(t *testing.T) {
	todos := []Todo{
		{ID: "1", Title: "A", Detail: "x", Confidence: 0.8, DependsOn: []string{"1"}},
	}
	err := validateTodos(todos)
	if err == nil {
		t.Fatal("expected error for self-loop")
	}
}

func TestValidateTodos_OrphanDependency(t *testing.T) {
	todos := []Todo{
		{ID: "1", Title: "A", Detail: "x", Confidence: 0.8, DependsOn: []string{"99"}},
	}
	err := validateTodos(todos)
	if err == nil {
		t.Fatal("expected error for orphan dependency")
	}
}

func TestValidateTodos_EmptyID(t *testing.T) {
	todos := []Todo{
		{ID: "", Title: "A", Detail: "x", Confidence: 0.8},
	}
	err := validateTodos(todos)
	if err == nil {
		t.Fatal("expected error for empty id")
	}
}

func TestValidateTodos_EmptyTitle(t *testing.T) {
	todos := []Todo{
		{ID: "1", Title: "", Detail: "x", Confidence: 0.8},
	}
	err := validateTodos(todos)
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestValidateTodos_EmptyDetail(t *testing.T) {
	todos := []Todo{
		{ID: "1", Title: "A", Detail: "", Confidence: 0.8},
	}
	err := validateTodos(todos)
	if err == nil {
		t.Fatal("expected error for empty detail")
	}
}

func TestValidateTodos_InvalidConfidence(t *testing.T) {
	todos := []Todo{
		{ID: "1", Title: "A", Detail: "x", Confidence: 1.5},
	}
	err := validateTodos(todos)
	if err == nil {
		t.Fatal("expected error for invalid confidence > 1")
	}
}

func TestValidateTodos_ValidDAG(t *testing.T) {
	todos := []Todo{
		{ID: "1", Title: "A", Detail: "x", Confidence: 0.9, DependsOn: []string{}},
		{ID: "2", Title: "B", Detail: "y", Confidence: 0.8, DependsOn: []string{"1"}},
		{ID: "3", Title: "C", Detail: "z", Confidence: 0.85, DependsOn: []string{"1", "2"}},
	}
	err := validateTodos(todos)
	if err != nil {
		t.Fatalf("expected valid DAG, got: %v", err)
	}
}

func TestParsePlanOutput_WithPrefixCommentary(t *testing.T) {
	raw := "Sure, here's the plan:\n\n{\n  \"todos\": [\n    {\n      \"id\": \"1\",\n      \"title\": \"Init\",\n      \"detail\": \"Run go mod init\",\n      \"confidence\": 0.95,\n      \"depends_on\": []\n    }\n  ]\n}\n"
	todos, err := parsePlannerOutput(raw)
	if err != nil {
		t.Fatalf("expected no error with prefix commentary, got: %v", err)
	}
	if len(todos) != 1 {
		t.Fatalf("expected 1 todo, got %d", len(todos))
	}
	if todos[0].Title != "Init" {
		t.Errorf("expected title=Init, got %s", todos[0].Title)
	}
}

func TestValidateTodos_Cycles(t *testing.T) {
	// 1 -> 2 -> 3 -> 1
	todos := []Todo{
		{ID: "1", Title: "A", Detail: "x", Confidence: 0.9, DependsOn: []string{"3"}},
		{ID: "2", Title: "B", Detail: "y", Confidence: 0.8, DependsOn: []string{"1"}},
		{ID: "3", Title: "C", Detail: "z", Confidence: 0.85, DependsOn: []string{"2"}},
	}
	err := validateTodos(todos)
	if err == nil {
		t.Fatal("expected error for cycle")
	}
}

func TestPlannerSystemPrompt_NotEmpty(t *testing.T) {
	if plannerSystemPrompt == "" {
		t.Fatal("plannerSystemPrompt must not be empty")
	}
	if !strings.Contains(plannerSystemPrompt, "todos") {
		t.Fatal("plannerSystemPrompt should mention todos")
	}
}

func TestTodoJSONRoundtrip(t *testing.T) {
	orig := Todo{
		ID:         "42",
		Title:      "Test",
		Detail:     "Run the thing",
		Confidence: 0.75,
		DependsOn:  []string{"1", "2"},
	}
	data, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Todo
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != orig.ID || got.Title != orig.Title || got.Confidence != orig.Confidence {
		t.Errorf("roundtrip mismatch: got %+v", got)
	}
}

func TestParsePlanOutput_FindsJSONObject(t *testing.T) {
	// Model prepends "Here's the plan:" before JSON.
	raw := `Here's the plan:

{
  "todos": [{"id": "a", "title": "X", "detail": "y", "confidence": 0.9, "depends_on": []}]
}

More explanation after.`
	todos, err := parsePlannerOutput(raw)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(todos) != 1 || todos[0].ID != "a" {
		t.Errorf("unexpected: %+v", todos)
	}
}

func TestParsePlanOutput_NoJSONObject(t *testing.T) {
	raw := `No JSON here at all.`
	_, err := parsePlannerOutput(raw)
	if err == nil {
		t.Fatal("expected error when no JSON object present")
	}
}
