// Engine-side seam for the drive package.
//
// drive.Driver doesn't import internal/engine directly — that would
// create an import cycle (engine -> drive -> engine via the wired
// Tools.SubagentRunner). Instead it talks to a small Runner interface,
// implemented by the engine adapter in driveadapter.go. Tests can
// fake the runner to verify scheduler ordering, retry behavior, and
// persistence without spinning up the real provider stack.
//
// The two methods cover the two LLM call shapes drive needs:
//
//   - PlannerCall: a single one-shot completion (no tool loop). Used to
//     translate the task into a JSON TODO list. The caller passes the
//     planner system prompt and the task; the runner returns whatever
//     text the model produced. JSON parsing is the planner's job, not
//     the runner's.
//
//   - ExecuteTodo: a full tool-loop sub-agent run. Maps directly to
//     engine.RunSubagent under the hood — same fresh sub-conversation,
//     same step/token budget, same event stream — so every TODO gets
//     the production safety story.

package drive

import "context"

// PlannerRequest is the input to a single planner LLM call. Model is
// optional — when empty the runner uses its default. The system+user
// pair is sent verbatim with no other history (the planner is
// stateless across runs by design).
type PlannerRequest struct {
	Model  string
	System string
	User   string
}

// PlannerResponse echoes the model output plus the provider/model
// actually used, for the drive:plan:done event.
type PlannerResponse struct {
	Text     string
	Provider string
	Model    string
	Tokens   int
}

// ExecuteTodoRequest is one TODO dispatched as a sub-agent. Brief is
// the cumulative summary of prior TODOs the executor stitches in front
// of the actual TODO body; AllowedTools is an optional whitelist when
// the planner declared file_scope or tagged the TODO as read-only.
type ExecuteTodoRequest struct {
	TodoID       string
	Title        string
	Detail       string
	Brief        string
	Model        string
	AllowedTools []string
	MaxSteps     int
}

// ExecuteTodoResponse carries the sub-agent's final summary plus
// counters for the per-TODO event payload.
type ExecuteTodoResponse struct {
	Summary    string
	ToolCalls  int
	DurationMs int64
	Parked     bool
}

// Runner is the engine seam. The real implementation lives in
// driveadapter.go (constructed by callers via drive.NewEngineRunner)
// and wraps engine.Engine. Tests provide their own implementations
// without importing internal/engine.
type Runner interface {
	PlannerCall(ctx context.Context, req PlannerRequest) (PlannerResponse, error)
	ExecuteTodo(ctx context.Context, req ExecuteTodoRequest) (ExecuteTodoResponse, error)

	// BeginAutoApprove activates a scoped tool-approval override for
	// the duration of the drive run. tools is the list of tool names
	// to auto-approve (the wildcard "*" approves all). Returns a
	// release function the driver MUST call (typically deferred) to
	// restore the previous approver. Implementations that don't
	// support an approval gate (tests, headless runs) can return a
	// no-op release.
	//
	// Safety: the override is process-wide for the engine while
	// active. Any other operation that fires through the engine
	// (e.g. a parallel /chat in the TUI) inherits it. That's the
	// intended trade-off for "calıs babam calıs" — the user kicked
	// off an autonomous run; treat their session as consenting.
	// Pass an empty/nil tools list to skip activation entirely (the
	// driver does this when no auto-approve is configured).
	BeginAutoApprove(tools []string) func()
}
