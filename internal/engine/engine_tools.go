// Tool-invocation methods for the Engine. Extracted from engine.go to
// keep construction/lifecycle there. Groups the public ListTools /
// CallTool entry points with the shared approval + hook + panic-guard
// lifecycle path used by both user-initiated and agent-initiated
// tool calls.

package engine

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/hooks"
	"github.com/dontfuckmycode/dfmc/internal/skills"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func (e *Engine) ListTools() []string {
	if e.Tools == nil {
		return nil
	}
	return e.Tools.List()
}

// invalidateContextForTool tracks files modified by edit_file, write_file,
// or apply_patch so the next buildContextChunks call excludes them from
// context retrieval. This prevents stale context chunks from being served
// when a file has been modified in the last few minutes — the LLM must read
// the fresh version via read_file instead.
// It also records files read via read_file so they are excluded from
// context deduplication (the model already has the content via conversation).
func (e *Engine) invalidateContextForTool(name string, params map[string]any) {
	if e.Context == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	switch name {
	case "edit_file", "write_file":
		if path, ok := params["path"].(string); ok && path != "" {
			abs, _ := filepath.Abs(path)
			if e.modifiedFiles == nil {
				e.modifiedFiles = make(map[string]time.Time)
			}
			e.modifiedFiles[abs] = time.Now()
			e.Context.Invalidate(abs)
		}
	case "apply_patch":
		if patch, ok := params["patch"].(string); ok && patch != "" {
			paths := extractPathsFromPatch(patch)
			if e.modifiedFiles == nil {
				e.modifiedFiles = make(map[string]time.Time)
			}
			now := time.Now()
			for _, p := range paths {
				abs, _ := filepath.Abs(p)
				e.modifiedFiles[abs] = now
				e.Context.Invalidate(abs)
			}
		}
	case "read_file":
		// Mark as seen so context building skips it (deduplication).
		// No need to invalidate codemap — reading doesn't change content.
		if path, ok := params["path"].(string); ok && path != "" {
			abs, _ := filepath.Abs(path)
			if e.seenFiles == nil {
				e.seenFiles = make(map[string]struct{})
			}
			e.seenFiles[abs] = struct{}{}
		}
	}
}

// skillToolPolicy returns a guidance string when an active skill constrains
// the tool being called. Returns empty string when no policy applies.
// "Prefer" suggestions are soft; the model remains free to override with
// sufficient justification.
func (e *Engine) skillToolPolicy(toolName string) string {
	if len(e.activeSkills) == 0 {
		return ""
	}
	var pref []string
	var allowed []string
	for _, name := range e.activeSkills {
		skill, ok := skills.SkillForName(e.ProjectRoot, name)
		if !ok {
			continue
		}
		for _, t := range skill.Preferred {
			if t == toolName {
				continue
			}
			pref = append(pref, t)
		}
		for _, t := range skill.Allowed {
			if t == toolName {
				continue
			}
			allowed = append(allowed, t)
		}
	}
	if len(pref) > 0 || len(allowed) > 0 {
		parts := make([]string, 0, 2)
		if len(pref) > 0 {
			parts = append(parts, "prefer: "+strings.Join(pref, ", "))
		}
		if len(allowed) > 0 {
			parts = append(parts, "allowed: "+strings.Join(allowed, ", "))
		}
		return "skill tool policy — " + strings.Join(parts, " | ")
	}
	return ""
}

// extractPathsFromPatch returns the unique set of file paths affected by a
// unified-diff string. It parses both --- a/<path> and +++ b/<path> headers.
func extractPathsFromPatch(patch string) []string {
	seen := map[string]struct{}{}
	var results []string
	for _, line := range strings.Split(patch, "\n") {
		m := diffPathRE.FindStringSubmatch(line)
		if len(m) < 2 {
			continue
		}
		p := filepath.ToSlash(m[1])
		if p != "" && p != "/dev/null" {
			if _, exists := seen[p]; !exists {
				seen[p] = struct{}{}
				results = append(results, p)
			}
		}
	}
	return results
}

// diffPathRE matches --- a/<path> and +++ b/<path> lines in unified diffs.
var diffPathRE = regexp.MustCompile(`^(?:--- a/|\+\+\+ b/)([^\t ]+)`)

// Source is the logical origin of a tool call. Used by the approval
// gate to distinguish user-initiated calls (bypass) from calls that
// come from a network surface (web, ws, mcp — require approval on
// gated tools).
type Source string

