package tools

// meta_help.go — `tool_help` meta tool. Returns the full schema +
// long-form usage guide for a single backend tool. Typical sequence:
// tool_search to find the right name, then tool_help to learn the
// args, then tool_call to execute.

import (
	"context"
	"fmt"
	"strings"
)

type toolHelpTool struct{ engine *Engine }

func (t *toolHelpTool) Name() string { return "tool_help" }
func (t *toolHelpTool) Description() string {
	return "Return the full specification (args, returns, examples) for one backend tool."
}
func (t *toolHelpTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "tool_help",
		Title:   "Tool help",
		Summary: "Fetch the full schema and usage guide for a named backend tool.",
		Purpose: "Use after tool_search to learn the exact args a tool expects before calling it.",
		Risk:    RiskRead,
		Tags:    []string{"meta", "discovery", "schema"},
		Args: []Arg{
			{Name: "name", Type: ArgString, Required: true, Description: "Exact tool name (from tool_search results)."},
		},
		Returns:    "{name, spec:{...}, schema:{...}, help:\"...\"}",
		Examples:   []string{`{"name":"grep_codebase"}`},
		Idempotent: true,
		CostHint:   "cheap",
	}
}
func (t *toolHelpTool) Execute(_ context.Context, req Request) (Result, error) {
	name := strings.TrimSpace(asString(req.Params, "name", ""))
	if name == "" {
		return Result{}, missingNameError("tool_help", req.Params, `{"name":"grep_codebase"}`)
	}
	spec, ok := t.engine.Spec(name)
	if !ok {
		return Result{}, fmt.Errorf(
			"tool_help: unknown tool %q. "+
				"Discover the right name first by calling tool_search: "+
				`{"name":"tool_search","args":{"query":"%s"}}. `+
				"tool_search returns matching tool names; pass one back to tool_help for the schema",
			name, name)
	}
	return Result{
		Output: spec.LongHelp(),
		Data: map[string]any{
			"name":   spec.Name,
			"spec":   spec,
			"schema": spec.JSONSchema(),
			"help":   spec.LongHelp(),
		},
	}, nil
}
