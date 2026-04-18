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

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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
		return Result{}, missingParamError("tool_search", "query", req.Params,
			`{"query":"grep"} or {"query":"write files","limit":5}`,
			`query is a free-text search across tool names + descriptions. Returns the top backend tools (meta tools are filtered out — call them directly, not via tool_search).`)
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
		return Result{}, missingNameError("tool_help", req.Params, `{"name":"grep_codebase"}`)
	}
	spec, ok := t.engine.Spec(name)
	if !ok {
		return Result{}, fmt.Errorf(
			"tool_help: unknown tool %q. "+
				"Discover the right name first by calling tool_search: "+
				`{"name":"tool_search","args":{"query":"%s"}}. `+
				"tool_search returns matching tool names; pass one back to tool_help for the schema.",
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
		return Result{}, missingNameError("tool_call", req.Params,
			`{"name":"read_file","args":{"path":"main.go","line_start":1,"line_end":40}}`)
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
	sub := Request{ProjectRoot: req.ProjectRoot, Params: args}
	res, err := t.engine.Execute(ctx, name, sub)
	if err != nil {
		return res, fmt.Errorf("%s: %w", name, err)
	}
	return res, nil
}

// ---- tool_batch_call ------------------------------------------------------

// metaInBatchHint maps a meta tool name to the right action a model
// should take when it accidentally nested it inside tool_batch_call.
// `tool_search` / `tool_help` belong in their own single tool_call (or
// at the agent loop level when the model already knows the tool name);
// `tool_call` / `tool_batch_call` should never appear inside a batch
// at all — the batch IS the dispatcher. Returning this with the error
// stops the model looping on the same wrong shape.
func metaInBatchHint(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "tool_search":
		return `tool_search is for discovering tool names — call it ONCE on its own (not in a batch) before you know which backend tool to use. Then put the actual backend tool (read_file, grep_codebase, edit_file, etc.) into the batch.`
	case "tool_help":
		return `tool_help fetches a single tool's spec — call it on its own, then put the resolved backend tool into the batch.`
	case "tool_call", "tool_batch_call":
		return fmt.Sprintf(`tool_batch_call IS the multi-call dispatcher — drop the %q wrapper and put the backend tools (read_file, grep_codebase, edit_file, ...) directly in the calls array as {"name":"<backend>","args":{...}} entries.`, name)
	default:
		return fmt.Sprintf(`%q is a meta tool. Drop the wrapper and put the actual backend tool name (read_file, grep_codebase, edit_file, ...) directly in the calls array.`, name)
	}
}

type toolBatchCallTool struct{ engine *Engine }

