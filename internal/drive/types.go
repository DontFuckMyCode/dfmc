// Package drive runs a self-driving "plan -> execute TODOs in order"
// loop on top of engine.Engine. The user kicks off `dfmc drive "<task>"`,
// the planner LLM call breaks the task into a DAG of TODOs, and the
// scheduler walks ready TODOs (deps satisfied) one by one through the
// regular Ask cycle. Each TODO gets a fresh sub-conversation seeded
// with a brief of prior work so context stays bounded; the engine's
// own autonomous_resume handles mid-TODO budget exhaust transparently.
//
// Phase 1 (this file set) is sequential single-provider. Phase 2 adds
// parallelism with file-scope conflict detection. Phase 3 adds per-TODO
// provider routing via Config.Routing map (ProviderTag → profile name);
// the override is applied by providerForTag and SelectDriveProfile.
//
// Why a separate package and not an Engine method:
//   - Drive runs are stateful long-lived objects with their own
//     persistence — bundling them on Engine bloats an already-fat type.
//   - The CLI and TUI both consume the same Driver via the engine
//     handle; isolating it here keeps the integration surface narrow.
//   - Drive tests want to fake the engine (planner shape verification,
//     scheduler ordering) without spinning up the full provider stack.

package drive

import (
	"time"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
)

// Driver-loop tuning (Config + DefaultConfig + Apply +
// providerForTag) lives in config.go.

// TodoStatus is the lifecycle state of a single TODO inside a drive run.
// Pending TODOs are waiting on dependencies; Running ones are currently
// being executed by the scheduler; Done/Blocked/Skipped are terminal.
//
// Blocked vs Skipped: Blocked means the TODO itself failed (after all
// retries); Skipped means a dependency it relied on was Blocked, so
// the scheduler refuses to run it (running it would just hit the same
// missing-prereq state).
type TodoStatus string

const (
	TodoPending        TodoStatus = "pending"
	TodoRunning        TodoStatus = "running"
	TodoDone           TodoStatus = "done"
	TodoBlocked        TodoStatus = "blocked"
	TodoSkipped        TodoStatus = "skipped"
	TodoVerifying      TodoStatus = "verifying"
	TodoWaiting        TodoStatus = "waiting"
	TodoExternalReview TodoStatus = "external_review"
)

// BlockReason describes why a TODO ended as TodoBlocked.
// Persisted in the Run JSON so the TUI and remote can show a human-readable
// reason alongside the raw error message.
type BlockReason string

const (
	BlockReasonFatal             BlockReason = "fatal_error"        // non-retriable error (tool denied, auth failure, etc.)
	BlockReasonRetriesExhausted  BlockReason = "retries_exhausted"  // all retry attempts failed
	BlockReasonSpawnInvalid      BlockReason = "spawn_invalid"      // planner emitted malformed child TODOs
	BlockReasonDependencyBlocked BlockReason = "dependency_blocked" // a depends-on TODO was itself blocked
	BlockReasonMaxFailedTodos    BlockReason = "max_failed_todos"   // run-level consecutive blocked limit hit
)

