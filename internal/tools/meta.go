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
//	tool_batch_call(calls[])          → executes N backend tools (sequential)
//
// The model pays token cost for only these 4 specs in the prompt; backend
// tools are discovered on demand. Parallel execution lives in the agent loop
// (S4), not here — tool_batch_call's only promise is "these N calls, in
// order, in one round-trip".
//
// All four tools implement the standard Tool interface so they can be
// registered alongside normal tools and executed through the same Engine
// pipeline (failure tracking, output compression, etc.).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// defaultSearchLimit caps the search result count. Default is low on purpose:
// the model can narrow its query instead of drowning in results.
const defaultSearchLimit = 10

// RegisterMetaTools registers the 4 meta tools against the given Engine. Call
// this once during Engine construction. The meta tools hold a reference to
// the Engine so they can dispatch to backend tools.
func RegisterMetaTools(e *Engine) {
	e.Register(&toolSearchTool{engine: e})
	e.Register(&toolHelpTool{engine: e})
	e.Register(&toolCallTool{engine: e})
	e.Register(&toolBatchCallTool{engine: e})
}

// ---- tool_search ----------------------------------------------------------

type toolSearchTool struct{ engine *Engine }

func (t *toolSearchTool) Name() string { return "tool_search" }
func (t *toolSearchTool) Description() string {
	return "Search the backend tool registry by name, tag, or summary."
}
func (t *toolSearchTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "tool_search",
		Title:   "Search tools",
		Summary: "Find backend tools by query; returns ranked short descriptions.",
		Purpose: "Discover which tools exist before calling one. Query matches name, tags, summary.",
		Risk:    RiskRead,
		Tags:    []string{"meta", "discovery"},
		Args: []Arg{
			{Name: "query", Type: ArgString, Required: true, Description: "Free-text search (name, tag, or topic)."},
			{Name: "limit", Type: ArgInteger, Default: defaultSearchLimit, Description: "Max number of results (default 10)."},
		},
		Returns:    "{query, count, results:[{name, summary, risk, tags}]}",
		Examples:   []string{`{"query":"grep"}`, `{"query":"write files","limit":5}`},
		Idempotent: true,
		CostHint:   "cheap",
	}
}
func (t *toolSearchTool) Execute(_ context.Context, req Request) (Result, error) {
	query := strings.TrimSpace(asString(req.Params, "query", ""))
	if query == "" {
		return Result{}, fmt.Errorf("query is required")
	}
	limit := asInt(req.Params, "limit", defaultSearchLimit)
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	hits := t.engine.Search(query, limit)
	// Filter meta tools out of search results so the model doesn't burn
	// turns listing the tools it already has.
	visible := make([]ToolSpec, 0, len(hits))
	for _, s := range hits {
		if isMetaTool(s.Name) {
			continue
		}
		visible = append(visible, s)
	}

	results := make([]map[string]any, 0, len(visible))
	var lines []string
	for _, s := range visible {
		results = append(results, map[string]any{
			"name":    s.Name,
			"summary": s.Summary,
			"risk":    string(s.Risk),
			"tags":    s.Tags,
		})
		lines = append(lines, s.ShortHelp())
	}
	output := strings.Join(lines, "\n")
	if output == "" {
		output = "(no matches)"
	}
	return Result{
		Output: output,
		Data: map[string]any{
			"query":   query,
			"count":   len(results),
			"results": results,
		},
	}, nil
}

// ---- tool_help ------------------------------------------------------------

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
		return Result{}, fmt.Errorf("name is required")
	}
	spec, ok := t.engine.Spec(name)
	if !ok {
		return Result{}, fmt.Errorf("unknown tool: %s", name)
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

// ---- tool_call ------------------------------------------------------------

type toolCallTool struct{ engine *Engine }

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
			{Name: "args", Type: ArgObject, Required: true, Description: "Argument object matching the tool's schema."},
		},
		Returns:  "The backend tool's Result (output, data, truncated, duration_ms).",
		Examples: []string{`{"name":"read_file","args":{"path":"main.go","line_start":1,"line_end":40}}`},
		CostHint: "io-bound",
	}
}
func (t *toolCallTool) Execute(ctx context.Context, req Request) (Result, error) {
	name := pickToolName(req.Params)
	if name == "" {
		return Result{}, fmt.Errorf("name is required")
	}
	if isMetaTool(name) {
		return Result{}, fmt.Errorf("tool_call cannot invoke meta tools (got %q)", name)
	}
	args, err := extractArgsObject(req.Params, "args")
	if err != nil {
		return Result{}, err
	}
	sub := Request{ProjectRoot: req.ProjectRoot, Params: args}
	res, err := t.engine.Execute(ctx, name, sub)
	if err != nil {
		return res, fmt.Errorf("%s: %w", name, err)
	}
	return res, nil
}

// ---- tool_batch_call ------------------------------------------------------

type toolBatchCallTool struct{ engine *Engine }

