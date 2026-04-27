// Package context — trajectory.go derives short "dynamic" hints from the
// running agent's tool-call history. These hints are injected between loop
// rounds so the model sees lightweight, evidence-based coaching that
// reflects what the run actually did, not just the initial system prompt.
//
// Design notes:
//   - Hints are *micro-touches* — 1-2 short sentences per hint, max 2 hints
//     per turn. The user called these "minik dokunuslar" and asked for
//     post-execution-shaped prompts. Too many hints becomes noise.
//   - Hints are stateless per-turn: the caller is expected to track which
//     hint text it already injected (via recentHints) so we don't repeat
//     ourselves round after round.
//   - All rules prefer observable facts (tool name, arg values, output
//     size, error text) over interpretation. We never hallucinate.
package context

import (
	"fmt"
	"strings"
)

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
}

// TrajectoryHints returns up to 2 short coaching lines derived from the
// most recent round of tool calls. `fresh` is the slice of traces from the
// *current* loop step; `all` is the running history including fresh; both
// may be empty. `recent` is a short de-dup window of hints already injected
// in this conversation — rules skip if they'd re-emit an already-seen hint.
//
// RoundSummary is a one-line recap of what the round accomplished.
// OpenQuestions lists unresolved issues the next round should address.
// Confidence is 0-1: low confidence (< 0.5) triggers expanded retrieval.
func TrajectoryHints(fresh, all []TraceEntry, recent []string) *TrajectoryOutput {
	if len(fresh) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	for _, h := range recent {
		seen[strings.TrimSpace(h)] = struct{}{}
	}
	out := make([]string, 0, 2)
	push := func(line string) bool {
		line = strings.TrimSpace(line)
		if line == "" {
			return false
		}
		if _, dup := seen[line]; dup {
			return false
		}
		seen[line] = struct{}{}
		out = append(out, line)
		return len(out) >= 2
	}

	// Build round summary from fresh traces.
	roundSummary := buildRoundSummary(fresh)

	// Collect open questions.
	var openQuestions []string

	// Rule 1: any failed tool this round → retry-safely hint. Highest
	// priority because silent retries burn budget fast.
	for _, t := range fresh {
		if t.Ok {
			continue
		}
		et := t.EffectiveTool()
		brief := firstLine(t.Err)
		if brief == "" {
			brief = "unknown error"
		}
		msg := fmt.Sprintf("Prior call %s failed (%s). Don't retry with the same inputs — read the error, adjust arguments, or pick a different tool.", et, brief)
		openQuestions = append(openQuestions, fmt.Sprintf("%s failed: %s", et, brief))
		if push(msg) {
			return &TrajectoryOutput{
				Hints:         out,
				RoundSummary:  roundSummary,
				OpenQuestions: openQuestions,
				Confidence:    computeConfidence(fresh),
			}
		}
		break // one failure hint per turn
	}

	// Rule 2: wrote/edited a file → remind about validation. Only the
	// most recent successful mutation to avoid spam across multi-file edits.
	for i := len(fresh) - 1; i >= 0; i-- {
		t := fresh[i]
		if !t.Ok {
			continue
		}
		switch t.EffectiveTool() {
		case "edit_file", "write_file", "apply_patch":
			path := strings.TrimSpace(argAsString(t.Args, "path"))
			if path == "" {
				path = strings.TrimSpace(argAsString(t.Args, "file"))
			}
			if path == "" {
				path = "the file you just changed"
			}
			hint := "Just mutated " + path + ". Validate with the smallest targeted check (build/vet/test that touches it) before declaring done — don't trust edits on faith."
			if push(hint) {
				return &TrajectoryOutput{
					Hints:         out,
					RoundSummary:  roundSummary,
					OpenQuestions: openQuestions,
					Confidence:    computeConfidence(fresh),
				}
			}
		}
		// Only consider the most recent mutation.
		if t.Ok {
			break
		}
	}

	// Rule 3: large search result → narrow before widening.
	for _, t := range fresh {
		if !t.Ok {
			continue
		}
		if t.EffectiveTool() != "grep_codebase" {
			continue
		}
		if t.OutputChars < 4000 && !strings.Contains(t.OutputPreview, "truncated") {
			continue
		}
		hint := "grep_codebase returned a lot. Narrow with a tighter regex or `glob` filter before expanding — wide scans waste the context budget."
		if push(hint) {
			return &TrajectoryOutput{
				Hints:         out,
				RoundSummary:  roundSummary,
				OpenQuestions: openQuestions,
				Confidence:    computeConfidence(fresh),
			}
		}
	}

	// Rule 4: repeated calls to the same tool with similar args → consolidate.
	if dup := detectRepeatedCalls(all); dup != "" {
		hint := "You've called " + dup + " several times on similar inputs. Consolidate via tool_batch_call, or rethink whether another tool would answer the question in one shot."
		if push(hint) {
			return &TrajectoryOutput{
				Hints:         out,
				RoundSummary:  roundSummary,
				OpenQuestions: openQuestions,
				Confidence:    computeConfidence(fresh),
			}
		}
	}

	// Rule 5: shell did the wrong job. run_command used for things that
	// have a dedicated tool.
	for _, t := range fresh {
		if !t.Ok {
			continue
		}
		if t.EffectiveTool() != "run_command" {
			continue
		}
		cmd := strings.TrimSpace(argAsString(t.Args, "command"))
		if cmd == "" {
			continue
		}
		if alt := preferDedicatedTool(cmd); alt != "" {
			hint := "run_command was used for a task with a dedicated tool: prefer " + alt + " next time — it's safer and the output is structured."
			if push(hint) {
				return &TrajectoryOutput{
					Hints:         out,
					RoundSummary:  roundSummary,
					OpenQuestions: openQuestions,
					Confidence:    computeConfidence(fresh),
				}
			}
		}
	}

	return &TrajectoryOutput{
		Hints:         out,
		RoundSummary:  roundSummary,
		OpenQuestions: openQuestions,
		Confidence:    computeConfidence(fresh),
	}
}

