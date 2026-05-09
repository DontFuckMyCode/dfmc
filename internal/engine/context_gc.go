// context_gc.go — engine-side garbage collector for the active
// conversation branch. Runs AFTER the model's [cleanup:] hint is
// honoured (which is the LLM curating its own history) and picks up
// purely-mechanical garbage the model can't be expected to spot:
//
//   - Failed-retry: an assistant turn whose only tool calls all failed,
//     and a later turn successfully repeated the same tool on the same
//     target. The earlier turn is "stale noise" — keeping it just makes
//     the model re-read the same error.
//   - Duplicate-read: a successful read_file on path P with end line L,
//     followed by a later successful read_file on the SAME path with
//     end >= L. The earlier slice is fully covered.
//
// Conservative by construction:
//   - Never drops a message with non-empty stripped Content (the model
//     said something the user might still need).
//   - Never drops the most recent assistant turn (it's the working set
//     the model just produced; the model will reference it next round).
//   - A single tool call we cannot prove is dominated keeps the whole
//     message — partial dominance is not enough.
//   - Unknown tools (anything outside read_file/write_file/edit_file/
//     apply_patch/list_dir/grep_codebase/find_symbol/glob) cannot be
//     keyed safely, so they always count as non-dominated.
//
// This pass is a complement to the LLM's [cleanup:] hint, not a
// replacement: the model still owns "did this exchange resolve a
// question?" judgement; the engine only handles the patterns where
// dominance is structurally provable from the trace.

package engine

import (
	"strings"

	"github.com/dontfuckmycode/dfmc/pkg/types"
)

type gcReason string

const (
	gcReasonFailedRetry   gcReason = "failed_retry_superseded"
	gcReasonDuplicateRead gcReason = "duplicate_read_superseded"
)

type gcDecision struct {
	DropIDs []string
	Reasons map[string]gcReason
}

type gcReadSpan struct {
	msgIdx  int
	endLine int // 0 = open-ended / full file
}

// ContextGCDecision is the public preview of what the engine-side GC
// would drop on the current active branch. Reasons are bucketed by
// pattern name so callers (TUI, web) can group them in the UI.
type ContextGCDecision struct {
	DropIDs []string
	Reasons map[string]string
}

// PreviewContextGC runs the deterministic dominance pass without
// touching the conversation. Returns an empty decision when there is
// no active branch or nothing to drop. Cheap — pure-function over a
// snapshot of the active branch.
func (e *Engine) PreviewContextGC() ContextGCDecision {
	if e == nil || e.Conversation == nil {
		return ContextGCDecision{}
	}
	active := e.Conversation.Active()
	if active == nil {
		return ContextGCDecision{}
	}
	branch := active.Branches[active.Branch]
	d := garbageCollectActiveBranch(branch)
	out := ContextGCDecision{Reasons: map[string]string{}}
	out.DropIDs = append(out.DropIDs, d.DropIDs...)
	for id, r := range d.Reasons {
		out.Reasons[id] = string(r)
	}
	return out
}

// RunContextGC executes the dominance pass and prunes the matching
// messages from the active branch. Returns the same decision shape
// as PreviewContextGC plus the count actually removed (Conversation
// .RemoveMessagesByID may return less than len(DropIDs) if a parallel
// caller already removed some). Publishes a context:gc event on the
// engine bus so any subscribed surface (TUI, web SSE) reflects the
// prune. Cheap to call repeatedly — a no-op when nothing dominates.
func (e *Engine) RunContextGC() (ContextGCDecision, int) {
	decision := e.PreviewContextGC()
	if len(decision.DropIDs) == 0 || e.Conversation == nil {
		return decision, 0
	}
	dropped := e.Conversation.RemoveMessagesByID(decision.DropIDs)
	if dropped > 0 && e.EventBus != nil {
		e.EventBus.Publish(Event{
			Type:   "context:gc",
			Source: "engine",
			Payload: map[string]any{
				"dropped": dropped,
				"ids":     decision.DropIDs,
				"reasons": decision.Reasons,
				"manual":  true,
			},
		})
	}
	return decision, dropped
}

