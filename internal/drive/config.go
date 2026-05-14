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
	"errors"
	"strings"
	"sync"
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

	// PlannerModel is an optional override for the planner LLM.
	// Empty = use the engine's active provider/model. Setting this
	// to a stronger model (e.g. opus) is recommended in production
	// because planning quality dominates the rest of the run.
	PlannerModel string

	// PlannerFallbackModels is a chain of models tried in order when
	// the primary PlannerModel fails (parse error or validation error).
	// Each fallback model receives the same prompt as the primary.
	// Useful for: primary=fast/cheap, fallback=stronger. Empty = no
	// fallback (single model, current default). Errors are only returned
	// when ALL models in the chain fail.
	PlannerFallbackModels []string

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

	// PlannerContextProvider injects extra context into the planner LLM
	// call. When non-nil, Context() is called with the task and its
	// returned text is appended to the planner system prompt. This lets
	// operators supply repository structure, active-file summaries, or
	// custom persona instructions without hard-coding them in the
	// planner package. Nil = no injection (default).
	PlannerContextProvider PlannerContextProvider

	// AutoSurvey runs a pre-flight "survey" pass before the main
	// planner call. The survey TODO inspects the repository state
	// (file structure, active bugs, recent commits) and its output
	// is fed back into the planner as extra context. Default false.
	// When true, the supervisor expands the plan with a survey phase
	// before the planner LLM is called.
	AutoSurvey bool

	// AdaptiveStepBudget enables runtime step-budget adjustment. When
	// non-nil, BudgetFor is called per-TODO dispatch to determine step
	// count. Nil = static lane-based defaults (default behavior).
	AdaptiveStepBudget StepBudgetProvider

	// RetryBackoff computes how long to wait before retrying a transient-
	// failed TODO. Nil = no delay (immediate retry). Non-nil = the function
	// is called with the current attempt number and returns the wait duration.
	// DefaultRetryBackoff (2s * 2^attempt, capped at 5 min) is used when this
	// field is nil AND Retries > 0. Setting Retries=0 disables retry entirely.
	//
	// Common configs:
	//   - nil         → immediate retry (existing behavior)
	//   - nil, Retries=1 → DefaultRetryBackoff: 2s, 4s
	//   - custom func → inject jitter, fixed delays, or rate-limit-aware backoff
	RetryBackoff RetryBackoffFunc

	// PlannerCircuitBreaker tunes the planner stage circuit breaker.
	// Zero fields default to FailureThreshold=5, RecoveryTimeout=2min.
	PlannerCircuitBreaker CircuitBreakerConfig
	// ExecutorCircuitBreaker tunes the executor stage circuit breaker.
	ExecutorCircuitBreaker CircuitBreakerConfig
	// VerifierCircuitBreaker tunes the verifier stage circuit breaker.
	VerifierCircuitBreaker CircuitBreakerConfig
}

// PlannerContextProvider is implemented by callers that want to
// inject structured context (repo overview, active files, custom
// rules) into the planner LLM call. Returning the empty string
// is valid — the driver treats it as "no context to inject".
type PlannerContextProvider interface {
	// Context receives the raw task string and returns free-form
	// text to append to the planner system prompt. The text is
	// injected as-is with no escaping; keep it concise (under 1 KB)
	// to avoid truncating the planner's output budget.
	Context(task string) string
}

// DefaultConfig is the safety-leaning preset used when the caller
// passes a zero-value Config or omits fields. The defaults bias
// toward "stops sooner" — easier to widen later than to recover from
// a runaway autonomous loop.
func DefaultConfig() Config {
	return Config{
		MaxTodos:         20,
		MaxFailedTodos:   3,
		MaxWallTime:      60 * time.Minute,
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

// CircuitBreakerConfig tunes the per-stage circuit breaker. All values
// are treated as defaults when zero.
type CircuitBreakerConfig struct {
	// FailureThreshold blocks the stage when this many consecutive failures
	// are recorded. Default 5.
	FailureThreshold int
	// RecoveryTimeout re-enables a closed circuit after this much idle time
	// following a circuit-open event. Default 2 minutes.
	RecoveryTimeout time.Duration
	// HalfOpenMaxCalls allows this many calls through while probing recovery.
	// Default 3.
	HalfOpenMaxCalls int
}

// State returns the current circuit state. Exported so callers can
// expose it in events/UI.
type CircuitState int

const (
	CircuitClosed CircuitState = iota
	CircuitOpen
	CircuitHalfOpen
)

// String implements fmt.Stringer.
func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half_open"
	}
	return "unknown"
}

