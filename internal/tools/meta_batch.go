package tools

// meta_batch.go — `tool_batch_call` meta tool. Fans N backend tool
// calls onto a semaphore bounded by AgentConfig.ParallelBatchSize. A
// per-call failure does NOT abort the batch — every call returns its
// own success/error so the model gets all results, partially failed
// or not. Refuses to dispatch other meta tools (with a self-teaching
// hint via metaInBatchHint).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"runtime/debug"
	"sync"
)

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
				Description: "Array of {name, args} objects. Give each args object its own `_reason` when possible; otherwise the batch-level `_reason` is inherited.",
				Items:       &Arg{Type: ArgObject, Description: "{name:string, args:object}; args may include _reason"},
			},
		},
		Returns:  "{count, results:[{name, success, output, data, error, duration_ms}]}",
		Examples: []string{`{"calls":[{"name":"read_file","args":{"path":"a.go","_reason":"compare first implementation"}},{"name":"read_file","args":{"path":"b.go","_reason":"compare second implementation"}}]}`},
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
				`For a single call, use tool_call directly — batch is for parallel fan-out`)
	}
	if len(calls) > maxBatchCalls {
		return Result{}, fmt.Errorf(
			"tool_batch_call: too many calls (%d, max %d). "+
				"Split the work into sequential batches of <= %d each — the agent loop will compact tool output between batches, which would not happen inside a single oversized call. "+
				"Typical healthy fan-out is 2-8 calls; anything in the dozens usually means the planner is doing inside one round what should be spread across rounds",
			len(calls), maxBatchCalls, maxBatchCalls)
	}
	ctx, release, budgetErr := enterMetaBudget(ctx, len(calls))
	if budgetErr != nil {
		return Result{}, budgetErr
	}
	defer release()

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
		reason := inheritToolReason(ctx, call.Args)
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
		if reason != "" {
			entry["reason"] = reason
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
		if t.engine.IsDisabled(call.Name) {
			entry["success"] = false
			entry["error"] = fmt.Sprintf("%q is disabled and cannot be called. Enable it via the tools panel or `dfmc tools enable %s`", call.Name, call.Name)
			results[i] = entry
			lines[i] = fmt.Sprintf("#%d %s: disabled", i+1, call.Name)
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
			// Release must run even when Execute panics — otherwise the
			// semaphore slot leaks and subsequent batches hang.
			defer func() { <-sem }()
			var res Result
			var execErr error
			func() {
				defer func() {
					if p := recover(); p != nil {
						execErr = fmt.Errorf("panic in %s: %v\n%s", c.Name, p, string(debug.Stack()))
					}
				}()
				res, execErr = t.engine.Execute(batchCtx, c.Name,
					Request{ProjectRoot: req.ProjectRoot, Params: c.Args})
			}()
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
	batchCancel()

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