// Todo is one unit of work the planner emitted. The fields beyond the
// core ID/Title/Detail are intentionally richer than a simple checklist:
//   - DependsOn shapes the scheduler walk order.
//   - FileScope lets the scheduler prevent conflicting parallel writes.
//   - ReadOnly marks TODOs that only inspect/verify state. This lets the
//     scheduler keep empty-scope survey/review/verify work parallel
//     without pretending the task knows exact file targets.
//   - ProviderTag is the coarse router label ("code", "review", "test").
//   - WorkerClass is the finer execution persona ("coder", "reviewer",
//     "security", ...). This drives sub-agent role shaping.
//   - Skills carries named runtime capabilities that should be activated
//     for this TODO (debug/review/audit/etc.).
//   - AllowedTools narrows the preferred/allowed tool surface for the
//     executor when the planner knows a TODO is read-only or verification-
//     only. The engine treats it as guidance rather than a hard sandbox.
//   - Verification and Confidence capture the planner's intent so future
//     supervisor / verifier workers can reason about which TODOs must be
//     checked before a run is considered complete.
//
// All fields are persisted as part of the Run JSON in bbolt; the JSON
// keys match the lowercase field names so externally-edited resume
// state stays compatible.
type Todo struct {
	ID            string      `json:"id"`
	ParentID      string      `json:"parent_id,omitempty"`
	Origin        string      `json:"origin,omitempty"`
	Kind          string      `json:"kind,omitempty"`
	Title         string      `json:"title"`
	Detail        string      `json:"detail"`
	DependsOn     []string    `json:"depends_on,omitempty"`
	FileScope     []string    `json:"file_scope,omitempty"`
	ReadOnly      bool        `json:"read_only,omitempty"`
	ProviderTag   string      `json:"provider_tag,omitempty"`
	WorkerClass   string      `json:"worker_class,omitempty"`
	Skills        []string    `json:"skills,omitempty"`
	AllowedTools  []string    `json:"allowed_tools,omitempty"`
	Labels        []string    `json:"labels,omitempty"`
	Verification  string      `json:"verification,omitempty"`
	Confidence    float64     `json:"confidence,omitempty"`
	Status        TodoStatus  `json:"status"`
	Brief         string      `json:"brief,omitempty"`
	Error         string      `json:"error,omitempty"`
	BlockedReason BlockReason `json:"blocked_reason,omitempty"`
	Attempts      int         `json:"attempts"`
	StartedAt     time.Time   `json:"started_at,omitempty"`
	EndedAt       time.Time   `json:"ended_at,omitempty"`
	// LastContext holds the retrieval outcome from the most recent
	// context build, so resume can reuse the same chunks instead of
	// re-running retrieval from scratch.
	LastContext *ctxmgr.ContextSnapshot `json:"last_context,omitempty"`
	// Budget holds the max tool steps allowed for this TODO. When zero,
	// the executor derives a budget from WorkerClass at dispatch time.
	Budget int `json:"budget,omitempty"`
}

// RunStatus is the lifecycle state of an entire drive run.
//   - Planning: planner LLM call in flight.
//   - Running: at least one TODO Pending or Running.
//   - Done: every TODO terminal, no Blocked.
//   - Stopped: user-interrupted (Ctrl+C or Esc-pause-then-abort).
//   - Failed: too many consecutive Blocked TODOs (cfg.MaxFailedTodos)
//     OR planner returned no TODOs OR run hit MaxWallTime.
type RunStatus string

const (
	RunPlanning RunStatus = "planning"
	RunRunning  RunStatus = "running"
	RunDone     RunStatus = "done"
	RunStopped  RunStatus = "stopped"
	RunFailed   RunStatus = "failed"
)

// Run is the top-level record persisted per drive invocation. The ID
// is a short ULID-style string the user can pass to `--resume <id>`.
// Reason captures the human-readable cause when Status is Stopped or
// Failed (e.g. "max_failed_todos exceeded: 3 consecutive blocks").
type Run struct {
	ID        string                 `json:"id"`
	Task      string                 `json:"task"`
	Status    RunStatus              `json:"status"`
	Reason    string                 `json:"reason,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
	EndedAt   time.Time              `json:"ended_at,omitempty"`
	Plan      *ExecutionPlanSnapshot `json:"plan,omitempty"`
	Todos     []Todo                 `json:"todos"`
}

type ExecutionPlanSnapshot struct {
	Layers         [][]string     `json:"layers,omitempty"`
	Roots          []string       `json:"roots,omitempty"`
	Leaves         []string       `json:"leaves,omitempty"`
	WorkerCounts   map[string]int `json:"worker_counts,omitempty"`
	LaneCaps       map[string]int `json:"lane_caps,omitempty"`
	LaneOrder      []string       `json:"lane_order,omitempty"`
	SurveyID       string         `json:"survey_id,omitempty"`
	VerificationID string         `json:"verification_id,omitempty"`
	MaxParallel    int            `json:"max_parallel,omitempty"`
}

// Counts returns done/blocked/skipped tallies. Convenience for the
// final summary event and the TUI status chip.
func (r *Run) Counts() (done, blocked, skipped, pending int) {
	for _, t := range r.Todos {
		switch t.Status {
		case TodoDone:
			done++
		case TodoBlocked:
			blocked++
		case TodoSkipped:
			skipped++
		case TodoPending, TodoRunning:
			pending++
		}
	}
	return
}

