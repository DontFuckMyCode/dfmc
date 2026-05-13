package session

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// AgentID identifies an agent within a session. 0 means "no agent" / invalid.
type AgentID int

const RootAgentID AgentID = 1 // The first agent is always the root (user-facing).

// AgentStatus describes what an agent is currently doing.
type AgentStatus int

const (
	StatusIdle              AgentStatus = iota // not started yet
	StatusRunning                               // actively working (tool loop)
	StatusWaitingDelegation                     // has a task in inbox, will process soon
	StatusWaitingUserInput                       // autonomy=limited + cap hit; needs input
	StatusParked                                 // paused, waiting to be resumed
	StatusDone                                   // finished cleanly
	StatusFailed                                 // finished with an error
)

func (s AgentStatus) String() string {
	switch s {
	case StatusIdle:
		return "idle"
	case StatusRunning:
		return "running"
	case StatusWaitingDelegation:
		return "waiting_delegation"
	case StatusWaitingUserInput:
		return "waiting_user_input"
	case StatusParked:
		return "parked"
	case StatusDone:
		return "done"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// AutonomyLevel controls whether an agent can ask the user for input.
type AutonomyLevel int

const (
	// AutonomyFull: agent never asks user. If it hits a limit, it parks or compacts.
	AutonomyFull AutonomyLevel = iota
	// AutonomyLimited: agent asks coordinator when it hits a cap or needs a decision.
	AutonomyLimited
	// AutonomyBlocked: agent is halted; coordinator must resume or kill.
	AutonomyBlocked
)

func (a AutonomyLevel) String() string {
	switch a {
	case AutonomyFull:
		return "full"
	case AutonomyLimited:
		return "limited"
	case AutonomyBlocked:
		return "blocked"
	default:
		return "unknown"
	}
}

// TaskStatus describes a delegation task's lifecycle.
type TaskStatus int

const (
	TaskStatusPending TaskStatus = iota
	TaskStatusRunning
	TaskStatusDone
	TaskStatusFailed
	TaskStatusCancelled
)

func (t TaskStatus) String() string {
	switch t {
	case TaskStatusPending:
		return "pending"
	case TaskStatusRunning:
		return "running"
	case TaskStatusDone:
		return "done"
	case TaskStatusFailed:
		return "failed"
	case TaskStatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

// AttentionType categorizes a short-lived attention event.
type AttentionType int

const (
	AttentionToolResult AttentionType = iota
	AttentionFileCreated
	AttentionError
	AttentionInfo
	AttentionQuestion
	AttentionDelegationSent
	AttentionDelegationDone
)

func (a AttentionType) String() string {
	switch a {
	case AttentionToolResult:
		return "tool_result"
	case AttentionFileCreated:
		return "file_created"
	case AttentionError:
		return "error"
	case AttentionInfo:
		return "info"
	case AttentionQuestion:
		return "question"
	case AttentionDelegationSent:
		return "delegation_sent"
	case AttentionDelegationDone:
		return "delegation_done"
	default:
		return "unknown"
	}
}

// DelegationTask represents a unit of work assigned from a parent to a child agent.
type DelegationTask struct {
	ID     uuid.UUID `json:"id"`
	From   AgentID   `json:"from"` // direct parent only (enforced by Session.Delegate)
	Task   string    `json:"task"` // natural language description

	// SystemPrompt is set by the orchestrating agent to give this child agent
	// a task-specific identity or context. It is prepended to the child's
	// base system prompt when the agent builds its prompt bundle.
	// Example: "You are debugging the auth module. Focus on token validation."
	SystemPrompt string `json:"system_prompt,omitempty"`

	// Autonomy of the child agent while working on this task.
	// If Limited and the child hits a cap, child's status becomes
	// waiting_user_input and the coordinator (parent) is notified.
	Autonomy AutonomyLevel `json:"autonomy"`

	// If true, the parent blocks on pendingResults[ID] until the task completes.
	// If false, fire-and-forget.
	AwaitResult bool `json:"await_result"`

	// Populated by the child agent when Status becomes Done or Failed.
	Result DelegationResult `json:"result,omitempty"`

	Status TaskStatus `json:"status"`

	CreatedAt time.Time `json:"created_at"`
	StartedAt time.Time `json:"started_at,omitempty"`
	DoneAt    time.Time `json:"done_at,omitempty"`
}

// DelegationResult is the output of a completed delegation task.
type DelegationResult struct {
	TaskID      uuid.UUID `json:"task_id"`
	Status      TaskStatus `json:"status"`      // done or failed
	Summary     string    `json:"summary"`      // short human-readable summary
	ToolCount   int       `json:"tool_count"`   // number of tool calls made
	TokensUsed  int       `json:"tokens_used"`  // approximate tokens consumed
	FilesTouched []string `json:"files_touched"`// files created or modified
	Payload     json.RawMessage `json:"payload,omitempty"` // extra data (error message, etc.)
}

// AttentionEvent is a short-lived notification published by an agent.
// Unlike DelegationTask (work assignment), AttentionEvent is for awareness.
// It is NOT auto-injected into any agent's context.
type AttentionEvent struct {
	ID        uuid.UUID       `json:"id"`
	From      AgentID         `json:"from"`
	Type      AttentionType   `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	Timestamp time.Time       `json:"timestamp"`
	ReadBy    map[AgentID]bool `json:"read_by"` // agents that have consumed this
}

// AgentTreeNode is a node in the session's agent tree, used for UI display.
// The actual Agent is stored in Session.agents; this is just a read-only view.
type AgentTreeNode struct {
	ID       AgentID     `json:"id"`
	Name     string      `json:"name"`
	Status   AgentStatus `json:"status"`
	Parent   AgentID     `json:"parent"`   // 0 for root
	Children []AgentID   `json:"children"`
	Depth    int         `json:"depth"`    // distance from root
}
