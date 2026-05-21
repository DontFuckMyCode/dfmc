package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// parseStringListParam pulls a list of strings out of a tool param
// that may arrive as []any (the JSON path), []string (Go-side
// callers), or a comma-separated string (model shorthand). Returns
// nil when the key is missing, the value is nil, or every element is
// blank — callers can treat all three as "no constraint".
func parseStringListParam(params map[string]any, key string) []string {
	v, ok := params[key]
	if !ok || v == nil {
		return nil
	}
	var out []string
	switch vv := v.(type) {
	case []any:
		for _, x := range vv {
			if s := strings.TrimSpace(fmt.Sprint(x)); s != "" {
				out = append(out, s)
			}
		}
	case []string:
		for _, s := range vv {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	case string:
		for _, p := range strings.Split(vv, ",") {
			if s := strings.TrimSpace(p); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// SubagentRequest is the structured input to a sub-agent run. The engine
// is responsible for interpreting scope hints — the tools package only
// forwards them.
type SubagentRequest struct {
	Task         string   `json:"task"`          // The self-contained prompt for the sub-agent.
	Role         string   `json:"role"`          // Optional role hint: "researcher", "reviewer", ...
	AllowedTools []string `json:"allowed_tools"` // Optional restriction — if empty, engine picks defaults.
	// AllowedPaths constrains write tools (write_file, edit_file,
	// symbol_rename, symbol_move) to paths under one of the given
	// prefixes. Empty list means "no constraint". Reads are
	// unaffected — operators who want a read-restriction should drop
	// read tools from AllowedTools instead.
	AllowedPaths []string `json:"allowed_paths,omitempty"`
	MaxSteps     int      `json:"max_steps"` // Tool-call budget; 0 means engine default.
	Model        string   `json:"model"`     // Optional provider profile override.
	// ToolSource is an engine-internal approval scope marker. It is not
	// user/model supplied; adapters like drive may set it so approval
	// overrides apply only to their own sub-agent tool calls.
	ToolSource string `json:"-"`
	// Skills carries skill names to inject as system-prompt overlays in
	// sub-agent prompts. The engine resolves names to playbook text before
	// building the system prompt.
	Skills []string `json:"skills,omitempty"`
	// Autonomous, when true, opts the sub-agent into the auto-resume
	// wrapper instead of the bare runNativeToolLoop. Drive sets this so
	// a TODO never silently parks on tool-budget exhaustion mid-task —
	// the wrapper force-compacts and re-enters the loop transparently
	// up to resume_max_multiplier. Engine-internal flag; not on the wire.
	Autonomous bool `json:"-"`
}

// Recursive delegation bound: each nested sub-agent occupies a slot in
// the engine's parallel-subagent counter, so a chain of N delegate_task
// calls hits ErrSubagentConcurrencyLimit at depth=parallel_batch_size
// rather than running forever. Recursion is bounded indirectly by the
// concurrency cap, not by a separate depth field.

// SubagentResult is what the engine reports back to the parent tool loop.
type SubagentResult struct {
	Summary    string         `json:"summary"`    // Human-readable final answer.
	ToolCalls  int            `json:"tool_calls"` // How many tool calls the sub-agent spent.
	DurationMs int64          `json:"duration_ms"`
	Data       map[string]any `json:"data,omitempty"`
}

// SubagentRunner is satisfied by engine.Engine. We declare it here so tools
// can call the sub-agent entry point without importing the engine package
// (which would be circular).
type SubagentRunner interface {
	RunSubagent(ctx context.Context, req SubagentRequest) (SubagentResult, error)
}

// DelegateTaskTool lets the model spawn a bounded sub-agent for a focused
// task. The sub-agent has its own fresh message history — context-hungry
// research or scan work is moved off the main loop's token budget.
//
// Registered with a runner implementation at engine construction time via
// SetSubagentRunner. Without a runner the tool reports an offline-friendly
// error so the model learns to avoid calling it.
type DelegateTaskTool struct {
	mu     sync.RWMutex
	runner SubagentRunner
}

func NewDelegateTaskTool() *DelegateTaskTool { return &DelegateTaskTool{} }
func (t *DelegateTaskTool) Name() string     { return "delegate_task" }
func (t *DelegateTaskTool) Description() string {
	return "Spawn a bounded sub-agent for a focused, independently verifiable task. Best for parallel codebase research, scoped reviews, isolated implementation slices, or long scans that should not bloat the parent context. Provide a self-contained task, optional role/model/max_steps, and allowed_tools when the work should be least-privilege. Avoid delegation for tightly coupled next-step work the parent must do immediately."
}

func (t *DelegateTaskTool) SetRunner(r SubagentRunner) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runner = r
}

func (t *DelegateTaskTool) Execute(ctx context.Context, req Request) (Result, error) {
	task := strings.TrimSpace(asString(req.Params, "task", ""))
	if task == "" {
		return Result{}, missingParamError("delegate_task", "task", req.Params,
			`{"task":"Audit ./internal/provider/ for unbounded retries","role":"reviewer","max_steps":12}`,
			`task is the natural-language brief the sub-agent will run. Optional: role (e.g. "reviewer", "fixer"), allowed_tools (string[] or comma-list), max_steps (1-40), model.`)
	}
	t.mu.RLock()
	runner := t.runner
	t.mu.RUnlock()
	if runner == nil {
		return Result{}, fmt.Errorf("sub-agent runner not configured on this engine")
	}

	role := strings.TrimSpace(asString(req.Params, "role", ""))
	model := strings.TrimSpace(asString(req.Params, "model", ""))
	maxSteps := asInt(req.Params, "max_steps", 0)
	if maxSteps < 0 {
		maxSteps = 0
	}
	if maxSteps > 40 {
		maxSteps = 40
	}

	allowed := parseStringListParam(req.Params, "allowed_tools")
	allowedPaths := parseStringListParam(req.Params, "allowed_paths")

	// Depth starts at 0 for delegate_task; internal callers (e.g. drive)
	// may pass a non-zero depth from their own recursive context.
	res, err := runSubagentRetrying(ctx, runner, SubagentRequest{
		Task:         task,
		Role:         role,
		AllowedTools: allowed,
		AllowedPaths: allowedPaths,
		MaxSteps:     maxSteps,
		Model:        model,
	}, defaultSubagentRetryAttempts)
	if err != nil {
		return Result{}, fmt.Errorf("sub-agent: %w", err)
	}
	data := map[string]any{
		"summary":     res.Summary,
		"tool_calls":  res.ToolCalls,
		"duration_ms": res.DurationMs,
	}
	for k, v := range res.Data {
		if _, clash := data[k]; !clash {
			data[k] = v
		}
	}
	return Result{
		Output:     res.Summary,
		Data:       data,
		DurationMs: res.DurationMs,
	}, nil
}