const (
	SourceUser Source = "user" // TUI/CLI real user input; always allowed
	SourceWeb  Source = "web"
	SourceWS   Source = "ws"
	SourceMCP  Source = "mcp"
	SourceCLI  Source = "cli" // dfmc tool run — operator's own tooling
)

func (e *Engine) CallTool(ctx context.Context, name string, params map[string]any) (tools.Result, error) {
	if err := e.requireReady("tool call"); err != nil {
		return tools.Result{}, err
	}
	res, err := e.executeToolWithLifecycle(ctx, name, params, string(SourceUser))
	if err != nil {
		e.EventBus.Publish(Event{
			Type:    "tool:error",
			Source:  "engine",
			Payload: err.Error(),
		})
		return res, err
	}
	e.EventBus.Publish(Event{
		Type:   "tool:complete",
		Source: "engine",
		Payload: map[string]any{
			"name":       name,
			"durationMs": res.DurationMs,
		},
	})
	return res, nil
}

// CallToolFromSource is the network-surface entry point. Unlike CallTool
// (user-initiated, bypasses gate), CallToolFromSource tags the call with
// its origin so the approval gate can distinguish real user input from
// traffic originating from web/WS/MCP surfaces. Network sources that
// bypass the gate would let any browser tab drive run_command.
func (e *Engine) CallToolFromSource(ctx context.Context, name string, params map[string]any, source Source) (tools.Result, error) {
	if err := e.requireReady("tool call"); err != nil {
		return tools.Result{}, err
	}
	res, err := e.executeToolWithLifecycle(ctx, name, params, string(source))
	if err != nil {
		e.EventBus.Publish(Event{
			Type:    "tool:error",
			Source:  "engine",
			Payload: err.Error(),
		})
		return res, err
	}
	e.EventBus.Publish(Event{
		Type:   "tool:complete",
		Source: "engine",
		Payload: map[string]any{
			"name":       name,
			"durationMs": res.DurationMs,
		},
	})
	return res, nil
}

// executeToolWithLifecycle is the single point of entry for every tool
// invocation in the engine. It owns:
//   - approval gate (config.Tools.RequireApproval / Approver callback)
//   - pre_tool/post_tool hook dispatch with full payload
//   - raw tools.Engine.Execute call
//
// Both the user-initiated CallTool path and the agent-loop-initiated
// path (agent_loop_native, subagent) funnel through here so hooks and
// approval behave identically regardless of who decided to fire the
// tool.
//
// The `source` tag distinguishes user-initiated calls ("user") from
// agent calls ("agent", "subagent"). The approval gate currently only
// gates agent-initiated calls — user typing /tool is already explicit
// consent.
// executeToolWithPanicGuard converts any panic raised inside a tool's
// Execute into a regular error. Without this guard, a nil-pointer or
// out-of-bounds inside any tool implementation kills the entire DFMC
// process — taking down the agent loop, every connected web/SSE
// client, the TUI session, and every queued reply. Worse, the panic
// happens at an unpredictable point in the agent's reasoning so the
// failure looks like a hang from the user's side.
//
// Tools are first-party but they exec subprocesses, parse arbitrary
// AST shapes, walk filesystems with surprising layouts. The blast
// radius of "one tool bug crashes everything" is large enough to
// justify the cost of a defer/recover wrapper. The agent loop already
// knows how to surface tool errors back to the model (`isError=true`
// tool_result), so the recovered panic is just another error from
// the loop's perspective.
//
// We attach a stack trace to the error so a crash dump in the
// conversation log lets us file a real bug report instead of "the
// thing died."
func (e *Engine) executeToolWithPanicGuard(ctx context.Context, name string, params map[string]any) (res tools.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			err = fmt.Errorf("tool %s panicked: %v\n%s", name, r, truncateStackForError(stack))
			// Reset res so the caller sees an empty Result + the error,
			// not whatever partial state the tool may have populated
			// before panicking.
			res = tools.Result{}
			e.EventBus.Publish(Event{
				Type:   "tool:panicked",
				Source: "engine",
				Payload: map[string]any{
					"name":  name,
					"panic": fmt.Sprintf("%v", r),
				},
			})
		}
	}()
	return e.Tools.Execute(ctx, name, tools.Request{
		ProjectRoot: e.ProjectRoot,
		Params:      params,
	})
}

