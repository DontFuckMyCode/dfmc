package tools

// meta_call.go — `tool_call` meta tool. Single-tool dispatch with two
// safety guards: (1) refuse to recursively dispatch other meta tools,
// (2) auto-unwrap one layer of accidental {name:"tool_call", args:{
// name:"<backend>", args:{...}}} double-wrapping with a one-line hint
// so the model corrects on the next call.

import (
	"context"
	"fmt"
)

type toolCallTool struct{ engine *Engine }

// MaxMetaUnwrapDepth is the maximum number of nested tool_call layers that will
// be unwrapped before reaching a real backend tool. Exported so the engine
// package can share it without duplicating the constant.
const MaxMetaUnwrapDepth = 4

func (t *toolCallTool) Name() string { return "tool_call" }
func (t *toolCallTool) Description() string {
	return "Execute a single backend tool by name with arguments."
}
func (t *toolCallTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "tool_call",
		Title:   "Call tool",
		Summary: "Dispatch a single backend tool with its argument object.",
		Purpose: "The primary execution path. Prefer tool_batch_call when making several related calls.",
		Risk:    RiskExecute, // worst-case; actual risk depends on target tool
		Tags:    []string{"meta", "execute"},
		Args: []Arg{
			{Name: "name", Type: ArgString, Required: true, Description: "Backend tool name."},
			{Name: "args", Type: ArgObject, Required: true, Description: "Argument object matching the tool's schema. Include `_reason` here too when the backend call has a more specific reason than the wrapper call."},
		},
		Returns:  "The backend tool's Result (output, data, truncated, duration_ms).",
		Examples: []string{`{"name":"read_file","args":{"path":"main.go","line_start":1,"line_end":40,"_reason":"checking the function before editing it"}}`},
		CostHint: "io-bound",
	}
}
func (t *toolCallTool) Execute(ctx context.Context, req Request) (Result, error) {
	name := pickToolName(req.Params)
	if name == "" {
		return Result{}, missingNameError("tool_call", req.Params,
			`{"name":"read_file","args":{"path":"main.go","line_start":1,"line_end":40}}`)
	}
	ctx, release, budgetErr := enterMetaBudget(ctx, 1)
	if budgetErr != nil {
		return Result{}, budgetErr
	}
	defer release()
	unwrapDepth := 0
	for name == "tool_call" {
		if unwrapDepth >= MaxMetaUnwrapDepth {
			return Result{}, fmt.Errorf(
				`tool_call nesting exceeded max unwrap depth (%d). Drop the wrapper and call the backend tool directly: {"name":"<tool>","args":{...}}`,
				MaxMetaUnwrapDepth)
		}
		inner, ierr := extractArgsObject(req.Params, "args")
		if ierr != nil {
			return Result{}, fmt.Errorf("tool_call double-wrap: %w", ierr)
		}
		req.Params = inner
		name = pickToolName(req.Params)
		unwrapDepth++
	}
	// Auto-unwrap double-wrap: the model invoked tool_call with
	// {name:"tool_call", args:{name:"read_file", args:{...}}} —
	// canonical shape but one layer too deep. Pre-fix this returned
	// "cannot invoke meta tools (got tool_call)" and the model just
	// looped on the same wrap. Post-fix we peel one layer, dispatch
	// the inner call, and prepend a one-line hint so the model learns
	// to drop the wrapper next round. Hard cap at one unwrap so a
	// truly broken {name:tool_call, args:{name:tool_call, args:{...}}}
	// chain trips a real error instead of recursing forever.
	if name == "tool_call" {
		inner, ierr := extractArgsObject(req.Params, "args")
		if ierr != nil {
			return Result{}, fmt.Errorf("tool_call double-wrap: %w", ierr)
		}
		innerName := pickToolName(inner)
		if innerName == "" || innerName == "tool_call" {
			return Result{}, fmt.Errorf(
				`tool_call was invoked with name="tool_call" — that's a double-wrap. Drop the outer wrapper and call the backend tool directly: {"name":"<tool>","args":{...}}. Got nested args=%v`,
				inner)
		}
		if isMetaTool(innerName) {
			return Result{}, fmt.Errorf("tool_call cannot invoke meta tools even via unwrap (got nested %q)", innerName)
		}
		innerArgs, aerr := extractArgsObject(inner, "args")
		if aerr != nil {
			return Result{}, aerr
		}
		inheritToolReason(ctx, innerArgs)
		sub := Request{ProjectRoot: req.ProjectRoot, Params: innerArgs}
		res, err := t.engine.Execute(ctx, innerName, sub)
		hint := fmt.Sprintf("[tool_call: auto-unwrapped redundant outer tool_call → dispatched %s. Next time call %s directly: {\"name\":%q,\"args\":{...}}]\n", innerName, innerName, innerName)
		if err != nil {
			return res, fmt.Errorf("%s%s: %w", hint, innerName, err)
		}
		res.Output = hint + res.Output
		return res, nil
	}
	if isMetaTool(name) {
		return Result{}, fmt.Errorf("tool_call cannot invoke meta tools (got %q). Call the backend tool directly: {\"name\":\"read_file\",\"args\":{...}}. Meta tools (tool_call, tool_batch_call, tool_search, tool_help) are dispatched by the agent loop, not by each other", name)
	}
	args, err := extractArgsObject(req.Params, "args")
	if err != nil {
		return Result{}, err
	}
	inheritToolReason(ctx, args)
	sub := Request{ProjectRoot: req.ProjectRoot, Params: args}
	res, err := t.engine.Execute(ctx, name, sub)
	if err != nil {
		if unwrapDepth > 0 {
			hint := fmt.Sprintf("[tool_call: auto-unwrapped %d redundant tool_call layer(s) -> dispatched %s. Next time call %s directly: {\"name\":%q,\"args\":{...}}]\n", unwrapDepth, name, name, name)
			return res, fmt.Errorf("%s%s: %w", hint, name, err)
		}
		return res, fmt.Errorf("%s: %w", name, err)
	}
	if unwrapDepth > 0 {
		hint := fmt.Sprintf("[tool_call: auto-unwrapped %d redundant tool_call layer(s) -> dispatched %s. Next time call %s directly: {\"name\":%q,\"args\":{...}}]\n", unwrapDepth, name, name, name)
		res.Output = hint + res.Output
	}
	return res, nil
}
