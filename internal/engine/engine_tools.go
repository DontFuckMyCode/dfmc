// Tool-invocation methods for the Engine. Extracted from engine.go to
// keep construction/lifecycle there. Groups the public ListTools /
// CallTool entry points with the shared approval + hook + panic-guard
// lifecycle path used by both user-initiated and agent-initiated
// tool calls.

package engine

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/skills"
	"github.com/dontfuckmycode/dfmc/internal/tools"
)

func (e *Engine) ListTools() []string {
	if e.Tools == nil {
		return nil
	}
	return e.Tools.List()
}

// SetToolEnabled enables or disables a backend tool by name. Protected tools
// cannot be disabled. The change takes effect immediately — disabled tools
// vanish from Specs/Search/List and refuse Execute.
func (e *Engine) SetToolEnabled(name string, enabled bool) error {
	if e.Tools == nil {
		return nil
	}
	return e.Tools.SetEnabled(name, enabled)
}

// IsToolDisabled reports whether a tool is currently disabled.
func (e *Engine) IsToolDisabled(name string) bool {
	if e.Tools == nil {
		return false
	}
	return e.Tools.IsDisabled(name)
}

// ListDisabledTools returns a sorted list of all disabled tool names.
func (e *Engine) ListDisabledTools() []string {
	if e.Tools == nil {
		return nil
	}
	return e.Tools.ListDisabled()
}

// ToolIsProtected reports whether a tool cannot be disabled.
func (e *Engine) ToolIsProtected(name string) bool {
	return tools.IsToolProtected(name)
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
	for line := range strings.SplitSeq(patch, "\n") {
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
	return e.callToolFromSource(ctx, name, params, SourceUser)
}

// CallToolFromSource is the network-surface entry point. Unlike CallTool
// (user-initiated, bypasses gate), CallToolFromSource tags the call with
// its origin so the approval gate can distinguish real user input from
// traffic originating from web/WS/MCP surfaces. Network sources that
// bypass the gate would let any browser tab drive run_command.
func (e *Engine) CallToolFromSource(ctx context.Context, name string, params map[string]any, source Source) (tools.Result, error) {
	return e.callToolFromSource(ctx, name, params, source)
}

// callToolFromSource is the shared body for CallTool / CallToolFromSource.
// Owns the readiness gate, lifecycle dispatch, and tool:error / tool:complete
// event emission so the two public entry points stay one-liners.
func (e *Engine) callToolFromSource(ctx context.Context, name string, params map[string]any, source Source) (tools.Result, error) {
	if err := e.requireReady("tool call"); err != nil {
		return tools.Result{}, err
	}
	// Allocate a per-call Seq up front so every Event the lifecycle
	// emits for THIS execution (tool:denied, tool:timeout, tool:error,
	// tool:complete, tool:panicked) carries the same value. Stash on
	// ctx so the deeper layers (executeToolWithLifecycle,
	// executeToolWithPanicGuard) can pick it up without a wider
	// parameter list.
	seq := e.allocToolEventSeq()
	ctx = withToolEventSeq(ctx, seq)
	res, err := e.executeToolWithLifecycle(ctx, name, params, source)
	if err != nil {
		e.EventBus.Publish(Event{
			Type:    "tool:error",
			Source:  "engine",
			Seq:     seq,
			Payload: err.Error(),
		})
		return res, err
	}
	e.EventBus.Publish(Event{
		Type:   "tool:complete",
		Source: "engine",
		Seq:    seq,
		Payload: map[string]any{
			"name":       name,
			"durationMs": res.DurationMs,
		},
	})
	return res, nil
}
