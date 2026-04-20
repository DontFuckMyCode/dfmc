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
	"strings"
	"time"
)

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
	TodoPending TodoStatus = "pending"
	TodoRunning TodoStatus = "running"
	TodoDone    TodoStatus = "done"
	TodoBlocked TodoStatus = "blocked"
	TodoSkipped TodoStatus = "skipped"
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
	ID           string     `json:"id"`
	ParentID     string     `json:"parent_id,omitempty"`
	Origin       string     `json:"origin,omitempty"`
	Kind         string     `json:"kind,omitempty"`
	Title        string     `json:"title"`
	Detail       string     `json:"detail"`
	DependsOn    []string   `json:"depends_on,omitempty"`
	FileScope    []string   `json:"file_scope,omitempty"`
	ReadOnly     bool       `json:"read_only,omitempty"`
	ProviderTag  string     `json:"provider_tag,omitempty"`
	WorkerClass  string     `json:"worker_class,omitempty"`
	Skills       []string   `json:"skills,omitempty"`
	AllowedTools []string   `json:"allowed_tools,omitempty"`
	Labels       []string   `json:"labels,omitempty"`
	Verification string     `json:"verification,omitempty"`
	Confidence   float64    `json:"confidence,omitempty"`
	Status       TodoStatus `json:"status"`
	Brief        string     `json:"brief,omitempty"`
	Error        string     `json:"error,omitempty"`
	Attempts     int        `json:"attempts"`
	StartedAt    time.Time  `json:"started_at,omitempty"`
	EndedAt      time.Time  `json:"ended_at,omitempty"`
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

// Config tunes the driver loop. Zero-valued fields fall back to safe
// defaults — drive runs that hit a default should never hard-stop the
// user's terminal, but the defaults are conservative enough that a
// runaway model can't burn an unlimited number of API calls before
// the user notices.
type Config struct {
	// MaxTodos hard-caps the planner output. Anything beyond is
	// truncated with a note. Default 20.
	MaxTodos int

	// MaxFailedTodos stops the run when this many consecutive TODOs
	// end up Blocked. Default 3 — a model that blocks 3 in a row
	// usually means the task itself is malformed or the codebase is
	// in a state the model can't navigate.
	MaxFailedTodos int

	// MaxWallTime caps total wall-clock duration. Default 30 minutes.
	// Hitting this marks the run Stopped and the in-flight TODO
	// (if any) gets retried on resume.
	MaxWallTime time.Duration

	// DrainGraceWindow is the bounded extra wait after stop/fail/cancel
	// before the driver finalizes a run with in-flight workers still
	// running. Default 2 seconds. Lower it in tests to avoid sleeping on
	// real wall clock; raise it in production if providers routinely need
	// a little longer to flush final summaries after cancellation.
	DrainGraceWindow time.Duration

	// Retries is the per-TODO retry count on failure (0 means
	// "execute once, no retry"). Default 1 — one retry catches
	// transient tool failures (rate limits, file-changed-since-read
	// drift) without blowing budget.
	Retries int

	// PlannerModel is an optional override for the planner LLM.
	// Empty = use the engine's active provider/model. Setting this
	// to a stronger model (e.g. opus) is recommended in production
	// because planning quality dominates the rest of the run.
	PlannerModel string

	// MaxParallel is the upper bound on concurrent TODO executors.
	// Default 3 — enough to overlap I/O-bound waits (LLM calls take
	// seconds, file reads are cheap) without triggering provider
	// rate-limiting on most accounts. Set to 1 to force sequential
	// execution (useful when debugging or when MaxParallel races
	// expose a planner-declared file_scope mistake).
	//
	// Conflicting file_scope across TODOs already throttles fan-out
	// at scheduling time (see readyBatch); this is just the ceiling
	// in the no-conflict happy path.
	MaxParallel int

	// Routing maps a TODO ProviderTag (e.g. "code", "review", "test")
	// to a provider profile name registered with the engine's
	// provider router. Empty map / missing keys = use the engine's
	// active provider for that TODO. Lookup is case-insensitive on
	// the tag side; the value is passed verbatim to the provider
	// router.
	//
	// Example:
	//   Routing: {"plan": "anthropic-opus", "code": "anthropic-sonnet",
	//             "test": "anthropic-haiku"}
	Routing map[string]string

	// AutoApprove lists the tool names that should bypass the user
	// approval gate while the drive run is active. The wildcard "*"
	// approves every tool (recommended only when the user wants
	// truly unattended execution). Empty/nil = use the engine's
	// existing approver verbatim, which means the driver will
	// pause for user confirmation on every gated tool — usually
	// not what you want for a "watch it work" drive run.
	//
	// The override is scoped to the run: the driver restores the
	// previous approver in finalize, even on panic / cancel paths.
	// Configure for autonomous-but-safe runs:
	//   AutoApprove: ["read_file","grep_codebase","glob","ast_query",
	//                 "find_symbol","list_dir","web_fetch","web_search",
	//                 "edit_file","write_file","apply_patch"]
	// (i.e. all read tools + the standard write tools, but NOT
	// run_command and git_commit — leave those gated even in drive).
	AutoApprove []string

	// AutoVerify appends a deterministic supervisor-generated
	// verification TODO after planning. The extra TODO depends on all
	// mutating/high-risk work items and runs the narrowest reasonable
	// verification pass (tests/build/review) using the normal scheduler.
	// Default false so existing drive behavior stays stable unless the
	// caller explicitly opts into stronger end-of-run verification.
	AutoVerify bool

	// AutoSurvey prepends a deterministic supervisor-generated
	// discovery/survey TODO when the planner skipped an initial repo
	// mapping step. The synthetic task becomes the parent of every root
	// task so later workers start from a shared understanding.
	AutoSurvey bool
}

