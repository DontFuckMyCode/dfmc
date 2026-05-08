// cli_mcp_task_schema.go — MCP tool descriptors + per-call argument
// types for the dfmc_task_* synthetic tool family. Sibling of
// cli_mcp_task.go which keeps the taskMCPHandler struct, Handles
// prefix gate, Call dispatcher, and the five callXxx executors that
// drive the engine's task store.
//
// Splitting the JSON-Schema descriptors + arg structs out keeps
// cli_mcp_task.go focused on "what does each operation actually do"
// while this file owns "what shape does each operation accept".
// Adding a new dfmc_task_* tool means adding a descriptor here AND a
// callXxx in cli_mcp_task.go — the two stay in lockstep but neither
// dominates the other.

package cli

import (
	"github.com/dontfuckmycode/dfmc/internal/mcp"
)

const taskToolPrefix = "dfmc_task_"

func (h *taskMCPHandler) Tools() []mcp.ToolDescriptor {
	return []mcp.ToolDescriptor{
		{
			Name:        "dfmc_task_create",
			Description: "Create a new task in the persistent task store. Tasks can be hierarchical (parent_id) and carry metadata like worker_class, file_scope, and verification policy.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"title":        map[string]any{"type": "string", "description": "Task title (required)"},
					"parent_id":    map[string]any{"type": "string", "description": "Parent task ID for hierarchical tasks"},
					"origin":       map[string]any{"type": "string", "description": "Origin hint: 'todo_write', 'planner', or 'supervisor'"},
					"state":        map[string]any{"type": "string", "description": "Initial state: pending (default), running, done, blocked, skipped, waiting, external_review"},
					"worker_class": map[string]any{"type": "string", "description": "Planner/coder/reviewer/tester/security/synthesizer"},
					"depends_on":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Task IDs this task depends on"},
					"file_scope":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "File paths this task operates on"},
					"labels":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Arbitrary string tags"},
					"verification": map[string]any{"type": "string", "description": "none/light/required/deep"},
					"confidence":   map[string]any{"type": "number", "description": "Confidence 0-1"},
					"summary":      map[string]any{"type": "string", "description": "Brief summary of outcome"},
				},
				"required":             []string{"title"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_task_get",
			Description: "Fetch a single task by its ID.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "Task ID (e.g. tsk-a1b2c3)"},
				},
				"required":             []string{"id"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_task_list",
			Description: "List tasks from the persistent store, with optional filters. Returns newest-first.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"parent_id": map[string]any{"type": "string", "description": "Filter to children of this parent ID"},
					"run_id":    map[string]any{"type": "string", "description": "Filter to tasks from a specific drive run"},
					"state":     map[string]any{"type": "string", "description": "Filter by state"},
					"label":     map[string]any{"type": "string", "description": "Filter by label tag"},
					"limit":     map[string]any{"type": "integer", "description": "Max results (default 25)"},
					"offset":    map[string]any{"type": "integer", "description": "Skip N results for pagination"},
				},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_task_update",
			Description: "Partially update a task: change its state, title, summary, confidence, or blocked_reason.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":             map[string]any{"type": "string"},
					"title":          map[string]any{"type": "string"},
					"state":          map[string]any{"type": "string"},
					"summary":        map[string]any{"type": "string"},
					"confidence":     map[string]any{"type": "number"},
					"blocked_reason": map[string]any{"type": "string"},
				},
				"required":             []string{"id"},
				"additionalProperties": false,
			},
		},
		{
			Name:        "dfmc_task_delete",
			Description: "Delete a task from the store by ID.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
				"required":             []string{"id"},
				"additionalProperties": false,
			},
		},
	}
}

type taskCreateArgs struct {
	Title        string   `json:"title"`
	ParentID     string   `json:"parent_id,omitempty"`
	Origin       string   `json:"origin,omitempty"`
	State        string   `json:"state,omitempty"`
	WorkerClass  string   `json:"worker_class,omitempty"`
	DependsOn    []string `json:"depends_on,omitempty"`
	FileScope    []string `json:"file_scope,omitempty"`
	Labels       []string `json:"labels,omitempty"`
	Verification string   `json:"verification,omitempty"`
	Confidence   float64  `json:"confidence,omitempty"`
	Summary      string   `json:"summary,omitempty"`
}

type taskGetArgs struct {
	ID string `json:"id"`
}

type taskListArgs struct {
	ParentID string `json:"parent_id,omitempty"`
	RunID    string `json:"run_id,omitempty"`
	State    string `json:"state,omitempty"`
	Label    string `json:"label,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}

type taskUpdateArgs struct {
	ID            string  `json:"id"`
	Title         string  `json:"title,omitempty"`
	State         string  `json:"state,omitempty"`
	Summary       string  `json:"summary,omitempty"`
	Confidence    float64 `json:"confidence,omitempty"`
	BlockedReason string  `json:"blocked_reason,omitempty"`
	// IfVersion mirrors the HTTP If-Match header: when set to a
	// non-negative value, the update routes through UpdateTaskCAS and
	// fails with a "version_conflict" error if the stored version no
	// longer matches. Omit (or use a negative value) for the original
	// last-writer-wins semantics. Pointer so omitted/zero are
	// distinguishable — version 0 is a legitimate first-edit case.
	IfVersion *int `json:"if_version,omitempty"`
}

type taskDeleteArgs struct {
	ID string `json:"id"`
}
