// Package drive runs a self-driving "plan -> execute TODOs in order"
// loop on top of engine.Engine.

package drive

import "strings"

// StatusFlags provides a more extensible status system using bit flags.
// New statuses can be added by defining new constants without modifying
// every switch statement in the codebase.
type StatusFlags uint32

// Status flag constants — powers of 2 for OR-ability.
const (
	FlagPending   StatusFlags = 1 << iota // waiting on dependencies
	FlagRunning                            // currently executing
	FlagTerminal                           // done, blocked, skipped — no further scheduling
	FlagWaiting                            // external wait (review, approval, etc.)
	FlagExternal                           // external process (external_review)
	FlagVerifying                          // verification in progress
)

// HasFlag reports whether s has the given flag set.
func (s StatusFlags) HasFlag(f StatusFlags) bool {
	return s&f != 0
}

// IsTerminal reports whether this flag represents a terminal state.
func (s StatusFlags) IsTerminal() bool {
	return s.HasFlag(FlagTerminal)
}

// StatusInfo pairs a legacy string status with its flag representation.
// This enables gradual migration from string-based to flag-based status.
type StatusInfo struct {
	Legacy  string      // Original string status for JSON compatibility
	Flags   StatusFlags // Flag-based status representation
}

// BuildStatusInfo creates a StatusInfo from a legacy string status.
func BuildStatusInfo(s string) StatusInfo {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pending":
		return StatusInfo{Legacy: s, Flags: FlagPending}
	case "running":
		return StatusInfo{Legacy: s, Flags: FlagRunning}
	case "done":
		return StatusInfo{Legacy: s, Flags: FlagTerminal}
	case "blocked":
		return StatusInfo{Legacy: s, Flags: FlagTerminal}
	case "skipped":
		return StatusInfo{Legacy: s, Flags: FlagTerminal}
	case "verifying":
		return StatusInfo{Legacy: s, Flags: FlagPending | FlagVerifying}
	case "waiting":
		return StatusInfo{Legacy: s, Flags: FlagPending | FlagWaiting}
	case "external_review":
		return StatusInfo{Legacy: s, Flags: FlagPending | FlagExternal}
	default:
		return StatusInfo{Legacy: s, Flags: FlagPending}
	}
}

// NewStatus creates a StatusInfo for a status constant.
func NewStatus(legacy string) StatusInfo {
	return BuildStatusInfo(legacy)
}

// IsTerminal reports whether this status is terminal.
func (s StatusInfo) IsTerminal() bool {
	return s.Flags.IsTerminal()
}

// IsPending reports whether this status is pending.
func (s StatusInfo) IsPending() bool {
	return s.Flags.HasFlag(FlagPending) && !s.Flags.HasFlag(FlagTerminal)
}

// IsRunning reports whether this status is running.
func (s StatusInfo) IsRunning() bool {
	return s.Flags.HasFlag(FlagRunning)
}

// HasVerifying reports whether the status includes verifying.
func (s StatusInfo) HasVerifying() bool {
	return s.Flags.HasFlag(FlagVerifying)
}

// HasWaiting reports whether the status includes waiting.
func (s StatusInfo) HasWaiting() bool {
	return s.Flags.HasFlag(FlagWaiting)
}

// HasExternal reports whether the status includes external.
func (s StatusInfo) HasExternal() bool {
	return s.Flags.HasFlag(FlagExternal)
}

// String returns the legacy string representation.
func (s StatusInfo) String() string {
	return s.Legacy
}

// StatusHelpers provides common status check functions for use across the codebase.
// These can be imported and used instead of directly checking status strings.
type StatusHelpers struct{}

// IsPending checks if the given status is pending.
func (StatusHelpers) IsPending(s TodoStatus) bool {
	return s == TodoPending || s == TodoVerifying || s == TodoWaiting || s == TodoExternalReview
}

// IsRunning checks if the given status is running.
func (StatusHelpers) IsRunning(s TodoStatus) bool {
	return s == TodoRunning
}

// IsTerminal checks if the given status is terminal (done, blocked, skipped).
func (StatusHelpers) IsTerminal(s TodoStatus) bool {
	return s == TodoDone || s == TodoBlocked || s == TodoSkipped
}

// IsActive checks if the status is pending or running.
func (StatusHelpers) IsActive(s TodoStatus) bool {
	return s == TodoPending || s == TodoRunning || s == TodoVerifying || s == TodoWaiting
}

// Common status helpers for use throughout the codebase.
var Status = StatusHelpers{}
