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

import (
	"context"

	ctxmgr "github.com/dontfuckmycode/dfmc/internal/context"
)

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
	ProviderTag  string // routing hint for per-worker provider selection
	Title        string
	Detail       string
	Brief        string
	Role         string
	Skills       []string
	Labels       []string
	Verification string
	Model        string
	// ProfileCandidates is the ordered provider/model chain the
	// supervisor selected for this TODO. The engine should try them in
	// order, preserving the same sub-agent seed/context on each attempt.
	ProfileCandidates []string
	AllowedTools      []string
	MaxSteps          int
}

// ExecuteTodoResponse carries the sub-agent's final summary plus
// counters for the per-TODO event payload.
type ExecuteTodoResponse struct {
	Summary    string
	ToolCalls  int
	DurationMs int64
	Parked     bool
	Provider   string
	Model      string
	Attempts   int
	// FallbackUsed reports whether the sub-agent had to move off its
	// first profile candidate to finish the task.
	FallbackUsed  bool
	FallbackFrom  string
	FallbackChain []string
	// FallbackReasons stores one error string per failed profile attempt,
	// aligned with the profiles tried before the final successful attempt.
	FallbackReasons []string
	// LastContext holds the retrieval outcome from the sub-agent's
	// final context build, so the caller can reuse the same chunks
	// instead of re-running retrieval from scratch on resume.
	LastContext *ctxmgr.ContextSnapshot
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
	// Safety: production implementations should scope the override to
	// drive-owned sub-agent tool calls so unrelated chat/tool traffic
	// in the same process does not inherit the allowlist. Pass an
	// empty/nil tools list to skip activation entirely (the driver does
	// this when no auto-approve is configured).
	BeginAutoApprove(tools []string) func()
}
