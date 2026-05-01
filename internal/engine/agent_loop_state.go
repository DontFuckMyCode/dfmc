// agent_loop_state.go — mutable state for one runNativeToolLoop
// invocation. Replaces a pile of locally-threaded parameters with one
// pointer so phase helpers (preflightBudget, postStepBudget, …) can
// take *loopRunState instead of 14-arg signatures that grow every time
// a new field gets added to the loop.
//
// Construction site: runNativeToolLoop builds one loopRunState per
// invocation and passes &state to the phase helpers. The struct
// captures both:
//
//   - seed: the parkedAgentState snapshot used by park to freeze
//     loop state for /continue resume. Carries cumulative counters
//     and tool source across resumes.
//   - mutating loop locals: msgs, traces, totalTokens, step,
//     autoRecoveries, lastProvider, lastModel — all updated in-place
//     by phase helpers as the loop iterates.
//   - per-run constants: question, chunks, systemPrompt,
//     systemBlocks, descriptors, lim — set once at construction,
//     never mutated.

package engine

import (
	"sync"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// loopRunState is the mutable state of one runNativeToolLoop call.
// Phase helpers receive *loopRunState and mutate the relevant fields
// directly; the loop body owns construction and final dispatch.
type loopRunState struct {
	seed *parkedAgentState

	// Mutates each iteration.
	msgs           []provider.Message
	traces         []nativeToolTrace
	totalTokens    int
	step           int
	autoRecoveries int
	lastProvider   string
	lastModel      string

	// Stable for the run.
	question     string
	chunks       []types.ContextChunk
	systemPrompt string
	systemBlocks []provider.SystemBlock
	descriptors  []provider.ToolDescriptor
	lim          agentLimits
	cacheMu      *sync.Mutex
}

// park freezes the current state and returns the parked completion.
// Centralises the 14-arg parkNativeToolLoop call so phase helpers can
// emit a park sentinel with one method call.
func (s *loopRunState) park(e *Engine, notice string, reason ParkReason) nativeToolCompletion {
	return e.parkNativeToolLoop(
		s.question, s.seed, s.msgs, s.traces, s.chunks,
		s.systemPrompt, s.systemBlocks, s.descriptors,
		s.lastProvider, s.lastModel, s.totalTokens, s.step,
		notice, reason,
	)
}
