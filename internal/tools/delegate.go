package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// SubagentRequest is the structured input to a sub-agent run. The engine
// is responsible for interpreting scope hints — the tools package only
// forwards them.
type SubagentRequest struct {
	Task         string   `json:"task"`          // The self-contained prompt for the sub-agent.
	Role         string   `json:"role"`          // Optional role hint: "researcher", "reviewer", ...
	AllowedTools []string `json:"allowed_tools"` // Optional restriction — if empty, engine picks defaults.
	MaxSteps     int      `json:"max_steps"`     // Tool-call budget; 0 means engine default.
	Model        string   `json:"model"`         // Optional provider profile override.
	// ToolSource is an engine-internal approval scope marker. It is not
	// user/model supplied; adapters like drive may set it so approval
	// overrides apply only to their own sub-agent tool calls.
	ToolSource string `json:"-"`
	// Skills carries skill names to inject as system-prompt overlays in
	// sub-agent prompts. The engine resolves names to playbook text before
	// building the system prompt.
	Skills []string `json:"skills,omitempty"`
}

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
	return "Spawn a bounded sub-agent to handle a focused task."
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

	var allowed []string
	if v, ok := req.Params["allowed_tools"]; ok && v != nil {
		switch vv := v.(type) {
		case []any:
			for _, x := range vv {
				if s := strings.TrimSpace(fmt.Sprint(x)); s != "" {
					allowed = append(allowed, s)
				}
			}
		case []string:
			for _, s := range vv {
				if s = strings.TrimSpace(s); s != "" {
					allowed = append(allowed, s)
				}
			}
		case string:
			for _, p := range strings.Split(vv, ",") {
				if s := strings.TrimSpace(p); s != "" {
					allowed = append(allowed, s)
				}
			}
		}
	}

	res, err := runner.RunSubagent(ctx, SubagentRequest{
		Task:         task,
		Role:         role,
		AllowedTools: allowed,
		MaxSteps:     maxSteps,
		Model:        model,
	})
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
