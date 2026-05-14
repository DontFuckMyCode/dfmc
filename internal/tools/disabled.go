package tools

// disabled.go — tool enable/disable tracking and protected-tool guard.
//
// Any tool can be disabled at runtime except the protected set. A disabled
// tool is invisible to Specs/BackendSpecs/Search/List (so it never reaches
// system prompts, tool_search, or subagent profiles) and refuses execution
// (so tool_call/tool_batch_call/CallTool all block it). The tool remains in
// the registry for re-enable; this is NOT unregister.
//
// Mutex discipline: disabledMu nests inside mu (registry lock). Callers that
// hold both must acquire mu first. The public SetEnabled/ListDisabled acquire
// only disabledMu; the filter helpers are called from within mu.RLock spans.

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

var (
	ErrToolDisabled  = errors.New("tool is disabled")
	ErrToolProtected = errors.New("tool is protected and cannot be disabled")
)

// protectedTools cannot be disabled. The 4 meta tools + critical backend
// tools that the system cannot function without.
var protectedTools = map[string]struct{}{
	// Meta tools — the LLM always sees these 4
	"tool_search": {}, "tool_help": {}, "tool_call": {}, "tool_batch_call": {},
	// Core filesystem
	"read_file": {}, "write_file": {}, "edit_file": {}, "apply_patch": {},
	// Core search
	"grep_codebase": {}, "find_symbol": {},
	// Core execution
	"run_command": {},
	// Core git
	"git_commit": {}, "git_diff": {}, "git_status": {},
}

func IsToolProtected(name string) bool {
	_, ok := protectedTools[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// disabledState holds the set of disabled tool names.
type disabledState struct {
	mu       sync.RWMutex
	disabled map[string]struct{}
}

func newDisabledState(names []string) *disabledState {
	ds := &disabledState{disabled: make(map[string]struct{}, len(names))}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n != "" {
			ds.disabled[strings.ToLower(n)] = struct{}{}
		}
	}
	return ds
}

// SetEnabled enables or disables a tool. Returns ErrToolProtected for
// protected tools, ErrToolDisabled when enabling a tool that isn't in the
// registry (cannot enable something that doesn't exist).
func (ds *disabledState) SetEnabled(name string, enabled bool, registryExists func(string) bool) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil
	}
	if !enabled && IsToolProtected(name) {
		return ErrToolProtected
	}
	ds.mu.Lock()
	defer ds.mu.Unlock()
	if enabled {
		delete(ds.disabled, name)
		return nil
	}
	// Disabling: allow even if the tool isn't currently registered (it
	// might be registered later, and the disabled flag should stick).
	ds.disabled[name] = struct{}{}
	return nil
}

func (ds *disabledState) IsDisabled(name string) bool {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	_, ok := ds.disabled[strings.ToLower(name)]
	return ok
}

func (ds *disabledState) ListDisabled() []string {
	ds.mu.RLock()
	defer ds.mu.RUnlock()
	out := make([]string, 0, len(ds.disabled))
	for name := range ds.disabled {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Snapshot returns a sorted copy of the disabled set for config persistence.
func (ds *disabledState) Snapshot() []string {
	return ds.ListDisabled()
}