// DefaultConfig is the safety-leaning preset used when the caller
// passes a zero-value Config or omits fields. The defaults bias
// toward "stops sooner" — easier to widen later than to recover from
// a runaway autonomous loop.
func DefaultConfig() Config {
	return Config{
		MaxTodos:         20,
		MaxFailedTodos:   3,
		MaxWallTime:      30 * time.Minute,
		DrainGraceWindow: 2 * time.Second,
		Retries:          1,
		MaxParallel:      3,
	}
}

// Apply fills zero-valued fields in c from defaults. Returns the
// merged config so callers can `cfg = cfg.Apply()` in one line.
func (c Config) Apply() Config {
	d := DefaultConfig()
	if c.MaxTodos <= 0 {
		c.MaxTodos = d.MaxTodos
	}
	if c.MaxFailedTodos <= 0 {
		c.MaxFailedTodos = d.MaxFailedTodos
	}
	if c.MaxWallTime <= 0 {
		c.MaxWallTime = d.MaxWallTime
	}
	if c.DrainGraceWindow <= 0 {
		c.DrainGraceWindow = d.DrainGraceWindow
	}
	if c.Retries < 0 {
		c.Retries = d.Retries
	}
	if c.MaxParallel <= 0 {
		c.MaxParallel = d.MaxParallel
	}
	return c
}

// providerForTag returns the provider profile name to use for a TODO
// with the given ProviderTag. Empty string = no override (use engine
// default). Lookup is case-insensitive on the tag side; an unmapped
// tag also returns "" so unknown tags safely degrade to the default
// rather than crashing.
func (c Config) providerForTag(tag string) string {
	if c.Routing == nil {
		return ""
	}
	// Direct hit first to avoid the lowercase allocation in the
	// common case.
	if v, ok := c.Routing[tag]; ok {
		return v
	}
	lc := strings.ToLower(strings.TrimSpace(tag))
	for k, v := range c.Routing {
		if strings.ToLower(strings.TrimSpace(k)) == lc {
			return v
		}
	}
	return ""
}
