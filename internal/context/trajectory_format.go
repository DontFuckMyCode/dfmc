package context

// trajectory_format.go — public types for the trajectory coach
// (TraceEntry input + TrajectoryOutput result) and the
// FormatTrajectoryHints renderer that turns a result into the
// "[trajectory coach]" system-note block injected between agent-loop
// rounds. Sibling of trajectory.go which keeps the per-round rule
// engine (TrajectoryHints) and trajectory_detect.go /
// trajectory_helpers.go which keep the detectors and small string
// utilities the rules reach for.
//
// Splitting the public surface out keeps trajectory.go scoped to
// "what does the rule engine actually decide" while this file owns
// "what shape does the engine input/output take, and how does the
// caller render the output for the next round's system note."

import "strings"

// TraceEntry is a trimmed view of one tool-call + result pair. The caller
// populates only what it can cheaply see from the agent loop — we keep the
// surface narrow on purpose.
type TraceEntry struct {
	Tool          string         // e.g., "edit_file", "tool_call" (+Inner for bridged)
	Inner         string         // backend tool name when Tool=="tool_call"; else ""
	Args          map[string]any // provider-reported input
	OutputPreview string         // first ~400 chars of Result.Output
	OutputChars   int            // full byte length of Output
	Ok            bool           // true when Err is empty
	Err           string         // tool error text when Ok==false
	Step          int            // loop step when the call occurred
}

// EffectiveTool returns the user-facing tool name — for bridged calls we
// surface the backend tool (tool_call("grep_codebase") → "grep_codebase").
func (t TraceEntry) EffectiveTool() string {
	if strings.TrimSpace(t.Inner) != "" {
		return t.Inner
	}
	return t.Tool
}

// TrajectoryOutput bundles the trajectory hints with metadata about the round.
type TrajectoryOutput struct {
	Hints         []string // up to 2 short coaching lines
	RoundSummary  string   // one-line recap of the round
	OpenQuestions []string // unresolved issues for the next round
	Confidence    float64  // 0-1; low triggers expanded retrieval on next round

	// Stuck* fields are populated when Rule 0 (repeated-failure) fires.
	// They give downstream surfaces (TUI chip, web activity feed, metrics)
	// a structured way to render the pattern without grepping the hint
	// text. Empty StuckTool means no stuck pattern this round.
	StuckTool      string
	StuckCount     int
	StuckErrSample string

	// Unverified* fields surface the running unvalidated-edits count,
	// computed every round (not just when Rule 2 escalates). Engine
	// publishes a separate agent:coach:unverified event when the count
	// crosses the escalation threshold so the TUI / web feed can
	// correlate the always-visible "unverified: N" badge with a
	// matching warn notice in the chat scrollback. UnverifiedCount==0
	// means no current streak.
	UnverifiedCount     int
	UnverifiedPaths     []string
	UnverifiedEscalated bool // true when Rule 2 fired its directive form
}

// FormatTrajectoryHints wraps a TrajectoryOutput into a single system-note block
// suitable for injection as a user message between agent-loop rounds.
// Returns "" when there are no hints. When Confidence < 0.5, also includes
// the round summary and open questions to trigger expanded retrieval.
func FormatTrajectoryHints(out *TrajectoryOutput) string {
	if out == nil || len(out.Hints) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[trajectory coach]\n")
	for _, h := range out.Hints {
		b.WriteString("• ")
		b.WriteString(strings.TrimSpace(h))
		b.WriteByte('\n')
	}
	// When confidence is low, include the round summary so the next retrieval
	// pass does expanded exploration.
	if out.Confidence < 0.5 && strings.TrimSpace(out.RoundSummary) != "" {
		b.WriteString("• round: ")
		b.WriteString(strings.TrimSpace(out.RoundSummary))
		b.WriteByte('\n')
		for _, q := range out.OpenQuestions {
			b.WriteString("  open: ")
			b.WriteString(strings.TrimSpace(q))
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