// garbageCollectActiveBranch returns the IDs of messages that are
// safe to drop from the rolling history. See file-level doc for the
// dominance rules. The input slice is read-only; the caller is
// responsible for actually removing the IDs via Conversation
// .RemoveMessagesByID.
func garbageCollectActiveBranch(msgs []types.Message) gcDecision {
	out := gcDecision{Reasons: map[string]gcReason{}}
	if len(msgs) < 2 {
		return out
	}

	laterSuccessByKey := map[string][]int{}
	laterReadsByPath := map[string][]gcReadSpan{}

	for i, m := range msgs {
		if m.Role != types.RoleAssistant {
			continue
		}
		for j, r := range m.Results {
			if !r.Success {
				continue
			}
			var params map[string]any
			if j < len(m.ToolCalls) {
				params = m.ToolCalls[j].Params
			}
			if key, ok := toolCallDominanceKey(r.Name, params); ok {
				laterSuccessByKey[key] = append(laterSuccessByKey[key], i)
			}
			if r.Name == "read_file" {
				path := stringFromParams(params, "path", "file_path")
				if path != "" {
					laterReadsByPath[path] = append(laterReadsByPath[path], gcReadSpan{
						msgIdx:  i,
						endLine: intFromParams(params, "line_end", "end_line", "to_line"),
					})
				}
			}
		}
	}

	lastAssistantIdx := -1
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == types.RoleAssistant {
			lastAssistantIdx = i
			break
		}
	}

	for i, m := range msgs {
		if i == lastAssistantIdx || m.Role != types.RoleAssistant || m.ID == "" {
			continue
		}
		if strings.TrimSpace(m.Content) != "" {
			continue
		}
		if len(m.Results) == 0 {
			continue
		}
		dominated := true
		var reason gcReason
		for j, r := range m.Results {
			var params map[string]any
			if j < len(m.ToolCalls) {
				params = m.ToolCalls[j].Params
			}
			if !r.Success {
				key, ok := toolCallDominanceKey(r.Name, params)
				if !ok || !hasIndexAfter(laterSuccessByKey[key], i) {
					dominated = false
					break
				}
				reason = gcReasonFailedRetry
				continue
			}
			if r.Name == "read_file" {
				path := stringFromParams(params, "path", "file_path")
				if path == "" || !readSpanCovered(laterReadsByPath[path], i, intFromParams(params, "line_end", "end_line", "to_line")) {
					dominated = false
					break
				}
				if reason == "" {
					reason = gcReasonDuplicateRead
				}
				continue
			}
			// A successful non-read tool call (grep_codebase, find_symbol,
			// list_dir, glob, run_command, …) is NOT considered dominated
			// even when a later success on the same key exists. Reason:
			// the underlying state may have changed between the two calls
			// (a write_file/edit_file/apply_patch in between would shift
			// what grep matches or what list_dir returns) and we have no
			// cheap way to prove dominance from the trace alone. Keep it.
			dominated = false
			break
		}
		if dominated && reason != "" {
			out.DropIDs = append(out.DropIDs, m.ID)
			out.Reasons[m.ID] = reason
		}
	}
	return out
}

// toolCallDominanceKey returns a (name, target) key the GC can use to
// decide whether two calls touch the same thing. Only built-in tools
// where the primary identifier is unambiguous are included; unknown
// tools return ok=false and are treated as non-dominated.
func toolCallDominanceKey(name string, params map[string]any) (string, bool) {
	var primary string
	switch name {
	case "read_file", "write_file", "edit_file", "apply_patch", "list_dir":
		primary = stringFromParams(params, "path", "file_path")
	case "grep_codebase":
		primary = stringFromParams(params, "pattern", "query")
	case "find_symbol":
		primary = stringFromParams(params, "name", "symbol")
	case "glob":
		primary = stringFromParams(params, "pattern")
	default:
		return "", false
	}
	if primary == "" {
		return "", false
	}
	return name + "|" + primary, true
}

func stringFromParams(params map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := params[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

func intFromParams(params map[string]any, keys ...string) int {
	for _, k := range keys {
		v, ok := params[k]
		if !ok {
			continue
		}
		switch n := v.(type) {
		case int:
			return n
		case int64:
			return int(n)
		case float64:
			return int(n)
		}
	}
	return 0
}

func hasIndexAfter(idxs []int, after int) bool {
	for _, i := range idxs {
		if i > after {
			return true
		}
	}
	return false
}

// readSpanCovered reports whether some later read_file on the same path
// covers the span ending at myEnd. End=0 means open-ended (whole file);
// any later read with end >= myEnd, or with end=0 itself, dominates.
func readSpanCovered(spans []gcReadSpan, after int, myEnd int) bool {
	for _, s := range spans {
		if s.msgIdx <= after {
			continue
		}
		if myEnd == 0 {
			// We were full-file; only a full-file later read dominates.
			if s.endLine == 0 {
				return true
			}
			continue
		}
		if s.endLine == 0 || s.endLine >= myEnd {
			return true
		}
	}
	return false
}