// truncateStackForError keeps the first ~2 KiB of a stack trace so a
// recovered tool panic doesn't bloat the conversation JSONL with a
// 10 KiB Go runtime dump for every retry. The head frames are the
// useful bit anyway — they point at the panic site.
//
// The constant is named maxStackLen rather than `cap` because `cap`
// shadows the built-in cap() — a later refactor that introduced
// `cap(slice)` anywhere in this function (or any function inheriting
// the const via package scope, since this was at package level) would
// silently bind to the const and either fail to compile or, worse,
// type-check as int and produce wrong values.
func truncateStackForError(stack []byte) string {
	const maxStackLen = 2048
	if len(stack) <= maxStackLen {
		return string(stack)
	}
	return string(stack[:maxStackLen]) + "\n[stack truncated]"
}

func (e *Engine) executeToolWithLifecycle(ctx context.Context, name string, params map[string]any, source string) (tools.Result, error) {
	if e.Tools == nil {
		return tools.Result{}, errors.New("tool engine is not initialized")
	}
	// Sub-agent allowlist gate — fires before approval so unlisted tools
	// are refused without prompting even when the approver is permissive.
	if denial := checkSubagentAllowlist(ctx, name, metaInnerNames(name, params)); denial != "" {
		e.recordDenial(name, source, denial)
		e.EventBus.Publish(Event{
			Type:   "tool:denied",
			Source: "engine",
			Payload: map[string]any{
				"name":   name,
				"reason": denial,
				"source": source,
			},
		})
		return tools.Result{}, fmt.Errorf("tool %s denied: %s", name, denial)
	}
	// Approval gate — only engages for non-user sources and only when
	// the tool is on the approval list. Blocks until the Approver
	// responds or returns an implicit deny on timeout. See approver.go.
	if source != "user" && e.requiresApproval(name, source) {
		decision := e.askToolApproval(ctx, name, params, source)
		if !decision.Approved {
			reason := decision.Reason
			if reason == "" {
				reason = "user denied"
			}
			e.recordDenial(name, source, reason)
			e.EventBus.Publish(Event{
				Type:   "tool:denied",
				Source: "engine",
				Payload: map[string]any{
					"name":   name,
					"reason": reason,
					"source": source,
				},
			})
			return tools.Result{}, fmt.Errorf("tool %s denied: %s", name, reason)
		}
	}
	// Peek at the params so we can mirror pre/post_tool hook fires onto
	// each inner backend tool name when the outer name is a meta wrapper
	// (tool_call / tool_batch_call). Approval stays at the meta level
	// so a 4-tool batch doesn't fire 4 prompts, but hooks fan out so an
	// operator-configured pre_tool for e.g. run_command still sees the
	// call. See engine_meta_hooks.go for the unwrap logic.
	innerNames := metaInnerNames(name, params)
	if e.Hooks != nil && e.Hooks.Count(hooks.EventPreTool) > 0 {
		e.Hooks.Fire(ctx, hooks.EventPreTool, hooks.Payload{
			"tool_name":    name,
			"tool_source":  source,
			"project_root": e.ProjectRoot,
		})
		for _, inner := range innerNames {
			e.Hooks.Fire(ctx, hooks.EventPreTool, hooks.Payload{
				"tool_name":    inner,
				"tool_source":  source,
				"wrapped_by":   name,
				"project_root": e.ProjectRoot,
			})
		}
	}
	res, err := e.executeToolWithPanicGuard(ctx, name, params)
	if err == nil {
		e.invalidateContextForTool(name, params)
	}
	if e.Hooks != nil && e.Hooks.Count(hooks.EventPostTool) > 0 {
		success := "true"
		if err != nil {
			success = "false"
		}
		e.Hooks.Fire(ctx, hooks.EventPostTool, hooks.Payload{
			"tool_name":        name,
			"tool_source":      source,
			"tool_success":     success,
			"tool_duration_ms": fmt.Sprintf("%d", res.DurationMs),
			"project_root":     e.ProjectRoot,
		})
		// Inner post_tool fires carry the outer meta success. For
		// tool_batch_call this is best-effort: one inner can fail while
		// others succeed and the outer may still report success=true
		// (the batch returns each entry's success separately). Hooks
		// that need per-inner granularity should subscribe to the
		// engine event bus's tool:step events instead.
		for _, inner := range innerNames {
			e.Hooks.Fire(ctx, hooks.EventPostTool, hooks.Payload{
				"tool_name":        inner,
				"tool_source":      source,
				"tool_success":     success,
				"tool_duration_ms": fmt.Sprintf("%d", res.DurationMs),
				"wrapped_by":       name,
				"project_root":     e.ProjectRoot,
			})
		}
	}
	return res, err
}