func (t *toolBatchCallTool) Name() string { return "tool_batch_call" }
func (t *toolBatchCallTool) Description() string {
	return "Execute multiple backend tool calls in one round-trip (sequential)."
}
func (t *toolBatchCallTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "tool_batch_call",
		Title:   "Batch call tools",
		Summary: "Run several backend tool calls in order; results are returned as an array.",
		Purpose: "Reduces round-trips when a task needs several independent reads. A per-call failure does not abort the batch.",
		Risk:    RiskExecute,
		Tags:    []string{"meta", "execute", "batch"},
		Args: []Arg{
			{
				Name: "calls", Type: ArgArray, Required: true,
				Description: "Array of {name, args} objects.",
				Items: &Arg{Type: ArgObject, Description: "{name:string, args:object}"},
			},
		},
		Returns:  "{count, results:[{name, success, output, data, error, duration_ms}]}",
		Examples: []string{`{"calls":[{"name":"read_file","args":{"path":"a.go"}},{"name":"read_file","args":{"path":"b.go"}}]}`},
		CostHint: "io-bound",
	}
}
func (t *toolBatchCallTool) Execute(ctx context.Context, req Request) (Result, error) {
	calls, err := extractCallsArray(req.Params)
	if err != nil {
		return Result{}, err
	}
	if len(calls) == 0 {
		return Result{}, fmt.Errorf("calls is empty")
	}

	results := make([]map[string]any, 0, len(calls))
	var lines []string
	for i, call := range calls {
		entry := map[string]any{"name": call.Name}
		if isMetaTool(call.Name) {
			entry["success"] = false
			entry["error"] = "tool_batch_call cannot invoke meta tools"
			results = append(results, entry)
			lines = append(lines, fmt.Sprintf("#%d %s: refused (meta tool)", i+1, call.Name))
			continue
		}
		sub := Request{ProjectRoot: req.ProjectRoot, Params: call.Args}
		res, err := t.engine.Execute(ctx, call.Name, sub)
		entry["duration_ms"] = res.DurationMs
		if err != nil {
			entry["success"] = false
			entry["error"] = err.Error()
			lines = append(lines, fmt.Sprintf("#%d %s: FAIL %s", i+1, call.Name, err.Error()))
			results = append(results, entry)
			continue
		}
		entry["success"] = true
		entry["output"] = res.Output
		entry["data"] = res.Data
		if res.Truncated {
			entry["truncated"] = true
		}
		lines = append(lines, fmt.Sprintf("#%d %s: OK (%dms)", i+1, call.Name, res.DurationMs))
		results = append(results, entry)
	}
	return Result{
		Output: strings.Join(lines, "\n"),
		Data: map[string]any{
			"count":   len(results),
			"results": results,
		},
	}, nil
}

// ---- helpers --------------------------------------------------------------

func isMetaTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "tool_search", "tool_help", "tool_call", "tool_batch_call":
		return true
	}
	return false
}

func extractArgsObject(params map[string]any, key string) (map[string]any, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		// Be defensive: some models (especially third-party OpenAI-compatible
		// endpoints) emit the arguments under "input" or "arguments" despite
		// our schema naming the field "args". Accept those as aliases when
		// the primary key is missing, rather than failing the call outright.
		if key == "args" {
			for _, alt := range []string{"input", "arguments", "params"} {
				if v, has := params[alt]; has && v != nil {
					raw = v
					ok = true
					break
				}
			}
		}
		if !ok || raw == nil {
			return map[string]any{}, nil
		}
	}
	switch v := raw.(type) {
	case map[string]any:
		return v, nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return map[string]any{}, nil
		}
		var out map[string]any
		if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
			return nil, fmt.Errorf("%s must be a JSON object: %w", key, err)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s must be an object, got %T", key, raw)
	}
}

// pickToolName reads the tool-name field from a call object, accepting
// `name` (the schema-correct key) and `tool` as a fallback. Some models
// — particularly fine-tuned OpenAI-compat endpoints — emit `tool` when
// reproducing a tool-call shape from training data. Accepting the alias
// turns what would be a hard failure into a working call.
func pickToolName(obj map[string]any) string {
	if name := strings.TrimSpace(asString(obj, "name", "")); name != "" {
		return name
	}
	return strings.TrimSpace(asString(obj, "tool", ""))
}

type batchCall struct {
	Name string
	Args map[string]any
}

func extractCallsArray(params map[string]any) ([]batchCall, error) {
	raw, ok := params["calls"]
	if !ok || raw == nil {
		return nil, fmt.Errorf("calls is required")
	}
	var arr []any
	switch v := raw.(type) {
	case []any:
		arr = v
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil, fmt.Errorf("calls is empty")
		}
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			return nil, fmt.Errorf("calls must be a JSON array: %w", err)
		}
	default:
		return nil, fmt.Errorf("calls must be an array, got %T", raw)
	}
	out := make([]batchCall, 0, len(arr))
	for i, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("calls[%d] must be an object", i)
		}
		name := pickToolName(obj)
		if name == "" {
			return nil, fmt.Errorf("calls[%d].name is required", i)
		}
		args, err := extractArgsObject(obj, "args")
		if err != nil {
			return nil, fmt.Errorf("calls[%d].args: %w", i, err)
		}
		out = append(out, batchCall{Name: name, Args: args})
	}
	return out, nil
}
