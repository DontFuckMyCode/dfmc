package tools

// meta.go — the 4 meta tools the LLM actually sees.
//
// Why: A registry of 40+ tools, each with full JSON-Schema args, would balloon
// every system prompt — several thousand tokens BEFORE the user question.
// Instead we expose 4 stable meta tools that proxy to the backend registry:
//
//	tool_search(query, limit?)        → discovers which backend tools exist
//	tool_help(name)                   → fetches the full spec for one tool
//	tool_call(name, args)             → executes a single backend tool
//	tool_batch_call(calls[])          → executes N backend tools in parallel
//
// The model pays token cost for only these 4 specs in the prompt; backend
// tools are discovered on demand. tool_batch_call fans calls out onto a
// semaphore bounded by AgentConfig.ParallelBatchSize (default 4); results
// are returned in input order regardless of completion order. A per-call
// failure does not abort the batch.
//
// All four tools implement the standard Tool interface so they can be
// registered alongside normal tools and executed through the same Engine
// pipeline (failure tracking, output compression, etc.).
//
// Per-tool implementations live in the siblings:
//
//	meta_search.go  — toolSearchTool
//	meta_help.go    — toolHelpTool
//	meta_call.go    — toolCallTool (single dispatch + auto-unwrap)
//	meta_batch.go   — toolBatchCallTool (parallel fan-out + dispatcher refusal)
//
// Shared shape: the per-turn budget (enterMetaBudget), the registry hook
// (RegisterMetaTools), and the shared helpers (isMetaTool / pickToolName /
// extractArgsObject / inheritToolReason / previewBatchTarget /
// missingNameError) live here so each meta-tool sibling depends only on
// the core, not on each other.

import (
	"context"
	"fmt"
	"sync"
)

// defaultSearchLimit caps the search result count. Default is low on purpose:
// the model can narrow its query instead of drowning in results.
const defaultSearchLimit = 10

// maxBatchCalls is the upfront ceiling on tool_batch_call's calls array.
// Rejected before allocating per-call result slices or spawning workers.
// Models occasionally emit pathological batches (sometimes from a runaway
// `for ... append` loop on the planner side); without an upfront cap one
// such call could pin a goroutine pool for minutes and burn the entire
// per-step token budget on tool output. 32 is enough for any sane fan-out
// (typical batches are 2–8) — anything larger should be split into
// sequential sub-batches so the loop can compact in between.
const maxBatchCalls = 32

// Meta-tool cumulative budget across a single agent turn. Seeded once at the
// agent-loop boundary so repeated tool_call / tool_batch_call expansion shares
// one counter instead of getting a fresh allowance per dispatch. Defaults
// apply when the state is seeded without explicit limits (tests, legacy
// callers); production callers pass the AgentConfig values through
// SeedMetaToolBudgetWithLimits.
const (
	defaultMetaCallBudget = 64
	defaultMetaDepthLimit = 4
)

type metaBudgetKey struct{}

type metaBudgetState struct {
	mu         sync.Mutex
	depth      int
	used       int
	callBudget int
	depthLimit int
}

// SeedMetaToolBudget seeds the context with the shared meta-tool execution
// budget using the built-in defaults (64 calls, depth 4). Safe to call
// repeatedly — the first call wins and later calls are no-ops.
func SeedMetaToolBudget(ctx context.Context) context.Context {
	return SeedMetaToolBudgetWithLimits(ctx, 0, 0)
}

// SeedMetaToolBudgetWithLimits seeds the meta-tool budget with explicit
// per-turn caps. Pass 0 for either limit to fall back to the built-in
// default. If the context already carries a budget state (double-seed),
// the existing state wins — later callers cannot tighten or relax limits
// set by an earlier seeder in the same turn.
func SeedMetaToolBudgetWithLimits(ctx context.Context, callBudget, depthLimit int) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(metaBudgetKey{}).(*metaBudgetState); ok {
		return ctx
	}
	if callBudget <= 0 {
		callBudget = defaultMetaCallBudget
	}
	if depthLimit <= 0 {
		depthLimit = defaultMetaDepthLimit
	}
	return context.WithValue(ctx, metaBudgetKey{}, &metaBudgetState{
		callBudget: callBudget,
		depthLimit: depthLimit,
	})
}

func enterMetaBudget(ctx context.Context, calls int) (context.Context, func(), error) {
	ctx = SeedMetaToolBudget(ctx)
	state, _ := ctx.Value(metaBudgetKey{}).(*metaBudgetState)
	if state == nil {
		return ctx, func() {}, nil
	}
	if calls <= 0 {
		calls = 1
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	// Check depth first (nesting recursion), then cumulative call count
	// (total work in the turn). Depth limit prevents meta-in-meta chains;
	// budget limit prevents a single turn from issuing hundreds of
	// backend calls through repeated batching. Both are checked under the
	// same lock so the counter state stays consistent regardless of which
	// check fires first.
	state.depth++
	depth := state.depth
	if depth > state.depthLimit {
		state.depth--
		return ctx, nil, fmt.Errorf(
			"%w (%d > %d). Split the work across separate rounds instead of recursively chaining tool_call/tool_batch_call",
			ErrMetaDepthExceeded, depth, state.depthLimit)
	}
	state.used += calls
	used := state.used
	if used > state.callBudget {
		state.depth--
		state.used -= calls
		return ctx, nil, fmt.Errorf(
			"%w (%d > %d backend calls planned in one turn). Split the work into smaller batches or let the agent answer before fanning out again",
			ErrMetaBudgetExhausted, used, state.callBudget)
	}
	return ctx, func() {
		state.mu.Lock()
		state.depth--
		state.mu.Unlock()
	}, nil
}

// RegisterMetaTools registers the 4 meta tools against the given Engine. Call
// this once during Engine construction. The meta tools hold a reference to
// the Engine so they can dispatch to backend tools.
func RegisterMetaTools(e *Engine) {
	e.Register(&toolSearchTool{engine: e})
	e.Register(&toolHelpTool{engine: e})
	e.Register(&toolCallTool{engine: e})
	e.Register(&toolBatchCallTool{engine: e})
}
