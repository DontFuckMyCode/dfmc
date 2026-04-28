package supervisor

import (
	"encoding/json"
	"fmt"
	"time"
)

// Persistence manages supervisor run state in bbolt. The schema extends
// the drive.Run bucket with supervisor-specific fields on read/write so
// existing drive runs are still loadable (with zero-valued supervisor
// fields).
//
// Schema evolution: supervisor fields are stored under the "sv" key inside
// each run's JSON value. Missing "sv" key means a pre-supervisor run —
// all supervisor fields default to zero.

const supervisorVersion = 1

// supervisorFields is the supervisor-specific slice stored alongside the
// drive.Run JSON in the "drive-runs" bucket.
type supervisorFields struct {
	Version      int       `json:"v"`
	Status       string    `json:"status"`
	TotalSteps   int       `json:"total_steps"`
	TotalTokens  int       `json:"total_tokens"`
	TasksDone    int       `json:"tasks_done"`
	TasksFailed  int       `json:"tasks_failed"`
	TasksSkipped int       `json:"tasks_skipped"`
	EndedAt      time.Time `json:"ended_at"`
}

// Embedder is the interface that supervisor persistence implementations
// must satisfy to load/save run data.
type Embedder interface {
	// Bucket returns the bbolt bucket name for this run type.
	Bucket() []byte
	// SaveRunJSON stores the JSON-encoded run under the given key.
	SaveRunJSON(key []byte, json []byte) error
	// LoadRunJSON retrieves the JSON-encoded run under the given key.
	LoadRunJSON(key []byte) ([]byte, error)
}

// Save persists the supervisor run state to storage. It marshals the
// supervisor-specific fields and embeds them into the drive run JSON.
func Save(emb Embedder, runID string, fields *supervisorFields, baseJSON []byte) error {
	var base map[string]any
	if len(baseJSON) > 0 {
		if err := json.Unmarshal(baseJSON, &base); err != nil {
			return fmt.Errorf("unmarshal base run: %w", err)
		}
	} else {
		base = make(map[string]any)
	}
	base["sv"] = fields
	merged, err := json.Marshal(base)
	if err != nil {
		return fmt.Errorf("marshal merged: %w", err)
	}
	return emb.SaveRunJSON([]byte(runID), merged)
}

// LoadSupervisorFields extracts the supervisor-specific fields from a
// JSON-encoded drive run. Returns nil if the run has no supervisor fields
// (pre-supervisor era).
func LoadSupervisorFields(runJSON []byte) *supervisorFields {
	if len(runJSON) == 0 {
		return nil
	}
	var doc map[string]any
	if err := json.Unmarshal(runJSON, &doc); err != nil {
		return nil
	}
	svRaw, ok := doc["sv"]
	if !ok {
		return nil
	}
	sv, ok := svRaw.(map[string]any)
	if !ok {
		return nil
	}
	fields := &supervisorFields{}
	if v, ok := sv["v"].(float64); ok {
		fields.Version = int(v)
	}
	if v, ok := sv["status"].(string); ok {
		fields.Status = v
	}
	if v, ok := sv["total_steps"].(float64); ok {
		fields.TotalSteps = int(v)
	}
	if v, ok := sv["total_tokens"].(float64); ok {
		fields.TotalTokens = int(v)
	}
	if v, ok := sv["tasks_done"].(float64); ok {
		fields.TasksDone = int(v)
	}
	if v, ok := sv["tasks_failed"].(float64); ok {
		fields.TasksFailed = int(v)
	}
	if v, ok := sv["tasks_skipped"].(float64); ok {
		fields.TasksSkipped = int(v)
	}
	if v, ok := sv["ended_at"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			fields.EndedAt = t
		}
	}
	return fields
}
