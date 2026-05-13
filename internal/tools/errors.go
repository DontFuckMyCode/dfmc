// errors.go — public sentinel errors for the tools domain.
//
// Sentinel set (detect via errors.Is):
//   - ErrEngineClosed         — defined in lifecycle.go; tool execution
//                               attempted after Close().
//   - ErrMetaBudgetExhausted  — meta-tool batch crossed the cumulative
//                               backend call cap for one turn.
//   - ErrMetaDepthExceeded    — tool_call / tool_batch_call nested past
//                               agent.meta_depth_limit.
//
// Production sites wrap these via fmt.Errorf("%w …", ErrXxx, …) so the
// typed match keeps working while the human-readable message remains
// the same as before.

package tools

import "errors"

var (
	ErrMetaBudgetExhausted = errors.New("meta tool budget exhausted")
	ErrMetaDepthExceeded   = errors.New("meta tool nesting exceeded depth limit")
	// ErrSubagentDepthExceeded prevents unbounded recursive delegation
	ErrSubagentDepthExceeded = errors.New("sub-agent recursion depth limit exceeded")
)