// CircuitBreaker implements the circuit breaker pattern for a named stage
// (e.g. "planner", "executor"). It is safe to use concurrently.
type CircuitBreaker struct {
	cfg           CircuitBreakerConfig
	state         CircuitState
	mu            sync.Mutex
	failures      int
	lastFailure   time.Time
	halfOpenTried int // how many probe calls have fired in half-open
}

// NewCircuitBreaker creates a breaker with the given config and
// defaults applied.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 5
	}
	if cfg.RecoveryTimeout <= 0 {
		cfg.RecoveryTimeout = 2 * time.Minute
	}
	if cfg.HalfOpenMaxCalls <= 0 {
		cfg.HalfOpenMaxCalls = 3
	}
	return &CircuitBreaker{cfg: cfg, state: CircuitClosed}
}

// Check returns true when the circuit is closed or half-open AND a probe
// slot is available. When false the caller should return a
// ErrCircuitOpen error immediately.
func (cb *CircuitBreaker) Check() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitOpen:
		if time.Since(cb.lastFailure) > cb.cfg.RecoveryTimeout {
			cb.state = CircuitHalfOpen
			cb.halfOpenTried = 0
			return true
		}
		return false
	case CircuitHalfOpen:
		if cb.halfOpenTried < cb.cfg.HalfOpenMaxCalls {
			cb.halfOpenTried++
			return true
		}
		return false
	}
	return false // unreachable
}

// Record registers a failure. The circuit is tripped to Open when
// FailureThreshold consecutive failures are seen.
func (cb *CircuitBreaker) Record(success bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if success {
		if cb.state == CircuitHalfOpen {
			cb.state = CircuitClosed
			cb.failures = 0
			cb.halfOpenTried = 0
		}
		cb.failures = 0
		return
	}
	cb.failures++
	cb.lastFailure = time.Now()
	switch cb.state {
	case CircuitClosed:
		if cb.failures >= cb.cfg.FailureThreshold {
			cb.state = CircuitOpen
		}
	case CircuitHalfOpen:
		cb.state = CircuitOpen
		cb.halfOpenTried = 0
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// ErrCircuitOpen is returned by a stage when the circuit is open.
var ErrCircuitOpen = errors.New("circuit breaker is open")

// StepBudgetProvider lets the caller inject a dynamic per-TODO step
// budget instead of using the static Config.ExecutorStepBudget.
// Return 0 to fall back to the config default.
type StepBudgetProvider interface {
	// BudgetFor returns the max steps to allow for this TODO. Returning 0
	// falls back to the static executor step budget from Config.
	BudgetFor(todo Todo, run *Run) int
}

// RetryBackoffFunc computes the backoff delay for a given retry attempt.
// Implementations must be safe to call concurrently.
type RetryBackoffFunc func(attempt int) time.Duration

// DefaultRetryBackoff implements exponential backoff with a 2s base, capped
// at 5 minutes.  attempt=0 → 2s, attempt=1 → 4s, attempt=2 → 8s, ...
var DefaultRetryBackoff RetryBackoffFunc = func(attempt int) time.Duration {
	const base = 2 * time.Second
	const maxDelay = 5 * time.Minute
	delay := base * time.Duration(1<<attempt)
	if delay > maxDelay {
		delay = maxDelay
	}
	if delay < base {
		delay = base
	}
	return delay
}
