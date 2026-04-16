package engine

// subagent.go — bounded sub-agent runner used by the delegate_task tool.
//
// A sub-agent runs its own provider-native tool loop with a fresh message
// history and its own step/token budget. It does NOT share parked state with
// the parent: any parked state saved during its run is cleared before the
// parent's (saved aside) state is restored, so a model can delegate
// recursively without stomping on its own workspace.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/internal/tools"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// RunSubagent implements tools.SubagentRunner. The delegate_task tool calls
// this to execute a scoped sub-task with its own fresh context and budget.
func (e *Engine) RunSubagent(ctx context.Context, req tools.SubagentRequest) (tools.SubagentResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(req.Task) == "" {
		return tools.SubagentResult{}, fmt.Errorf("task is required")
	}
	if e == nil || e.Providers == nil {
		return tools.SubagentResult{}, fmt.Errorf("engine not initialized")
	}
	if !e.shouldUseNativeToolLoop() {
		return tools.SubagentResult{}, fmt.Errorf("sub-agent requires a provider with tool support (current: %s)", e.provider())
	}

	// Preserve parent's parked state across this (and any concurrent
	// sibling) sub-agent runs. enterSubagent uses a reference counter so
	// tool_batch_call(delegate_task) fan-outs don't race each other on the
	// shared agentParked field — the parent's state is stashed once and
	// restored once.
	defer e.enterSubagent()()

	task := buildSubagentPrompt(req)
	chunks := e.buildContextChunks(task)
	systemPrompt, systemBlocks := e.buildNativeToolSystemPromptBundle(task, chunks)
	descriptors := metaSpecsToDescriptors(e.Tools.MetaSpecs())

	contextTokens := 0
	for _, c := range chunks {
		contextTokens += c.TokenCount
	}

	seed := &parkedAgentState{
		Question:      task,
		Messages:      e.buildToolLoopRequestMessages(task, chunks, systemPrompt, nil),
		Chunks:        chunks,
		SystemPrompt:  systemPrompt,
		SystemBlocks:  systemBlocks,
		Descriptors:   descriptors,
		ContextTokens: contextTokens,
		TotalTokens:   0,
		Step:          0,
		LastProvider:  e.provider(),
		LastModel:     e.model(),
	}
	if strings.TrimSpace(req.Model) != "" {
		// Treat Model as a profile override for the sub-agent's first call.
		seed.LastProvider = strings.TrimSpace(req.Model)
	}

	lim := e.agentLimits()
	if req.MaxSteps > 0 && req.MaxSteps < lim.MaxSteps {
		lim.MaxSteps = req.MaxSteps
	}
	// Sub-agents get a smaller token budget by default so they can't exhaust
	// the parent's budget with a runaway loop.
	if lim.MaxTokens > 0 {
		lim.MaxTokens = lim.MaxTokens / 2
		if lim.MaxTokens < 10000 {
			lim.MaxTokens = 10000
		}
	}

	start := time.Now()
	e.publishAgentLoopEvent("agent:subagent:start", map[string]any{
		"task":           truncateString(req.Task, 200),
		"role":           req.Role,
		"max_tool_steps": lim.MaxSteps,
		"allowed_tools":  req.AllowedTools,
	})
	completion, err := e.runNativeToolLoop(ctx, seed, lim)
	dur := time.Since(start).Milliseconds()
	e.publishAgentLoopEvent("agent:subagent:done", map[string]any{
		"duration_ms": dur,
		"tool_rounds": len(completion.ToolTraces),
		"parked":      completion.Parked,
		"err":         errString(err),
	})
	if err != nil {
		return tools.SubagentResult{DurationMs: dur}, err
	}

	res := tools.SubagentResult{
		Summary:    strings.TrimSpace(completion.Answer),
		ToolCalls:  len(completion.ToolTraces),
		DurationMs: dur,
		Data: map[string]any{
			"provider":     completion.Provider,
			"model":        completion.Model,
			"tokens":       completion.TokenCount,
			"parked":       completion.Parked,
			"context_refs": len(completion.Context),
		},
	}
	if completion.Parked {
		// Surface this as part of the summary so the parent knows the
		// sub-task was bounded.
		res.Summary = strings.TrimSpace(res.Summary + "\n\n[note: sub-agent reached its step budget; summary reflects partial work]")
	}
	return res, nil
}

// buildSubagentPrompt stitches role and allowed-tool hints onto the raw task
// so the sub-agent sees them as part of its user-facing question. Keeping
// these in the user prompt (rather than inventing a parallel system-prompt
// variant) means behavior degrades gracefully on providers we haven't
// specially tuned for.
func buildSubagentPrompt(req tools.SubagentRequest) string {
	var b strings.Builder
	role := strings.TrimSpace(req.Role)
	if role != "" {
		b.WriteString("You are acting as a ")
		b.WriteString(role)
		b.WriteString(" sub-agent spawned by the main session. Focus narrowly on the task and report back a concise summary.\n\n")
	} else {
		b.WriteString("You are a bounded sub-agent. Complete the task and return a concise summary.\n\n")
	}
	if len(req.AllowedTools) > 0 {
		b.WriteString("Preferred tools: ")
		b.WriteString(strings.Join(req.AllowedTools, ", "))
		b.WriteString(". Avoid tools outside this list unless essential.\n\n")
	}
	b.WriteString("Task:\n")
	b.WriteString(strings.TrimSpace(req.Task))
	return b.String()
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// assert tools.SubagentRunner at compile time.
var _ tools.SubagentRunner = (*Engine)(nil)

// ensure provider.Message / types import usage don't become unused in edits.
var _ = provider.Message{}
var _ = types.RoleUser
