// errors.go — public sentinel errors for the engine domain.
//
// Sentinel set (detect via errors.Is):
//   - ErrEngineNil                  — receiver is nil. Programmer error.
//   - ErrEngineNotInitialized       — engine method called before Init or
//                                     after Shutdown. Carries the calling
//                                     op and state in the wrapped message.
//   - ErrNoParkedAgent              — autonomous-resume / /continue with
//                                     nothing parked.
//   - ErrSubagentConcurrencyLimit   — RunSubagent / orchestrate fan-out
//                                     above agent.subagent_concurrency.
//
// Production sites wrap these via fmt.Errorf("…: %w", ErrXxx, …) so the
// typed match keeps working while the human-readable message remains
// the same as before.

package engine

import "errors"

var (
	ErrEngineNil                = errors.New("engine is nil")
	ErrEngineNotInitialized     = errors.New("engine not initialized")
	ErrNoParkedAgent            = errors.New("no parked agent loop to resume")
	ErrSubagentConcurrencyLimit = errors.New("sub-agent concurrency limit reached")
)