func (t *toolBatchCallTool) Name() string { return "tool_batch_call" }
func (t *toolBatchCallTool) Description() string {
	return "Execute multiple backend tool calls in one round-trip (parallel, bounded)."
}
func (t *toolBatchCallTool) Spec() ToolSpec {
	return ToolSpec{
		Name:    "tool_batch_call",
		Title:   "Batch call tools",
		Summary: "Run several backend tool calls in parallel; results are returned in input order.",
		Purpose: "Reduces wall-clock and round-trips when a task needs several independent reads. Concurrency is bounded by the agent config; a per-call failure does not abort the batch.",
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
		return Result{}, fmt.Errorf(
			`tool_batch_call: calls is empty. Pass at least one {name, args} object: ` +
				`{"calls":[{"name":"read_file","args":{"path":"a.go"}},{"name":"read_file","args":{"path":"b.go"}}]}. ` +
				`For a single call, use tool_call directly — batch is for parallel fan-out.`)
	}
	if len(calls) > maxBatchCalls {
		return Result{}, fmt.Errorf(
			"tool_batch_call: too many calls (%d, max %d). "+
				"Split the work into sequential batches of <= %d each — the agent loop will compact tool output between batches, which would not happen inside a single oversized call. "+
				"Typical healthy fan-out is 2-8 calls; anything in the dozens usually means the planner is doing inside one round what should be spread across rounds.",
			len(calls), maxBatchCalls, maxBatchCalls)
	}

	limit := 1
	if t.engine != nil {
		if n := t.engine.cfg.Agent.ParallelBatchSize; n > 1 {
			limit = n
		}
	}
	if limit > len(calls) {
		limit = len(calls)
	}

	results := make([]map[string]any, len(calls))
	lines := make([]string, len(calls))

	// Shared cancellation context so the dispatcher can surface ctx
	// cancellation (Ctrl-C, agent loop deadline, parent timeout) to
	// every in-flight Execute call. Per-call errors do NOT cancel
	// siblings — batch is best-effort by design; the model can rely on
	// "all results come back, some may be errors". If we ever want
	// fail-fast semantics, that should be a separate tool, not a
	// silent contract change here.
	batchCtx, batchCancel := context.WithCancel(ctx)
	defer batchCancel()

	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup

	for i, call := range calls {
		// target = one-line preview of the most identifying arg
		// (path / pattern / command / ...). Lets downstream renderers
		// show "✓ read_file foo.go" instead of an opaque "✓ read_file".
		// Captured up front so the goroutine doesn't have to re-derive
		// it from c.Args after the call returns.
		target := previewBatchTarget(call.Args)
		entry := map[string]any{"name": call.Name}
		if target != "" {
			entry["target"] = target
		}
		if isMetaTool(call.Name) {
			// Self-teaching error: name the right shape for the next
			// round so the model doesn't loop. The original "cannot
			// invoke meta tools" was too terse — weaker models retried
			// the same shape. Now we tell them exactly what to do:
			// drop the meta wrapper, name the backend tool directly.
			hint := metaInBatchHint(call.Name)
			entry["success"] = false
			entry["error"] = fmt.Sprintf("tool_batch_call cannot dispatch meta tool %q. %s", call.Name, hint)
			results[i] = entry
			lines[i] = fmt.Sprintf("#%d %s: refused (meta tool)", i+1, call.Name)
			continue
		}
		// Halt dispatch when ctx is already cancelled — no point firing
		// goroutines that will immediately observe ctx.Err() and abort.
		if err := batchCtx.Err(); err != nil {
			entry["success"] = false
			entry["error"] = "batch cancelled before dispatch: " + err.Error()
			results[i] = entry
			lines[i] = fmt.Sprintf("#%d %s: cancelled", i+1, call.Name)
			continue
		}

		wg.Add(1)
		// Acquire-or-cancel: a slow batch with cancellation pending
		// would otherwise block forever in `sem <- struct{}{}` while
		// all workers wait for ctx — they don't know to stop. Watch
		// both the slot and the ctx so cancellation drains cleanly.
		select {
		case sem <- struct{}{}:
		case <-batchCtx.Done():
			wg.Done()
			entry["success"] = false
			entry["error"] = "batch cancelled before dispatch: " + batchCtx.Err().Error()
			results[i] = entry
			lines[i] = fmt.Sprintf("#%d %s: cancelled", i+1, call.Name)
			continue
		}
		go func(idx int, c batchCall, slot map[string]any) {
			defer wg.Done()
			defer func() { <-sem }()
			sub := Request{ProjectRoot: req.ProjectRoot, Params: c.Args}
			res, execErr := t.engine.Execute(batchCtx, c.Name, sub)
			slot["duration_ms"] = res.DurationMs
			if execErr != nil {
				slot["success"] = false
				slot["error"] = execErr.Error()
				results[idx] = slot
				lines[idx] = fmt.Sprintf("#%d %s: FAIL %s", idx+1, c.Name, execErr.Error())
				return
			}
			slot["success"] = true
			slot["output"] = res.Output
			slot["data"] = res.Data
			if res.Truncated {
				slot["truncated"] = true
			}
			results[idx] = slot
			lines[idx] = fmt.Sprintf("#%d %s: OK (%dms)", idx+1, c.Name, res.DurationMs)
		}(i, call, entry)
	}
	// Memory-model note: wg.Wait() establishes a happens-before edge
	// from every goroutine's writes to results/lines into this caller's
	// reads below. Do NOT add any access to results / lines above this
	// line outside the dispatch loop — it would introduce a data race.
	wg.Wait()

	joined := make([]string, 0, len(lines))
	for _, l := range lines {
		if l != "" {
			joined = append(joined, l)
		}
	}
	return Result{
		Output: strings.Join(joined, "\n"),
		Data: map[string]any{
			"count":    len(results),
			"results":  results,
			"parallel": limit,
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

// previewBatchTarget returns a one-line "what is this call about?" hint
// derived from the call's args. Picks the first identifying key in a
// deterministic priority order (path > pattern > query > command > dir
// > url > name) so the TUI shows "✓ read_file foo.go" instead of just
// "✓ read_file". Empty string when nothing identifying is present —
// the caller skips the field rather than rendering "✓ read_file (no
// args)".
func previewBatchTarget(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range []string{"path", "pattern", "query", "command", "dir", "url", "name"} {
		if raw, ok := args[key]; ok {
			value := strings.TrimSpace(fmt.Sprint(raw))
			if value == "" {
				continue
			}
			// run_command stays useful when we surface command + first arg.
			if key == "command" {
				if rest := previewCommandArgs(args["args"]); rest != "" {
					value = value + " " + rest
				}
			}
			if len(value) > 64 {
				value = value[:61] + "..."
			}
			return value
		}
	}
	return ""
}

// previewCommandArgs renders a short, single-line preview of the args
// that follow `command` for run_command-shaped calls. Accepts the
// shapes commandArgs() accepts (string, []string, []any).
func previewCommandArgs(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case []string:
		return strings.Join(v, " ")
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, " ")
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// missingNameError builds the actionable "name is required" reply for
// the meta tools. Pre-fix the error was just "name is required" — the
// model couldn't tell whether it had passed the wrong key, sent args
// at the wrong nesting level, or just forgotten the field. Listing the
// keys it ACTUALLY sent + the canonical example lets the next call
// self-correct in a single round instead of looping with the same bug.
func missingNameError(toolName string, params map[string]any, example string) error {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	got := "(empty)"
	if len(keys) > 0 {
		got = "[" + strings.Join(keys, ", ") + "]"
	}
	return fmt.Errorf(
		"%s requires a `name` field naming the backend tool to invoke. "+
			"Got params keys %s but no `name` (or alias `tool`). "+
			"Correct shape: %s",
		toolName, got, example)
}

type batchCall struct {
	Name string
	Args map[string]any
}

func extractCallsArray(params map[string]any) ([]batchCall, error) {
	raw, ok := params["calls"]
	if !ok || raw == nil {
		return nil, missingParamError("tool_batch_call", "calls", params,
			`{"calls":[{"name":"read_file","args":{"path":"a.go"}},{"name":"read_file","args":{"path":"b.go"}}]}`,
			`calls must be a JSON array of {name, args} objects — one per backend tool call to fan out in parallel. For a single call, use tool_call directly (batch is only worth it for 2+ parallel-safe reads).`)
	}
	example := `{"calls":[{"name":"read_file","args":{"path":"a.go"}},{"name":"read_file","args":{"path":"b.go"}}]}`
	var arr []any
	switch v := raw.(type) {
	case []any:
		arr = v
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil, fmt.Errorf(
				"tool_batch_call: calls is empty. Pass at least one {name, args} object: %s",
				example)
		}
		if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
			return nil, fmt.Errorf(
				"tool_batch_call: calls must be a JSON array (got string %q that does not parse: %v). "+
					"Pass it as a real JSON array, not a string: %s",
				trimmed, err, example)
		}
	default:
		return nil, fmt.Errorf(
			"tool_batch_call: calls must be a JSON array of {name, args} objects, got %T. "+
				"Correct shape: %s",
			raw, example)
	}
	out := make([]batchCall, 0, len(arr))
	for i, item := range arr {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf(
				"tool_batch_call: calls[%d] must be an object {name, args}, got %T. Full shape: %s",
				i, item, example)
		}
		name := pickToolName(obj)
		if name == "" {
			return nil, fmt.Errorf(
				"tool_batch_call: calls[%d] is missing the `name` field (or alias `tool`). "+
					"Each call needs the backend tool name. Full shape: %s",
				i, example)
		}
		args, err := extractArgsObject(obj, "args")
		if err != nil {
			return nil, fmt.Errorf("tool_batch_call: calls[%d].args: %w", i, err)
		}
		out = append(out, batchCall{Name: name, Args: args})
	}
	return out, nil
}
