package supervisor

import (
	"encoding/json"
	"testing"
	"time"
)

// mockEmbedder implements Embedder for testing.
type mockEmbedder struct {
	saved []byte
}

func (m *mockEmbedder) Bucket() []byte { return []byte("test-runs") }

func (m *mockEmbedder) SaveRunJSON(key, data []byte) error {
	m.saved = data
	return nil
}

func (m *mockEmbedder) LoadRunJSON(key []byte) ([]byte, error) {
	return m.saved, nil
}

func TestSave_EmptyBase(t *testing.T) {
	emb := &mockEmbedder{}
	fields := &supervisorFields{
		Status:     "running",
		TotalSteps: 10,
	}
	err := Save(emb, "run-1", fields, nil)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if len(emb.saved) == 0 {
		t.Fatal("expected saved data")
	}
	var doc map[string]any
	if err := json.Unmarshal(emb.saved, &doc); err != nil {
		t.Fatalf("saved is not valid JSON: %v", err)
	}
	sv, ok := doc["sv"]
	if !ok {
		t.Fatal("expected sv field in saved JSON")
	}
	svMap, ok := sv.(map[string]any)
	if !ok {
		t.Fatal("sv should be a map")
	}
	if svMap["status"] != "running" {
		t.Errorf("status: got %v", svMap["status"])
	}
	if svMap["total_steps"].(float64) != 10 {
		t.Errorf("total_steps: got %v", svMap["total_steps"])
	}
}

func TestSave_WithBase(t *testing.T) {
	emb := &mockEmbedder{}
	baseJSON := []byte(`{"id":"run-1","status":"running"}`)
	fields := &supervisorFields{
		Status:     "done",
		TotalSteps: 5,
	}
	err := Save(emb, "run-1", fields, baseJSON)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(emb.saved, &doc); err != nil {
		t.Fatalf("saved is not valid JSON: %v", err)
	}
	if doc["id"] != "run-1" {
		t.Errorf("base id lost: got %v", doc["id"])
	}
	if doc["status"] != "running" {
		t.Errorf("base status lost: got %v", doc["status"])
	}
	if _, ok := doc["sv"]; !ok {
		t.Fatal("expected sv field")
	}
}

func TestSave_InvalidBaseJSON(t *testing.T) {
	emb := &mockEmbedder{}
	fields := &supervisorFields{}
	err := Save(emb, "run-1", fields, []byte(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid base JSON")
	}
}

func TestLoadSupervisorFields_Empty(t *testing.T) {
	got := LoadSupervisorFields(nil)
	if got != nil {
		t.Errorf("nil input: got %+v", got)
	}
	got = LoadSupervisorFields([]byte{})
	if got != nil {
		t.Errorf("empty input: got %+v", got)
	}
}

func TestLoadSupervisorFields_InvalidJSON(t *testing.T) {
	got := LoadSupervisorFields([]byte(`{invalid`))
	if got != nil {
		t.Errorf("invalid JSON: got %+v", got)
	}
}

func TestLoadSupervisorFields_NoSVField(t *testing.T) {
	got := LoadSupervisorFields([]byte(`{"id":"run-1","status":"done"}`))
	if got != nil {
		t.Errorf("no sv field: got %+v", got)
	}
}

func TestLoadSupervisorFields_WithSVField(t *testing.T) {
	runJSON := []byte(`{
		"id": "run-1",
		"status": "done",
		"sv": {
			"v": 1,
			"status": "done",
			"total_steps": 5,
			"total_tokens": 1000,
			"tasks_done": 3,
			"tasks_failed": 0,
			"tasks_skipped": 1
		}
	}`)
	got := LoadSupervisorFields(runJSON)
	if got == nil {
		t.Fatal("expected non-nil result")
	}
	if got.TotalSteps != 5 {
		t.Errorf("TotalSteps: got %d", got.TotalSteps)
	}
	if got.TasksDone != 3 {
		t.Errorf("TasksDone: got %d", got.TasksDone)
	}
	if got.EndedAt != (time.Time{}) {
		t.Errorf("EndedAt: got %v", got.EndedAt)
	}
}

func TestLoadSupervisorFields_ZeroSupervisorFields(t *testing.T) {
	// A run with sv: {} should return zero-value fields, not nil
	runJSON := []byte(`{"id":"run-1","sv":{}}`)
	got := LoadSupervisorFields(runJSON)
	if got == nil {
		t.Fatal("expected zero-value fields, not nil")
	}
}