// buildRoundSummary produces a one-line recap of the round's activity.
func buildRoundSummary(fresh []TraceEntry) string {
	if len(fresh) == 0 {
		return ""
	}
	var actions []string
	searched := false
	for _, t := range fresh {
		if !t.Ok {
			continue
		}
		switch t.EffectiveTool() {
		case "edit_file":
			actions = append(actions, "edited "+abbrevPath(argAsString(t.Args, "path")))
		case "write_file":
			actions = append(actions, "wrote "+abbrevPath(argAsString(t.Args, "path")))
		case "apply_patch":
			actions = append(actions, "applied patch")
		case "grep_codebase":
			searched = true
		case "read_file":
			actions = append(actions, "read "+abbrevPath(argAsString(t.Args, "path")))
		case "codemap":
			actions = append(actions, "explored codemap")
		case "run_command":
			actions = append(actions, "ran command")
		}
	}
	if len(actions) == 0 {
		if searched {
			return "searched codebase"
		}
		return "no significant file activity"
	}
	if len(actions) > 3 {
		actions = actions[:3]
		actions = append(actions, "...")
	}
	return strings.Join(actions, ", ")
}

// computeConfidence returns 0-1 based on how many tools succeeded and whether
// there are unresolved errors or large search results.
func computeConfidence(fresh []TraceEntry) float64 {
	if len(fresh) == 0 {
		return 0.5
	}
	ok := 0
	for _, t := range fresh {
		if t.Ok {
			ok++
		}
	}
	rate := float64(ok) / float64(len(fresh))
	// Deduct for failures.
	if rate < 1.0 {
		rate -= 0.1
	}
	if rate < 0 {
		rate = 0
	}
	return rate
}

// abbrevPath returns the last path component, trimmed.
func abbrevPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return p
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	if i := strings.LastIndexByte(p, '\\'); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// detectRepeatedCalls returns the name of a tool that was called 3+ times
// in the last ~6 traces with overlapping argument values. Empty string
// when nothing looks repetitive.
func detectRepeatedCalls(all []TraceEntry) string {
	if len(all) < 3 {
		return ""
	}
	window := all
	if len(window) > 6 {
		window = window[len(window)-6:]
	}
	counts := map[string]int{}
	argSeen := map[string]map[string]struct{}{}
	for _, t := range window {
		if !t.Ok {
			continue
		}
		name := t.EffectiveTool()
		counts[name]++
		if argSeen[name] == nil {
			argSeen[name] = map[string]struct{}{}
		}
		argSeen[name][canonicalArgFingerprint(t.Args)] = struct{}{}
	}
	for name, n := range counts {
		if n < 3 {
			continue
		}
		// Only flag when there's argument overlap (same fingerprint ≥ twice).
		unique := len(argSeen[name])
		if unique <= n-1 {
			return name
		}
	}
	return ""
}

// canonicalArgFingerprint returns a stable string for similar-looking args.
// We deliberately strip long values so "read file A lines 1-20" and
// "read file A lines 40-60" count as the same fingerprint when the path is
// the same (repeated file-bouncing is a bad pattern either way).
func canonicalArgFingerprint(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := []string{"path", "file", "pattern", "query", "command", "name"}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := strings.TrimSpace(argAsString(args, k))
		if v == "" {
			continue
		}
		if len(v) > 48 {
			v = v[:48]
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "|")
}

// preferDedicatedTool maps shell commands onto better-suited DFMC tools.
// Returns "" when the command is a legitimate shell-only task.
func preferDedicatedTool(cmd string) string {
	first := strings.ToLower(firstToken(cmd))
	switch first {
	case "cat", "head", "tail", "less", "more":
		return "read_file"
	case "grep", "rg", "ack", "ag":
		return "grep_codebase"
	case "find":
		return "glob"
	case "ls", "dir":
		return "list_dir"
	case "sed", "awk":
		return "edit_file or apply_patch"
	case "echo":
		// Only flag echo when it's redirecting to a file.
		if strings.Contains(cmd, ">") {
			return "write_file"
		}
	case "curl", "wget":
		return "web_fetch"
	}
	return ""
}

func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for i, r := range s {
		if r == ' ' || r == '\t' {
			return s[:i]
		}
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}

func argAsString(args map[string]any, key string) string {
	if len(args) == 0 || key == "" {
		return ""
	}
	v, ok := args[key]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprint(v)
	}
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
