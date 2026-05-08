// config.go — driver loop tuning. Sibling of types.go which keeps
// the persisted records (Todo, TodoStatus, BlockReason, Run,
// RunStatus, ExecutionPlanSnapshot) and the package doc-comment.
//
// Splitting Config out keeps types.go scoped to "what we persist
// per-run" while this file owns "what knobs change behavior."
// Adding a new safety/throughput limit lands here; adding a new
// per-TODO field lands in types.go.

package drive

import (
	"strings"
	"time"
)

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
