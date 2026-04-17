package engine

// agent_parked.go — parked/resumable agent loop state and mid-loop user
// interjections (the "/btw" channel).
//
// When the native tool loop hits MaxSteps, instead of erroring out we freeze
// the loop state (question, running message history, traces, tokens, context
// chunks, system prompt) and emit a "parked" completion. The user can type
// /continue to resume exactly where it left off, optionally with a note
// appended. Between iterations the loop also
// drains any /btw notes the user has queued so they land before the next
// provider round-trip.

import (
	"strings"
	"time"

	"github.com/dontfuckmycode/dfmc/internal/provider"
	"github.com/dontfuckmycode/dfmc/pkg/types"
)

// parkedAgentState is the minimum set of values needed to rejoin a native
// tool loop mid-run. It is built inside askWithNativeTools when the loop hits
// its step ceiling, and consumed by ResumeAgent.
type parkedAgentState struct {
	Question      string
	Messages      []provider.Message
	Traces        []nativeToolTrace
	Chunks        []types.ContextChunk
	SystemPrompt  string
	SystemBlocks  []provider.SystemBlock
	Descriptors   []provider.ToolDescriptor
	ContextTokens int
	TotalTokens   int
	Step          int
	LastProvider  string
	LastModel     string
	ParkedAt      time.Time
	// RecentCoachHints remembers trajectory hints already injected into
	// this loop so the composer doesn't repeat itself round after round.
	// Bounded to the last ~8 entries to keep de-dup cheap.
	RecentCoachHints []string
	// CumulativeSteps / CumulativeTokens accumulate across every
	// /continue resume. Step + TotalTokens get reset on resume so each
	// attempt gets a fresh MaxSteps budget, but these cumulative
	// counters enforce an outer ceiling (resumeMaxMultiplier * MaxSteps)
	// so a model that keeps parking can't burn tokens forever.
	CumulativeSteps  int
	CumulativeTokens int
}

// HasParkedAgent reports whether a previous agent loop was parked (cap hit)
// and is waiting for resume.
func (e *Engine) HasParkedAgent() bool {
	if e == nil {
		return false
	}
	e.agentMu.Lock()
	defer e.agentMu.Unlock()
	return e.agentParked != nil
}

// ParkedAgentSummary returns a human-readable snapshot of the parked loop,
// safe to surface in the UI. Empty string when no state is parked.
func (e *Engine) ParkedAgentSummary() string {
	if e == nil {
		return ""
	}
	e.agentMu.Lock()
	defer e.agentMu.Unlock()
	if e.agentParked == nil {
		return ""
	}
	p := e.agentParked
	q := strings.TrimSpace(p.Question)
	if len(q) > 80 {
		q = q[:77] + "..."
	}
	return "parked at step " + itoaInt(p.Step) + " — " + q
}

// ClearParkedAgent drops the parked state without resuming. Called e.g. when
// the user submits a fresh unrelated question.
func (e *Engine) ClearParkedAgent() {
	if e == nil {
		return
	}
	e.agentMu.Lock()
	e.agentParked = nil
	e.agentMu.Unlock()
}

// QueueAgentNote appends a short user note to the pending /btw queue. The
// next step boundary inside the agent loop drains the queue and appends each
// note as a user message before the next provider round-trip. Safe to call
// from any goroutine.
func (e *Engine) QueueAgentNote(note string) {
	if e == nil {
		return
	}
	trimmed := strings.TrimSpace(note)
	if trimmed == "" {
		return
	}
	e.agentMu.Lock()
	e.agentNotesQueue = append(e.agentNotesQueue, trimmed)
	depth := len(e.agentNotesQueue)
	e.agentMu.Unlock()
	if e.EventBus != nil {
		e.EventBus.Publish(Event{
			Type:   "agent:note:queued",
			Source: "engine",
			Payload: map[string]any{
				"note":  trimmed,
				"queue": depth,
			},
		})
	}
}

// drainAgentNotes pops all pending /btw notes, returning them in submission
// order. Called at step boundaries inside the native tool loop.
func (e *Engine) drainAgentNotes() []string {
	if e == nil {
		return nil
	}
	e.agentMu.Lock()
	defer e.agentMu.Unlock()
	if len(e.agentNotesQueue) == 0 {
		return nil
	}
	out := make([]string, len(e.agentNotesQueue))
	copy(out, e.agentNotesQueue)
	e.agentNotesQueue = e.agentNotesQueue[:0]
	return out
}

// takeParkedAgent atomically moves the parked state out of the engine so a
// resume call can work on it without racing with another resume.
func (e *Engine) takeParkedAgent() *parkedAgentState {
	if e == nil {
		return nil
	}
	e.agentMu.Lock()
	defer e.agentMu.Unlock()
	p := e.agentParked
	e.agentParked = nil
	return p
}

// saveParkedAgent stores the snapshot under the engine mutex.
func (e *Engine) saveParkedAgent(p *parkedAgentState) {
	if e == nil || p == nil {
		return
	}
	p.ParkedAt = time.Now()
	e.agentMu.Lock()
	e.agentParked = p
	e.agentMu.Unlock()
}

// enterSubagent stashes the parent's parked state aside when the first
// subagent starts, and returns an exit function the caller defers to
// restore it when the last subagent finishes. Safe for concurrent
// subagents spawned via tool_batch_call(delegate_task): the parent's
// parked state is only moved aside once (counter 0→1) and restored once
// (counter 1→0). Any parked state produced by a subagent's own loop is
// discarded on exit — subagents don't park-resume; the parent does.
func (e *Engine) enterSubagent() func() {
	if e == nil {
		return func() {}
	}
	e.agentMu.Lock()
	if e.subagentInFlight == 0 {
		e.subagentStashed = e.agentParked
		e.agentParked = nil
	}
	e.subagentInFlight++
	e.agentMu.Unlock()
	return func() {
		e.agentMu.Lock()
		e.subagentInFlight--
		if e.subagentInFlight <= 0 {
			e.subagentInFlight = 0
			// Discard whatever a subagent parked — it's scoped to its own
			// task and would confuse a later /continue from the parent.
			e.agentParked = e.subagentStashed
			e.subagentStashed = nil
		}
		e.agentMu.Unlock()
	}
}

// itoaInt is a tiny allocation-free int formatter for status strings, to
// avoid importing strconv just for two digits.
func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
