// Process-wide cancellation registry for active drive runs.
//
// Drive runs go straight from the user's command into a Driver
// goroutine — there's no obvious place to hand the user back the
// cancel handle. This registry closes that gap: every Driver.Run
// (and Driver.Resume) registers its cancel func keyed by run ID
// before entering the loop and deregisters on exit. Anything in
// the same process can call Cancel(runID) to interrupt the loop.
//
// Why a process-wide registry and not a per-Driver field: the CLI
// `dfmc drive stop <id>`, the web POST /api/v1/drive/{id}/stop,
// and the TUI /drive stop slash command all need to find the
// running driver by ID, but they don't share a Driver pointer.
// The bbolt single-process lock means only one dfmc owns the
// project at a time, so a process-wide map is sufficient.
//
// Multi-process cancellation (one dfmc CLI cancelling another
// dfmc's run) is not supported here. The process lock makes it
// architecturally impossible anyway: only one dfmc holds the
// store, so the second call would fail with ErrStoreLocked before
// it even saw the registry.

package drive

import (
	"context"
	"sync"
)

// regEntry pairs the cancel func with metadata so callers (e.g.
// `dfmc drive stop <id>`) can list active runs without needing a
// separate ledger. Task is the originating user task verbatim;
// useful for "which drive am I cancelling?" prompts.
type regEntry struct {
	Cancel context.CancelFunc
	Task   string
}

var (
	registryMu sync.RWMutex
	registry   = map[string]*regEntry{}
)

// register stores the cancel func and task for runID. Called from
// Driver right before it enters executeLoop. Overwrites silently
// on duplicate ID — should never happen since IDs are random, but
// belt-and-suspenders.
func register(runID, task string, cancel context.CancelFunc) {
	if runID == "" || cancel == nil {
		return
	}
	registryMu.Lock()
	registry[runID] = &regEntry{Cancel: cancel, Task: task}
	registryMu.Unlock()
}

// unregister removes the entry. Called by Driver in defer so even
// panics clean up. Idempotent; removing an absent ID is a no-op.
func unregister(runID string) {
	if runID == "" {
		return
	}
	registryMu.Lock()
	delete(registry, runID)
	registryMu.Unlock()
}

// Cancel triggers cancellation for runID and reports whether an
// active run was found. The Driver loop receives the ctx
// cancellation and finalizes the run as RunStopped with reason
// "ctx cancelled" — same path as a Ctrl+C. Safe to call from any
// goroutine.
//
// Returns false when the ID isn't registered. That can mean:
//   - The run already finished (and was unregistered).
//   - The ID is wrong / typoed.
//   - The run is in a different process (not supported here; see
//     the package doc comment).
//
// Callers should distinguish these cases by also calling
// store.Load(id) — a Loaded run with terminal status means
// "already done", a missing run means "wrong ID", and a Loaded
// non-terminal run with !Cancel(id) means "stale registry leak"
// (shouldn't happen given the defer in Driver, but worth logging).
func Cancel(runID string) bool {
	registryMu.Lock()
	entry, ok := registry[runID]
	if ok {
		delete(registry, runID)
	}
	registryMu.Unlock()
	if !ok {
		return false
	}
	entry.Cancel()
	return true
}

// ActiveRun records one currently-running drive for the listing
// surface. The CLI's `dfmc drive list` joins this against the
// persisted store so each row carries an "active" badge.
type ActiveRun struct {
	RunID string
	Task  string
}

// ListActive returns a snapshot of every currently-cancellable
// drive run. The returned slice is owned by the caller; the
// registry is only locked for the duration of the copy.
func ListActive() []ActiveRun {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]ActiveRun, 0, len(registry))
	for id, entry := range registry {
		out = append(out, ActiveRun{RunID: id, Task: entry.Task})
	}
	return out
}

// IsActive reports whether runID has a registered cancel func.
// Cheap O(1) check used by the web layer to return 404 vs 200
// from POST /api/v1/drive/{id}/stop without having to call
// Cancel speculatively.
func IsActive(runID string) bool {
	registryMu.RLock()
	_, ok := registry[runID]
	registryMu.RUnlock()
	return ok
}
