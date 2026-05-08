package supervisor

// coordinator_types.go — pure data carriers used by the Supervisor
// surface (BudgetPool with its tiny lease/restore arithmetic, the
// RunResult / SupervisorStatus result shapes, and the ExecuteTask*
// callback request/response pair). Sibling of coordinator.go which
// keeps the Supervisor struct + run-loop dispatcher (Run / runImpl /
// Stop) and coordinator_dispatch.go / coordinator_status.go which
// own the per-worker dispatch and status accounting paths.
//
// Splitting types out keeps the lifecycle file focused on the
// concurrency contract; nothing here imports internal/engine or
// internal/drive — the engine-side adapter wires ExecuteTaskFunc via
// SetWorkerFunc so the supervisor stays bridge-agnostic.

import (
	"context"
	"time"
)

// BudgetPool is the shared token/step budget all workers draw from.
// Each worker is allocated a lease when it starts; the pool is restored
// when the worker reports actual spend on completion.
type BudgetPool struct {
	TotalTokens int // 0 means unlimited
	UsedTokens  int
	TotalSteps  int // 0 means unlimited
	UsedSteps   int
}

// Remaining returns the currently available token budget.
func (b *BudgetPool) Remaining() int {
	if b.TotalTokens <= 0 {
		return -1 // unlimited
	}
	remaining := b.TotalTokens - b.UsedTokens
	if remaining < 0 {
		return 0
	}
	return remaining
}

// AllocTokens attempts to reserve tokens for a worker's lease.
// Returns the allocated amount (may be less than requested if pool is low).
func (b *BudgetPool) AllocTokens(tokens int) int {
	if b.TotalTokens <= 0 {
		return tokens // unlimited
	}
	avail := b.TotalTokens - b.UsedTokens
	if avail <= 0 {
		return 0
	}
	alloc := tokens
	if alloc > avail {
		alloc = avail
	}
	b.UsedTokens += alloc
	return alloc
}

// RestoredTokens returns tokens to the pool (called on worker completion).
func (b *BudgetPool) RestoreTokens(tokens int) {
	b.UsedTokens -= tokens
	if b.UsedTokens < 0 {
		b.UsedTokens = 0
	}
}

// RunResult is the final outcome of a supervisor run.
type RunResult struct {
	RunID        string
	Status       string // "done", "failed", "stopped"
	Reason       string
	TasksDone    int
	TasksFailed  int
	TasksSkipped int
	TotalSteps   int
	TotalTokens  int
	Duration     time.Duration
}

// SupervisorStatus describes the current state of a running supervisor.
type SupervisorStatus struct {
	Active   bool
	RunID    string
	InFlight int
	Done     int
	Failed   int
	Skipped  int
}

// ExecuteTaskFunc is the callback the supervisor invokes to execute one task.
// It is registered via SetWorkerFunc, typically by the engine-side adapter
// that bridges to the drive executor.
type ExecuteTaskFunc func(ctx context.Context, req ExecuteTaskRequest) (ExecuteTaskResponse, error)

// ExecuteTaskRequest is the input passed to a worker's ExecuteTaskFunc.
type ExecuteTaskRequest struct {
	TaskID            string
	ProviderTag       string
	Title             string
	Detail            string
	Brief             string
	Role              string
	Skills            []string
	Labels            []string
	Verification      string
	Model             string
	ProfileCandidates []string
	AllowedTools      []string
	MaxSteps          int
	TokenBudget       int // allocated from BudgetPool for this task
	// PriorSummary chains trajectory summaries from completed dependency tasks
	// into this task's prompt so the next worker knows what prior work occurred.
	PriorSummary string
}

// ExecuteTaskResponse is the output from a worker's ExecuteTaskFunc.
type ExecuteTaskResponse struct {
	Summary         string
	ToolCalls       int
	DurationMs      int64
	Provider        string
	Model           string
	Attempts        int
	TokensUsed      int
	FallbackUsed    bool
	FallbackReasons []string
}
